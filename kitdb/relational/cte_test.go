package relational

import (
	"context"
	"fmt"
	"strings"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestCommonTableExpressionsDerivedTablesAndAggregates(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	ctx := context.Background()

	query := `
		WITH expensive AS (
			SELECT merchant, id, title, price FROM products WHERE price >= $1
		), labeled(item_id, label, cost) AS (
			SELECT id, UPPER(title), price FROM expensive WHERE merchant = $2
		)
		SELECT item_id, label, cost FROM labeled ORDER BY item_id DESC LIMIT 2
	`
	columns, err := engine.Describe(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 3 || columns[0].Name != "item_id" || columns[1].Name != "label" ||
		columns[2].Kind != "decimal" {
		t.Fatalf("CTE description = %#v", columns)
	}
	result, err := engine.Execute(ctx, query, "15.00", "shopee")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 2 || result.Rows[0][0] != int64(3) || result.Rows[0][1] != "MONITOR" ||
		result.Rows[1][0] != int64(2) || result.Rows[1][2] != "20.5" {
		t.Fatalf("CTE rows = %#v", result.Rows)
	}

	aggregated, err := engine.Execute(ctx, `
		WITH chosen AS (
			SELECT merchant, price FROM products WHERE price >= 10
		)
		SELECT merchant, COUNT(*) AS items, SUM(price) AS total
		FROM chosen GROUP BY merchant ORDER BY merchant
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregated.Rows) != 2 || aggregated.Rows[0][0] != "lazada" || aggregated.Rows[0][1] != int64(1) ||
		aggregated.Rows[1][0] != "shopee" || aggregated.Rows[1][1] != int64(3) || aggregated.Rows[1][2] != "60.75" {
		t.Fatalf("materialized aggregate = %#v", aggregated.Rows)
	}

	derived, err := engine.Execute(ctx, `
		SELECT item_id, UPPER(label) AS label
		FROM (
			SELECT id AS item_id, title AS label FROM products WHERE merchant = 'shopee'
		) AS selected
		WHERE item_id >= 2 ORDER BY label
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(derived.Rows) != 2 || derived.Rows[0][1] != "MONITOR" || derived.Rows[1][1] != "MOUSE" {
		t.Fatalf("derived rows = %#v", derived.Rows)
	}

	compound, err := engine.Execute(ctx, `
		WITH identifiers AS (
			SELECT id FROM products WHERE merchant = 'shopee' AND id = 1
			UNION ALL SELECT 99
		)
		SELECT id FROM identifiers ORDER BY id DESC
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(compound.Rows) != 2 || compound.Rows[0][0] != int64(99) || compound.Rows[1][0] != int64(1) {
		t.Fatalf("compound CTE = %#v", compound.Rows)
	}

	empty, err := engine.Execute(ctx, `SELECT * FROM (SELECT 1 AS id LIMIT 0) AS none`)
	if err != nil || len(empty.Rows) != 0 {
		t.Fatalf("limited scalar derived table = %#v, %v", empty.Rows, err)
	}
}

func TestCommonTableExpressionUsesTransactionSnapshotAndReadYourWrites(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	ctx := context.Background()
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Execute(ctx, `
		INSERT INTO products (merchant, id, title, price) VALUES ('shopee', 9, 'Camera', 99.00)
	`); err != nil {
		t.Fatal(err)
	}
	result, err := transaction.Execute(ctx, `
		WITH chosen AS (SELECT id, title FROM products WHERE merchant = 'shopee')
		SELECT title FROM chosen WHERE id = 9
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0][0] != "Camera" {
		t.Fatalf("transaction-local CTE = %#v", result.Rows)
	}
}

func TestCommonTableExpressionFailsClosedAtSemanticAndResourceBounds(t *testing.T) {
	engine := openCTETestEngine(t, 3)
	ctx := context.Background()
	for _, test := range []struct {
		source string
		want   string
	}{
		{`WITH one AS (SELECT * FROM one) SELECT * FROM one`, "recursive or forward reference"},
		{`WITH one AS (SELECT * FROM two), two AS (SELECT 1 AS id) SELECT * FROM one`, "recursive or forward reference"},
		{`WITH one AS (SELECT id, id FROM products LIMIT 1) SELECT * FROM one`, "ambiguous output field"},
		{`WITH one(a) AS (SELECT id, title FROM products LIMIT 1) SELECT * FROM one`, "declares 1 columns but returns 2"},
		{`WITH one AS (SELECT 1 AS id LIMIT 4) SELECT * FROM one`, "LIMIT 4 exceeds this server's result limit of 3"},
		{`WITH one AS (SELECT id FROM products LIMIT 3), two AS (SELECT 1 AS id) SELECT * FROM one`, "3-row materialization budget"},
		{`WITH one AS (SELECT id FROM products LIMIT 1) SELECT * FROM one JOIN products p ON p.id = one.id`, "JOIN from common table"},
	} {
		if _, err := engine.Execute(ctx, test.source); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("Execute(%q) error = %v, want %q", test.source, err, test.want)
		}
	}

	budget := newMaterializationBudget(10)
	if err := budget.consume(Result{
		Columns: []Column{{Name: "payload", Kind: "text"}},
		Rows:    [][]any{{strings.Repeat("x", maximumMaterializedBytes)}},
	}); err == nil || !strings.Contains(err.Error(), "byte materialization budget") {
		t.Fatalf("byte budget error = %v", err)
	}
	cyclic := make([]any, 1)
	cyclic[0] = cyclic
	cyclicBudget := newMaterializationBudget(10)
	if err := cyclicBudget.consume(Result{
		Columns: []Column{{Name: "payload", Kind: "json"}}, Rows: [][]any{{cyclic}},
	}); err == nil || !strings.Contains(err.Error(), "byte materialization budget") {
		t.Fatalf("cyclic value budget error = %v", err)
	}
	columns := make([]Column, maximumMaterializedColumns+1)
	for index := range columns {
		columns[index] = Column{Name: "field_" + strings.Repeat("x", index), Kind: "text"}
	}
	if _, err := relationFromColumns("wide", columns, nil); err == nil || !strings.Contains(err.Error(), "exceeds 128 columns") {
		t.Fatalf("column budget error = %v", err)
	}
}

func TestCommonTableExpressionExplainAnalyze(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	result, err := engine.Execute(context.Background(), `
		EXPLAIN ANALYZE
		WITH chosen AS (SELECT id, title FROM products WHERE merchant = 'shopee')
		SELECT title FROM chosen WHERE id >= 2
	`)
	if err != nil {
		t.Fatal(err)
	}
	var materialization, direct, actual, rows bool
	for _, row := range result.Rows {
		switch row[1] {
		case "materialization":
			materialization = true
		case "actual":
			actual = strings.Contains(row[2].(string), "path=materialized-scan")
		case "rows":
			rows = strings.Contains(row[2].(string), "scanned=3 matched=2")
		case "materialization actual":
			direct = strings.Contains(row[2].(string), "direct_rows=3")
		}
	}
	if !materialization || !direct || !actual || !rows {
		t.Fatalf("CTE EXPLAIN ANALYZE = %#v", result.Rows)
	}
}

func TestDirectMaterializationAdmitsBeforeRetainingRow(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	ctx := context.Background()
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()

	statement, err := kitdbsql.ParseStatement(`SELECT title FROM products`)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := describeSelectFromCatalog(transaction.catalog, statement.Select, nil)
	if err != nil {
		t.Fatal(err)
	}
	budget := newMaterializationBudget(100)
	budget.maximumBytes = 250
	builder, err := newMaterializedRelationBuilder("chosen", columns, nil, budget, true)
	if err != nil {
		t.Fatal(err)
	}
	err = transaction.materializePhysicalRows(ctx, statement.Select, nil, builder)
	if err == nil || !strings.Contains(err.Error(), "250-byte materialization budget") {
		t.Fatalf("streaming materialization error = %v", err)
	}
	if got := len(builder.relation.rows); got != 1 {
		t.Fatalf("retained rows after admission failure = %d, want 1", got)
	}
	if budget.rows != 1 || budget.directRows != 1 {
		t.Fatalf("materialization accounting = rows:%d direct:%d", budget.rows, budget.directRows)
	}
}

func TestBufferedMaterializationAdmitsOrderWorkingSetBeforeRetaining(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	ctx := context.Background()
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()

	statement, err := kitdbsql.ParseStatement(`SELECT id, title FROM products ORDER BY title`)
	if err != nil {
		t.Fatal(err)
	}
	budget := newMaterializationBudget(100)
	budget.maximumBytes = 500
	_, err = transaction.materializeSelectInScope(
		ctx, "ordered", nil, statement.Select, nil, false, nil, budget,
	)
	if err == nil || !strings.Contains(err.Error(), "query memory budget while buffering ORDER BY") {
		t.Fatalf("buffered ORDER BY budget error = %v", err)
	}
	if budget.rows != 0 {
		t.Fatalf("retained materialized rows after working-set failure = %d, want 0", budget.rows)
	}
	if budget.workingBytes != 0 {
		t.Fatalf("working bytes after failed materialization = %d, want 0", budget.workingBytes)
	}
	if budget.peakBytes == 0 || budget.peakBytes > budget.maximumBytes {
		t.Fatalf("working-set peak = %d, budget = %d", budget.peakBytes, budget.maximumBytes)
	}
}

func TestBufferedIndexOrderRetainsOnlyProjectedFields(t *testing.T) {
	engine, err := OpenWithOptions(t.TempDir()+`\narrow-order.kitdb`, Options{MaximumResultRows: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE wide_rows (
			rank INTEGER PRIMARY KEY,
			id INTEGER NOT NULL,
			payload TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	for index := int64(1); index <= 3; index++ {
		if _, err := engine.Execute(
			ctx,
			`INSERT INTO wide_rows (rank, id, payload) VALUES ($1, $2, $3)`,
			index, index*10, strings.Repeat("x", 4096),
		); err != nil {
			t.Fatal(err)
		}
	}
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	statement, err := kitdbsql.ParseStatement(`
		SELECT id, rank FROM wide_rows ORDER BY rank LIMIT 3
	`)
	if err != nil {
		t.Fatal(err)
	}
	budget := newMaterializationBudget(100)
	budget.maximumBytes = 4000
	relation, err := transaction.materializeSelectInScope(
		ctx, "narrow", nil, statement.Select, nil, false, nil, budget,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(relation.rows) != 3 || relation.rows[0]["id"] != int64(10) ||
		relation.rows[2]["rank"] != int64(3) {
		t.Fatalf("narrow ordered rows = %#v", relation.rows)
	}
	if budget.rows != 3 || budget.peakBytes == 0 || budget.peakBytes > budget.maximumBytes {
		t.Fatalf(
			"narrow ordered materialization = rows:%d retained:%d peak:%d limit:%d",
			budget.rows, budget.bytes, budget.peakBytes, budget.maximumBytes,
		)
	}
}

func TestBufferedMaterializationReportsDistinctPeakMemory(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	ctx := context.Background()
	query := `
		WITH merchants AS (
			SELECT DISTINCT merchant FROM products ORDER BY merchant
		)
		SELECT merchant FROM merchants ORDER BY merchant
	`
	selected, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Rows) != 2 || selected.Rows[0][0] != "lazada" || selected.Rows[1][0] != "shopee" {
		t.Fatalf("buffered DISTINCT rows = %#v", selected.Rows)
	}

	explained, err := engine.Execute(ctx, "EXPLAIN ANALYZE "+query)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range explained.Rows {
		if row[1] != "materialization actual" {
			continue
		}
		var rows, direct, retained, peak int
		if _, err := fmt.Sscanf(
			row[2].(string),
			"rows=%d direct_rows=%d retained_bytes=%d peak_bytes=%d",
			&rows, &direct, &retained, &peak,
		); err != nil {
			t.Fatalf("materialization telemetry %q: %v", row[2], err)
		}
		if rows != 2 || direct != 0 || retained <= 0 || peak <= retained {
			t.Fatalf(
				"materialization telemetry = rows:%d direct:%d retained:%d peak:%d",
				rows, direct, retained, peak,
			)
		}
		found = true
	}
	if !found {
		t.Fatalf("missing materialization telemetry in %#v", explained.Rows)
	}
}

func TestMaterializationWorkingSetReplacementReleasesReservation(t *testing.T) {
	budget := newMaterializationBudget(10)
	budget.maximumBytes = 256
	working := newMaterializationWorkingSet(budget)
	if err := working.reserve(200, "ORDER BY Top-N candidates"); err != nil {
		t.Fatal(err)
	}
	if err := working.replace(200, 50, "ORDER BY Top-N candidates"); err != nil {
		t.Fatal(err)
	}
	if working.bytes != 50 || budget.workingBytes != 50 || budget.peakBytes != 200 {
		t.Fatalf(
			"replacement accounting = working:%d budget:%d peak:%d",
			working.bytes, budget.workingBytes, budget.peakBytes,
		)
	}
	if err := budget.admitRetained(206); err != nil {
		t.Fatal(err)
	}
	if err := working.reserve(1, "ORDER BY Top-N candidates"); err == nil {
		t.Fatal("working set exceeded the shared byte ceiling")
	}
	working.close()
	if working.bytes != 0 || budget.workingBytes != 0 || budget.peakBytes != 256 {
		t.Fatalf(
			"closed accounting = working:%d budget:%d peak:%d",
			working.bytes, budget.workingBytes, budget.peakBytes,
		)
	}
}

func TestBufferedAggregateAdmitsGroupStateBeforeRetaining(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	ctx := context.Background()
	grouped, err := engine.Execute(ctx, `
		WITH item_counts AS (
			SELECT id, COUNT(*) AS items FROM products GROUP BY id
		)
		SELECT id, items FROM item_counts ORDER BY id
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Rows) != 3 || grouped.Rows[0][0] != int64(1) || grouped.Rows[0][1] != int64(2) {
		t.Fatalf("buffered GROUP BY rows = %#v", grouped.Rows)
	}
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()

	statement, err := kitdbsql.ParseStatement(`
		SELECT merchant, COUNT(*) AS items FROM products GROUP BY merchant
	`)
	if err != nil {
		t.Fatal(err)
	}
	budget := newMaterializationBudget(100)
	budget.maximumBytes = 500
	_, err = transaction.materializeSelectInScope(
		ctx, "summary", nil, statement.Select, nil, false, nil, budget,
	)
	if err == nil || !strings.Contains(err.Error(), "query memory budget while buffering GROUP BY state") {
		t.Fatalf("GROUP BY budget error = %v", err)
	}
	if budget.rows != 0 || budget.workingBytes != 0 {
		t.Fatalf("GROUP BY failure retained rows=%d working=%d", budget.rows, budget.workingBytes)
	}
	if budget.peakBytes == 0 || budget.peakBytes > budget.maximumBytes {
		t.Fatalf("GROUP BY peak = %d, budget = %d", budget.peakBytes, budget.maximumBytes)
	}
}

func TestAggregateStateAdmissionPrecedesMinimumReplacement(t *testing.T) {
	budget := newMaterializationBudget(10)
	budget.maximumBytes = 100
	working := newMaterializationWorkingSet(budget)
	state := aggregateState{}
	err := updateAggregateStateAccounted(
		working, &state, "min", strings.Repeat("x", 200), &kitdbsql.Field{Kind: "text"}, false,
	)
	if err == nil || !strings.Contains(err.Error(), "while buffering GROUP BY aggregate state") {
		t.Fatalf("aggregate state budget error = %v", err)
	}
	if state.has || state.value != nil || state.retainedBytes != 0 || working.bytes != 0 {
		t.Fatalf("aggregate state changed after failed admission: %#v working=%d", state, working.bytes)
	}
}

func TestBufferedJoinAdmitsScratchBeforeRetaining(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE merchants (name TEXT PRIMARY KEY, label TEXT NOT NULL)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO merchants (name, label) VALUES ('shopee', 'Shopee'), ('lazada', 'Lazada')
	`); err != nil {
		t.Fatal(err)
	}
	joined, err := engine.Execute(ctx, `
		WITH enriched AS (
			SELECT p.id, p.title, m.label
			FROM products p JOIN merchants m ON m.name = p.merchant
		)
		SELECT id, title, label FROM enriched WHERE label = 'Shopee' ORDER BY id
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(joined.Rows) != 3 || joined.Rows[0][1] != "Keyboard" || joined.Rows[2][1] != "Monitor" {
		t.Fatalf("buffered JOIN rows = %#v", joined.Rows)
	}
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	statement, err := kitdbsql.ParseStatement(`
		SELECT p.id, p.title, m.label
		FROM products p JOIN merchants m ON m.name = p.merchant
	`)
	if err != nil {
		t.Fatal(err)
	}
	budget := newMaterializationBudget(100)
	budget.maximumBytes = 500
	_, err = transaction.materializeSelectInScope(
		ctx, "enriched", nil, statement.Select, nil, false, nil, budget,
	)
	if err == nil || !strings.Contains(err.Error(), "query memory budget while buffering JOIN source row") {
		t.Fatalf("JOIN budget error = %v", err)
	}
	if budget.rows != 0 || budget.workingBytes != 0 {
		t.Fatalf("JOIN failure retained rows=%d working=%d", budget.rows, budget.workingBytes)
	}
}

func TestBufferedUnionAdmitsBranchReferencesBeforeRetaining(t *testing.T) {
	engine := openCTETestEngine(t, 100)
	ctx := context.Background()
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	statement, err := kitdbsql.ParseStatement(`SELECT 1 AS id UNION ALL SELECT 2`)
	if err != nil {
		t.Fatal(err)
	}
	budget := newMaterializationBudget(100)
	budget.maximumBytes = 190
	_, err = transaction.materializeSelectInScope(
		ctx, "identifiers", nil, statement.Select, nil, false, nil, budget,
	)
	if err == nil || !strings.Contains(err.Error(), "query memory budget while buffering UNION ALL branch row references") {
		t.Fatalf("UNION ALL budget error = %v", err)
	}
	if budget.rows != 0 || budget.workingBytes != 0 {
		t.Fatalf("UNION ALL failure retained rows=%d working=%d", budget.rows, budget.workingBytes)
	}
}

func openCTETestEngine(t *testing.T, maximumRows int) *Engine {
	t.Helper()
	engine, err := OpenWithOptions(t.TempDir()+`\cte.kitdb`, Options{MaximumResultRows: maximumRows})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE products (merchant TEXT NOT NULL, id INTEGER NOT NULL, title TEXT NOT NULL, price DECIMAL(10,2) NOT NULL, PRIMARY KEY (merchant, id))`,
		`INSERT INTO products (merchant, id, title, price) VALUES
			('shopee', 1, 'Keyboard', 10.00),
			('shopee', 2, 'Mouse', 20.50),
			('shopee', 3, 'Monitor', 30.25),
			('lazada', 1, 'Desk', 40.00)`,
	} {
		if _, err := engine.Execute(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	return engine
}
