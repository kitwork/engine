//go:build turso_scale && windows

package searchscale

import (
	"database/sql"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	tursolibs "github.com/tursodatabase/turso-go-platform-libs"
	turso "turso.tech/database/tursogo"
)

var (
	tursoScaleLibrary = flag.String("kitwork-turso-library", "", "path to an FTS-enabled turso_sync_sdk_kit.dll")
	tursoScaleSizes   = flag.String("kitwork-turso-sizes", "100000", "comma-separated document counts")
	tursoScaleSamples = flag.Int("kitwork-turso-samples", 21, "hot query samples per query after warmup")
)

var tursoScaleTitles = [...]string{
	"Áo thun cotton nam Nike chính hãng %s",
	"Áo khoác nữ công sở thanh lịch %s",
	"Quần jean nam co giãn bền đẹp %s",
	"Giày thể thao Nike nhẹ êm chân %s",
	"Túi xách nữ da mềm cao cấp %s",
	"Đồng hồ nam chống nước hiện đại %s",
}

const tursoScaleBody = "Sản phẩm được thiết kế cho nhu cầu sử dụng hằng ngày với chất liệu bền đẹp đường may chắc chắn kiểu dáng hiện đại dễ phối đồ phù hợp nhiều hoàn cảnh đóng gói cẩn thận kiểm tra chất lượng trước khi giao hàng"

type tursoQueryMeasurement struct {
	p50            time.Duration
	p95            time.Duration
	max            time.Duration
	allocatedBytes uint64
	hits           int
	batch          int
}

func TestTursoScale(t *testing.T) {
	loadTursoScaleLibrary(t)
	sizes := parseTursoSizes(t, *tursoScaleSizes)
	if *tursoScaleSamples < 5 {
		t.Fatal("kitwork-turso-samples must be at least 5")
	}

	for _, size := range sizes {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			runTursoScale(t, size, *tursoScaleSamples)
		})
	}
}

func TestTursoNativeAccentBehavior(t *testing.T) {
	loadTursoScaleLibrary(t)
	dsn := filepath.Join(t.TempDir(), "accent.db") + "?experimental=index_method"
	db := openTursoScaleDB(t, dsn)
	defer db.Close()
	mustTursoExec(t, db, "CREATE TABLE docs (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")
	mustTursoExec(t, db, "CREATE INDEX docs_fts ON docs USING fts (body)")
	mustTursoExec(t, db, "INSERT INTO docs VALUES (1, 'Áo khoác nữ công sở')")

	var accented, unaccented int
	if err := db.QueryRow("SELECT count(*) FROM docs WHERE fts_match(body, ?)", "áo").Scan(&accented); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM docs WHERE fts_match(body, ?)", "ao").Scan(&unaccented); err != nil {
		t.Fatal(err)
	}
	if accented != 1 {
		t.Fatalf("accented Turso query returned %d rows, want 1", accented)
	}
	t.Logf("TURSO_ACCENT accented_hits=%d unaccented_hits=%d", accented, unaccented)
}

func loadTursoScaleLibrary(t *testing.T) {
	t.Helper()
	if *tursoScaleLibrary == "" {
		t.Fatal("kitwork-turso-library is required")
	}
	library, err := filepath.Abs(*tursoScaleLibrary)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(library); err != nil || info.IsDir() {
		t.Fatalf("invalid Turso library %q: %v", library, err)
	}
	if filepath.Base(library) != "turso_sync_sdk_kit.dll" {
		t.Fatalf("Turso system loader requires turso_sync_sdk_kit.dll, got %q", library)
	}
	t.Setenv("PATH", filepath.Dir(library)+string(os.PathListSeparator)+os.Getenv("PATH"))
	turso.InitLibrary(tursolibs.LoadTursoLibraryConfig{LoadStrategy: tursolibs.SystemLibraryLoadStrategy})
}

func parseTursoSizes(t *testing.T, input string) []int {
	t.Helper()
	parts := strings.Split(input, ",")
	sizes := make([]int, 0, len(parts))
	for _, part := range parts {
		size, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || size <= 0 {
			t.Fatalf("invalid scale size %q", part)
		}
		sizes = append(sizes, size)
	}
	return sizes
}

