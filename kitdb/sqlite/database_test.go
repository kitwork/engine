package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestDatabase(t *testing.T, path string) *Database {
	t.Helper()
	database, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

type queryFunc func(query string, arguments ...any) (*sql.Rows, error)

// digest hashes every user table, rows in rowid order, values with their types.
func digest(t *testing.T, query queryFunc) string {
	t.Helper()
	rows, err := query(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'kitwork_history' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	hash := sha256.New()
	for _, table := range tables {
		fmt.Fprintf(hash, "table %s\n", table)
		rows, err := query(`SELECT _rowid_, * FROM ` + quoteIdentifier(table) + ` ORDER BY _rowid_`)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for position := range values {
			pointers[position] = &values[position]
		}
		for rows.Next() {
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for _, value := range values {
				switch typed := value.(type) {
				case []byte:
					fmt.Fprintf(hash, "blob:%x|", typed)
				case string:
					fmt.Fprintf(hash, "text:%q|", typed)
				default:
					fmt.Fprintf(hash, "%T:%v|", value, value)
				}
			}
			hash.Write([]byte{'\n'})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func liveDigest(t *testing.T, database *Database) (uint64, string) {
	t.Helper()
	var revision uint64
	var result string
	if err := database.Read(context.Background(), func(snapshot *Snapshot) error {
		revision, result = snapshot.Revision, digest(t, snapshot.Query)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return revision, result
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dataSourceName(path, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return digest(t, func(query string, arguments ...any) (*sql.Rows, error) { return db.Query(query, arguments...) })
}

func revisionMoments(t *testing.T, database *Database) map[uint64]time.Time {
	t.Helper()
	moments := map[uint64]time.Time{}
	if err := database.Changes(context.Background(), 0, func(commit Commit) error {
		moments[commit.Revision] = commit.At
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return moments
}

const productsSchema = `CREATE TABLE products (id INTEGER PRIMARY KEY, sku TEXT UNIQUE, name TEXT NOT NULL, price REAL, data BLOB)`

// randomWrite is one request of the workload: inserts, updates, upserts,
// REPLACE conflicts, rowid changes, deletes, NUL characters, empty blobs; some
// statements fail inside a request that commits, some requests fail whole.
func randomWrite(random *rand.Rand, writer string) func(*Transaction) error {
	kind, id, other := random.IntN(10), 1+random.IntN(60), 1+random.IntN(60)
	sku, price := fmt.Sprintf("sku-%d", random.IntN(40)), float64(random.IntN(100000))/100
	failWhole := random.IntN(9) == 0
	return func(tx *Transaction) error {
		var err error
		switch kind {
		case 0, 1:
			_, err = tx.Exec(`INSERT OR IGNORE INTO products (id, sku, name, price) VALUES (?, ?, ?, ?)`, id, sku, "áo "+writer, price)
		case 2:
			_, err = tx.Exec(`INSERT OR REPLACE INTO products (id, sku, name, price, data) VALUES (?, ?, ?, ?, ?)`, id, sku, "a\x00b", price, []byte{})
		case 3:
			_, err = tx.Exec(`UPDATE products SET price = price * 2, name = name || '!' WHERE id = ?`, id)
		case 4:
			_, err = tx.Exec(`INSERT INTO products (id, sku, name) VALUES (?, ?, 'mới') ON CONFLICT (id) DO UPDATE SET name = 'cập nhật ' || excluded.sku`, id, sku)
		case 5:
			_, err = tx.Exec(`UPDATE OR IGNORE products SET id = ? WHERE id = ?`, other+1000, id)
		case 6:
			_, err = tx.Exec(`DELETE FROM products WHERE id = ?`, id)
		case 7:
			_, err = tx.Exec(`INSERT INTO notes (body) VALUES (?)`, writer+" ghi chú")
			if err == nil {
				// a failing statement inside a committing request leaves nothing
				_, failed := tx.Exec(`INSERT INTO products (id, sku, name) VALUES (?, ?, NULL)`, id, sku)
				if failed == nil {
					return errors.New("a NOT NULL violation was accepted")
				}
			}
		case 8:
			if random.IntN(4) == 0 {
				_, err = tx.Exec(`DELETE FROM notes`)
			} else {
				_, err = tx.Exec(`UPDATE notes SET body = body || '.' WHERE _rowid_ % 3 = 0`)
			}
		case 9:
			_, err = tx.Exec(`INSERT OR FAIL INTO products (id, sku, name) VALUES (?, 'unique-a', 'x'), (?, 'unique-a', 'y')`, id+2000, id+3000)
			if err == nil {
				return errors.New("OR FAIL inserted two rows with one UNIQUE value")
			}
			err = nil
		}
		if err != nil {
			return err
		}
		if failWhole {
			return errors.New("the request gives up after writing")
		}
		return nil
	}
}

func TestRestoreMatchesEveryRevisionOfAConcurrentWorkload(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database := openTestDatabase(t, path)
	if _, err := database.Migrate(ctx, "boot", productsSchema, `CREATE TABLE notes (body TEXT)`, `CREATE INDEX products_price ON products (price)`); err != nil {
		t.Fatal(err)
	}
	recorded := map[uint64]string{}
	record := func() {
		revision, sum := liveDigest(t, database)
		recorded[revision] = sum
	}
	record()
	for phase := 0; phase < 12; phase++ {
		if phase == 5 {
			if _, err := database.Migrate(ctx, "boot", `ALTER TABLE products ADD COLUMN stock INTEGER DEFAULT 7`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Anchor(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var group sync.WaitGroup
		for worker := 0; worker < 8; worker++ {
			group.Add(1)
			go func(worker int) {
				defer group.Done()
				random := rand.New(rand.NewPCG(uint64(phase), uint64(worker)))
				for count := 0; count < 12; count++ {
					_, _ = database.Write(ctx, fmt.Sprintf("worker-%d", worker), randomWrite(random, fmt.Sprintf("w%d", worker)))
				}
			}(worker)
		}
		group.Wait()
		record()
	}
	// a few lone commits, recorded one by one
	random := rand.New(rand.NewPCG(99, 99))
	for count := 0; count < 20; count++ {
		if _, err := database.Write(ctx, "lone", randomWrite(random, "lone")); err == nil {
			record()
		}
	}

	last, err := database.VerifyHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if last != database.Revision() {
		t.Fatalf("history ends at %d, the database at %d", last, database.Revision())
	}
	moments := revisionMoments(t, database)
	if len(moments) < 40 {
		t.Fatalf("the workload made only %d revisions", len(moments))
	}
	checked := 0
	for revision, want := range recorded {
		if revision == 0 {
			continue
		}
		target := filepath.Join(t.TempDir(), "restored.db")
		restored, err := database.Restore(ctx, moments[revision], target)
		if err != nil {
			t.Fatalf("restore revision %d: %v", revision, err)
		}
		if restored != revision {
			t.Fatalf("restore at the moment of revision %d reached %d", revision, restored)
		}
		if got := fileDigest(t, target); got != want {
			t.Fatalf("revision %d restored to different rows", revision)
		}
		checked++
	}
	if checked < 15 {
		t.Fatalf("only %d revisions were compared", checked)
	}
	// a moment between two commits restores the earlier one
	between := moments[last-1].Add(time.Duration(moments[last].Sub(moments[last-1]) / 2))
	if moments[last].Sub(moments[last-1]) > 1 {
		target := filepath.Join(t.TempDir(), "between.db")
		if restored, err := database.Restore(ctx, between, target); err != nil || restored != last-1 {
			t.Fatalf("a moment between %d and %d restored %d: %v", last-1, last, restored, err)
		}
	}
}

func TestFailedRequestsAndStatementsLeaveNoTrace(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"))
	if _, err := database.Migrate(ctx, "boot", productsSchema); err != nil {
		t.Fatal(err)
	}
	insert := func(tx *Transaction, id int, sku string) error {
		_, err := tx.Exec(`INSERT INTO products (id, sku, name) VALUES (?, ?, 'x')`, id, sku)
		return err
	}
	requests := map[string]func(*Transaction) error{
		"gives up": func(tx *Transaction) error {
			_ = insert(tx, 1, "a")
			_ = insert(tx, 2, "b")
			return errors.New("no")
		},
		"keeps going": func(tx *Transaction) error {
			if err := insert(tx, 10, "k"); err != nil {
				return err
			}
			if insert(tx, 11, "k") == nil {
				return errors.New("duplicate sku accepted")
			}
			return insert(tx, 12, "m")
		},
		"panics": func(tx *Transaction) error {
			_ = insert(tx, 20, "p")
			panic("boom")
		},
		"commit": func(tx *Transaction) error { _, err := tx.Exec(`COMMIT`); return err },
		"two": func(tx *Transaction) error {
			_, err := tx.Exec(`INSERT INTO products (id, sku, name) VALUES (30, 't', 'x'); DELETE FROM products`)
			return err
		},
		"or rollback": func(tx *Transaction) error {
			_, err := tx.Exec(`INSERT OR ROLLBACK INTO products (id, sku, name) VALUES (31, 'r', 'x')`)
			return err
		},
		"schema": func(tx *Transaction) error { _, err := tx.Exec(`CREATE TABLE x (a)`); return err },
		"pragma": func(tx *Transaction) error { _, err := tx.Exec(`PRAGMA foreign_keys = 0`); return err },
	}
	var group sync.WaitGroup
	outcomes := sync.Map{}
	for name, request := range requests {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := database.Write(ctx, name, request)
			outcomes.Store(name, err)
		}()
	}
	group.Wait()
	for name := range requests {
		err, _ := outcomes.Load(name)
		if (name == "keeps going") != (err == nil) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var ids []int64
	if err := database.Read(ctx, func(snapshot *Snapshot) error {
		rows, err := snapshot.Query(`SELECT id FROM products ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids) != "[10 12]" {
		t.Fatalf("rows %v; only the request that kept going may remain", ids)
	}
	var recordedIDs []int64
	if err := database.Changes(ctx, 0, func(commit Commit) error {
		for _, change := range commit.Changes {
			if change.Operation == Insert {
				recordedIDs = append(recordedIDs, change.RowID)
				if change.By != "keeps going" {
					t.Errorf("insert %d recorded as written by %q", change.RowID, change.By)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Slice(recordedIDs, func(i, j int) bool { return recordedIDs[i] < recordedIDs[j] })
	if fmt.Sprint(recordedIDs) != "[10 12]" {
		t.Fatalf("history recorded inserts %v", recordedIDs)
	}
	if _, err := database.VerifyHistory(ctx); err != nil {
		t.Fatal(err)
	}

	// A request that changes a row and gives up, and a committing request whose
	// only statement fails, record nothing: no revision, not even a no-op update.
	before := database.Revision()
	if _, err := database.Write(ctx, "gives up", func(tx *Transaction) error {
		if _, err := tx.Exec(`UPDATE products SET name = 'bad' WHERE id = 10`); err != nil {
			return err
		}
		return errors.New("no")
	}); err == nil {
		t.Fatal("the failing request committed")
	}
	if _, err := database.Write(ctx, "ignores", func(tx *Transaction) error {
		_, _ = tx.Exec(`UPDATE products SET sku = 'm' WHERE id = 10`) // UNIQUE violation
		// changes the first row, then fails on the second: the statement leaves nothing
		if _, err := tx.Exec(`UPDATE products SET sku = 'same'`); err == nil {
			return errors.New("two rows took one UNIQUE value")
		}
		_, _ = tx.Exec(`DELETE FROM products WHERE id = 12 AND name = (SELECT name FROM products WHERE id = 99999 OR abs(-9223372036854775808))`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if after := database.Revision(); after != before {
		t.Fatalf("requests that changed nothing moved the revision from %d to %d", before, after)
	}
}

func TestVerifyAndRestoreDetectTamperedHistory(t *testing.T) {
	ctx := context.Background()
	for _, tamper := range []string{
		`UPDATE kitwork_history SET changes = randomblob(length(changes)) WHERE rev = 3`,
		`UPDATE kitwork_history SET checksum = zeroblob(32) WHERE rev = 3`,
		`DELETE FROM kitwork_history WHERE rev = 3`,
		`UPDATE kitwork_history SET at = at - 1000000000000 WHERE rev = 4`,
	} {
		path := filepath.Join(t.TempDir(), "kitwork.db")
		database, err := Open(ctx, path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Migrate(ctx, "boot", productsSchema); err != nil {
			t.Fatal(err)
		}
		for id := 1; id <= 5; id++ {
			if _, err := database.Write(ctx, "t", func(tx *Transaction) error {
				_, err := tx.Exec(`INSERT INTO products (id, sku, name) VALUES (?, ?, 'x')`, id, fmt.Sprint(id))
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		moment := time.Now().Add(time.Hour)
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		raw, err := sql.Open("sqlite", dataSourceName(path))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(tamper); err != nil {
			t.Fatal(err)
		}
		raw.Close()
		database = openTestDatabase(t, path)
		if _, err := database.VerifyHistory(ctx); !errors.Is(err, ErrCorruptHistory) {
			t.Fatalf("%s: verify returned %v", tamper, err)
		}
		if _, err := database.Restore(ctx, moment, filepath.Join(t.TempDir(), "restored.db")); !errors.Is(err, ErrCorruptHistory) {
			t.Fatalf("%s: restore returned %v", tamper, err)
		}
	}
}

func TestUnsupportedSchemaIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database := openTestDatabase(t, path)
	for _, statement := range []string{
		`CREATE TABLE keyed (k TEXT PRIMARY KEY, v TEXT) WITHOUT ROWID`,
		`CREATE TABLE computed (a INTEGER, b INTEGER GENERATED ALWAYS AS (a * 2))`,
		`CREATE TRIGGER t AFTER INSERT ON products BEGIN DELETE FROM products; END`,
		`CREATE VIRTUAL TABLE v USING fts5(body)`,
	} {
		if _, err := database.Migrate(ctx, "boot", statement); !errors.Is(err, ErrUnsupportedSchema) {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	revision, _ := liveDigest(t, database)
	if revision != 0 {
		t.Fatalf("refused statements committed revision %d", revision)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", dataSourceName(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{productsSchema, `CREATE TRIGGER t AFTER INSERT ON products BEGIN DELETE FROM products; END`} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	if _, err := Open(ctx, path, Options{}); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("a file with a trigger opened: %v", err)
	}
}

func TestSecondOpenIsLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kitwork.db")
	openTestDatabase(t, path)
	if _, err := Open(context.Background(), path, Options{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("second open: %v", err)
	}
}

func TestClassifyStatement(t *testing.T) {
	for _, test := range []struct {
		text string
		kind statementKind
		ok   bool
	}{
		{"SELECT 1", statementRead, true},
		{"  -- comment\n select 'a;b' /* ; */ ;  ", statementRead, true},
		{"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", statementData, true},
		{"WITH x AS (SELECT 1) SELECT * FROM x", statementRead, true},
		{"INSERT INTO t VALUES ('rollback')", statementData, true},
		{`UPDATE "rollback" SET a = 1`, statementData, true},
		{"CREATE INDEX i ON t (a)", statementSchema, true},
		{"INSERT INTO t VALUES (1); SELECT 1", 0, false},
		{"INSERT OR ROLLBACK INTO t VALUES (1)", 0, false},
		{"BEGIN", 0, false},
		{"commit", 0, false},
		{"SAVEPOINT s", 0, false},
		{"PRAGMA journal_mode = DELETE", 0, false},
		{"ATTACH 'x' AS y", 0, false},
		{"VACUUM", 0, false},
		{"CREATE TEMP TABLE t (a)", 0, false},
		{"", 0, false},
		{"SELECT 'unterminated", 0, false},
	} {
		shape, err := classifyStatement(test.text)
		if (err == nil) != test.ok || (test.ok && shape.kind != test.kind) {
			t.Fatalf("%q: kind %d, error %v", test.text, shape.kind, err)
		}
	}
}

// TestCrashChild is the writer the crash test kills; it runs only as that child.
func TestCrashChild(t *testing.T) {
	path := os.Getenv("KITDB_SQLITE_CRASH_DATABASE")
	if path == "" {
		t.Skip("runs as the child of TestKilledWriterKeepsHistoryConsistent")
	}
	database, err := Open(context.Background(), path, Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	fmt.Println("ready")
	var group sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			random := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(worker)))
			for {
				_, _ = database.Write(context.Background(), "child", randomWrite(random, "child"))
			}
		}(worker)
	}
	group.Wait()
}

func TestKilledWriterKeepsHistoryConsistent(t *testing.T) {
	if testing.Short() {
		t.Skip("starts and kills child processes")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, "boot", productsSchema, `CREATE TABLE notes (body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	previous := uint64(0)
	for round := 0; round < 4; round++ {
		child := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1")
		child.Env = append(os.Environ(), "KITDB_SQLITE_CRASH_DATABASE="+path)
		output, err := child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 64)
		if count, err := output.Read(buffer); err != nil || !strings.Contains(string(buffer[:count]), "ready") {
			_ = child.Process.Kill()
			t.Fatalf("child did not start: %q %v", buffer[:count], err)
		}
		time.Sleep(time.Duration(150+round*120) * time.Millisecond)
		if err := child.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = child.Wait()

		database, err := Open(ctx, path, Options{})
		if err != nil {
			t.Fatalf("round %d: reopen after kill: %v", round, err)
		}
		last, err := database.VerifyHistory(ctx)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if last <= previous {
			t.Fatalf("round %d: the child committed nothing (history at %d)", round, last)
		}
		previous = last
		revision, want := liveDigest(t, database)
		moments := revisionMoments(t, database)
		target := filepath.Join(t.TempDir(), "restored.db")
		restored, err := database.Restore(ctx, moments[revision], target)
		if err != nil || restored != revision {
			t.Fatalf("round %d: restore to %d reached %d: %v", round, revision, restored, err)
		}
		if got := fileDigest(t, target); got != want {
			t.Fatalf("round %d: replaying the history after a kill gives different rows", round)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("round %d: %d revisions", round, last)
	}
}

func TestConcurrentReadsSeeWholeRevisions(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"))
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE pairs (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(ctx, "boot", func(tx *Transaction) error {
		_, err := tx.Exec(`INSERT INTO pairs VALUES (1, 0, 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for reader := 0; reader < 4; reader++ {
		group.Add(1)
		go func() {
			defer group.Done()
			var last uint64
			for {
				select {
				case <-stop:
					return
				default:
				}
				err := database.Read(ctx, func(snapshot *Snapshot) error {
					rows, err := snapshot.Query(`SELECT a, b FROM pairs WHERE id = 1`)
					if err != nil {
						return err
					}
					defer rows.Close()
					var a, b int64
					for rows.Next() {
						if err := rows.Scan(&a, &b); err != nil {
							return err
						}
					}
					if a != b {
						return fmt.Errorf("revision %d shows half a write: %d %d", snapshot.Revision, a, b)
					}
					if snapshot.Revision < last {
						return fmt.Errorf("revision went back from %d to %d", last, snapshot.Revision)
					}
					last = snapshot.Revision
					return rows.Err()
				})
				if err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	for count := 0; count < 300; count++ {
		if _, err := database.Write(ctx, "w", func(tx *Transaction) error {
			if _, err := tx.Exec(`UPDATE pairs SET a = a + 1 WHERE id = 1`); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE pairs SET b = b + 1 WHERE id = 1`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
}

// TestTextHoldingNULIsRefused: a text argument holding the NUL character is refused before it runs, and nothing is
// written; the same bytes as a BLOB are kept whole. The reason, pinned: a TEXT value that does hold a NUL (written
// past Exec, here by Maintain) comes back in the history cut at it, as an update's old value, because the pre-update hook reads TEXT
// up to its first NUL (modernc.org/sqlite pre_update_hook.go, libc.GoString). If that ever stops being true, the
// last check fails and the refusal can be reconsidered.
//
// CONTROL (04/11/2026): with the refusal taken out of Exec, "the text holding NUL was written: <nil>".
func TestTextHoldingNULIsRefused(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"))
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	insert := func(id int, title, data any) error {
		_, err := database.Write(ctx, "t", func(tx *Transaction) error {
			_, err := tx.Exec(`INSERT INTO notes (id, title, data) VALUES (?, ?, ?)`, id, title, data)
			return err
		})
		return err
	}
	if err := insert(1, "a\x00b", nil); !errors.Is(err, ErrStatementRefused) || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("the text holding NUL was written: %v", err)
	}
	if err := insert(2, "ab", []byte("a\x00b")); err != nil {
		t.Fatalf("a BLOB holding NUL: %v", err)
	}
	var rows int
	var data []byte
	err := database.Read(ctx, func(snapshot *Snapshot) error {
		result, err := snapshot.Query(`SELECT count(*), max(data) FROM notes`)
		if err != nil {
			return err
		}
		defer result.Close()
		result.Next()
		return result.Scan(&rows, &data)
	})
	if err != nil || rows != 1 || string(data) != "a\x00b" {
		t.Fatalf("after the refusal: %d rows, data %q (%v), want the BLOB row alone, whole", rows, data, err)
	}

	// The reason: a TEXT with a NUL in the file comes back cut in the history's Before.
	if _, err := database.Maintain(ctx, "t", func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `INSERT INTO notes (id, title) VALUES (3, 'x' || char(0) || 'y')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := database.Revision()
	if _, err := database.Write(ctx, "t", func(tx *Transaction) error {
		_, err := tx.Exec(`UPDATE notes SET title = 'z' WHERE id = 3`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var cut any
	if err := database.Changes(ctx, before, func(commit Commit) error {
		for _, change := range commit.Changes {
			if change.Table != "notes" || change.RowID != 3 {
				continue
			}
			for _, update := range change.Updates {
				if change.Columns[update.Position] == "title" {
					cut = update.Old
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if cut != "x" {
		t.Fatalf("the old value of a TEXT holding NUL is %q, want it cut at the NUL (\"x\"): the hook may read TEXT whole now", cut)
	}
}
