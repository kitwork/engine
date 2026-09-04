package relational

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestStandaloneCharacterLengthPaddingComparisonAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "character.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE labels (
			id BIGINT PRIMARY KEY,
			slug VARCHAR(4) NOT NULL UNIQUE,
			code CHAR(4) NOT NULL UNIQUE,
			native CHAR NOT NULL,
			note CHARACTER VARYING
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO labels (id, slug, code, native, note) VALUES
		(1, 'café', 'A', 'Z', 'unbounded'),
		(2, 'đỏ', 'B   ', 'Q', 'text')
	`); err != nil {
		t.Fatal(err)
	}

	rows, err := engine.Execute(ctx, `
		SELECT slug, code, native, LENGTH(code), code || '!', CAST(code AS TEXT)
		FROM labels WHERE id = 1
	`)
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("character SELECT = %#v, %v", rows.Rows, err)
	}
	want := []any{"café", "A   ", "Z", int64(1), "A!", "A"}
	for index := range want {
		if rows.Rows[0][index] != want[index] {
			t.Fatalf("character SELECT column %d = %#v, want %#v", index, rows.Rows[0][index], want[index])
		}
	}

	casted, err := engine.Execute(ctx, `
		SELECT CAST('abcdef' AS VARCHAR(3)), CAST('xy' AS CHAR(4)),
		       CAST('A' AS CHAR(4)) = 'A'
	`)
	if err != nil || len(casted.Rows) != 1 || casted.Rows[0][0] != "abc" ||
		casted.Rows[0][1] != "xy  " || casted.Rows[0][2] != true {
		t.Fatalf("character CAST = %#v, %v", casted.Rows, err)
	}

	for _, statement := range []string{
		`INSERT INTO labels VALUES (3, 'abcde', 'C', 'R', 'too long')`,
		`INSERT INTO labels VALUES (3, 'okay', 'ABCDE', 'R', 'too long')`,
		`UPDATE labels SET slug = 'toolong' WHERE id = 1`,
	} {
		if _, err := engine.Execute(ctx, statement); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("overlength statement %q error = %v", statement, err)
		}
	}
	if _, err := engine.Execute(ctx, `INSERT INTO labels VALUES (3, 'xy   ', 'C     ', 'R', 'spaces')`); err != nil {
		t.Fatalf("space-only excess was not truncated: %v", err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO labels VALUES (4, 'four', 'A  ', 'S', 'duplicate')`); err == nil {
		t.Fatal("blank-equivalent CHAR bypassed UNIQUE")
	}
	if _, err := engine.Execute(ctx,
		`INSERT INTO labels (id, slug, code, native, note) VALUES ($1, $2, $3, $4, $5)`,
		int64(4), string([]byte{0xff}), "D", "S", "invalid UTF-8",
	); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid UTF-8 constrained text error = %v", err)
	}

	unchanged, err := engine.Execute(ctx, `SELECT slug FROM labels WHERE id = 1`)
	if err != nil || len(unchanged.Rows) != 1 || unchanged.Rows[0][0] != "café" {
		t.Fatalf("failed UPDATE changed row = %#v, %v", unchanged.Rows, err)
	}
	equality, err := engine.Execute(ctx, `SELECT id FROM labels WHERE code = 'A'`)
	if err != nil || len(equality.Rows) != 1 || equality.Rows[0][0] != int64(1) {
		t.Fatalf("blank-insignificant CHAR equality = %#v, %v", equality.Rows, err)
	}
	explained, err := engine.Execute(ctx, `EXPLAIN SELECT id FROM labels WHERE code = 'A'`)
	if err != nil || len(explained.Rows) != 1 || explained.Rows[0][1] != "unique lookup" {
		t.Fatalf("CHAR index plan = %#v, %v", explained.Rows, err)
	}
	likeExact, err := engine.Execute(ctx, `SELECT id FROM labels WHERE code LIKE 'A'`)
	if err != nil || len(likeExact.Rows) != 0 {
		t.Fatalf("CHAR LIKE ignored physical padding = %#v, %v", likeExact.Rows, err)
	}
	likePrefix, err := engine.Execute(ctx, `SELECT id FROM labels WHERE code LIKE 'A%'`)
	if err != nil || len(likePrefix.Rows) != 1 || likePrefix.Rows[0][0] != int64(1) {
		t.Fatalf("CHAR prefix LIKE = %#v, %v", likePrefix.Rows, err)
	}
	grouped, err := engine.Execute(ctx, `
		SELECT code, COUNT(*) AS count
		FROM labels GROUP BY code HAVING code = 'A' ORDER BY code
	`)
	if err != nil || len(grouped.Rows) != 1 || grouped.Rows[0][0] != "A   " || grouped.Rows[0][1] != int64(1) {
		t.Fatalf("CHAR GROUP BY/HAVING = %#v, %v", grouped.Rows, err)
	}
	if _, err := engine.Execute(ctx, `CREATE TABLE altered_labels (id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE altered_labels ADD COLUMN code CHAR(2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO altered_labels (id, code) VALUES (1, 'V')`); err != nil {
		t.Fatal(err)
	}
	altered, err := engine.Execute(ctx, `SELECT code FROM altered_labels WHERE id = 1`)
	if err != nil || len(altered.Rows) != 1 || altered.Rows[0][0] != "V " {
		t.Fatalf("ALTER ADD CHAR = %#v, %v", altered.Rows, err)
	}
	engine.mu.RLock()
	alteredSchema, err := engine.schemaLocked("altered_labels")
	engine.mu.RUnlock()
	if err != nil || alteredSchema.Version != kitdbsql.SchemaVersion6 || alteredSchema.Fields[1].TextLength == nil ||
		*alteredSchema.Fields[1].TextLength != 2 {
		t.Fatalf("ALTER ADD character schema = %#v, %v", alteredSchema, err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE countries (
			id BIGINT PRIMARY KEY,
			code CHAR(4) NOT NULL UNIQUE
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE visits (
			id BIGINT PRIMARY KEY,
			short_code CHAR(2) REFERENCES countries(code),
			long_code CHAR(8) REFERENCES countries(code)
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE invalid_visits (
			id BIGINT PRIMARY KEY,
			variable_code VARCHAR(8) REFERENCES countries(code)
		)
	`); err == nil || !strings.Contains(err.Error(), "incompatible types") {
		t.Fatalf("mixed CHAR/VARCHAR foreign key error = %v", err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO countries VALUES (1, 'A')`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO visits VALUES (1, 'A', 'A')`); err != nil {
		t.Fatalf("character foreign-key normalization = %v", err)
	}
	if _, err := engine.Execute(ctx, `DELETE FROM countries WHERE id = 1`); err == nil {
		t.Fatal("character foreign-key reference did not protect target DELETE")
	}

	catalog, err := engine.postgresCatalogSnapshot("character")
	if err != nil {
		t.Fatal(err)
	}
	columns := informationSchemaColumns(catalog)
	lengths := make(map[string]any)
	for _, row := range columns.rows {
		if row["table_name"] == "labels" {
			lengths[row["column_name"].(string)] = row["character_maximum_length"]
		}
	}
	if lengths["slug"] != int64(4) || lengths["code"] != int64(4) ||
		lengths["native"] != int64(1) || lengths["note"] != nil {
		t.Fatalf("information_schema character lengths = %#v", lengths)
	}
	attributes := postgresAttributes(catalog)
	typmods := make(map[string]any)
	for _, row := range attributes.rows {
		if row["attname"] == "slug" || row["attname"] == "code" {
			typmods[row["attname"].(string)] = row["atttypmod"]
		}
	}
	if typmods["slug"] != int64(8) || typmods["code"] != int64(8) {
		t.Fatalf("pg_attribute character typmods = %#v", typmods)
	}
	slugColumn := postgresColumn(columnForField("slug", schemaFieldByNameForTest(t, engine, "labels", "slug")))
	codeColumn := postgresColumn(columnForField("code", schemaFieldByNameForTest(t, engine, "labels", "code")))
	if slugColumn.DataTypeOID != pgwire.OIDVarchar || slugColumn.TypeModifier != 8 ||
		codeColumn.DataTypeOID != pgwire.OIDBPChar || codeColumn.TypeModifier != 8 {
		t.Fatalf("PostgreSQL character wire columns = %#v / %#v", slugColumn, codeColumn)
	}
	for _, parameter := range []pgwire.Parameter{
		{OID: pgwire.OIDVarchar, Format: 1, Data: []byte("đỏ")},
		{OID: pgwire.OIDBPChar, Format: 1, Data: []byte("A   ")},
	} {
		decoded, err := decodePostgresParameter(parameter)
		if err != nil || decoded != string(parameter.Data) {
			t.Fatalf("binary character parameter = %#v, %v", decoded, err)
		}
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
	schema, err := engine.schemaLocked("labels")
	engine.mu.RUnlock()
	if err != nil || schema.Version != kitdbsql.SchemaVersion6 || schema.Fields[1].TextLength == nil ||
		*schema.Fields[1].TextLength != 4 || schema.Fields[4].TextLength != nil {
		t.Fatalf("reopened character schema = %#v, %v", schema, err)
	}
	reopened, err := engine.Execute(ctx, `SELECT slug, code FROM labels WHERE id = 1`)
	if err != nil || len(reopened.Rows) != 1 || reopened.Rows[0][0] != "café" || reopened.Rows[0][1] != "A   " {
		t.Fatalf("reopened character row = %#v, %v", reopened.Rows, err)
	}
}
