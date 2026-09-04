// dbcompare exercises the standalone SQL API, never Kitwork's ORM or VM.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/relational"
	_ "modernc.org/sqlite"
)

type response struct {
	rows  [][]any
	path  string
	stats *relational.ExecutionStats
}
type database interface {
	execute(context.Context, string) (response, error)
	checkpoint(context.Context) error
	close() error
}

type kitDatabase struct{ *relational.Engine }

func (d kitDatabase) execute(ctx context.Context, q string) (response, error) {
	r, err := d.Execute(ctx, q)
	path := "scalar-or-index"
	if r.Execution != nil {
		path = r.Execution.Path
	}
	return response{r.Rows, path, r.Execution}, err
}
func (d kitDatabase) checkpoint(context.Context) error { _, err := d.Checkpoint(); return err }
func (d kitDatabase) close() error                     { return d.Close() }

type sqliteDatabase struct{ *sql.DB }

func (d sqliteDatabase) execute(ctx context.Context, q string) (response, error) {
	if !strings.HasPrefix(q, "SELECT") && !strings.HasPrefix(q, "EXPLAIN") {
		_, err := d.ExecContext(ctx, q)
		return response{}, err
	}
	r, err := d.QueryContext(ctx, q)
	if err != nil {
		return response{}, err
	}
	defer r.Close()
	columns, err := r.Columns()
	if err != nil {
		return response{}, err
	}
	result := response{path: "sqlite"}
	for r.Next() {
		row := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range row {
			pointers[i] = &row[i]
		}
		if err := r.Scan(pointers...); err != nil {
			return response{}, err
		}
		result.rows = append(result.rows, row)
	}
	return result, r.Err()
}
func (d sqliteDatabase) checkpoint(ctx context.Context) error {
	var busy, log, done int
	err := d.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &done)
	if err == nil && busy != 0 {
		return fmt.Errorf("sqlite checkpoint busy")
	}
	return err
}
func (d sqliteDatabase) close() error { return d.Close() }

func openKit(path, mode string, cache int, history bool) (kitDatabase, error) {
	d, err := relational.OpenWithOptions(path, relational.Options{
		BatchAggregates:         mode != "kitdb-row",
		ExperimentalProjections: mode == "kitdb-analytics",
		Kernel:                  kitdb.OpenOptions{PageCacheBytes: int64(cache) << 20, RetainHistory: history},
	})
	return kitDatabase{d}, err
}

func openSQLite(path string, cache int) (sqliteDatabase, error) {
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return sqliteDatabase{}, err
	}
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA wal_autocheckpoint=0", fmt.Sprintf("PRAGMA cache_size=-%d", cache*1024), "PRAGMA mmap_size=0", "PRAGMA foreign_keys=ON"} {
		if _, err = d.Exec(q); err != nil {
			d.Close()
			return sqliteDatabase{}, err
		}
	}
	return sqliteDatabase{d}, nil
}

type timing struct {
	Name             string                     `json:"name"`
	Path             string                     `json:"path"`
	SQL              string                     `json:"sql"`
	SamplesMS        []float64                  `json:"samples_ms"`
	MedianMS         float64                    `json:"median_ms"`
	MinMS            float64                    `json:"min_ms"`
	MaxMS            float64                    `json:"max_ms"`
	GoAllocatedBytes *uint64                    `json:"go_allocated_bytes,omitempty"`
	Rows             [][]any                    `json:"rows"`
	Execution        *relational.ExecutionStats `json:"execution,omitempty"`
}

type phase struct {
	Name       string                       `json:"name"`
	MS         float64                      `json:"ms"`
	Projection *relational.ProjectionReport `json:"projection,omitempty"`
}

type report struct {
	Engine   string             `json:"engine"`
	Version  string             `json:"version"`
	Rows     int                `json:"rows"`
	Settings string             `json:"settings"`
	Phases   []phase            `json:"phases"`
	Queries  []timing           `json:"queries"`
	Plans    map[string][][]any `json:"plans"`
	Files    map[string]int64   `json:"files"`
	Error    string             `json:"error,omitempty"`
}

