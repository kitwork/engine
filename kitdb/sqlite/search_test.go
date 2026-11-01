package sqlite

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/search"
)

var searchWords = []string{"áo", "thun", "nam", "nữ", "điện", "thoại", "sữa", "rửa", "mặt", "bình", "giữ", "nhiệt", "đồng", "hồ", "kem", "chống", "nắng"}

func searchSentence(random *rand.Rand, minimum, maximum int) string {
	words := make([]string, minimum+random.IntN(maximum-minimum+1))
	for position := range words {
		words[position] = searchWords[random.IntN(len(searchWords))]
	}
	return strings.Join(words, " ")
}

var searchColumns = []SearchColumn{{Name: "name", Boost: 3}, {Name: "description"}}

var searchQueries = []search.MatchQuery{
	{Field: "name", Text: "áo thun"},
	{Fields: []string{"name", "description"}, Text: "dien thoai"},
	{Fields: []string{"name", "description"}, Text: "kem chống nắng", Operator: search.QueryAny},
	{Field: "name", Phrase: "bình giữ"},
	{Fields: []string{"name", "description"}, Prefix: "nh"},
}

func openSearchDatabase(t *testing.T, path string, fold int) *Database {
	t.Helper()
	database, err := Open(context.Background(), path, Options{
		Search:     []SearchTable{{Table: "products", Columns: searchColumns}},
		SearchFold: fold,
	})
	if err != nil {
		t.Fatal(err)
	}
	return database
}

// rebuiltSearch indexes the snapshot's rows from scratch, the oracle a live
// search must equal.
func rebuiltSearch(t *testing.T, snapshot *Snapshot) *search.Index {
	t.Helper()
	fields := make([]search.Field, 0, len(searchColumns))
	for _, column := range searchColumns {
		boost := column.Boost
		if boost == 0 {
			boost = 1
		}
		fields = append(fields, search.Text(column.Name, search.VietnameseAnalyzer(), search.Boost(boost)))
	}
	schema, err := search.NewSchema(fields...)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "oracle")
	writer, err := search.NewIndexWriter(directory, schema, search.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := snapshot.Query(`SELECT _rowid_, name, description FROM products ORDER BY _rowid_`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var rowID int64
		var name, description any
		if err := rows.Scan(&rowID, &name, &description); err != nil {
			t.Fatal(err)
		}
		if err := writer.Add(context.Background(), search.Document{ID: strconv.FormatInt(rowID, 10),
			Fields: map[string]string{"name": searchText(name), "description": searchText(description)}}); err != nil {
			t.Fatal(err)
		}
	}
	rows.Close()
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	index, err := search.OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return index
}

func normalizedHits(hits []SearchHit) []SearchHit {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].RowID < hits[j].RowID
	})
	return hits
}

