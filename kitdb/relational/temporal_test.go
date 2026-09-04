package relational

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestStandaloneTemporalCanonicalizationArithmeticCatalogAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "temporal.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE events (
			id BIGINT PRIMARY KEY,
			on_day DATE NOT NULL,
			at_time TIME(3) NOT NULL,
			local_at TIMESTAMP(3) NOT NULL,
			occurred_at TIMESTAMPTZ(6) NOT NULL,
			elapsed INTERVAL NOT NULL,
			created_at TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatal(err)
	}
	engine.mu.RLock()
	createdSchema, err := engine.schemaLocked("events")
	engine.mu.RUnlock()
	if err != nil || createdSchema.Version != kitdbsql.SchemaVersion5 {
		t.Fatalf("exact temporal schema version = %d, %v", createdSchema.Version, err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO events (id, on_day, at_time, local_at, occurred_at, elapsed) VALUES
		(1, '2026-08-27', '12:34:56.1236', '2026-08-27 14:30:00.9996',
		 '2026-08-27T14:30:00.1234567+07:00', '1 year 2 mons 3 days 04:05:06.700000'),
		(2, '2026-08-26', '00:00:00', '2026-08-26 00:00:00',
		 '2026-08-26T00:00:00Z', '30 days')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `CREATE INDEX events_occurred_idx ON events (occurred_at, id)`); err != nil {
		t.Fatal(err)
	}

	rows, err := engine.Execute(ctx, `
		SELECT id, on_day, at_time, local_at, occurred_at, elapsed, created_at
		FROM events ORDER BY occurred_at
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 2 || rows.Rows[0][0] != int64(2) || rows.Rows[1][0] != int64(1) ||
		rows.Rows[1][1] != "2026-08-27" || rows.Rows[1][2] != "12:34:56.124" ||
		rows.Rows[1][3] != "2026-08-27T14:30:01.000" ||
		rows.Rows[1][4] != "2026-08-27T07:30:00.123457Z" ||
		rows.Rows[1][5] != "14 mons 3 days 04:05:06.7" ||
		!strings.HasSuffix(rows.Rows[1][6].(string), "Z") {
		t.Fatalf("temporal rows = %#v", rows.Rows)
	}

	calculated, err := engine.Execute(ctx, `
		SELECT on_day + 2 AS later_day,
		       on_day - DATE '2026-08-20' AS day_count,
		       local_at + INTERVAL '1 day' AS later_local,
		       occurred_at - TIMESTAMPTZ '2026-08-27T07:30:00Z' AS elapsed_since
		FROM events WHERE id = 1
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(calculated.Rows) != 1 || calculated.Rows[0][0] != "2026-08-29" ||
		calculated.Rows[0][1] != int64(7) || calculated.Rows[0][2] != "2026-08-28T14:30:01.000" ||
		calculated.Rows[0][3] != "00:00:00.123457" {
		t.Fatalf("temporal arithmetic = %#v", calculated.Rows)
	}
	functions, err := engine.Execute(ctx, `
		SELECT DATE_TRUNC('month', occurred_at) AS month_start,
		       DATE_PART('hour', occurred_at) AS utc_hour,
		       DATE_PART('second', elapsed) AS interval_second
		FROM events WHERE id = 1
	`)
	if err != nil || len(functions.Rows) != 1 ||
		functions.Rows[0][0] != "2026-08-01T00:00:00.000000Z" ||
		functions.Rows[0][1] != float64(7) || functions.Rows[0][2] != float64(6.7) {
		t.Fatalf("temporal functions = %#v, %v", functions.Rows, err)
	}
	clock, err := engine.Execute(ctx, `SELECT CURRENT_DATE, CURRENT_TIME, LOCALTIMESTAMP, CURRENT_TIMESTAMP`)
	if err != nil || len(clock.Rows) != 1 || len(clock.Rows[0]) != 4 {
		t.Fatalf("current temporal values = %#v, %v", clock.Rows, err)
	}
	for index, kind := range []string{"date", "time", "timestamp", "timestamptz"} {
		if clock.Columns[index].Kind != kind || clock.Rows[0][index] == nil {
			t.Fatalf("current temporal column %d = %#v / %#v", index, clock.Columns[index], clock.Rows[0][index])
		}
	}

	filtered, err := engine.Execute(ctx, `
		SELECT id FROM events
		WHERE occurred_at >= TIMESTAMPTZ '2026-08-27T00:00:00+07:00'
		ORDER BY occurred_at
	`)
	if err != nil || len(filtered.Rows) != 1 || filtered.Rows[0][0] != int64(1) {
		t.Fatalf("temporal predicate = %#v, %v", filtered.Rows, err)
	}
	explained, err := engine.Execute(ctx, `
		EXPLAIN SELECT id FROM events
		WHERE occurred_at >= TIMESTAMPTZ '2026-08-27T00:00:00+07:00'
		ORDER BY occurred_at, id
	`)
	if err != nil || len(explained.Rows) != 1 || explained.Rows[0][1] != "index scan" ||
		!strings.Contains(explained.Rows[0][2].(string), "events_occurred_idx") ||
		!strings.Contains(explained.Rows[0][2].(string), "range=occurred_at") {
		t.Fatalf("temporal EXPLAIN = %#v, %v", explained.Rows, err)
	}

	catalog, err := engine.postgresCatalogSnapshot("temporal")
	if err != nil {
		t.Fatal(err)
	}
	columns := informationSchemaColumns(catalog)
	var occurredMetadata map[string]any
	for _, row := range columns.rows {
		if row["table_name"] == "events" && row["column_name"] == "occurred_at" {
			occurredMetadata = row
			break
		}
	}
	if occurredMetadata == nil || occurredMetadata["data_type"] != "timestamp with time zone" ||
		occurredMetadata["datetime_precision"] != int64(6) {
		t.Fatalf("information_schema temporal metadata = %#v", occurredMetadata)
	}
	field := schemaFieldByNameForTest(t, engine, "events", "local_at")
	wireColumn := postgresColumn(columnForField("local_at", field))
	if wireColumn.DataTypeOID != pgwire.OIDTimestamp || wireColumn.TypeModifier != 3 {
		t.Fatalf("PostgreSQL temporal wire column = %#v", wireColumn)
	}
	if wireField := postgresField("2026-08-27T14:30:01.000", columnForField("local_at", field)); string(wireField.Data) != "2026-08-27 14:30:01.000" {
		t.Fatalf("PostgreSQL temporal text field = %q", wireField.Data)
	}
	binaryTimestamp, err := pgwire.EncodeTimestampBinary("2026-08-27 14:30:01.123456", false)
	if err != nil {
		t.Fatal(err)
	}
	decodedTimestamp, err := decodePostgresParameter(pgwire.Parameter{
		OID: pgwire.OIDTimestamp, Format: 1, Data: binaryTimestamp,
	})
	if err != nil || decodedTimestamp != "2026-08-27T14:30:01.123456" {
		t.Fatalf("PostgreSQL binary TIMESTAMP parameter = %#v, %v", decodedTimestamp, err)
	}
	binaryInterval, err := pgwire.EncodeIntervalBinary("2 mons 3 days 04:05:06.7")
	if err != nil {
		t.Fatal(err)
	}
	decodedInterval, err := decodePostgresParameter(pgwire.Parameter{
		OID: pgwire.OIDInterval, Format: 1, Data: binaryInterval,
	})
	if err != nil || decodedInterval != "2 mons 3 days 04:05:06.7" {
		t.Fatalf("PostgreSQL binary INTERVAL parameter = %#v, %v", decodedInterval, err)
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
	schema, err := engine.schemaLocked("events")
	engine.mu.RUnlock()
	if err != nil || schema.Version != kitdbsql.SchemaVersion5 || schema.Fields[3].TimePrecision == nil ||
		*schema.Fields[3].TimePrecision != 3 {
		t.Fatalf("reopened temporal schema = %#v, %v", schema, err)
	}
	reopened, err := engine.Execute(ctx, `SELECT occurred_at, elapsed FROM events WHERE id = 1`)
	if err != nil || reopened.Rows[0][0] != "2026-08-27T07:30:00.123457Z" ||
		reopened.Rows[0][1] != "14 mons 3 days 04:05:06.7" {
		t.Fatalf("reopened temporal row = %#v, %v", reopened.Rows, err)
	}
}

func TestStandaloneTemporalProfileRejectsAmbiguousOrLossyValues(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "invalid-temporal.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE events (id BIGINT PRIMARY KEY, local_at TIMESTAMP, occurred_at TIMESTAMPTZ, elapsed INTERVAL)
	`); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`INSERT INTO events VALUES (1, '2026-02-30 00:00:00', '2026-01-01T00:00:00Z', '1 day')`,
		`INSERT INTO events VALUES (1, '0000-01-01 00:00:00', '2026-01-01T00:00:00Z', '1 day')`,
		`INSERT INTO events VALUES (1, '9999-12-31 23:59:59.9999996', '2026-01-01T00:00:00Z', '1 day')`,
		`INSERT INTO events VALUES (1, '2026-01-01T00:00:00+07:00', '2026-01-01T00:00:00Z', '1 day')`,
		`INSERT INTO events VALUES (1, '2026-01-01 00:00:00', 'not-a-time', '1 day')`,
		`INSERT INTO events VALUES (1, '2026-01-01 00:00:00', '2026-01-01T00:00:00Z', '1 fortnight')`,
		`CREATE INDEX elapsed_idx ON events (elapsed)`,
	} {
		if _, err := engine.Execute(ctx, source); err == nil {
			t.Fatalf("ambiguous/lossy temporal statement accepted: %s", source)
		}
	}
}

func TestStandaloneTemporalClockDefaultsAndOverflowBoundaries(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "temporal-boundaries.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE clocks (
			id BIGINT PRIMARY KEY,
			on_day DATE NOT NULL DEFAULT CURRENT_DATE,
			at_time TIME(6) NOT NULL DEFAULT CURRENT_TIME,
			local_at TIMESTAMP(6) NOT NULL DEFAULT LOCALTIMESTAMP,
			occurred_at TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE mixed_profiles (
			id BIGINT PRIMARY KEY,
			occurred_at TIMESTAMPTZ,
			amount NUMERIC(18,2)
		)
	`); err != nil {
		t.Fatal(err)
	}
	engine.mu.RLock()
	mixedSchema, err := engine.schemaLocked("mixed_profiles")
	engine.mu.RUnlock()
	if err != nil || mixedSchema.Version != kitdbsql.SchemaVersion5 {
		t.Fatalf("mixed numeric/temporal schema version = %d, %v", mixedSchema.Version, err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE instant_order (id BIGINT PRIMARY KEY, happened_at TIMESTAMPTZ NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO instant_order VALUES
		(2, '2026-01-01T00:00:00.1Z'),
		(3, '2026-01-01T00:00:01Z'),
		(1, '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `CREATE INDEX instant_order_time_idx ON instant_order (happened_at, id)`); err != nil {
		t.Fatal(err)
	}
	ordered, err := engine.Execute(ctx, `SELECT id FROM instant_order ORDER BY happened_at, id`)
	if err != nil || len(ordered.Rows) != 3 || ordered.Rows[0][0] != int64(1) ||
		ordered.Rows[1][0] != int64(2) || ordered.Rows[2][0] != int64(3) {
		t.Fatalf("unconstrained TIMESTAMPTZ index order = %#v, %v", ordered.Rows, err)
	}
	ranged, err := engine.Execute(ctx, `
		SELECT id FROM instant_order
		WHERE happened_at > TIMESTAMPTZ '2026-01-01T00:00:00Z'
		ORDER BY happened_at, id
	`)
	if err != nil || len(ranged.Rows) != 2 || ranged.Rows[0][0] != int64(2) || ranged.Rows[1][0] != int64(3) {
		t.Fatalf("unconstrained TIMESTAMPTZ index range = %#v, %v", ranged.Rows, err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO clocks (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	defaults, err := engine.Execute(ctx, `SELECT on_day, at_time, local_at, occurred_at FROM clocks WHERE id = 1`)
	if err != nil || len(defaults.Rows) != 1 {
		t.Fatalf("clock defaults = %#v, %v", defaults.Rows, err)
	}
	for index, value := range defaults.Rows[0] {
		if value == nil || value == "" {
			t.Fatalf("clock default %d = %#v", index, value)
		}
	}
	consistent, err := engine.Execute(ctx, `SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP`)
	if err != nil || consistent.Rows[0][0] != consistent.Rows[0][1] {
		t.Fatalf("statement clock is inconsistent = %#v, %v", consistent.Rows, err)
	}

	maximumInterval := temporalInterval{microseconds: int64(1<<63 - 1)}
	clock := exactTemporal{kind: "time", value: 23 * microsecondsPerHour}
	wrapped, err := addIntervalToTemporal(clock, maximumInterval)
	want := (clock.value + maximumInterval.microseconds%microsecondsPerDay) % microsecondsPerDay
	if err != nil || wrapped.value != want {
		t.Fatalf("large TIME wrap = %#v, %v; want %d", wrapped, err, want)
	}
	minimumInterval := temporalInterval{microseconds: int64(-1 << 63)}
	if _, err := addIntervals(temporalInterval{}, minimumInterval, true); err == nil {
		t.Fatal("subtracting minimum INTERVAL did not fail closed")
	}
	parsedMinimum, err := parseTemporalInterval("-9223372036854.775808 seconds")
	if err != nil || parsedMinimum.microseconds != minimumInterval.microseconds {
		t.Fatalf("minimum INTERVAL parse = %#v, %v", parsedMinimum, err)
	}
	canonicalMinimum := formatTemporalInterval(parsedMinimum)
	reparsedMinimum, err := parseTemporalInterval(canonicalMinimum)
	if err != nil || reparsedMinimum != parsedMinimum {
		t.Fatalf("minimum INTERVAL canonical round trip = %q %#v, %v", canonicalMinimum, reparsedMinimum, err)
	}
	minimumMonths, err := parseTemporalInterval("-2147483648 mons")
	if err != nil || minimumMonths.months != int32(-1<<31) {
		t.Fatalf("minimum month component = %#v, %v", minimumMonths, err)
	}
}