func runTursoScale(t *testing.T, count, samples int) {
	t.Helper()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "products.db")
	dsn := databasePath + "?experimental=index_method"
	db := openTursoScaleDB(t, dsn)

	mustTursoExec(t, db, `CREATE TABLE products (
		id INTEGER PRIMARY KEY,
		sku TEXT NOT NULL UNIQUE,
		title TEXT NOT NULL,
		body TEXT NOT NULL,
		search_title TEXT NOT NULL,
		search_body TEXT NOT NULL
	)`)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineRSS, err := currentProcessRSS()
	if err != nil {
		t.Fatal(err)
	}
	peakRSS := atomic.Uint64{}
	peakRSS.Store(baselineRSS)
	stopSampling := make(chan struct{})
	samplingStopped := make(chan struct{})
	go sampleProcessRSS(&peakRSS, stopSampling, samplingStopped)

	insertStarted := time.Now()
	insertTursoProducts(t, db, count)
	insertElapsed := time.Since(insertStarted)

	indexStarted := time.Now()
	mustTursoExec(t, db, "CREATE INDEX products_fts ON products USING fts (search_title, search_body)")
	indexElapsed := time.Since(indexStarted)

	optimizeStarted := time.Now()
	_, optimizeErr := db.Exec("OPTIMIZE INDEX products_fts")
	optimizeElapsed := time.Since(optimizeStarted)
	if optimizeErr != nil {
		t.Logf("TURSO_OPTIMIZE_UNAVAILABLE documents=%d error=%q", count, optimizeErr)
	}
	buildElapsed := insertElapsed + indexElapsed
	if optimizeErr == nil {
		buildElapsed += optimizeElapsed
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	close(stopSampling)
	<-samplingStopped

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	buildAllocated := after.TotalAlloc - before.TotalAlloc
	databaseBytes, fileCount, err := tursoDirectorySize(directory)
	if err != nil {
		t.Fatal(err)
	}

	db = openTursoScaleDB(t, dsn)
	t.Cleanup(func() { _ = db.Close() })
	var documentCount int
	if err := db.QueryRow("SELECT count(*) FROM products").Scan(&documentCount); err != nil {
		t.Fatal(err)
	}

	queries := []struct {
		name string
		text string
	}{
		{name: "exact_sku", text: fmt.Sprintf("sku%09d", count-1)},
		{name: "one_common", text: "thun"},
		{name: "two_terms", text: "áo nike"},
		{name: "four_terms", text: "áo thun cotton nam"},
		{name: "unaccented", text: "ao khoac nu cong so"},
	}
	measurements := make(map[string]tursoQueryMeasurement, len(queries))
	for _, item := range queries {
		measurements[item.name] = measureTursoQuery(t, db, item.text, samples)
	}
	queryRSS, err := currentProcessRSS()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("TURSO_SCALE documents=%d avg_tokens=63.0 insert=%s index=%s optimize=%s optimize_ok=%t build=%s build_docs_per_sec=%.0f database_mb=%.2f files=%d baseline_rss_mb=%.2f peak_build_rss_mb=%.2f query_rss_mb=%.2f baseline_go_heap_mb=%.2f build_allocated_gb=%.2f native_dll_mb=%.2f",
		documentCount,
		insertElapsed.Round(time.Millisecond),
		indexElapsed.Round(time.Millisecond),
		optimizeElapsed.Round(time.Millisecond),
		optimizeErr == nil,
		buildElapsed.Round(time.Millisecond),
		float64(documentCount)/buildElapsed.Seconds(),
		tursoBytesToMiB(databaseBytes),
		fileCount,
		tursoBytesToMiB(baselineRSS),
		tursoBytesToMiB(peakRSS.Load()),
		tursoBytesToMiB(queryRSS),
		tursoBytesToMiB(before.HeapAlloc),
		float64(buildAllocated)/(1<<30),
		fileSizeMiB(t, *tursoScaleLibrary),
	)
	for _, item := range queries {
		measurement := measurements[item.name]
		t.Logf("TURSO_QUERY documents=%d name=%s text=%q hits=%d batch=%d p50_ms=%.3f p95_ms=%.3f max_ms=%.3f go_allocated_kb_per_op=%.2f",
			documentCount,
			item.name,
			item.text,
			measurement.hits,
			measurement.batch,
			float64(measurement.p50)/float64(time.Millisecond),
			float64(measurement.p95)/float64(time.Millisecond),
			float64(measurement.max)/float64(time.Millisecond),
			float64(measurement.allocatedBytes)/(1<<10),
		)
	}
}

func openTursoScaleDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("turso", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return db
}

