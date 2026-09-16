package sql

import "testing"

func TestParseStandaloneAnalyticsStatusPragma(t *testing.T) {
	for _, test := range []struct {
		source string
		table  string
	}{
		{`PRAGMA analytics_status(products)`, "products"},
		{`pragma ANALYTICS_STATUS('shopping')`, "shopping"},
		{`PRAGMA analytics_status(public.events);`, "events"},
	} {
		parsed, err := ParseStatement(test.source)
		if err != nil {
			t.Fatalf("%s: %v", test.source, err)
		}
		if parsed.Kind != StatementPragma || parsed.Pragma == nil ||
			parsed.Pragma.Name != "analytics_status" || parsed.Pragma.Argument != test.table {
			t.Fatalf("%s: %#v", test.source, parsed.Pragma)
		}
	}
	for _, source := range []string{
		`PRAGMA analytics_status`,
		`PRAGMA analytics_status()`,
		`PRAGMA analytics_status(products, events)`,
		`PRAGMA analytics_status(1)`,
	} {
		if _, err := ParseStatement(source); err == nil {
			t.Fatalf("accepted invalid PRAGMA: %s", source)
		}
	}
}

func TestParseStandaloneCreateInsertAndSelect(t *testing.T) {
	created, err := ParseStatement(`
		CREATE TABLE public.products (
			merchant TEXT NOT NULL,
			id INTEGER NOT NULL,
			status CHOICE('active', 'disabled') DEFAULT 'active',
			title TEXT,
			PRIMARY KEY (merchant, id),
			UNIQUE (merchant, title)
		) STRICT;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if created.CreateTable == nil || created.CreateTable.Name != "products" ||
		len(created.CreateTable.Columns) != 4 || len(created.CreateTable.PrimaryKey) != 2 ||
		len(created.CreateTable.Unique) != 1 {
		t.Fatalf("CREATE plan = %#v", created.CreateTable)
	}
	if got := created.CreateTable.Columns[2]; got.Type.Kind != "enum" ||
		len(got.Choices) != 2 || !got.HasDefault || got.Default.Text != "active" {
		t.Fatalf("choice column = %#v", got)
	}

	inserted, err := ParseStatement(`
		INSERT INTO public.products (merchant, id, title)
		VALUES ($1, $2, 'Keyboard'), ($1, $3, 'Mouse')
		RETURNING merchant, id;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if inserted.Insert == nil || len(inserted.Insert.Rows) != 2 ||
		inserted.Insert.Rows[1][1].Parameter != 3 || len(inserted.Insert.Returning) != 2 {
		t.Fatalf("INSERT plan = %#v", inserted.Insert)
	}

	selected, err := ParseStatement(`
		SELECT merchant, id FROM public.products
		WHERE merchant = $1 AND id >= 10
		ORDER BY id DESC LIMIT 20 OFFSET 2;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Select == nil || selected.Select.Table != "products" ||
		len(selected.Select.Conditions) != 2 || len(selected.Select.Order) != 1 ||
		selected.Select.Limit != 20 || selected.Select.Offset != 2 {
		t.Fatalf("SELECT plan = %#v", selected.Select)
	}

	updated, err := ParseStatement(`
		UPDATE public.products SET title = $1, status = 'disabled'
		WHERE merchant = $2 AND id = $3 RETURNING id, title
	`)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Update == nil || updated.Update.Table != "products" ||
		len(updated.Update.Assignments) != 2 || updated.Update.Assignments[0].Value.Parameter != 1 ||
		len(updated.Update.Conditions) != 2 || len(updated.Update.Returning) != 2 {
		t.Fatalf("UPDATE plan = %#v", updated.Update)
	}

	deleted, err := ParseStatement(`
		DELETE FROM public.products WHERE merchant = $1 AND id = $2 RETURNING *
	`)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Delete == nil || deleted.Delete.Table != "products" ||
		len(deleted.Delete.Conditions) != 2 || len(deleted.Delete.Returning) != 1 ||
		!deleted.Delete.Returning[0].All {
		t.Fatalf("DELETE plan = %#v", deleted.Delete)
	}
}

func TestParseStandaloneSearchAndCursor(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT merchant, id, name, _score, _snippet, _cursor
		FROM shopping
		WHERE (name, brand, description) SEARCH $1
		  AND merchant = 'shopee'
		ORDER BY _score DESC
		LIMIT 50 AFTER $2
	`)
	if err != nil {
		t.Fatal(err)
	}
	plan := parsed.Select
	if plan == nil || plan.Search == nil || len(plan.Search.Fields) != 3 ||
		plan.Search.Query.Parameter != 1 || plan.Predicate == nil || len(plan.Conditions) != 1 ||
		!plan.HasAfter || plan.After.Parameter != 2 || plan.Limit != 50 {
		t.Fatalf("SEARCH plan = %#v", plan)
	}
	if plan.Search.Fields[0] != "name" || plan.Search.Fields[2] != "description" {
		t.Fatalf("SEARCH fields = %#v", plan.Search.Fields)
	}

	wildcard, err := ParseStatement(`SELECT * FROM shopping WHERE * SEARCH 'keyboard' LIMIT 20`)
	if err != nil || wildcard.Select.Search == nil || len(wildcard.Select.Search.Fields) != 0 {
		t.Fatalf("wildcard SEARCH = %#v, %v", wildcard.Select, err)
	}
	if _, err := ParseStatement(`SELECT * FROM shopping WHERE name SEARCH 'x' OR id = 1`); err == nil {
		t.Fatal("SEARCH under OR unexpectedly succeeded")
	}
}

func TestParseStandaloneSearchableColumn(t *testing.T) {
	parsed, err := ParseStatement(`
		CREATE TABLE products (
			id KITID PRIMARY KEY,
			name TEXT SEARCHABLE WEIGHT 3,
			description TEXT SEARCHABLE
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	columns := parsed.CreateTable.Columns
	if !columns[1].Searchable || columns[1].SearchWeight != 3 ||
		!columns[2].Searchable || columns[2].SearchWeight != 1 {
		t.Fatalf("searchable columns = %#v", columns)
	}
}

func TestParseStandaloneAnalyticsColumn(t *testing.T) {
	parsed, err := ParseStatement(`
		CREATE TABLE clicks (
			id BIGINT PRIMARY KEY,
			utm TEXT ANALYTICS,
			status CHOICE('active', 'disabled') ANALYTICS
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	columns := parsed.CreateTable.Columns
	if columns[0].Analytics || !columns[1].Analytics || !columns[2].Analytics {
		t.Fatalf("analytics columns = %#v", columns)
	}
	if _, err := ParseStatement(`CREATE TABLE invalid (id INTEGER PRIMARY KEY, payload JSONB ANALYTICS)`); err == nil {
		t.Fatal("unsupported analytics type was accepted")
	}
}

func TestParseStandaloneNumericModifiersAndCast(t *testing.T) {
	parsed, err := ParseStatement(`
		CREATE TABLE ledger (
			id BIGINT PRIMARY KEY,
			amount NUMERIC(12,4),
			ratio DECIMAL(8)
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	if amount := parsed.CreateTable.Columns[1]; amount.Type.Kind != "decimal" ||
		amount.Precision != 12 || amount.Scale != 4 {
		t.Fatalf("NUMERIC modifier = %#v", amount)
	}
	if ratio := parsed.CreateTable.Columns[2]; ratio.Precision != 8 || ratio.Scale != 0 {
		t.Fatalf("DECIMAL modifier = %#v", ratio)
	}
	selected, err := ParseStatement(`SELECT CAST(amount AS NUMERIC(10,2)) AS rounded FROM ledger`)
	if err != nil {
		t.Fatal(err)
	}
	cast := selected.Select.Projection[0].Expression
	if cast == nil || cast.Kind != "cast" || cast.CastKind != "decimal" || cast.Precision != 10 || cast.Scale != 2 {
		t.Fatalf("CAST plan = %#v", cast)
	}
	for _, source := range []string{
		`CREATE TABLE invalid (id INTEGER PRIMARY KEY, n NUMERIC(0,0))`,
		`CREATE TABLE invalid (id INTEGER PRIMARY KEY, n NUMERIC(10,11))`,
		`CREATE TABLE invalid (id INTEGER PRIMARY KEY, n NUMERIC(1001,1))`,
		`CREATE TABLE invalid (id INTEGER PRIMARY KEY, n NUMERIC(10.5,2))`,
	} {
		if _, err := ParseStatement(source); err == nil {
			t.Fatalf("accepted invalid numeric modifier: %s", source)
		}
	}
}

func TestParseStandaloneCharacterLengthsAndCasts(t *testing.T) {
	parsed, err := ParseStatement(`
		CREATE TABLE labels (
			id BIGINT PRIMARY KEY,
			slug VARCHAR(64),
			code CHARACTER(4),
			native_char CHAR,
			note CHARACTER VARYING
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	columns := parsed.CreateTable.Columns
	if columns[1].Type.Kind != "varchar" || columns[1].TextLength == nil || *columns[1].TextLength != 64 ||
		columns[2].Type.Kind != "char" || columns[2].TextLength == nil || *columns[2].TextLength != 4 ||
		columns[3].TextLength == nil || *columns[3].TextLength != 1 || columns[4].TextLength != nil {
		t.Fatalf("character modifiers = %#v", columns)
	}

	selected, err := ParseStatement(`SELECT CAST('abcdef' AS VARCHAR(3)), CAST('xy' AS CHAR(4))`)
	if err != nil {
		t.Fatal(err)
	}
	for index, length := range []int{3, 4} {
		cast := selected.Select.Projection[index].Expression
		if cast == nil || cast.Kind != "cast" || cast.TextLength == nil || *cast.TextLength != length {
			t.Fatalf("CAST %d = %#v", index, cast)
		}
	}

	for _, source := range []string{
		`CREATE TABLE invalid (id BIGINT PRIMARY KEY, value VARCHAR(0))`,
		`CREATE TABLE invalid (id BIGINT PRIMARY KEY, value CHAR(10485761))`,
		`CREATE TABLE invalid (id BIGINT PRIMARY KEY, value VARCHAR(2,3))`,
		`CREATE TABLE invalid (id BIGINT PRIMARY KEY, value TEXT(3))`,
	} {
		if _, err := ParseStatement(source); err == nil {
			t.Fatalf("accepted invalid character modifier: %s", source)
		}
	}
}

func TestParseStandaloneUUIDTypedLiteralAndCast(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT UUID 'A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11',
		       CAST('a0eebc999c0b4ef8bb6d6bb9bd380a11' AS UUID)
	`)
	if err != nil {
		t.Fatal(err)
	}
	for index, projection := range parsed.Select.Projection {
		expression := projection.Expression
		if expression == nil || expression.Kind != "cast" || expression.CastKind != "uuid" ||
			len(expression.Arguments) != 1 || expression.Arguments[0].Literal.Kind != LiteralString {
			t.Fatalf("UUID expression %d = %#v", index, expression)
		}
	}
}

func TestParseStandaloneAlterColumnSearchable(t *testing.T) {
	set, err := ParseStatement(`ALTER TABLE products ALTER COLUMN title SET SEARCHABLE WEIGHT 7`)
	if err != nil {
		t.Fatal(err)
	}
	if set.AlterTable == nil || set.AlterTable.Action != AlterTableSetSearchable ||
		set.AlterTable.OldName != "title" || set.AlterTable.SearchWeight != 7 {
		t.Fatalf("SET SEARCHABLE plan = %#v", set.AlterTable)
	}
	drop, err := ParseStatement(`ALTER TABLE products ALTER title DROP SEARCHABLE`)
	if err != nil {
		t.Fatal(err)
	}
	if drop.AlterTable == nil || drop.AlterTable.Action != AlterTableDropSearchable ||
		drop.AlterTable.OldName != "title" {
		t.Fatalf("DROP SEARCHABLE plan = %#v", drop.AlterTable)
	}
}

func TestParseStandaloneAlterColumnAnalytics(t *testing.T) {
	set, err := ParseStatement(`ALTER TABLE clicks ALTER COLUMN utm SET ANALYTICS`)
	if err != nil || set.AlterTable == nil || set.AlterTable.Action != AlterTableSetAnalytics || set.AlterTable.OldName != "utm" {
		t.Fatalf("SET ANALYTICS plan = %#v, %v", set.AlterTable, err)
	}
	drop, err := ParseStatement(`ALTER TABLE clicks ALTER utm DROP ANALYTICS`)
	if err != nil || drop.AlterTable == nil || drop.AlterTable.Action != AlterTableDropAnalytics || drop.AlterTable.OldName != "utm" {
		t.Fatalf("DROP ANALYTICS plan = %#v, %v", drop.AlterTable, err)
	}
}

func TestStandaloneParserRejectsUnimplementedAndKeylessDDL(t *testing.T) {
	for _, source := range []string{
		`CREATE TABLE products (title TEXT)`,
		`CREATE TABLE products (id INTEGER PRIMARY KEY, owner INTEGER REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED)`,
	} {
		if _, err := ParseStatement(source); err == nil {
			t.Fatalf("ParseStatement(%q) unexpectedly succeeded", source)
		}
	}
}

func TestParseStandaloneUpdateExpressions(t *testing.T) {
	parsed, err := ParseStatement(`
		UPDATE products
		SET price = price + 2 * $1, label = label || '-updated'
		WHERE id = 1
		RETURNING price, label
	`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Update == nil || len(parsed.Update.Assignments) != 2 {
		t.Fatalf("UPDATE plan = %#v", parsed.Update)
	}
	price := parsed.Update.Assignments[0].Expression
	if price == nil || price.Operator != "+" || len(price.Arguments) != 2 ||
		price.Arguments[1].Operator != "*" {
		t.Fatalf("price expression = %#v", price)
	}
	label := parsed.Update.Assignments[1].Expression
	if label == nil || label.Operator != "||" {
		t.Fatalf("label expression = %#v", label)
	}
}

func TestParseStandaloneScalarProjectionExpressions(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT id,
		       price * quantity AS total,
		       UPPER(title) label,
		       COALESCE(note, 'none') AS note
		FROM products
		WHERE price * quantity BETWEEN 10 AND 50
		ORDER BY total DESC, id
		LIMIT 20
	`)
	if err != nil {
		t.Fatal(err)
	}
	plan := parsed.Select
	if plan == nil || len(plan.Projection) != 4 || plan.Projection[0].Name != "id" {
		t.Fatalf("SELECT plan = %#v", plan)
	}
	if expression := plan.Projection[1].Expression; expression == nil || expression.Operator != "*" ||
		plan.Projection[1].Alias != "total" {
		t.Fatalf("total projection = %#v", plan.Projection[1])
	}
	if expression := plan.Projection[2].Expression; expression == nil || expression.Operator != "upper" ||
		plan.Projection[2].Alias != "label" {
		t.Fatalf("label projection = %#v", plan.Projection[2])
	}
	if plan.Predicate == nil || plan.Predicate.Operator != "between" || len(plan.Order) != 2 ||
		plan.Order[0].Column != "total" || !plan.Order[0].Descending {
		t.Fatalf("predicate/order = %#v / %#v", plan.Predicate, plan.Order)
	}
}

func TestParseStandaloneForeignKeyAndCheck(t *testing.T) {
	parsed, err := ParseStatement(`
		CREATE TABLE products (
			id INTEGER PRIMARY KEY,
			owner_id INTEGER REFERENCES users(id) ON DELETE RESTRICT,
			price DECIMAL CONSTRAINT price_positive CHECK (price > 0),
			discount DECIMAL,
			CONSTRAINT discount_range CHECK (
				discount IS NULL OR (discount >= 0 AND discount <= price)
			)
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	plan := parsed.CreateTable
	if plan == nil || len(plan.ForeignKeys) != 1 || len(plan.Checks) != 2 {
		t.Fatalf("constraint plan = %#v", plan)
	}
	if plan.ForeignKeys[0].TargetTable != "users" || plan.ForeignKeys[0].OnDelete != "restrict" {
		t.Fatalf("foreign key = %#v", plan.ForeignKeys[0])
	}
	if plan.Checks[1].Name != "discount_range" || plan.Checks[1].Expression.Operator != "or" {
		t.Fatalf("check = %#v", plan.Checks[1])
	}
}

func TestParseStandaloneCreateAndDropIndex(t *testing.T) {
	reindexed, err := ParseStatement(`REINDEX TABLE public.products`)
	if err != nil || reindexed.Reindex == nil || reindexed.Reindex.Table != "products" {
		t.Fatalf("REINDEX TABLE plan = %#v, %v", reindexed.Reindex, err)
	}
	created, err := ParseStatement(`
		CREATE INDEX IF NOT EXISTS products_merchant_id_idx
		ON public.products (merchant, id)
		WHERE status = 'active' AND deleted_at IS NULL
	`)
	if err != nil {
		t.Fatal(err)
	}
	if created.CreateIndex == nil || created.CreateIndex.Name != "products_merchant_id_idx" ||
		created.CreateIndex.Table != "products" || len(created.CreateIndex.Columns) != 2 ||
		len(created.CreateIndex.Conditions) != 2 || !created.CreateIndex.IfNotExists {
		t.Fatalf("CREATE INDEX plan = %#v", created.CreateIndex)
	}
	unique, err := ParseStatement(`CREATE UNIQUE INDEX products_sku_key ON products (merchant, sku)`)
	if err != nil || unique.CreateIndex == nil || !unique.CreateIndex.Unique {
		t.Fatalf("CREATE UNIQUE INDEX = %#v, %v", unique.CreateIndex, err)
	}
	dropped, err := ParseStatement(`DROP INDEX IF EXISTS public.products_merchant_id_idx`)
	if err != nil || dropped.DropIndex == nil || !dropped.DropIndex.IfExists ||
		dropped.DropIndex.Name != "products_merchant_id_idx" {
		t.Fatalf("DROP INDEX plan = %#v, %v", dropped.DropIndex, err)
	}
	droppedTable, err := ParseStatement(`DROP TABLE IF EXISTS public.products RESTRICT`)
	if err != nil || droppedTable.DropTable == nil || !droppedTable.DropTable.IfExists ||
		droppedTable.DropTable.Name != "products" {
		t.Fatalf("DROP TABLE plan = %#v, %v", droppedTable.DropTable, err)
	}
}

func TestParseStandaloneMetadataSafeAlterTable(t *testing.T) {
	added, err := ParseStatement(`ALTER TABLE public.products ADD COLUMN note TEXT`)
	if err != nil || added.AlterTable == nil || added.AlterTable.Action != AlterTableAddColumn ||
		added.AlterTable.Column == nil || added.AlterTable.Column.Name != "note" {
		t.Fatalf("ADD COLUMN = %#v, %v", added.AlterTable, err)
	}
	renamed, err := ParseStatement(`ALTER TABLE products RENAME COLUMN title TO name`)
	if err != nil || renamed.AlterTable == nil || renamed.AlterTable.Action != AlterTableRenameColumn ||
		renamed.AlterTable.OldName != "title" || renamed.AlterTable.NewName != "name" {
		t.Fatalf("RENAME COLUMN = %#v, %v", renamed.AlterTable, err)
	}
	dropped, err := ParseStatement(`ALTER TABLE products DROP COLUMN IF EXISTS note RESTRICT`)
	if err != nil || dropped.AlterTable == nil || dropped.AlterTable.Action != AlterTableDropColumn ||
		!dropped.AlterTable.IfExists {
		t.Fatalf("DROP COLUMN = %#v, %v", dropped.AlterTable, err)
	}
	renamedTable, err := ParseStatement(`ALTER TABLE products RENAME TO inventory`)
	if err != nil || renamedTable.AlterTable == nil || renamedTable.AlterTable.Action != AlterTableRenameTable ||
		renamedTable.AlterTable.NewName != "inventory" {
		t.Fatalf("RENAME TABLE = %#v, %v", renamedTable.AlterTable, err)
	}
}

func TestParseStandaloneAggregateAndExplain(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT merchant, COUNT(*) AS orders, SUM(amount) AS revenue, AVG(amount) AS average,
			MIN(amount) AS minimum, MAX(amount) AS maximum
		FROM sales WHERE active = true
		GROUP BY merchant ORDER BY revenue DESC LIMIT 10
	`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Select == nil || len(parsed.Select.Projection) != 6 ||
		parsed.Select.Projection[1].Aggregate != "count" ||
		parsed.Select.Projection[2].Aggregate != "sum" ||
		len(parsed.Select.GroupBy) != 1 || parsed.Select.Order[0].Column != "revenue" {
		t.Fatalf("aggregate plan = %#v", parsed.Select)
	}
	explained, err := ParseStatement(`EXPLAIN SELECT * FROM sales WHERE merchant = 'shop'`)
	if err != nil || explained.Explain == nil || explained.Explain.Table != "sales" {
		t.Fatalf("EXPLAIN plan = %#v, %v", explained.Explain, err)
	}
	analyzed, err := ParseStatement(`EXPLAIN ANALYZE SELECT * FROM sales WHERE merchant = 'shop'`)
	if err != nil || analyzed.Explain == nil || analyzed.Explain.Table != "sales" || !analyzed.ExplainAnalyze {
		t.Fatalf("EXPLAIN ANALYZE plan = %#v analyze=%t, %v", analyzed.Explain, analyzed.ExplainAnalyze, err)
	}
}

func TestParseStandaloneGroupByAndHaving(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT status, COUNT(*) AS orders, SUM(amount) AS revenue
		FROM sales
		GROUP BY status
		HAVING orders >= $1 AND revenue > 100
		ORDER BY revenue DESC
	`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Select == nil || parsed.Select.Having == nil || parsed.Select.Having.Operator != "and" ||
		len(parsed.Select.GroupBy) != 1 || parsed.Select.GroupBy[0] != "status" {
		t.Fatalf("GROUP/HAVING plan = %#v", parsed.Select)
	}
}

func TestParseStandaloneInnerAndLeftJoin(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT o.id, c.name AS customer
		FROM orders AS o
		LEFT JOIN customers c ON c.id = o.customer_id
		WHERE o.status = 'active'
		ORDER BY o.id DESC LIMIT 20
	`)
	if err != nil {
		t.Fatal(err)
	}
	plan := parsed.Select
	if plan == nil || plan.Table != "orders" || plan.TableAlias != "o" || len(plan.Joins) != 1 ||
		plan.Joins[0].Kind != "left" || plan.Joins[0].Alias != "c" ||
		plan.Joins[0].Left != "c.id" || plan.Joins[0].Right != "o.customer_id" ||
		plan.Projection[0].Name != "o.id" || plan.Conditions[0].Column != "o.status" {
		t.Fatalf("JOIN plan = %#v", plan)
	}
}

func TestParseTemporalTypesPrecisionAndTypedLiterals(t *testing.T) {
	parsed, err := ParseStatement(`
		CREATE TABLE events (
			id BIGINT PRIMARY KEY,
			on_day DATE,
			at_time TIME(0),
			local_at TIMESTAMP(3) WITHOUT TIME ZONE,
			occurred_at TIMESTAMP(6) WITH TIME ZONE,
			elapsed INTERVAL
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	columns := parsed.CreateTable.Columns
	if columns[1].Type.Kind != "date" || columns[2].Type.Kind != "time" ||
		columns[3].Type.Kind != "timestamp" || columns[4].Type.Kind != "timestamptz" ||
		columns[5].Type.Kind != "interval" || columns[2].TimePrecision == nil ||
		*columns[2].TimePrecision != 0 || columns[3].TimePrecision == nil ||
		*columns[3].TimePrecision != 3 || columns[4].TimePrecision == nil ||
		*columns[4].TimePrecision != 6 {
		t.Fatalf("temporal columns = %#v", columns)
	}

	selected, err := ParseStatement(`
		SELECT DATE '2026-08-27', TIMESTAMP '2026-08-27 14:30:00',
			TIMESTAMPTZ '2026-08-27T14:30:00+07:00', INTERVAL '1 day 02:03:04'
		FROM events
	`)
	if err != nil {
		t.Fatal(err)
	}
	for index, expected := range []string{"date", "timestamp", "timestamptz", "interval"} {
		expression := selected.Select.Projection[index].Expression
		if expression == nil || expression.Kind != "cast" || expression.CastKind != expected {
			t.Fatalf("typed literal %d = %#v", index, expression)
		}
	}
	filtered, err := ParseStatement(`
		SELECT id FROM events
		WHERE occurred_at >= TIMESTAMPTZ '2026-08-27T14:30:00+07:00'
	`)
	if err != nil || len(filtered.Select.Conditions) != 1 ||
		filtered.Select.Conditions[0].Column != "occurred_at" ||
		filtered.Select.Conditions[0].Value.Text != "2026-08-27T14:30:00+07:00" {
		t.Fatalf("typed temporal planner condition = %#v, %v", filtered.Select, err)
	}

	for _, source := range []string{
		`CREATE TABLE invalid (id BIGINT PRIMARY KEY, value TIME(7))`,
		`CREATE TABLE invalid (id BIGINT PRIMARY KEY, value DATE(1))`,
		`CREATE TABLE invalid (id BIGINT PRIMARY KEY, value TIME WITH TIME ZONE)`,
		`CREATE TABLE invalid (value INTERVAL PRIMARY KEY)`,
	} {
		if _, err := ParseStatement(source); err == nil {
			t.Fatalf("invalid temporal declaration accepted: %s", source)
		}
	}
}