func millis(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func distribution(t *timing) {
	s := append([]float64(nil), t.SamplesMS...)
	sort.Float64s(s)
	t.MinMS, t.MaxMS = s[0], s[len(s)-1]
	t.MedianMS = s[len(s)/2]
	if len(s)%2 == 0 {
		t.MedianMS = (s[len(s)/2-1] + s[len(s)/2]) / 2
	}
}

func measure(ctx context.Context, d database, q queryCase, repetitions, warmups int, mode string) (timing, error) {
	t := timing{Name: q.Name, SQL: q.SQL}
	for i := 0; i < warmups+repetitions; i++ {
		start := benchmarkNow()
		got, err := d.execute(ctx, q.SQL)
		elapsed := millis(benchmarkSince(start))
		if err != nil {
			return t, err
		}
		if err := checkRows(got.rows, q.Want); err != nil {
			return t, fmt.Errorf("%s: %w", q.Name, err)
		}
		if q.Batch && mode == "kitdb-analytics" && got.path != "kcol-batch" {
			return t, fmt.Errorf("expected KCOL, got %s", got.path)
		}
		if q.Batch && mode == "kitdb-batch" && got.path != "krow-batch" {
			return t, fmt.Errorf("expected KROW batch, got %s", got.path)
		}
		t.Path, t.Rows, t.Execution = got.path, got.rows, got.stats
		if i >= warmups {
			t.SamplesMS = append(t.SamplesMS, elapsed)
		}
	}
	// Allocation is an extra execution, outside timing samples. It is not RSS.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := d.execute(ctx, q.SQL)
	runtime.ReadMemStats(&after)
	if err != nil {
		return t, err
	}
	if err := checkRows(got.rows, q.Want); err != nil {
		return t, err
	}
	bytes := after.TotalAlloc - before.TotalAlloc
	t.GoAllocatedBytes = &bytes
	distribution(&t)
	return t, nil
}

func ingest(ctx context.Context, d database, w workload) ([]phase, error) {
	start := benchmarkNow()
	for _, q := range w.Schema {
		if _, err := d.execute(ctx, q); err != nil {
			return nil, err
		}
	}
	result := []phase{{Name: "schema", MS: millis(benchmarkSince(start))}}
	f, err := os.Open(w.Inserts)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	var insertTime, checkpointTime time.Duration
	loaded, sinceCheckpoint := 0, 0
	start = benchmarkNow()
	for scanner.Scan() {
		t := benchmarkNow()
		_, err := d.execute(ctx, scanner.Text())
		insertTime += benchmarkSince(t)
		if err != nil {
			return nil, err
		}
		count := min(w.Batch, w.Rows-loaded)
		loaded += count
		sinceCheckpoint += count
		if sinceCheckpoint >= w.CheckpointRows {
			t = benchmarkNow()
			err = d.checkpoint(ctx)
			checkpointTime += benchmarkSince(t)
			if err != nil {
				return nil, err
			}
			sinceCheckpoint = 0
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if loaded != w.Rows {
		return nil, fmt.Errorf("fixture row mismatch")
	}
	t := benchmarkNow()
	err = d.checkpoint(ctx)
	checkpointTime += benchmarkSince(t)
	result = append(result, phase{Name: "ingest_wall", MS: millis(benchmarkSince(start))}, phase{Name: "insert_sql", MS: millis(insertTime)}, phase{Name: "checkpoints", MS: millis(checkpointTime)})
	return result, err
}

func files(dir string) (map[string]int64, error) {
	r := make(map[string]int64)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		r[rel] = info.Size()
		return nil
	})
	return r, err
}

func querySuite(ctx context.Context, d database, w workload, r *report) error {
	r.Plans = make(map[string][][]any)
	for _, q := range w.Queries {
		prefix := "EXPLAIN "
		if r.Engine == "sqlite-go" {
			prefix = "EXPLAIN QUERY PLAN "
		}
		plan, err := d.execute(ctx, prefix+q.SQL)
		if err != nil {
			return fmt.Errorf("explain %s: %w", q.Name, err)
		}
		r.Plans[q.Name] = plan.rows
		t, err := measure(ctx, d, q, w.Repetitions, w.Warmups, r.Engine)
		if err != nil {
			return fmt.Errorf("%s: %w", q.Name, err)
		}
		r.Queries = append(r.Queries, t)
		fmt.Fprintf(os.Stderr, "  %-16s %-18s %9.3f ms (%s)\n", r.Engine, q.Name, t.MedianMS, t.Path)
	}
	return nil
}

func mutate(ctx context.Context, d database, w workload, r *report, columnar *relational.Engine) error {
	start := benchmarkNow()
	_, err := d.execute(ctx, w.Update)
	r.Phases = append(r.Phases, phase{Name: "single_update", MS: millis(benchmarkSince(start))})
	if err != nil {
		return err
	}
	q := w.AfterUpdate
	q.Name = "aggregate_after_update"
	mode := r.Engine
	if columnar != nil {
		mode = "kitdb-batch"
	}
	t, err := measure(ctx, d, q, 1, 0, mode)
	if err != nil {
		return err
	}
	r.Queries = append(r.Queries, t)
	if columnar != nil {
		for _, name := range []string{"refresh_after_update", "refresh_unchanged"} {
			start = benchmarkNow()
			p, err := columnar.RefreshAnalytics(ctx)
			r.Phases = append(r.Phases, phase{Name: name, MS: millis(benchmarkSince(start)), Projection: &p})
			if err != nil {
				return err
			}
			if name == "refresh_unchanged" && p.AnalyticsWrittenBytes != 0 {
				return fmt.Errorf("unchanged refresh wrote %d bytes", p.AnalyticsWrittenBytes)
			}
		}
		q.Name = "aggregate_after_refresh"
		t, err = measure(ctx, d, q, w.Repetitions, w.Warmups, r.Engine)
		if err != nil {
			return err
		}
		r.Queries = append(r.Queries, t)
	}
	return nil
}

func runKit(ctx context.Context, dir string, w workload) ([]report, error) {
	path := filepath.Join(dir, "data.kitdb")
	d, err := openKit(path, "kitdb-row", w.CacheMiB, false)
	if err != nil {
		return nil, err
	}
	load, err := ingest(ctx, d, w)
	err = errors.Join(err, d.close())
	if err != nil {
		return nil, err
	}
	var reports []report
	for _, mode := range []string{"kitdb-row", "kitdb-batch", "kitdb-analytics"} {
		start := benchmarkNow()
		d, err := openKit(path, mode, w.CacheMiB, mode == "kitdb-analytics")
		if err != nil {
			return reports, err
		}
		r := report{Engine: mode, Version: runtime.Version(), Rows: w.Rows, Settings: "standalone relational.Execute; durable WAL; GOMAXPROCS=1; page cache configured; retained history enabled only for columnar refresh; shared canonical ingest"}
		r.Phases = append(r.Phases, load...)
		r.Phases = append(r.Phases, phase{Name: "open", MS: millis(benchmarkSince(start))})
		if mode == "kitdb-analytics" {
			start = benchmarkNow()
			p, e := d.RefreshAnalytics(ctx)
			r.Phases = append(r.Phases, phase{Name: "initial_columnar", MS: millis(benchmarkSince(start)), Projection: &p})
			err = e
		}
		if err == nil {
			err = querySuite(ctx, d, w, &r)
		}
		// Keep the shared baseline unmodified until all three read profiles finish.
		if err == nil && mode == "kitdb-analytics" {
			err = mutate(ctx, d, w, &r, d.Engine)
		}
		if err == nil {
			r.Files, err = files(dir)
		}
		err = errors.Join(err, d.close())
		if err != nil {
			r.Error = err.Error()
		}
		reports = append(reports, r)
		if err != nil {
			return reports, err
		}
	}
	return reports, nil
}

func runSQLite(ctx context.Context, dir string, w workload) (r report, err error) {
	r = report{Engine: "sqlite-go", Rows: w.Rows, Settings: "modernc database/sql; WAL synchronous=FULL; auto-checkpoint off; mmap off; one connection; no prepared-statement reuse"}
	d, err := openSQLite(filepath.Join(dir, "data.sqlite"), w.CacheMiB)
	if err != nil {
		return r, err
	}
	defer func() { err = errors.Join(err, d.close()) }()
	v, err := d.execute(ctx, "SELECT sqlite_version()")
	if err != nil {
		return r, err
	}
	r.Version = fmt.Sprint(v.rows[0][0])
	r.Phases, err = ingest(ctx, d, w)
	if err == nil {
		err = querySuite(ctx, d, w, &r)
	}
	if err == nil {
		err = mutate(ctx, d, w, &r, nil)
	}
	if err == nil {
		r.Files, err = files(dir)
	}
	return r, err
}

func runNative(ctx context.Context, python, worker, site, engine, dir, config string) (report, error) {
	r := report{Engine: engine}
	command := exec.CommandContext(ctx, python, worker, "--engine", engine, "--directory", dir, "--workload", config)
	command.Env = os.Environ()
	if site != "" {
		command.Env = append(command.Env, "PYTHONPATH="+site)
	}
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(output, &r); err != nil {
		return r, err
	}
	return r, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateNative(r report, w workload, engine string) error {
	if r.Engine != engine || r.Rows != w.Rows || r.Version == "" || r.Error != "" {
		return fmt.Errorf("invalid native report identity/status")
	}
	expected := make(map[string]queryCase)
	for _, q := range w.Queries {
		expected[q.Name] = q
	}
	after := w.AfterUpdate
	after.Name = "aggregate_after_update"
	expected[after.Name] = after
	for _, t := range r.Queries {
		q, ok := expected[t.Name]
		if !ok || q.SQL != t.SQL {
			return fmt.Errorf("unknown/duplicate native query %s", t.Name)
		}
		delete(expected, t.Name)
		n := w.Repetitions
		if t.Name == after.Name {
			n = 1
		}
		if len(t.SamplesMS) != n {
			return fmt.Errorf("%s: incomplete timing samples", t.Name)
		}
		for _, ms := range t.SamplesMS {
			if math.IsNaN(ms) || math.IsInf(ms, 0) || ms <= 0 {
				return fmt.Errorf("%s: invalid sample", t.Name)
			}
		}
		if err := checkRows(t.Rows, q.Want); err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
	}
	if len(expected) != 0 {
		return fmt.Errorf("native report omitted %d queries", len(expected))
	}
	return nil
}

func run() error {
	scales := flag.String("rows", "10000,100000", "comma-separated row counts, max 1000000 per run")
	repetitions := flag.Int("repetitions", 7, "warm measurements per query")
	batch := flag.Int("batch", 128, "rows per durable SQL INSERT, 1..128 (common SQL token budget)")
	checkpoint := flag.Int("checkpoint-rows", 16384, "explicit checkpoint cadence (all engines)")
	cache := flag.Int("cache-mib", 64, "KitDB/SQLite page cache; DuckDB uses its own 512MB limit")
	output := flag.String("out", "", "new output directory; refuses existing paths")
	python := flag.String("python", "", "optional Python executable for native SQLite and DuckDB")
	worker := flag.String("worker", "benchmarks/dbcompare/native.py", "native adapter script")
	site := flag.String("python-site", "", "isolated Python dependency directory")
	native := flag.String("native", "sqlite-native,duckdb", "native engines when -python is supplied")
	timeout := flag.Duration("timeout", 30*time.Minute, "whole campaign timeout")
	replay := flag.String("replay", "", "existing scale directory: read-only SQL replay, no ingest/refresh/update")
	replayMode := flag.String("replay-mode", "kitdb-row", "kitdb-row, kitdb-batch or kitdb-analytics (existing sidecar only)")
	updated := flag.Bool("updated", false, "replay expects the fixture's midpoint UPDATE")
	queries := flag.String("queries", "indexed_page", "comma-separated fixture query names for replay")
	profile := flag.Bool("profile", false, "write CPU/allocation profiles during replay")
	flag.Parse()
	if *batch < 1 || *batch > 128 || *repetitions < 1 || *repetitions > 100 || *checkpoint < *batch || *cache < 1 || *cache > 1024 || *timeout <= 0 {
		return fmt.Errorf("invalid benchmark limits")
	}
	if *replay != "" {
		return replayQueriesMode(*replay, *output, *queries, *replayMode, *updated, *profile, *repetitions, *cache, *timeout)
	}
	var counts []int
	seen := map[int]bool{}
	for _, s := range strings.Split(*scales, ",") {
		n, err := strconv.Atoi(s)
		if err != nil || n < 128 || n > 1000000 || seen[n] {
			return fmt.Errorf("invalid/duplicate rows %q (128..1000000)", s)
		}
		seen[n] = true
		counts = append(counts, n)
	}
	if len(counts) > 5 {
		return fmt.Errorf("at most five scales")
	}
	for _, engine := range strings.Split(*native, ",") {
		if engine != "sqlite-native" && engine != "duckdb" {
			return fmt.Errorf("unknown native engine %q", engine)
		}
	}
	if *output == "" {
		*output = filepath.Join(".artifacts", "dbcompare-"+time.Now().Format("20060102-150405"))
	}
	root, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return err
	}
	if err = os.Mkdir(root, 0700); err != nil {
		return fmt.Errorf("create NEW report directory: %w", err)
	}
	runtime.GOMAXPROCS(1)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	build, _ := debug.ReadBuildInfo()
	metadata := map[string]any{"started": time.Now().Format(time.RFC3339), "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "gomaxprocs": 1, "cache_mib": *cache, "build": build, "method": "warm sequential SQL; QPC clock on Windows; fixture generation and process startup excluded; every result checked; no cold-cache or RSS claim"}
	if err = writeJSON(filepath.Join(root, "environment.json"), metadata); err != nil {
		return err
	}
	var reports []report
	var failures []error
	for _, n := range counts {
		dir := filepath.Join(root, strconv.Itoa(n))
		if err = os.Mkdir(dir, 0700); err != nil {
			return err
		}
		w, err := createWorkload(dir, n, *batch, *checkpoint, *repetitions, *cache)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\n%d rows: standalone SQL comparison\n", n)
		for _, engine := range []string{"kitdb", "sqlite-go"} {
			dbdir := filepath.Join(dir, engine)
			if err = os.Mkdir(dbdir, 0700); err != nil {
				return err
			}
			var group []report
			if engine == "kitdb" {
				group, err = runKit(ctx, dbdir, w)
			} else {
				var r report
				r, err = runSQLite(ctx, dbdir, w)
				group = []report{r}
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("%d %s: %w", n, engine, err))
				if len(group) == 0 {
					group = []report{{Engine: engine, Rows: n}}
				}
				group[len(group)-1].Error = err.Error()
			}
			reports = append(reports, group...)
			if e := writeJSON(filepath.Join(dir, engine+".json"), group); e != nil {
				return e
			}
		}
		if *python != "" {
			for _, engine := range strings.Split(*native, ",") {
				dbdir := filepath.Join(dir, engine)
				if err = os.Mkdir(dbdir, 0700); err != nil {
					return err
				}
				r, e := runNative(ctx, *python, *worker, *site, engine, dbdir, filepath.Join(dir, "workload.json"))
				if e == nil {
					e = validateNative(r, w, engine)
				}
				if e != nil {
					r.Error = e.Error()
					failures = append(failures, fmt.Errorf("%d %s: %w", n, engine, e))
				}
				reports = append(reports, r)
				if err = writeJSON(filepath.Join(dir, engine+".json"), r); err != nil {
					return err
				}
			}
		}
	}
	if err = writeJSON(filepath.Join(root, "results.json"), reports); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nRaw results: %s\n", filepath.Join(root, "results.json"))
	return errors.Join(failures...)
}
