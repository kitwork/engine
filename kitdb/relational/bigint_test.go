package relational

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func bigintTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := OpenWithOptions(filepath.Join(t.TempDir(), "big.kitdb"), Options{
		ExperimentalProjections: true, Kernel: kitdbengine.OpenOptions{RetainHistory: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestBigIntKeysConstraintsAndOrder(t *testing.T) {
	e := bigintTestEngine(t)
	q := func(source string, args ...any) Result { return functionTestExecute(t, e, source, args...) }
	q(`CREATE TABLE numbers (id BIGINT PRIMARY KEY, u INT8 UNIQUE, bucket TEXT, n BIGINT, CHECK (id = u))`)
	values := []int64{math.MinInt64, math.MinInt64 + 1, -9007199254740993, -1, 0, 9007199254740992, 9007199254740993, math.MaxInt64 - 1, math.MaxInt64}
	for i := len(values) - 1; i >= 0; i-- {
		q(`INSERT INTO numbers (id,u,bucket,n) VALUES ($1,$2,'a',$3)`, values[i], values[i], values[i])
	}
	q(`CREATE INDEX numbers_bucket_n ON numbers (bucket,n)`)
	q(`CREATE UNIQUE INDEX numbers_bucket_u ON numbers (bucket,u)`)
	q(`CREATE TABLE children (id INTEGER PRIMARY KEY, parent BIGINT REFERENCES numbers(id))`)
	q(`INSERT INTO children (id,parent) VALUES (1,9007199254740993)`)
	for _, value := range values {
		for _, query := range []string{
			`SELECT id FROM numbers WHERE id = $1`,
			`SELECT id FROM numbers WHERE u = $1`,
			`SELECT id FROM numbers WHERE bucket = 'a' AND n = $1`,
			`SELECT id FROM numbers WHERE bucket = 'a' AND u = $1`,
		} {
			if r := q(query, value); !reflect.DeepEqual(r.Rows, [][]any{{value}}) {
				t.Fatalf("%s(%d): %#v", query, value, r)
			}
		}
	}
	for _, query := range []string{
		`SELECT id FROM numbers ORDER BY id`,
		`SELECT id FROM numbers WHERE bucket = 'a' ORDER BY n`,
		`SELECT id FROM numbers ORDER BY id + 0`,
	} {
		r := q(query)
		for i, value := range values {
			if r.Rows[i][0] != value {
				t.Fatalf("%s: %#v", query, r.Rows)
			}
		}
	}
	if r := q(`SELECT id FROM numbers WHERE bucket = 'a' AND n >= 9007199254740992 AND n < 9223372036854775807 ORDER BY n DESC`); !reflect.DeepEqual(r.Rows, [][]any{{int64(math.MaxInt64 - 1)}, {int64(9007199254740993)}, {int64(9007199254740992)}}) {
		t.Fatalf("bounded reverse range: %#v", r)
	}
	if r := q(`SELECT id FROM numbers WHERE id IN (9007199254740992,9007199254740993) ORDER BY id`); len(r.Rows) != 2 {
		t.Fatal(r)
	}
	if r := q(`SELECT n, COUNT(*) FROM numbers GROUP BY n ORDER BY n`); len(r.Rows) != len(values) {
		t.Fatal(r)
	}
	q(`UPDATE numbers SET n = n - 1 WHERE id = 9007199254740993`)
	if r := q(`SELECT id FROM numbers WHERE bucket = 'a' AND n = 9007199254740992 ORDER BY id`); len(r.Rows) != 2 {
		t.Fatal(r)
	}
	for _, invalid := range []string{
		`INSERT INTO numbers (id,u) VALUES (9007199254740993,9007199254740993)`,
		`INSERT INTO numbers (id,u) VALUES (4,9007199254740993)`,
		`INSERT INTO children (id,parent) VALUES (2,9007199254740994)`,
		`DELETE FROM numbers WHERE id = 9007199254740993`,
	} {
		if _, err := e.Execute(context.Background(), invalid); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	q(`DELETE FROM numbers WHERE id = 9007199254740992`)
	if r := q(`SELECT id FROM numbers WHERE id = 9007199254740993`); len(r.Rows) != 1 {
		t.Fatal("delete lost adjacent key")
	}
}

func TestBigIntAggregatesFunctionsAndBoundaries(t *testing.T) {
	e := bigintTestEngine(t)
	q := func(source string, args ...any) Result { return functionTestExecute(t, e, source, args...) }
	q(`CREATE TABLE numbers (id INTEGER PRIMARY KEY, n BIGINT, g BIGINT DEFAULT 9007199254740993)`)
	q(`INSERT INTO numbers (id,n) VALUES (1,9223372036854775807),(2,9223372036854775806),(3,NULL)`)
	if r := q(`SELECT g FROM numbers WHERE id = 1`); r.Rows[0][0] != int64(9007199254740993) {
		t.Fatalf("default lost precision: %#v", r)
	}
	q(`CREATE FUNCTION add_one(n BIGINT) RETURNS BIGINT LANGUAGE SQL RETURN n + 1`)
	q(`CREATE FUNCTION choose_n(n BIGINT) RETURNS BIGINT LANGUAGE SQL RETURN coalesce(n,0)`)
	if r := q(`SELECT add_one(9007199254740992), choose_n(NULL)`); !reflect.DeepEqual(r.Rows, [][]any{{int64(9007199254740993), int64(0)}}) {
		t.Fatal(r)
	}
	if r := q(`SELECT -9223372036854775808, 9223372036854775807`); !reflect.DeepEqual(r.Rows, [][]any{{int64(math.MinInt64), int64(math.MaxInt64)}}) {
		t.Fatal(r)
	}
	if _, err := e.RefreshAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT COUNT(n),SUM(n),AVG(n),MIN(n),MAX(n) FROM numbers`,
		`SELECT COUNT(n),SUM(n),AVG(n),MIN(n),MAX(n) FROM numbers GROUP BY g`,
	} {
		r := q(query)
		want := [][]any{{int64(2), "18446744073709551613", "9223372036854775806.5", int64(math.MaxInt64 - 1), int64(math.MaxInt64)}}
		if !reflect.DeepEqual(r.Rows, want) || r.Columns[1].Kind != "decimal" || r.Execution == nil || r.Execution.Path != "kcol-batch" {
			t.Fatalf("exact aggregate vectors: %#v", r)
		}
		e.experimentalProjections, e.batchAggregates = false, false
		plain := q(query)
		if !reflect.DeepEqual(plain.Rows, want) {
			t.Fatalf("scalar exact aggregate: %#v", plain)
		}
		e.batchAggregates = true
		batch := q(query)
		if !reflect.DeepEqual(batch.Rows, want) || batch.Execution == nil || batch.Execution.Path != "krow-batch" {
			t.Fatalf("row batch exact aggregate: %#v", batch)
		}
		e.experimentalProjections = true
	}
	if r := q(`SELECT SUM(n) AS total FROM numbers GROUP BY g HAVING total > '18446744073709551612'`); len(r.Rows) != 1 {
		t.Fatal(r)
	}
	if r := q(`SELECT SUM(n),AVG(n) FROM numbers WHERE id < 0`); !reflect.DeepEqual(r.Rows, [][]any{{nil, nil}}) {
		t.Fatal(r)
	}
	if r := q(`SELECT MIN(n),MAX(n) FROM numbers`); r.Execution == nil || r.Execution.Path != "kcol-batch" || r.Rows[0][1] != int64(math.MaxInt64) {
		t.Fatalf("int64 vector extrema: %#v", r)
	}
	for _, invalid := range []string{
		`SELECT 9223372036854775808`, `SELECT -9223372036854775809`,
		`SELECT add_one(9223372036854775807)`, `SELECT abs(-9223372036854775808)`,
		`UPDATE numbers SET n = n + 1 WHERE id = 1`,
		`INSERT INTO numbers (id,n) VALUES (4,1),(5,9223372036854775808)`,
		`INSERT INTO numbers (id,n) VALUES (6,'9223372036854775808')`,
	} {
		if _, err := e.Execute(context.Background(), invalid); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	if r := q(`SELECT COUNT(*) FROM numbers`); r.Rows[0][0] != int64(3) {
		t.Fatal("failed statement leaked rows")
	}
	for _, invalid := range []any{float64(0x1p63), uint64(1) << 63, "-9223372036854775809", math.Inf(1), math.NaN()} {
		if _, err := e.Execute(context.Background(), `INSERT INTO numbers (id,n) VALUES (4,$1)`, invalid); err == nil {
			t.Fatalf("accepted %#v", invalid)
		}
	}
	q(`CREATE TABLE legacy (id INTEGER PRIMARY KEY)`)
	if _, err := e.Execute(context.Background(), `INSERT INTO legacy (id) VALUES (9007199254740993)`); err == nil {
		t.Fatal("legacy catalog admission changed")
	}
	q(`CREATE TABLE decimals (id INTEGER PRIMARY KEY, amount NUMERIC)`)
	q(`INSERT INTO decimals (id,amount) VALUES (1,9223372036854775807)`)
	if r := q(`SELECT amount FROM decimals WHERE id = 1`); r.Rows[0][0] != "9223372036854775807" {
		t.Fatalf("BIGINT to decimal lost digits: %#v", r)
	}
}

func TestBigIntMixedComparisonAgainstExactOracle(t *testing.T) {
	pairs := [][2]any{{int64(math.MaxInt64), float64(0x1p63)}, {int64(math.MinInt64), -0x1p63}, {int64(9007199254740993), float64(9007199254740992)}, {int64(-9007199254740993), float64(-9007199254740992)}, {int64(1), 1.25}, {int64(-1), -1.25}}
	rng := rand.New(rand.NewPCG(53, 64))
	for range 10000 {
		pairs = append(pairs, [2]any{int64(rng.Uint64()), math.Ldexp(rng.Float64()*2-1, int(rng.Uint64()%65))})
	}
	for _, pair := range pairs {
		var a, b big.Rat
		a.SetInt64(pair[0].(int64))
		b.SetFloat64(pair[1].(float64))
		want := a.Cmp(&b)
		if got := compareValues(pair[0], pair[1]); got != want || compareValues(pair[1], pair[0]) != -want {
			t.Fatalf("%#v: got %d want %d", pair, got, want)
		}
	}
}

func TestBigIntUniqueAndTransactionRollback(t *testing.T) {
	e := bigintTestEngine(t)
	q := func(source string, args ...any) Result { return functionTestExecute(t, e, source, args...) }
	q(`CREATE TABLE numbers (id INTEGER PRIMARY KEY, u BIGINT UNIQUE)`)
	q(`INSERT INTO numbers (id,u) VALUES (1,9007199254740992),(2,9007199254740993)`)
	if _, err := e.Execute(context.Background(), `INSERT INTO numbers (id,u) VALUES (3,9007199254740993)`); err == nil {
		t.Fatal("duplicate unique BIGINT accepted")
	}
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Execute(context.Background(), `UPDATE numbers SET u = 9223372036854775807 WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if r, err := tx.Execute(context.Background(), `SELECT id FROM numbers WHERE u = 9223372036854775807`); err != nil || len(r.Rows) != 1 {
		t.Fatalf("read own write: %#v %v", r, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if r := q(`SELECT id FROM numbers WHERE u = 9007199254740993`); !reflect.DeepEqual(r.Rows, [][]any{{int64(2)}}) {
		t.Fatalf("rollback lost unique key: %#v", r)
	}
	if r := q(`SELECT id FROM numbers WHERE u = 9223372036854775807`); len(r.Rows) != 0 {
		t.Fatal("rollback leaked unique key")
	}
}

func TestBigIntAccumulatorRoundingAndCancellation(t *testing.T) {
	legacy := newBatchGroups([]int{0}, []aggregateBinding{{function: "sum", field: &kitdbsql.Field{Kind: "integer"}}}, 100, nil)
	exact := newBatchGroups([]int{0}, []aggregateBinding{{function: "sum", field: &kitdbsql.Field{Kind: "bigint"}}}, 100, nil)
	if exact.entryCost < legacy.entryCost+128 {
		t.Fatal("exact group accumulator omitted from memory accounting")
	}
	for _, test := range []struct {
		values   []int64
		sum, avg string
	}{
		{[]int64{1, 0, 0}, "1", "0.3333333333333333"},
		{[]int64{-1, 0, 0, 0, 0, 0}, "-1", "-0.1666666666666667"},
		{[]int64{math.MaxInt64, math.MaxInt64, math.MinInt64, math.MinInt64}, "-2", "-0.5"},
	} {
		var sum integerSum
		for _, v := range test.values {
			sum.add(v)
		}
		if sum.result(int64(len(test.values)), "sum") != test.sum || sum.result(int64(len(test.values)), "avg") != test.avg {
			t.Fatalf("bad exact accumulator: %#v", test)
		}
	}
}

func TestBigIntCorruptColumnarDiscardsPartialExactSums(t *testing.T) {
	e := bigintTestEngine(t)
	q := func(source string, args ...any) Result { return functionTestExecute(t, e, source, args...) }
	q(`CREATE TABLE numbers (id INTEGER PRIMARY KEY, g BIGINT, p BIGINT, n BIGINT)`)
	for start := 0; start < 2100; start += 100 {
		var rows []string
		for i := start; i < start+100; i++ {
			rows = append(rows, fmt.Sprintf("(%d,%d,%d,9223372036854775807)", i, i%2, i))
		}
		q(`INSERT INTO numbers (id,g,p,n) VALUES ` + strings.Join(rows, ","))
	}
	if _, err := e.RefreshAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		`SELECT SUM(n),AVG(n) FROM numbers WHERE p < 2050`,
		`SELECT g,SUM(n),AVG(n) FROM numbers WHERE p < 2050 GROUP BY g ORDER BY g`,
	}
	wants := []Result{q(queries[0]), q(queries[1])}
	file, err := snapshotfile.Open(e.Path() + ".analytics")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := e.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := schemaFromCatalog(catalog, "numbers")
	if err != nil {
		t.Fatal(err)
	}
	section, err := file.Section(schema.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, base, length := section.Outer()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(e.Path()+".analytics", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var last [1]byte
	offset := base + length - 1
	if _, err := f.ReadAt(last[:], offset); err != nil {
		t.Fatal(err)
	}
	last[0] ^= 0x40
	if _, err := f.WriteAt(last[:], offset); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for i, query := range queries {
		got := q(query)
		if !reflect.DeepEqual(got.Rows, wants[i].Rows) || got.Execution == nil || got.Execution.Path != "krow-batch" || got.Execution.Fallback == "" {
			t.Fatalf("partial sum leaked: %#v stats=%+v", got, got.Execution)
		}
	}
}

func TestBigIntCrashRecovery(t *testing.T) {
	if path := os.Getenv("KITDB_BIGINT_CRASH_CHILD"); path != "" {
		e, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		functionTestExecute(t, e, `CREATE TABLE numbers (id BIGINT PRIMARY KEY, u BIGINT UNIQUE)`)
		functionTestExecute(t, e, `CREATE INDEX big_u ON numbers (u)`)
		functionTestExecute(t, e, `CREATE FUNCTION keep_big(n BIGINT) RETURNS BIGINT LANGUAGE SQL RETURN n`)
		functionTestExecute(t, e, `INSERT INTO numbers (id,u) VALUES (9223372036854775807,-9223372036854775808),(9007199254740993,9007199254740992)`)
		os.Exit(31) // No Close/checkpoint: recovery must replay the durable WAL.
	}
	path := filepath.Join(t.TempDir(), "crashed.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestBigIntCrashRecovery$")
	child.Env = append(os.Environ(), "KITDB_BIGINT_CRASH_CHILD="+path)
	output, err := child.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 31 {
		t.Fatalf("child: %v %s", err, output)
	}
	e, err := OpenWithOptions(path, Options{Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	check := func(e *Engine) {
		r := functionTestExecute(t, e, `SELECT keep_big(id),u FROM numbers WHERE u = -9223372036854775808`)
		if !reflect.DeepEqual(r.Rows, [][]any{{int64(math.MaxInt64), int64(math.MinInt64)}}) {
			t.Fatal(r)
		}
	}
	check(e)
	anchor := filepath.Join(t.TempDir(), "backup.kitdb")
	if _, err := e.database.CreateBackupAnchor(context.Background(), anchor); err != nil {
		t.Fatal(err)
	}
	backup, err := Open(anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	check(backup)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check(e)
}

func TestBigIntPostgresWire(t *testing.T) {
	e := bigintTestEngine(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- e.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "big", User: "kitdb", Password: "test-only"}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:test-only@%s/big?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{`CREATE TABLE numbers (id BIGINT PRIMARY KEY)`, `CREATE FUNCTION keep_big(n BIGINT) RETURNS BIGINT LANGUAGE SQL RETURN n`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	var value int64
	if err := db.QueryRow(`INSERT INTO numbers (id) VALUES ($1) RETURNING id`, int64(math.MaxInt64)).Scan(&value); err != nil || value != math.MaxInt64 {
		t.Fatalf("insert int8: %d %v", value, err)
	}
	if err := db.QueryRow(`SELECT keep_big(id) FROM numbers WHERE id = $1`, int64(math.MaxInt64)).Scan(&value); err != nil || value != math.MaxInt64 {
		t.Fatalf("prepared int8: %d %v", value, err)
	}
	var sum string
	if err := db.QueryRow(`SELECT SUM(id) FROM numbers`).Scan(&sum); err != nil || sum != "9223372036854775807" {
		t.Fatalf("numeric sum: %q %v", sum, err)
	}
	rows, err := db.Query(`SELECT id FROM numbers`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || !strings.EqualFold(types[0].DatabaseTypeName(), "INT8") {
		t.Fatalf("type metadata: %#v %v", types, err)
	}
}
