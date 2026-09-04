package relational

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestStandaloneDecimalCanonicalizationAndExactComparison(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "decimal.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE values_table (
			id INTEGER PRIMARY KEY,
			label TEXT NOT NULL,
			amount DECIMAL NOT NULL UNIQUE,
			CHECK (amount > 2)
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO values_table (id, label, amount) VALUES
		(1, '10', '0010.0000'),
		(2, '2', '2.5000'),
		(3, '100', '1000000000000000000000000000000.0001')
	`); err != nil {
		t.Fatal(err)
	}

	rows, err := engine.Execute(ctx, `SELECT id, amount FROM values_table ORDER BY amount`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 3 || rows.Rows[0][0] != int64(2) || rows.Rows[0][1] != "2.5" ||
		rows.Rows[1][0] != int64(1) || rows.Rows[1][1] != "10" || rows.Rows[2][0] != int64(3) {
		t.Fatalf("decimal ordering/canonical values = %#v", rows.Rows)
	}

	textRows, err := engine.Execute(ctx, `SELECT label FROM values_table ORDER BY label`)
	if err != nil {
		t.Fatal(err)
	}
	if len(textRows.Rows) != 3 || textRows.Rows[0][0] != "10" || textRows.Rows[1][0] != "100" ||
		textRows.Rows[2][0] != "2" {
		t.Fatalf("TEXT ordering = %#v", textRows.Rows)
	}

	filtered, err := engine.Execute(ctx, `SELECT id FROM values_table WHERE amount > '3.000' ORDER BY amount`)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Rows) != 2 || filtered.Rows[0][0] != int64(1) || filtered.Rows[1][0] != int64(3) {
		t.Fatalf("decimal predicate = %#v", filtered.Rows)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO values_table (id, label, amount) VALUES (4, 'duplicate', '10.0')`); err == nil {
		t.Fatal("canonical-equivalent decimal bypassed UNIQUE")
	}
	if _, err := engine.Execute(ctx, `INSERT INTO values_table (id, label, amount) VALUES (5, 'small', '1.999999999999999999')`); err == nil {
		t.Fatal("exact decimal CHECK accepted a value below its bound")
	}

	minimum, err := engine.Execute(ctx, `SELECT MIN(amount) AS minimum, MAX(amount) AS maximum FROM values_table`)
	if err != nil || len(minimum.Rows) != 1 || minimum.Rows[0][0] != "2.5" ||
		minimum.Rows[0][1] != "1000000000000000000000000000000.0001" {
		t.Fatalf("decimal MIN/MAX = %#v, %v", minimum.Rows, err)
	}
	aggregates, err := engine.Execute(ctx, `SELECT SUM(amount), AVG(amount) FROM values_table`)
	if err != nil || len(aggregates.Rows) != 1 ||
		aggregates.Rows[0][0] != "1000000000000000000000000000012.5001" ||
		aggregates.Rows[0][1] != "333333333333333333333333333337.5000333333333333" {
		t.Fatalf("decimal SUM/AVG = %#v, %v", aggregates.Rows, err)
	}
	if _, err := engine.Execute(ctx, `UPDATE values_table SET amount = amount + 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	updated, err := engine.Execute(ctx, `SELECT amount FROM values_table WHERE id = 1`)
	if err != nil || len(updated.Rows) != 1 || updated.Rows[0][0] != "11" {
		t.Fatalf("exact decimal UPDATE = %#v, %v", updated.Rows, err)
	}
}

func TestStandaloneNumericPrecisionScaleArithmeticCastAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "numeric.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE ledger (
			id BIGINT PRIMARY KEY,
			amount NUMERIC(5,2) NOT NULL,
			tax DECIMAL(6,3) NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO ledger (id, amount, tax) VALUES
		(1, 1.235, 0.1255),
		(2, -1.235, 0.1244),
		(3, 999.994, 0.001)
	`); err != nil {
		t.Fatal(err)
	}
	rows, err := engine.Execute(ctx, `SELECT id, amount, tax FROM ledger ORDER BY id`)
	if err != nil || len(rows.Rows) != 3 ||
		rows.Rows[0][1] != "1.24" || rows.Rows[0][2] != "0.126" ||
		rows.Rows[1][1] != "-1.24" || rows.Rows[1][2] != "0.124" ||
		rows.Rows[2][1] != "999.99" {
		t.Fatalf("NUMERIC coercion = %#v, %v", rows.Rows, err)
	}
	for _, statement := range []string{
		`INSERT INTO ledger (id, amount, tax) VALUES (4, 999.995, 0)`,
		`INSERT INTO ledger (id, amount, tax) VALUES (4, -999.995, 0)`,
	} {
		if _, err := engine.Execute(ctx, statement); err == nil || !strings.Contains(err.Error(), "exceeds NUMERIC(5,2)") {
			t.Fatalf("overflow %q error = %v", statement, err)
		}
	}
	calculated, err := engine.Execute(ctx, `
		SELECT amount + tax AS added,
		       amount * 2 AS doubled,
		       amount / 4 AS divided,
		       ROUND(amount / 3, 4) AS rounded
		FROM ledger WHERE id = 1
	`)
	if err != nil || len(calculated.Rows) != 1 ||
		calculated.Rows[0][0] != "1.366" || calculated.Rows[0][1] != "2.48" ||
		calculated.Rows[0][2] != "0.31" || calculated.Rows[0][3] != "0.4133" {
		t.Fatalf("NUMERIC expressions = %#v, %v", calculated.Rows, err)
	}
	constant, err := engine.Execute(ctx, `
		SELECT CAST('1.235' AS NUMERIC(4,2)) AS rounded,
		       CAST(12.6 AS INTEGER) AS integer_value,
		       0.1 + 0.2 AS exact_sum,
		       1e-3 + .002 AS exponent_sum
	`)
	if err != nil || len(constant.Rows) != 1 ||
		constant.Rows[0][0] != "1.24" || constant.Rows[0][1] != int64(13) ||
		constant.Rows[0][2] != "0.3" || constant.Rows[0][3] != "0.003" {
		t.Fatalf("NUMERIC CAST/literals = %#v, %v", constant.Rows, err)
	}
	if _, err := engine.Execute(ctx, `CREATE FUNCTION add_tax(n NUMERIC) RETURNS NUMERIC LANGUAGE SQL RETURN n + 0.1`); err != nil {
		t.Fatal(err)
	}
	functionResult, err := engine.Execute(ctx, `SELECT add_tax(0.2)`)
	if err != nil || functionResult.Rows[0][0] != "0.3" || functionResult.Columns[0].Kind != "decimal" {
		t.Fatalf("exact NUMERIC function = %#v, %v", functionResult, err)
	}
	parameter, err := engine.Execute(ctx, `SELECT CAST($1 AS NUMERIC(5,2))`, "2.345")
	if err != nil || parameter.Rows[0][0] != "2.35" {
		t.Fatalf("NUMERIC parameter = %#v, %v", parameter.Rows, err)
	}
	aggregates, err := engine.Execute(ctx, `SELECT SUM(amount), AVG(amount) FROM ledger`)
	if err != nil || aggregates.Rows[0][0] != "999.99" ||
		aggregates.Rows[0][1] != "333.33" {
		t.Fatalf("constrained NUMERIC aggregates = %#v, %v", aggregates.Rows, err)
	}
	catalog, err := engine.postgresCatalogSnapshot("numeric")
	if err != nil {
		t.Fatal(err)
	}
	columns := informationSchemaColumns(catalog)
	var metadata map[string]any
	for _, row := range columns.rows {
		if row["table_name"] == "ledger" && row["column_name"] == "amount" {
			metadata = row
			break
		}
	}
	if metadata == nil || metadata["numeric_precision"] != int64(5) || metadata["numeric_scale"] != int64(2) {
		t.Fatalf("information_schema NUMERIC metadata = %#v", metadata)
	}
	attributes := postgresAttributes(catalog)
	var typmod any
	for _, row := range attributes.rows {
		if row["attname"] == "amount" {
			typmod = row["atttypmod"]
			break
		}
	}
	wantTypmod := int64(postgresNumericTypeModifier(5, 2))
	if typmod != wantTypmod {
		t.Fatalf("pg_attribute.atttypmod = %v, want %d", typmod, wantTypmod)
	}
	wireColumn := postgresColumn(columnForField("amount", schemaFieldByNameForTest(t, engine, "ledger", "amount")))
	if wireColumn.DataTypeOID != pgwire.OIDNumeric || wireColumn.TypeModifier != int32(wantTypmod) {
		t.Fatalf("PostgreSQL NUMERIC wire column = %#v", wireColumn)
	}
	if field := postgresField("1.2", columnForField("amount", schemaFieldByNameForTest(t, engine, "ledger", "amount"))); field.Null || string(field.Data) != "1.20" {
		t.Fatalf("PostgreSQL NUMERIC text field = %#v", field)
	}
	binaryNumeric, err := pgwire.EncodeNumericBinary("2.35", wireColumn.TypeModifier)
	if err != nil {
		t.Fatal(err)
	}
	decodedParameter, err := decodePostgresParameter(pgwire.Parameter{OID: pgwire.OIDNumeric, Format: 1, Data: binaryNumeric})
	if err != nil || decodedParameter != "2.35" {
		t.Fatalf("binary NUMERIC parameter = %#v, %v", decodedParameter, err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	engine.mu.RLock()
	schema, err := engine.schemaLocked("ledger")
	engine.mu.RUnlock()
	if err != nil || schema.Version != 4 || schema.Fields[1].Precision != 5 || schema.Fields[1].Scale != 2 {
		t.Fatalf("reopened NUMERIC schema = %#v, %v", schema, err)
	}
	reopened, err := engine.Execute(ctx, `SELECT amount FROM ledger WHERE id = 1`)
	if err != nil || reopened.Rows[0][0] != "1.24" {
		t.Fatalf("reopened NUMERIC row = %#v, %v", reopened.Rows, err)
	}
}

func schemaFieldByNameForTest(t *testing.T, engine *Engine, table, name string) kitdbsql.Field {
	t.Helper()
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	schema, err := engine.schemaLocked(table)
	if err != nil {
		t.Fatal(err)
	}
	_, field, found := schema.FieldByName(name)
	if !found {
		t.Fatalf("field %s.%s is missing", table, name)
	}
	return field
}

func TestCanonicalDecimalBoundsAndEquivalentForms(t *testing.T) {
	cases := map[string]string{
		"+001.2300": "1.23",
		"-0.000":    "0",
		".5":        "0.5",
		"5.":        "5",
		"12e3":      "12000",
		"12e-3":     "0.012",
	}
	for source, want := range cases {
		got, err := canonicalDecimal(source)
		if err != nil || got != want {
			t.Fatalf("canonicalDecimal(%q) = %q, %v; want %q", source, got, err, want)
		}
	}
	for _, invalid := range []string{"", ".", "1e", "1.2.3", "1e999999", "NaN", "Infinity"} {
		if got, err := canonicalDecimal(invalid); err == nil {
			t.Fatalf("canonicalDecimal(%q) = %q, want error", invalid, got)
		}
	}
}

func TestExactDecimalArithmeticAndRoundingBoundaries(t *testing.T) {
	binaryCases := []struct {
		operator string
		left     string
		right    string
		want     string
	}{
		{"+", "0.1", "0.2", "0.3"},
		{"-", "1000000000000000000000000000000.1", "0.2", "999999999999999999999999999999.9"},
		{"*", "0.0001", "0.0002", "0.00000002"},
		{"/", "1", "6", "0.1666666666666667"},
		{"/", "-1", "6", "-0.1666666666666667"},
		{"%", "5.5", "2", "1.5"},
	}
	for _, test := range binaryCases {
		t.Run(test.operator+test.left, func(t *testing.T) {
			got, handled, err := exactDecimalBinary(
				test.operator, exactDecimal(test.left), exactDecimal(test.right),
			)
			if err != nil || !handled || got != exactDecimal(test.want) {
				t.Fatalf("%s %s %s = %#v handled=%t err=%v; want %s", test.left, test.operator, test.right, got, handled, err, test.want)
			}
		})
	}
	for _, test := range []struct {
		value string
		scale int
		want  string
	}{
		{"2.5", 0, "3"},
		{"-2.5", 0, "-3"},
		{"149", -1, "150"},
		{"-149", -1, "-150"},
		{"1.2345", 3, "1.235"},
	} {
		number, err := parseDecimalNumber(test.value)
		if err != nil {
			t.Fatal(err)
		}
		number, err = roundDecimalNumber(number, test.scale)
		if err != nil {
			t.Fatal(err)
		}
		got, err := number.text()
		if err != nil || got != test.want {
			t.Fatalf("round(%s,%d) = %q, %v; want %q", test.value, test.scale, got, err, test.want)
		}
	}
}

func TestStandaloneGroupedDecimalAggregatesRemainExact(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "decimal-groups.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE entries (
			id INTEGER PRIMARY KEY,
			category TEXT NOT NULL,
			amount NUMERIC(12,4) NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO entries (id, category, amount) VALUES
		(1, 'a', 0.1), (2, 'a', 0.2), (3, 'b', 10.005), (4, 'b', -10)
	`); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(ctx, `
		SELECT category, SUM(amount) AS total, AVG(amount) AS mean
		FROM entries
		GROUP BY category
		HAVING total >= 0.005
		ORDER BY category
	`)
	if err != nil || len(result.Rows) != 2 ||
		result.Rows[0][0] != "a" || result.Rows[0][1] != "0.3" || result.Rows[0][2] != "0.15" ||
		result.Rows[1][0] != "b" || result.Rows[1][1] != "0.005" || result.Rows[1][2] != "0.0025" {
		t.Fatalf("grouped decimal aggregates = %#v, %v", result.Rows, err)
	}
}
