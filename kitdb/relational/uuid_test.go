package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	_ "github.com/lib/pq"
)

func TestStandaloneExactUUIDCanonicalizationKeysCatalogAndReopen(t *testing.T) {
	const (
		accountID  = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
		externalID = "01234567-89ab-cdef-0123-456789abcdef"
	)
	path := filepath.Join(t.TempDir(), "uuid.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE accounts (id UUID PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE sessions (
			id BIGINT PRIMARY KEY,
			external_id UUID NOT NULL UNIQUE,
			account_id UUID NOT NULL REFERENCES accounts(id)
		)`,
		`CREATE INDEX sessions_account_idx ON sessions (account_id)`,
		`INSERT INTO accounts VALUES ('A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11', 'Kitwork')`,
		`INSERT INTO sessions VALUES (1, '0123456789ABCDEF0123456789ABCDEF',
			'{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}')`,
	} {
		if _, err := engine.Execute(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	selected, err := engine.Execute(ctx, `
		SELECT external_id, account_id, CAST(external_id AS TEXT)
		FROM sessions
		WHERE external_id = '{01234567-89AB-CDEF-0123-456789ABCDEF}'
	`)
	if err != nil || len(selected.Rows) != 1 {
		t.Fatalf("UUID SELECT = %#v, %v", selected.Rows, err)
	}
	if selected.Rows[0][0] != externalID || selected.Rows[0][1] != accountID || selected.Rows[0][2] != externalID {
		t.Fatalf("UUID canonical row = %#v", selected.Rows[0])
	}
	expression, err := engine.Execute(ctx, `
		SELECT COALESCE(NULLIF(account_id, account_id), 'A0EEBC999C0B4EF8BB6D6BB9BD380A11')
		FROM sessions WHERE id = 1
	`)
	if err != nil || len(expression.Rows) != 1 || expression.Rows[0][0] != accountID ||
		!expression.Columns[0].ExactUUID {
		t.Fatalf("UUID expression = %#v / %#v, %v", expression.Rows, expression.Columns, err)
	}
	explained, err := engine.Execute(ctx, `EXPLAIN SELECT id FROM sessions WHERE external_id = '0123456789abcdef0123456789abcdef'`)
	if err != nil || len(explained.Rows) != 1 || explained.Rows[0][1] != "unique lookup" {
		t.Fatalf("UUID unique plan = %#v, %v", explained.Rows, err)
	}
	byAccount, err := engine.Execute(ctx, `SELECT id FROM sessions WHERE account_id = 'A0EEBC999C0B4EF8BB6D6BB9BD380A11'`)
	if err != nil || len(byAccount.Rows) != 1 || byAccount.Rows[0][0] != int64(1) {
		t.Fatalf("UUID secondary lookup = %#v, %v", byAccount.Rows, err)
	}

	if _, err := engine.Execute(ctx, `
		INSERT INTO sessions VALUES (2, '{01234567-89ab-cdef-0123-456789abcdef}',
		'a0eebc999c0b4ef8bb6d6bb9bd380a11')
	`); err == nil {
		t.Fatal("alternate UUID spelling bypassed UNIQUE")
	}
	if _, err := engine.Execute(ctx, `UPDATE sessions SET external_id = 'not-a-uuid' WHERE id = 1`); err == nil ||
		!strings.Contains(err.Error(), "valid UUID") {
		t.Fatalf("invalid UUID UPDATE error = %v", err)
	}
	unchanged, err := engine.Execute(ctx, `SELECT external_id FROM sessions WHERE id = 1`)
	if err != nil || len(unchanged.Rows) != 1 || unchanged.Rows[0][0] != externalID {
		t.Fatalf("failed UUID UPDATE changed row = %#v, %v", unchanged.Rows, err)
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM sessions WHERE external_id LIKE '0123%'`); err == nil ||
		!strings.Contains(err.Error(), "CAST AS TEXT") {
		t.Fatalf("UUID LIKE error = %v", err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE invalid_refs (
			id BIGINT PRIMARY KEY,
			account_id KITID REFERENCES accounts(id)
		)
	`); err == nil || !strings.Contains(err.Error(), "incompatible types") {
		t.Fatalf("UUID/KitID foreign-key error = %v", err)
	}

	constants, err := engine.Execute(ctx, `
		SELECT UUID '{A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11}',
		       CAST('0123456789ABCDEF0123456789ABCDEF' AS UUID)
	`)
	if err != nil || len(constants.Rows) != 1 || constants.Rows[0][0] != accountID || constants.Rows[0][1] != externalID {
		t.Fatalf("UUID constants = %#v, %v", constants.Rows, err)
	}
	for _, column := range constants.Columns {
		wire := postgresColumn(column)
		if !column.ExactUUID || wire.DataTypeOID != pgwire.OIDUUID || wire.DataTypeSize != 16 {
			t.Fatalf("UUID constant column = %#v / %#v", column, wire)
		}
	}
	if _, err := engine.Execute(ctx, `
		CREATE FUNCTION keep_uuid(value UUID) RETURNS UUID
		LANGUAGE SQL RETURN value
	`); err != nil {
		t.Fatal(err)
	}
	functionValue, err := engine.Execute(ctx, `SELECT keep_uuid('A0EEBC999C0B4EF8BB6D6BB9BD380A11')`)
	if err != nil || len(functionValue.Rows) != 1 || functionValue.Rows[0][0] != accountID ||
		!functionValue.Columns[0].ExactUUID || postgresColumn(functionValue.Columns[0]).DataTypeOID != pgwire.OIDUUID {
		t.Fatalf("UUID function result = %#v / %#v, %v", functionValue.Rows, functionValue.Columns, err)
	}

	if _, err := engine.Execute(ctx, `CREATE TABLE generated (id UUID PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO generated (note) VALUES ('automatic')`); err != nil {
		t.Fatal(err)
	}
	generated, err := engine.Execute(ctx, `SELECT id FROM generated`)
	if err != nil || len(generated.Rows) != 1 {
		t.Fatalf("generated UUID = %#v, %v", generated.Rows, err)
	}
	if canonical, parseErr := kitdbsql.CanonicalUUID(generated.Rows[0][0].(string)); parseErr != nil || canonical != generated.Rows[0][0] {
		t.Fatalf("generated UUID is not canonical: %#v, %v", generated.Rows[0][0], parseErr)
	}
	generatedBits, err := kitdbsql.ParseUUID(generated.Rows[0][0].(string))
	if err != nil || generatedBits[6]>>4 != 4 || generatedBits[8]&0xc0 != 0x80 {
		t.Fatalf("generated UUID is not RFC variant UUIDv4: %x, %v", generatedBits, err)
	}

	engine.mu.RLock()
	schema, err := engine.schemaLocked("sessions")
	engine.mu.RUnlock()
	if err != nil || schema.Version != kitdbsql.SchemaVersion7 || !schema.Fields[1].ExactUUID || !schema.Fields[2].ExactUUID {
		t.Fatalf("UUID schema = %#v, %v", schema, err)
	}
	catalog, err := engine.postgresCatalogSnapshot("uuid")
	if err != nil {
		t.Fatal(err)
	}
	columns := informationSchemaColumns(catalog)
	for _, row := range columns.rows {
		if row["table_name"] == "sessions" && (row["column_name"] == "external_id" || row["column_name"] == "account_id") {
			if row["data_type"] != "uuid" || row["udt_name"] != "uuid" || row["character_maximum_length"] != nil {
				t.Fatalf("UUID information_schema row = %#v", row)
			}
		}
	}
	attributes := postgresAttributes(catalog)
	for _, row := range attributes.rows {
		if row["attname"] == "external_id" {
			if row["atttypid"] != uint32(pgwire.OIDUUID) || row["attlen"] != int64(16) {
				t.Fatalf("UUID pg_attribute row = %#v", row)
			}
		}
	}
	foundType := false
	for _, row := range postgresTypes().rows {
		if row["oid"] == uint32(pgwire.OIDUUID) {
			foundType = row["typname"] == "uuid" && row["typlen"] == int64(16) && row["typcategory"] == "U"
		}
	}
	if !foundType {
		t.Fatal("pg_type does not expose native UUID metadata")
	}

	legacy := kitdbsql.Field{Name: "legacy", Kind: "uuid"}
	if value, err := coerceField(legacy, "legacy-token"); err != nil || value != "legacy-token" {
		t.Fatalf("legacy UUID admission = %#v, %v", value, err)
	}
	if wire := postgresColumn(columnForField("legacy", legacy)); wire.DataTypeOID != pgwire.OIDText {
		t.Fatalf("legacy UUID wire column = %#v", wire)
	}

	binary, _ := pgwire.EncodeUUIDBinary(accountID)
	for _, parameter := range []pgwire.Parameter{
		{OID: pgwire.OIDUUID, Format: 0, Data: []byte("A0EEBC999C0B4EF8BB6D6BB9BD380A11")},
		{OID: pgwire.OIDUUID, Format: 1, Data: binary},
	} {
		decoded, err := decodePostgresParameter(parameter)
		if err != nil || decoded != accountID {
			t.Fatalf("UUID parameter = %#v, %v", decoded, err)
		}
	}

	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	engine.mu.RLock()
	reopenedSchema, schemaErr := engine.schemaLocked("sessions")
	engine.mu.RUnlock()
	if schemaErr != nil || reopenedSchema.Version != kitdbsql.SchemaVersion7 || !reopenedSchema.Fields[1].ExactUUID {
		t.Fatalf("reopened UUID schema = %#v, %v", reopenedSchema, schemaErr)
	}
	reopened, err := engine.Execute(ctx, `SELECT external_id FROM sessions WHERE account_id = $1`, accountID)
	if err != nil || len(reopened.Rows) != 1 || reopened.Rows[0][0] != externalID {
		t.Fatalf("reopened UUID lookup = %#v, %v", reopened.Rows, err)
	}
}

func TestStandaloneExactUUIDThroughPostgresClient(t *testing.T) {
	const want = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
	engine, err := Open(filepath.Join(t.TempDir(), "uuid-wire.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		engine.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.ServePostgres(ctx, listener, PostgresServerOptions{
			PostgresOptions: PostgresOptions{Database: "uuid", User: "kitdb", Password: "secret"},
			MaxConnections:  4,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-done:
			if serveErr != nil {
				t.Errorf("ServePostgres: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServePostgres did not stop")
		}
		if err := engine.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	database, err := sql.Open("postgres", fmt.Sprintf(
		"postgres://kitdb:secret@%s/uuid?sslmode=disable", listener.Addr().String(),
	))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE objects (id UUID PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO objects VALUES ($1, $2)`,
		"{A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11}", "native UUID"); err != nil {
		t.Fatal(err)
	}
	var identifier, name string
	if err := database.QueryRow(`SELECT id, name FROM objects WHERE id = $1`,
		"a0eebc999c0b4ef8bb6d6bb9bd380a11").Scan(&identifier, &name); err != nil {
		t.Fatal(err)
	}
	if identifier != want || name != "native UUID" {
		t.Fatalf("PostgreSQL UUID row = (%q, %q)", identifier, name)
	}
	var dataType, udtName string
	if err := database.QueryRow(`
		SELECT data_type, udt_name FROM information_schema.columns
		WHERE table_name = 'objects' AND column_name = 'id'
	`).Scan(&dataType, &udtName); err != nil {
		t.Fatal(err)
	}
	if dataType != "uuid" || udtName != "uuid" {
		t.Fatalf("PostgreSQL UUID catalog = (%q, %q)", dataType, udtName)
	}
}
