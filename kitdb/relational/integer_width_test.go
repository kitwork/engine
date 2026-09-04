package relational

import (
	"context"
	"encoding/binary"
	"math"
	"path/filepath"
	"reflect"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestExactIntegerWidthsCRUDIndexesAndExpressions(t *testing.T) {
	e := bigintTestEngine(t)
	q := func(source string, args ...any) Result { return functionTestExecute(t, e, source, args...) }

	q(`CREATE TABLE widths (
		id INTEGER PRIMARY KEY,
		s SMALLINT UNIQUE,
		i INT4 UNIQUE,
		b BIGINT
	)`)
	q(`INSERT INTO widths(id,s,i,b) VALUES
		(-2147483648,-32768,-2147483648,-9223372036854775808),
		(2147483647,32767,2147483647,9223372036854775807)`)
	q(`CREATE INDEX widths_s_i ON widths(s,i)`)
	q(`CREATE TABLE children (id INTEGER PRIMARY KEY, parent SMALLINT REFERENCES widths(s))`)
	q(`INSERT INTO children(id,parent) VALUES (1,32767)`)

	result := q(`SELECT id,s,i,b FROM widths ORDER BY id`)
	want := [][]any{
		{int64(math.MinInt32), int64(math.MinInt16), int64(math.MinInt32), int64(math.MinInt64)},
		{int64(math.MaxInt32), int64(math.MaxInt16), int64(math.MaxInt32), int64(math.MaxInt64)},
	}
	if !reflect.DeepEqual(result.Rows, want) {
		t.Fatalf("width rows: %#v", result)
	}
	if result.Columns[0].Kind != "int32" || result.Columns[1].Kind != "smallint" ||
		result.Columns[2].Kind != "int32" || result.Columns[3].Kind != "bigint" {
		t.Fatalf("width metadata: %#v", result.Columns)
	}
	if indexed := q(`SELECT id FROM widths WHERE s=32767 AND i=2147483647`); !reflect.DeepEqual(indexed.Rows, [][]any{{int64(math.MaxInt32)}}) {
		t.Fatalf("exact composite index: %#v", indexed)
	}

	constant := q(`SELECT 1 AS regular, 2147483648 AS wide`)
	if constant.Columns[0].Kind != "int32" || constant.Columns[1].Kind != "bigint" ||
		!reflect.DeepEqual(constant.Rows, [][]any{{int64(1), int64(2147483648)}}) {
		t.Fatalf("literal inference: %#v", constant)
	}
	if expression := q(`SELECT s + 1 AS promoted FROM widths WHERE s=32767`); expression.Columns[0].Kind != "int32" || expression.Rows[0][0] != int64(32768) {
		t.Fatalf("smallint promotion: %#v", expression)
	}

	q(`CREATE FUNCTION twice_small(n SMALLINT) RETURNS SMALLINT LANGUAGE SQL RETURN n+n`)
	q(`CREATE FUNCTION twice_integer(n INTEGER) RETURNS INTEGER LANGUAGE SQL RETURN n+n`)
	for _, source := range []string{
		`INSERT INTO widths(id,s,i) VALUES (0,32768,0)`,
		`INSERT INTO widths(id,s,i) VALUES (0,0,2147483648)`,
		`INSERT INTO widths(id,s,i) VALUES (2147483648,0,0)`,
		`UPDATE widths SET i=i+i WHERE id=2147483647`,
		`SELECT s+s FROM widths WHERE s=32767`,
		`SELECT i+i FROM widths WHERE i=2147483647`,
		`SELECT twice_small(32767)`,
		`SELECT twice_integer(2147483647)`,
		`CREATE TABLE bad_default (id INTEGER PRIMARY KEY, n SMALLINT DEFAULT 32768)`,
		`INSERT INTO children(id,parent) VALUES (2,1)`,
	} {
		if _, err := e.Execute(context.Background(), source); err == nil {
			t.Fatalf("accepted out-of-contract statement: %s", source)
		}
	}
	if count := q(`SELECT COUNT(*) FROM widths`).Rows[0][0]; count != int64(2) {
		t.Fatalf("failed statements leaked writes: %v", count)
	}
}

func TestExactIntegerAggregatesAcrossExecutionPaths(t *testing.T) {
	e := bigintTestEngine(t)
	q := func(source string, args ...any) Result { return functionTestExecute(t, e, source, args...) }
	q(`CREATE TABLE samples (id INTEGER PRIMARY KEY, s SMALLINT, i INTEGER)`)
	q(`INSERT INTO samples(id,s,i) VALUES (1,32767,2147483647),(2,-32768,-2147483648),(3,NULL,NULL)`)
	if _, err := e.RefreshAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
	query := `SELECT SUM(s),AVG(s),SUM(i),AVG(i) FROM samples`
	want := [][]any{{int64(-1), "-0.5", int64(-1), "-0.5"}}
	for _, profile := range []struct {
		projections, batch bool
		path               string
	}{
		{true, true, "kcol-batch"},
		{false, true, "krow-batch"},
		{false, false, ""},
	} {
		e.experimentalProjections, e.batchAggregates = profile.projections, profile.batch
		result := q(query)
		if !reflect.DeepEqual(result.Rows, want) || result.Columns[0].Kind != "bigint" ||
			result.Columns[1].Kind != "decimal" || result.Columns[2].Kind != "bigint" ||
			result.Columns[3].Kind != "decimal" {
			t.Fatalf("aggregate profile %+v: %#v", profile, result)
		}
		if profile.path != "" && (result.Execution == nil || result.Execution.Path != profile.path) {
			t.Fatalf("aggregate path %+v: %#v", profile, result.Execution)
		}
	}
}

func TestTypedSequencesSerialIdentityAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typed-sequence.kitdb")
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	q := func(source string, args ...any) Result { return functionTestExecute(t, e, source, args...) }
	q(`CREATE SEQUENCE s16 AS SMALLINT START 32766`)
	q(`CREATE SEQUENCE s32 AS INTEGER START 2147483647`)
	if got := q(`SELECT nextval('s16'),nextval('s16'),nextval('s32')`).Rows[0]; !reflect.DeepEqual(got, []any{int64(32766), int64(32767), int64(math.MaxInt32)}) {
		t.Fatalf("typed nextval: %#v", got)
	}
	for _, source := range []string{`SELECT nextval('s16')`, `SELECT nextval('s32')`} {
		if _, err := e.Execute(context.Background(), source); err == nil {
			t.Fatalf("typed sequence exceeded range: %s", source)
		}
	}
	q(`CREATE TABLE generated (
		id SERIAL PRIMARY KEY,
		ticket SMALLSERIAL UNIQUE,
		large BIGSERIAL UNIQUE,
		identity_id INTEGER GENERATED BY DEFAULT AS IDENTITY UNIQUE
	)`)
	if got := q(`INSERT INTO generated DEFAULT VALUES RETURNING id,ticket,large,identity_id`).Rows[0]; !reflect.DeepEqual(got, []any{int64(1), int64(1), int64(1), int64(1)}) {
		t.Fatalf("serial values: %#v", got)
	}
	q(`CREATE SEQUENCE shared16 AS SMALLINT`)
	q(`CREATE TABLE defaults (id INTEGER PRIMARY KEY, n SMALLINT DEFAULT nextval('shared16'))`)
	if _, err := e.Execute(context.Background(), `CREATE TABLE mismatch (id INTEGER PRIMARY KEY, n INTEGER DEFAULT nextval('shared16'))`); err == nil {
		t.Fatal("cross-width sequence default accepted")
	}

	catalog, err := e.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	types := make(map[string]string)
	maximums := make(map[string]int64)
	for _, sequence := range catalog.Sequences {
		types[sequence.Name], maximums[sequence.Name] = sequence.DataTypeName(), sequence.Maximum
	}
	if types["generated_ticket_seq"] != "smallint" || maximums["generated_ticket_seq"] != math.MaxInt16 ||
		types["generated_id_seq"] != "integer" || maximums["generated_id_seq"] != math.MaxInt32 ||
		types["generated_large_seq"] != "bigint" || maximums["generated_large_seq"] != math.MaxInt64 {
		t.Fatalf("owned sequence definitions: types=%v maxima=%v", types, maximums)
	}
	postgres, precision := sequencePostgresType(types["s16"])
	if postgres.DataType != "smallint" || postgres.UDTName != "int2" || precision != 16 {
		t.Fatalf("sequence metadata: %#v precision=%d", postgres, precision)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := q(`INSERT INTO generated DEFAULT VALUES RETURNING id,ticket,large,identity_id`).Rows[0]; !reflect.DeepEqual(got, []any{int64(2), int64(2), int64(2), int64(2)}) {
		t.Fatalf("serial reopen: %#v", got)
	}
}

func TestExactIntegerPostgresMetadataAndParameters(t *testing.T) {
	for _, test := range []struct {
		kind string
		oid  uint32
		size int16
	}{
		{"smallint", pgwire.OIDInt2, 2},
		{"int32", pgwire.OIDInt4, 4},
		{"bigint", pgwire.OIDInt8, 8},
	} {
		column := postgresColumn(Column{Name: "n", Kind: test.kind})
		if column.DataTypeOID != test.oid || column.DataTypeSize != test.size {
			t.Fatalf("%s metadata: %#v", test.kind, column)
		}
	}
	if _, err := decodePostgresParameter(pgwire.Parameter{OID: pgwire.OIDInt2, Data: []byte("32768")}); err == nil {
		t.Fatal("text int2 overflow accepted")
	}
	if _, err := decodePostgresParameter(pgwire.Parameter{OID: pgwire.OIDInt4, Data: []byte("2147483648")}); err == nil {
		t.Fatal("text int4 overflow accepted")
	}
	binaryInt2 := make([]byte, 2)
	binary.BigEndian.PutUint16(binaryInt2, uint16(math.MaxInt16))
	value, err := decodePostgresParameter(pgwire.Parameter{OID: pgwire.OIDInt2, Format: 1, Data: binaryInt2})
	if err != nil || value != int64(math.MaxInt16) {
		t.Fatalf("binary int2: %v %v", value, err)
	}
}

func TestLegacyIntegerCatalogContractRemainsFrozen(t *testing.T) {
	e := bigintTestEngine(t)
	q := func(source string, args ...any) Result { return functionTestExecute(t, e, source, args...) }
	q(`CREATE TABLE legacy (id TINYINT PRIMARY KEY)`)
	q(`INSERT INTO legacy(id) VALUES (9007199254740991)`)
	if got := q(`SELECT id FROM legacy WHERE id=9007199254740991`).Rows; !reflect.DeepEqual(got, [][]any{{int64(maximumExactInteger)}}) {
		t.Fatalf("legacy exact key: %#v", got)
	}
	if _, err := e.Execute(context.Background(), `INSERT INTO legacy(id) VALUES (9007199254740992)`); err == nil {
		t.Fatal("legacy integer range widened")
	}
	q(`CREATE TABLE current (id INTEGER PRIMARY KEY)`)
	catalog, err := e.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, entry := range catalog.Structs {
		schema, err := kitdbsql.DecodeSchema(entry.Definition)
		if err != nil {
			t.Fatal(err)
		}
		kinds[schema.Name] = schema.Fields[0].Kind
	}
	if kinds["legacy"] != "integer" || kinds["current"] != "int32" {
		t.Fatalf("durable compatibility kinds: %v", kinds)
	}

	// The core sequence default remains BIGINT when the optional dataType field
	// is absent, preserving old canonical sequence definitions.
	legacySequence := kitdbengine.Sequence{Version: 1, ID: "0123456789abcdef0123456789abcdef", Name: "old", Start: 1, Increment: 1, Minimum: 1, Maximum: math.MaxInt64}
	if legacySequence.DataTypeName() != "bigint" {
		t.Fatal("legacy sequence no longer defaults to bigint")
	}
}
