package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Only an explicitly supplied disposable local database may receive fixtures.
func validatePostgresDSN(source string) error {
	u, err := url.Parse(source)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return fmt.Errorf("DBCOMPARE_POSTGRES_DSN must be a PostgreSQL URL for a disposable local database")
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return fmt.Errorf("PostgreSQL benchmark refuses non-loopback hosts")
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(name, "kitdb_benchmark_") || len(name) <= len("kitdb_benchmark_") || len(name) > 63 {
		return fmt.Errorf("PostgreSQL benchmark database must start with kitdb_benchmark_ and have a unique suffix")
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return fmt.Errorf("invalid PostgreSQL benchmark database name")
		}
	}
	if u.Fragment != "" {
		return fmt.Errorf("PostgreSQL benchmark URL must not contain a fragment")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL benchmark URL options")
	}
	for key, values := range query {
		if (key != "sslmode" && key != "connect_timeout" && key != "application_name") || len(values) != 1 {
			return fmt.Errorf("PostgreSQL benchmark URL option %q is not permitted", key)
		}
	}
	return nil
}

type postgresDatabase struct{ *sql.DB }

func (d postgresDatabase) execute(ctx context.Context, query string) (response, error) {
	r, err := (sqliteDatabase{d.DB}).execute(ctx, query)
	r.path = "postgres-loopback"
	for _, row := range r.rows {
		for i, value := range row {
			if encoded, ok := value.([]byte); ok {
				row[i] = string(encoded)
			}
		}
	}
	return r, err
}

func (d postgresDatabase) checkpoint(ctx context.Context) error {
	_, err := d.ExecContext(ctx, "CHECKPOINT")
	return err
}

func (d postgresDatabase) close() error { return d.Close() }

func runPostgres(ctx context.Context, source string, w workload) (r report, returnErr error) {
	r = report{Engine: "postgres", Rows: w.Rows}
	if err := validatePostgresDSN(source); err != nil {
		return r, err
	}
	pool, err := sql.Open("postgres", source)
	if err != nil {
		return r, fmt.Errorf("open isolated PostgreSQL connection")
	}
	d := postgresDatabase{pool}
	defer func() { returnErr = errors.Join(returnErr, d.close()) }()
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	if err := d.PingContext(ctx); err != nil {
		return r, fmt.Errorf("connect isolated PostgreSQL: %w", err)
	}
	var actual, version string
	if err := d.QueryRowContext(ctx, "SELECT current_database(), version()").Scan(&actual, &version); err != nil {
		return r, err
	}
	u, _ := url.Parse(source)
	if actual != strings.TrimPrefix(u.Path, "/") || !strings.HasPrefix(version, "PostgreSQL ") || strings.Contains(version, "KitDB") {
		return r, fmt.Errorf("benchmark requires a real PostgreSQL server and the exact disposable database")
	}
	r.Version = version
	for _, key := range []string{"fsync", "full_page_writes"} {
		var setting string
		if err := d.QueryRowContext(ctx, "SHOW "+key).Scan(&setting); err != nil || setting != "on" {
			return r, fmt.Errorf("PostgreSQL requires %s=on", key)
		}
	}
	for _, query := range []string{
		"SET synchronous_commit=on", "SET max_parallel_workers_per_gather=0",
		"SET jit=off", "SET work_mem='64MB'", "SET statement_timeout='120s'",
	} {
		if _, err := d.ExecContext(ctx, query); err != nil {
			return r, err
		}
	}
	// CREATE without IF NOT EXISTS deliberately refuses a reused scale.
	schema := pq.QuoteIdentifier(fmt.Sprintf("dbcompare_%d", w.Rows))
	if _, err := d.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		return r, err
	}
	if _, err := d.ExecContext(ctx, "SET search_path TO "+schema+", pg_catalog"); err != nil {
		return r, err
	}
	r.Settings = "local TCP database/sql lib/pq; one connection; synchronous_commit/fsync/full_page_writes=on; parallel query=0; jit=off; work_mem=64MB (not RSS cap); no prepared reuse; ANALYZE after load; exact reference checks"
	r.Phases, err = ingest(ctx, d, w)
	if err == nil {
		err = querySuite(ctx, d, w, &r)
	}
	if err == nil {
		err = mutate(ctx, d, w, &r, nil)
	}
	if err != nil {
		return r, err
	}
	start := time.Now()
	var total int64
	if err := d.QueryRowContext(ctx, "SELECT pg_total_relation_size('products')").Scan(&total); err != nil {
		return r, err
	}
	r.Files = map[string]int64{"products_total_relation_bytes_not_cluster_or_WAL": total}
	r.Phases = append(r.Phases, phase{Name: "relation_size_observation", MS: millis(time.Since(start))})
	return r, nil
}