func insertTursoProducts(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	colors := [...]string{"đen", "trắng", "xanh", "đỏ", "nâu", "xám"}
	sizes := [...]string{"s", "m", "l", "xl", "xxl"}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	statement, err := tx.Prepare(`INSERT INTO products
		(id, sku, title, body, search_title, search_body) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()

	for i := 0; i < count; i++ {
		sku := fmt.Sprintf("sku%09d", i)
		title := fmt.Sprintf(tursoScaleTitles[i%len(tursoScaleTitles)], sku)
		body := fmt.Sprintf("%s Màu %s và kích thước %s. Mã tham chiếu %s.",
			tursoScaleBody, colors[i%len(colors)], sizes[i%len(sizes)], sku)
		if _, err := statement.Exec(i+1, sku, title, body, foldTursoText(title), foldTursoText(body)); err != nil {
			t.Fatalf("insert product %d: %v", i, err)
		}
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	committed = true
}

func measureTursoQuery(t *testing.T, db *sql.DB, text string, samples int) tursoQueryMeasurement {
	t.Helper()
	query := tursoQueryText(text)
	for range 3 {
		if _, _, err := runTursoQuery(db, query); err != nil {
			t.Fatal(err)
		}
	}

	batch := 1
	for {
		started := time.Now()
		for range batch {
			if _, _, err := runTursoQuery(db, query); err != nil {
				t.Fatal(err)
			}
		}
		if time.Since(started) >= 20*time.Millisecond || batch >= 1024 {
			break
		}
		batch *= 2
	}

	durations := make([]time.Duration, samples)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	hits := 0
	consumed := 0
	for i := range durations {
		started := time.Now()
		for range batch {
			var err error
			hits, consumed, err = runTursoQuery(db, query)
			if err != nil {
				t.Fatal(err)
			}
		}
		durations[i] = time.Since(started) / time.Duration(batch)
	}
	runtime.KeepAlive(consumed)
	runtime.ReadMemStats(&after)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return tursoQueryMeasurement{
		p50:            tursoPercentile(durations, 50),
		p95:            tursoPercentile(durations, 95),
		max:            durations[len(durations)-1],
		allocatedBytes: (after.TotalAlloc - before.TotalAlloc) / uint64(samples*batch),
		hits:           hits,
		batch:          batch,
	}
}

func runTursoQuery(db *sql.DB, query string) (int, int, error) {
	rows, err := db.Query(`SELECT id, title, body, fts_score(search_title, search_body, ?) AS score
		FROM products
		WHERE fts_match(search_title, search_body, ?)
		ORDER BY score DESC
		LIMIT 20`, query, query)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	hits := 0
	consumed := 0
	for rows.Next() {
		var id int64
		var title, body string
		var score float64
		if err := rows.Scan(&id, &title, &body, &score); err != nil {
			return 0, 0, err
		}
		hits++
		consumed += int(id&1) + len(title) + len(body) + int(score)
	}
	return hits, consumed, rows.Err()
}

func tursoQueryText(text string) string {
	return strings.Join(strings.Fields(foldTursoText(text)), " AND ")
}

var tursoFoldTable = buildTursoFoldTable()

func buildTursoFoldTable() map[rune]rune {
	groups := map[rune]string{
		'a': "áàảãạăắằẳẵặâấầẩẫậ",
		'e': "éèẻẽẹêếềểễệ",
		'i': "íìỉĩị",
		'o': "óòỏõọôốồổỗộơớờởỡợ",
		'u': "úùủũụưứừửữự",
		'y': "ýỳỷỹỵ",
		'd': "đ",
		'c': "ç",
		'n': "ñ",
	}
	result := make(map[rune]rune, 128)
	for base, variants := range groups {
		for _, variant := range variants {
			result[variant] = base
		}
	}
	return result
}

func foldTursoText(text string) string {
	var result strings.Builder
	result.Grow(len(text))
	space := true
	for _, r := range text {
		r = unicode.ToLower(r)
		if folded, ok := tursoFoldTable[r]; ok {
			r = folded
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result.WriteRune(r)
			space = false
			continue
		}
		if !space {
			result.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(result.String())
}

func mustTursoExec(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func tursoPercentile(sorted []time.Duration, percentile int) time.Duration {
	position := (len(sorted)*percentile + 99) / 100
	if position < 1 {
		position = 1
	}
	return sorted[position-1]
}

func tursoDirectorySize(path string) (uint64, int, error) {
	var size uint64
	files := 0
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
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
		size += uint64(info.Size())
		files++
		return nil
	})
	return size, files, err
}

func fileSizeMiB(t *testing.T, path string) float64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return tursoBytesToMiB(uint64(info.Size()))
}

func tursoBytesToMiB(bytes uint64) float64 {
	return float64(bytes) / (1 << 20)
}