// compareSearch checks every query of the snapshot against an index rebuilt
// from the snapshot's own rows: the same rows with the same scores.
func compareSearch(t *testing.T, label string, snapshot *Snapshot) {
	t.Helper()
	oracle := rebuiltSearch(t, snapshot)
	for _, query := range searchQueries {
		got, err := snapshot.Search("products", query, search.SearchOptions{Limit: 1000})
		if err != nil {
			t.Fatalf("%s %+v: %v", label, query, err)
		}
		hits, err := oracle.Search(context.Background(), query, search.SearchOptions{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		want := make([]SearchHit, len(hits))
		for position, hit := range hits {
			rowID, _ := strconv.ParseInt(hit.ID, 10, 64)
			want[position] = SearchHit{RowID: rowID, Score: hit.Score}
		}
		got, want = normalizedHits(got), normalizedHits(want)
		if len(got) != len(want) {
			t.Fatalf("%s %+v: %d hits, rebuilt index %d", label, query, len(got), len(want))
		}
		for position := range got {
			if got[position].RowID != want[position].RowID ||
				math.Abs(got[position].Score-want[position].Score) > 1e-9*math.Max(1, want[position].Score) {
				t.Fatalf("%s %+v hit %d: %+v, rebuilt %+v", label, query, position, got[position], want[position])
			}
		}
	}
}

func searchWrite(random *rand.Rand, next *int) func(*Transaction) error {
	kind, id := random.IntN(7), 1+random.IntN(max(*next, 1))
	name, description := searchSentence(random, 1, 5), searchSentence(random, 2, 10)
	created := *next + 1
	if kind == 0 || kind == 1 {
		*next = created
	}
	return func(tx *Transaction) error {
		var err error
		switch kind {
		case 0, 1:
			_, err = tx.Exec(`INSERT INTO products (id, name, description, price) VALUES (?, ?, ?, 1)`, created, name, description)
		case 2:
			_, err = tx.Exec(`UPDATE products SET name = ? WHERE id = ?`, name, id)
		case 3:
			_, err = tx.Exec(`UPDATE products SET price = price + 1 WHERE id = ?`, id) // not a searchable column
		case 4:
			_, err = tx.Exec(`DELETE FROM products WHERE id = ?`, id)
		case 5:
			_, err = tx.Exec(`INSERT OR REPLACE INTO products (id, name, description, price) VALUES (?, ?, ?, 2)`, id, name, description)
		case 6:
			_, err = tx.Exec(`UPDATE products SET description = NULL WHERE id = ?`, id)
		}
		return err
	}
}

func TestSearchMatchesARebuiltIndexAtEverySnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database := openSearchDatabase(t, path, 5)
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT, price INTEGER)`); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(3, 3))
	next := 0
	seed := make([]func(*Transaction) error, 0, 40)
	for count := 0; count < 40; count++ {
		next++
		id, name, description := next, searchSentence(random, 1, 5), searchSentence(random, 2, 10)
		seed = append(seed, func(tx *Transaction) error {
			_, err := tx.Exec(`INSERT INTO products (id, name, description, price) VALUES (?, ?, ?, 0)`, id, name, description)
			return err
		})
	}
	if _, err := database.Write(ctx, "seed", func(tx *Transaction) error {
		for _, insert := range seed {
			if err := insert(tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check := func(label string, database *Database) {
		if err := database.Read(ctx, func(snapshot *Snapshot) error {
			compareSearch(t, fmt.Sprintf("%s revision %d", label, snapshot.Revision), snapshot)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check("seed", database)
	for step := 0; step < 120; step++ {
		if _, err := database.Write(ctx, "w", searchWrite(random, &next)); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("step %d", step), database)
		if step%25 == 24 {
			if err := database.searchables["products"].fold(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	searchable := database.searchables["products"]
	searchable.mu.Lock()
	base := searchable.live.BaseRevision()
	searchable.mu.Unlock()
	if base == 0 {
		t.Fatal("no fold moved the index on disk")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: the index on disk and its state file resume where they were.
	database = openSearchDatabase(t, path, 5)
	check("reopened", database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	// A lost index is rebuilt from the table.
	if err := os.RemoveAll(path + ".search"); err != nil {
		t.Fatal(err)
	}
	database = openSearchDatabase(t, path, 5)
	defer database.Close()
	check("rebuilt", database)
}

func TestSearchFoldNeverPassesAnOpenSnapshot(t *testing.T) {
	ctx := context.Background()
	database := openSearchDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"), 1<<20)
	defer database.Close()
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT, price INTEGER)`); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(5, 5))
	next := 0
	for count := 0; count < 30; count++ {
		if _, err := database.Write(ctx, "w", searchWrite(random, &next)); err != nil {
			t.Fatal(err)
		}
	}
	held, release, finished := make(chan uint64), make(chan struct{}), make(chan error)
	go func() {
		finished <- database.Read(ctx, func(snapshot *Snapshot) error {
			if _, err := snapshot.Search("products", searchQueries[0], search.SearchOptions{}); err != nil {
				return err
			}
			held <- snapshot.Revision
			<-release
			compareSearch(t, "held snapshot after a fold", snapshot)
			return nil
		})
	}()
	revision := <-held
	for count := 0; count < 30; count++ {
		if _, err := database.Write(ctx, "w", searchWrite(random, &next)); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Read(ctx, func(snapshot *Snapshot) error {
		_, err := snapshot.Search("products", searchQueries[0], search.SearchOptions{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	searchable := database.searchables["products"]
	if err := searchable.fold(ctx); err != nil {
		t.Fatal(err)
	}
	searchable.mu.Lock()
	base := searchable.live.BaseRevision()
	searchable.mu.Unlock()
	if base != revision {
		t.Fatalf("the fold reached revision %d; the open snapshot holds %d", base, revision)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := searchable.fold(ctx); err != nil {
		t.Fatal(err)
	}
	searchable.mu.Lock()
	base = searchable.live.BaseRevision()
	searchable.mu.Unlock()
	if base <= revision {
		t.Fatalf("with the snapshot closed the fold stayed at %d", base)
	}
}

func TestSearchIgnoresUpdatesOfOtherColumns(t *testing.T) {
	ctx := context.Background()
	database := openSearchDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"), 1<<20)
	defer database.Close()
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT, price INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(ctx, "seed", func(tx *Transaction) error {
		_, err := tx.Exec(`INSERT INTO products VALUES (1, 'áo thun', 'cotton', 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	searchOnce := func() {
		if err := database.Read(ctx, func(snapshot *Snapshot) error {
			_, err := snapshot.Search("products", searchQueries[0], search.SearchOptions{})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	searchOnce()
	searchable := database.searchables["products"]
	searchable.mu.Lock()
	before := searchable.live.Pending()
	searchable.mu.Unlock()
	for count := 0; count < 200; count++ {
		if _, err := database.Write(ctx, "click", func(tx *Transaction) error {
			_, err := tx.Exec(`UPDATE products SET price = price + 1 WHERE id = 1`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	searchOnce()
	searchable.mu.Lock()
	after := searchable.live.Pending()
	searchable.mu.Unlock()
	if after != before {
		t.Fatalf("200 price updates added %d changes to the search index", after-before)
	}
}

func TestSearchCompactionMovesEveryTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := Open(ctx, path, Options{
		Search: []SearchTable{
			{Table: "products", Columns: searchColumns},
			{Table: "notes", Columns: []SearchColumn{{Name: "body"}}},
		},
		SearchFold: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Migrate(ctx, "boot",
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT, price INTEGER)`,
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(7, 7))
	next := 0
	notes := func() int {
		count := 0
		if err := database.Read(ctx, func(snapshot *Snapshot) error {
			hits, err := snapshot.Search("notes", search.MatchQuery{Field: "body", Text: "ghi chu"}, search.SearchOptions{Limit: 1000})
			count = len(hits)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return count
	}
	for round := 0; round < 12; round++ {
		for count := 0; count < 10; count++ {
			if _, err := database.Write(ctx, "w", searchWrite(random, &next)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := database.Write(ctx, "w", func(tx *Transaction) error {
			_, err := tx.Exec(`INSERT INTO notes (body) VALUES (?)`, fmt.Sprintf("ghi chú số %d", round))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := database.Read(ctx, func(snapshot *Snapshot) error {
			compareSearch(t, fmt.Sprintf("round %d", round), snapshot)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		notes()
		for _, s := range database.searchables {
			if err := s.fold(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := database.container.Info()
	if before.ObsoleteBytes == 0 {
		t.Fatalf("folds left no obsolete bytes: %+v", before)
	}
	if err := database.compactSearch(ctx); err != nil {
		t.Fatal(err)
	}
	after := database.container.Info()
	if after.ObsoleteBytes >= before.ObsoleteBytes || after.FileBytes >= before.FileBytes {
		t.Fatalf("compaction kept %+v of %+v", after, before)
	}
	if err := database.Read(ctx, func(snapshot *Snapshot) error {
		compareSearch(t, "after compaction", snapshot)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found := notes(); found != 12 {
		t.Fatalf("notes after compaction: %d hits, want 12", found)
	}
	if _, err := os.Stat(path + ".search.compact"); !os.IsNotExist(err) {
		t.Fatalf("the compacted file is still beside the container: %v", err)
	}
}

func TestSearchReplacesTheDirectoryLayout(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	if err := os.MkdirAll(filepath.Join(path+".search", "products"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".search", "products", "segment-1.ks"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	database := openSearchDatabase(t, path, 5)
	defer database.Close()
	info, err := os.Stat(path + ".search")
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the search index is not one file: %v %v", info, err)
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT, price INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(ctx, "w", func(tx *Transaction) error {
		_, err := tx.Exec(`INSERT INTO products VALUES (1, 'áo thun', 'cotton', 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Read(ctx, func(snapshot *Snapshot) error {
		compareSearch(t, "after the directory layout", snapshot)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
