package work

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

func TestKitDBRemoteSearchParserOwnsOneTopLevelPredicate(t *testing.T) {
	all, err := parseKitSQL(
		`SELECT id, _score FROM products WHERE * SEARCH $1 AND stock > 0 LIMIT 10`,
		kitSQLBindings{positional: []value.Value{value.New("ban phim")}, named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if all.search == nil || len(all.search.fields) != 0 || all.search.text != "ban phim" ||
		all.predicate == nil || all.limit != 10 {
		t.Fatalf("all-field SEARCH parse = %#v", all)
	}
	bare, err := parseKitSQL(
		`SELECT id FROM products WHERE SEARCH 'compatible alias'`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || bare.search == nil || len(bare.search.fields) != 0 {
		t.Fatalf("bare SEARCH compatibility parse = %#v, %v", bare, err)
	}

	tuple, err := parseKitSQL(
		`SELECT id FROM products WHERE (name, brand, description) SEARCH 'logitech keyboard'`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if tuple.search == nil || fmt.Sprint(tuple.search.fields) != "[name brand description]" ||
		tuple.search.text != "logitech keyboard" || tuple.predicate != nil || tuple.whereExpression != nil {
		t.Fatalf("tuple SEARCH parse = %#v", tuple)
	}

	single, err := parseKitSQL(
		`EXPLAIN SELECT name FROM products WHERE name SEARCH 'cotton' ORDER BY _score DESC`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || single.kind != "explain" || single.search == nil ||
		fmt.Sprint(single.search.fields) != "[name]" {
		t.Fatalf("single-field SEARCH parse = %#v, %v", single, err)
	}

	for _, source := range []string{
		`SELECT id FROM products WHERE name SEARCH 42`,
		`SELECT id FROM products WHERE name SEARCH 'shirt' OR stock > 0`,
		`SELECT id FROM products WHERE NOT name SEARCH 'shirt'`,
		`SELECT id FROM products WHERE name SEARCH 'shirt' AND brand SEARCH 'kit'`,
		`UPDATE products SET stock = 0 WHERE name SEARCH 'shirt'`,
		`DELETE FROM products WHERE SEARCH 'shirt'`,
	} {
		if parsed, err := parseKitSQL(source, kitSQLBindings{named: map[string]value.Value{}}); err == nil {
			t.Fatalf("parseKitSQL(%q) unexpectedly succeeded: %#v", source, parsed)
		}
	}
}

func TestKitDBRemoteSearchSQLRanksFieldsAndFiltersRows(t *testing.T) {
	_, execute := newKitDBRemoteSearchTest(t)
	insertKitDBRemoteSearchFixtures(t, execute)

	folded, err := execute(`
SELECT id, name, _score, _snippet
FROM products
WHERE name SEARCH 'ao cotton'
ORDER BY _score DESC
LIMIT 10`)
	if err != nil {
		t.Fatal(err)
	}
	if len(folded.rows) != 1 || folded.rows[0][0].String() != "p1" || folded.rows[0][2].N <= 0 ||
		!strings.Contains(folded.rows[0][3].String(), "<b>") {
		t.Fatalf("folded ranked SEARCH = %#v", folded)
	}
	if got := []string{folded.columns[0].name, folded.columns[1].name, folded.columns[2].name, folded.columns[3].name}; fmt.Sprint(got) != "[id name _score _snippet]" {
		t.Fatalf("SEARCH columns = %v", got)
	}

	descriptionOnly, err := execute(`SELECT id FROM products WHERE description SEARCH 'cotton' LIMIT 10`)
	if err != nil || len(descriptionOnly.rows) != 1 || descriptionOnly.rows[0][0].String() != "p2" {
		t.Fatalf("field-specific SEARCH = %#v, %v", descriptionOnly, err)
	}
	tuple, err := execute(`
SELECT id, _score AS relevance
FROM products
WHERE (name, brand) SEARCH 'ban phim logitech'
ORDER BY relevance DESC
LIMIT 10`)
	if err != nil || len(tuple.rows) != 1 || tuple.rows[0][0].String() != "p3" || tuple.rows[0][1].N <= 0 {
		t.Fatalf("tuple SEARCH = %#v, %v", tuple, err)
	}

	filtered, err := execute(`
SELECT id, name, _score
FROM products
WHERE * SEARCH 'logitech' AND stock > 0
LIMIT 10`)
	if err != nil || len(filtered.rows) != 1 || filtered.rows[0][0].String() != "p3" {
		t.Fatalf("SEARCH row filter = %#v, %v", filtered, err)
	}

	explained, err := execute(`
EXPLAIN QUERY PLAN SELECT id FROM products
WHERE (name, brand) SEARCH 'logitech' AND stock > 0 LIMIT 10`)
	if err != nil || len(explained.rows) != 1 {
		t.Fatalf("SEARCH EXPLAIN = %#v, %v", explained, err)
	}
	detail := explained.rows[0][3].String()
	if !strings.Contains(detail, "KITDB SEARCH products USING BM25") ||
		!strings.Contains(detail, "BOUNDED ROW FILTER") || !strings.Contains(detail, "CANDIDATES 200") {
		t.Fatalf("SEARCH EXPLAIN detail = %q", detail)
	}

	like, err := execute(`SELECT id FROM products WHERE name LIKE 'Áo%'`)
	if err != nil || len(like.rows) != 1 || like.rows[0][0].String() != "p1" {
		t.Fatalf("LIKE semantics changed = %#v, %v", like, err)
	}
	if _, err := execute(`SELECT id FROM products WHERE stock SEARCH '5'`); err == nil ||
		!strings.Contains(err.Error(), "not searchable") {
		t.Fatalf("non-searchable field error = %v", err)
	}
	if _, err := execute(`SELECT id FROM products WHERE SEARCH 'logitech' ORDER BY name`); err == nil ||
		!strings.Contains(err.Error(), "ORDER BY _score DESC") {
		t.Fatalf("unsafe SEARCH order error = %v", err)
	}
}

func TestKitDBRemoteSearchResidualFilterFailsClosedBeyondCandidateWindow(t *testing.T) {
	_, execute := newKitDBRemoteSearchTest(t)
	var statement strings.Builder
	statement.WriteString(`INSERT INTO products (id, name, brand, description, stock, price) VALUES `)
	for index := 0; index < searchMaximumLimit+1; index++ {
		if index != 0 {
			statement.WriteByte(',')
		}
		fmt.Fprintf(
			&statement,
			"('b%03d','Common item %03d','Kit','common catalog record',0,%d)",
			index, index, index,
		)
	}
	if _, err := execute(statement.String()); err != nil {
		t.Fatal(err)
	}
	if result, err := execute(`SELECT id FROM products WHERE SEARCH 'common' LIMIT 1`); err != nil || len(result.rows) != 1 {
		t.Fatalf("bounded unfiltered SEARCH = %#v, %v", result, err)
	}
	if _, err := execute(`SELECT id FROM products WHERE SEARCH 'common' AND stock > 0 LIMIT 1`); err == nil ||
		!strings.Contains(err.Error(), "bounded 200-candidate window") {
		t.Fatalf("broad residual SEARCH error = %v", err)
	}
}

func TestKitDBRemoteSearchPushesLeadingCompositeKeyFilterIntoTopK(t *testing.T) {
	_, execute := newKitDBRemoteCompositeSearchTest(t)
	insert := func(rows []string) {
		t.Helper()
		source := `INSERT INTO shopping (merchant, id, name, description) VALUES ` +
			strings.Join(rows, ",")
		if _, err := execute(source); err != nil {
			t.Fatal(err)
		}
	}
	tiki := make([]string, 0, searchMaximumLimit+1)
	for index := 0; index < searchMaximumLimit+1; index++ {
		tiki = append(tiki, fmt.Sprintf(
			"('tiki',%d,'highlands highlands coffee coffee','highlands coffee promotion')",
			index+1,
		))
	}
	insert(tiki)
	shopee := make([]string, 0, 130)
	for index := 0; index < 130; index++ {
		shopee = append(shopee, fmt.Sprintf(
			"('shopee',%d,'highlands coffee','local merchant result')",
			index+1,
		))
	}
	insert(shopee)

	queries := []string{
		`SELECT merchant, id, _score FROM shopping
WHERE * SEARCH 'highlands coffee' AND merchant = 'shopee'
ORDER BY _score DESC LIMIT 120`,
		`SELECT merchant, id, _score FROM shopping
WHERE merchant = 'shopee' AND * SEARCH 'highlands coffee'
ORDER BY _score DESC LIMIT 120`,
	}
	for _, source := range queries {
		result, err := execute(source)
		if err != nil {
			t.Fatalf("composite-key SEARCH %q: %v", source, err)
		}
		if len(result.rows) != 120 {
			t.Fatalf("composite-key SEARCH rows = %d, want 120", len(result.rows))
		}
		for _, row := range result.rows {
			if row[0].String() != "shopee" || row[2].N <= 0 {
				t.Fatalf("composite-key SEARCH row = %#v", row)
			}
		}
	}

	explained, err := execute(`EXPLAIN SELECT merchant FROM shopping
WHERE * SEARCH 'highlands coffee' AND merchant = 'shopee' LIMIT 120`)
	if err != nil || len(explained.rows) != 1 {
		t.Fatalf("composite-key SEARCH EXPLAIN = %#v, %v", explained, err)
	}
	if detail := explained.rows[0][3].String(); !strings.Contains(detail, "IDENTIFIER PREFIX FILTER") {
		t.Fatalf("composite-key SEARCH EXPLAIN detail = %q", detail)
	}
}

func TestKitDBPostgresSearchUsesBoundParameters(t *testing.T) {
	tenant, _ := newKitDBRemoteSearchTest(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- tenant.ServeKitDBPostgres(ctx, listener, KitDBPostgresOptions{
			MaintenanceDatabase: "kitdb", User: "kitdb", MaxConnections: 4,
			IdleTimeout: 5 * time.Second, QueryTimeout: 10 * time.Second,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-serverDone:
			if serveErr != nil {
				t.Errorf("ServeKitDBPostgres SEARCH: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServeKitDBPostgres SEARCH did not stop")
		}
	})

	database := openKitDBPostgresDatabaseTestClient(t, listener.Addr().String(), "searchsql", "search-secret")
	defer database.Close()
	queryCtx, queryCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer queryCancel()
	if _, err := database.ExecContext(queryCtx, `
INSERT INTO products (id, name, brand, description, stock, price)
VALUES ('pg1', 'Bàn phím cơ', 'Logitech', 'switch tactile', 3, 120)`); err != nil {
		t.Fatal(err)
	}
	var id, snippet string
	var score float64
	if err := database.QueryRowContext(queryCtx, `
SELECT id, _score, _snippet
FROM products
WHERE (name, brand) SEARCH $1 AND stock > 0
LIMIT 10`, "ban phim logitech").Scan(&id, &score, &snippet); err != nil {
		t.Fatal(err)
	}
	if id != "pg1" || score <= 0 || !strings.Contains(snippet, "<b>") {
		t.Fatalf("PostgreSQL SEARCH row = (%q, %f, %q)", id, score, snippet)
	}
}

func newKitDBRemoteSearchTest(
	t *testing.T,
) (*Tenant, func(string) (kitDBRemoteResult, error)) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const products = struct({
  id: text().key(),
  name: text().notNull().searchable({ weight: 5 }),
  brand: text().notNull().searchable({ weight: 2 }),
  description: text().notNull().searchable(),
  stock: int().default(0),
  price: int().default(0)
});
const db = kitdb("searchsql.kitdb", { products }, { token: "search-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tenant.Close)
	_, config, found := resolveServe(tenant, "searchsql.kitdb")
	if !found || config.database == nil {
		t.Fatal("KitDB SEARCH serve registration is unavailable")
	}
	execute := func(source string) (kitDBRemoteResult, error) {
		return executeKitDBRemoteSQL(
			context.Background(), nil, config.database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}
	return tenant, execute
}

func newKitDBRemoteCompositeSearchTest(
	t *testing.T,
) (*Tenant, func(string) (kitDBRemoteResult, error)) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const shopping = struct({
  merchant: text().key(1),
  id: int().key(2),
  name: text().notNull().searchable({ weight: 5 }),
  description: text().notNull().searchable()
});
const db = kitdb("composite-search.kitdb", { shopping }, { token: "search-secret", access: "readwrite" });
router.get(() => db.shopping.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tenant.Close)
	_, config, found := resolveServe(tenant, "composite-search.kitdb")
	if !found || config.database == nil {
		t.Fatal("KitDB composite SEARCH serve registration is unavailable")
	}
	execute := func(source string) (kitDBRemoteResult, error) {
		return executeKitDBRemoteSQL(
			context.Background(), nil, config.database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}
	return tenant, execute
}

func insertKitDBRemoteSearchFixtures(
	t *testing.T,
	execute func(string) (kitDBRemoteResult, error),
) {
	t.Helper()
	_, err := execute(`
INSERT INTO products (id, name, brand, description, stock, price) VALUES
  ('p1', 'Áo thun cotton nam', 'Kit', 'Mềm nhẹ mặc hằng ngày', 5, 100),
  ('p2', 'Quần jean xanh', 'Denim', 'Cotton co giãn', 0, 80),
  ('p3', 'Bàn phím cơ', 'Logitech', 'Switch tactile cho gaming', 2, 120),
  ('p4', 'Chuột không dây', 'Logitech', 'Phụ kiện văn phòng', 0, 60)`)
	if err != nil {
		t.Fatal(err)
	}
}
