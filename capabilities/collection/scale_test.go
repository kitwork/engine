//go:build scale

package collection

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

var (
	scaleSizes   = flag.String("kitwork-scale-sizes", "100000", "comma-separated document counts")
	scaleSamples = flag.Int("kitwork-scale-samples", 21, "hot query samples per query after warmup")
)

type scaleProductTemplate struct {
	title string
}

var scaleProductTemplates = [...]scaleProductTemplate{
	{title: "Áo thun cotton nam Nike chính hãng %s"},
	{title: "Áo khoác nữ công sở thanh lịch %s"},
	{title: "Quần jean nam co giãn bền đẹp %s"},
	{title: "Giày thể thao Nike nhẹ êm chân %s"},
	{title: "Túi xách nữ da mềm cao cấp %s"},
	{title: "Đồng hồ nam chống nước hiện đại %s"},
}

const scaleDescription = "Sản phẩm được thiết kế cho nhu cầu sử dụng hằng ngày với chất liệu bền đẹp đường may chắc chắn kiểu dáng hiện đại dễ phối đồ phù hợp nhiều hoàn cảnh đóng gói cẩn thận kiểm tra chất lượng trước khi giao hàng"

type scaleQueryMeasurement struct {
	p50            time.Duration
	p95            time.Duration
	max            time.Duration
	allocatedBytes uint64
	hits           int
	batch          int
}

func TestFTSScale(t *testing.T) {
	sizes := parseScaleSizes(t, *scaleSizes)
	if *scaleSamples < 5 {
		t.Fatalf("kitwork-scale-samples must be at least 5")
	}

	for _, size := range sizes {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			runFTSScale(t, size, *scaleSamples)
		})
	}
}

func parseScaleSizes(t *testing.T, input string) []int {
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

func runFTSScale(t *testing.T, count, samples int) {
	t.Helper()
	databasePath := t.TempDir() + string(os.PathSeparator) + "collection.db"
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}

	index := &ftsIndex{db: db, collection: "products"}
	if err := index.ensureSchema(); err != nil {
		t.Fatal(err)
	}

	generationStarted := time.Now()
	documents := makeScaleProducts(count)
	generationElapsed := time.Since(generationStarted)
	runtime.GC()

	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineRSS, rssErr := currentCollectionProcessRSS()
	if rssErr != nil {
		t.Fatal(rssErr)
	}
	peakHeap := atomic.Uint64{}
	peakHeap.Store(before.HeapAlloc)
	peakRSS := atomic.Uint64{}
	peakRSS.Store(baselineRSS)
	stopSampling := make(chan struct{})
	samplingStopped := make(chan struct{})
	go samplePeakHeap(&peakHeap, stopSampling, samplingStopped)
	stopRSSSampling := make(chan struct{})
	rssSamplingStopped := make(chan struct{})
	go sampleCollectionProcessRSS(&peakRSS, stopRSSSampling, rssSamplingStopped)

	buildStarted := time.Now()
	err = index.rebuild(documents, fmt.Sprintf("scale-%d", count))
	buildElapsed := time.Since(buildStarted)
	close(stopSampling)
	<-samplingStopped
	close(stopRSSSampling)
	<-rssSamplingStopped
	if err != nil {
		t.Fatal(err)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	updatePeak(&peakHeap, after.HeapAlloc)
	buildAllocated := after.TotalAlloc - before.TotalAlloc

	// The source slice is not part of the persisted search projection. Releasing it makes per-query
	// allocation measurements describe the query path rather than a pending source collection.
	documents = nil
	runtime.GC()

	queries := []struct {
		name  string
		query string
	}{
		{name: "exact_sku", query: fmt.Sprintf("sku%09d", count-1)},
		{name: "one_common", query: "thun"},
		{name: "two_terms", query: "áo nike"},
		{name: "four_terms", query: "áo thun cotton nam"},
		{name: "unaccented", query: "ao khoac nu cong so"},
	}

	measurements := make(map[string]scaleQueryMeasurement, len(queries))
	for _, query := range queries {
		measurements[query.name] = measureScaleQuery(t, index, query.query, samples)
	}
	queryRSS, err := currentCollectionProcessRSS()
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var documentCount, totalLength int
	if err := db.QueryRow(`SELECT ndoc, totallen FROM fts_stat WHERE collection = ?`, index.collection).
		Scan(&documentCount, &totalLength); err != nil {
		t.Fatal(err)
	}
	var vocabulary, postingBytes, largestPosting int64
	if err := db.QueryRow(`SELECT count(*), coalesce(sum(length(doclist)), 0),
		coalesce(max(length(doclist)), 0) FROM fts_term WHERE collection = ?`, index.collection).
		Scan(&vocabulary, &postingBytes, &largestPosting); err != nil {
		t.Fatal(err)
	}

	t.Logf("SCALE documents=%d avg_tokens=%.1f generation=%s build=%s build_docs_per_sec=%.0f file_mb=%.2f vocabulary=%d posting_mb=%.2f largest_posting_mb=%.2f baseline_rss_mb=%.2f peak_build_rss_mb=%.2f query_rss_mb=%.2f baseline_heap_mb=%.2f peak_heap_mb=%.2f build_allocated_gb=%.2f",
		documentCount,
		float64(totalLength)/float64(documentCount),
		generationElapsed.Round(time.Millisecond),
		buildElapsed.Round(time.Millisecond),
		float64(documentCount)/buildElapsed.Seconds(),
		bytesToMiB(uint64(info.Size())),
		vocabulary,
		bytesToMiB(uint64(postingBytes)),
		bytesToMiB(uint64(largestPosting)),
		bytesToMiB(baselineRSS),
		bytesToMiB(peakRSS.Load()),
		bytesToMiB(queryRSS),
		bytesToMiB(before.HeapAlloc),
		bytesToMiB(peakHeap.Load()),
		float64(buildAllocated)/(1<<30),
	)
	for _, query := range queries {
		measurement := measurements[query.name]
		t.Logf("QUERY documents=%d name=%s text=%q hits=%d batch=%d p50_ms=%.3f p95_ms=%.3f max_ms=%.3f allocated_kb_per_op=%.2f",
			documentCount,
			query.name,
			query.query,
			measurement.hits,
			measurement.batch,
			float64(measurement.p50)/float64(time.Millisecond),
			float64(measurement.p95)/float64(time.Millisecond),
			float64(measurement.max)/float64(time.Millisecond),
			float64(measurement.allocatedBytes)/(1<<10),
		)
	}
}

func makeScaleProducts(count int) []ftsSource {
	colors := [...]string{"đen", "trắng", "xanh", "đỏ", "nâu", "xám"}
	sizes := [...]string{"s", "m", "l", "xl", "xxl"}
	documents := make([]ftsSource, count)
	for i := range documents {
		sku := fmt.Sprintf("sku%09d", i)
		template := scaleProductTemplates[i%len(scaleProductTemplates)]
		documents[i] = ftsSource{
			slug:  sku,
			title: fmt.Sprintf(template.title, sku),
			body: fmt.Sprintf("%s Màu %s và kích thước %s. Mã tham chiếu %s.",
				scaleDescription, colors[i%len(colors)], sizes[i%len(sizes)], sku),
		}
	}
	return documents
}

func measureScaleQuery(t *testing.T, index *ftsIndex, query string, samples int) scaleQueryMeasurement {
	t.Helper()
	for range 3 {
		if _, err := index.search(query, 20); err != nil {
			t.Fatal(err)
		}
	}
	batch := 1
	for {
		started := time.Now()
		for range batch {
			if _, err := index.search(query, 20); err != nil {
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
	for i := range durations {
		started := time.Now()
		for range batch {
			result, err := index.search(query, 20)
			if err != nil {
				t.Fatal(err)
			}
			hits = len(result)
		}
		durations[i] = time.Since(started) / time.Duration(batch)
	}
	runtime.ReadMemStats(&after)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return scaleQueryMeasurement{
		p50:            percentileDuration(durations, 50),
		p95:            percentileDuration(durations, 95),
		max:            durations[len(durations)-1],
		allocatedBytes: (after.TotalAlloc - before.TotalAlloc) / uint64(samples*batch),
		hits:           hits,
		batch:          batch,
	}
}

func percentileDuration(sorted []time.Duration, percentile int) time.Duration {
	position := (len(sorted)*percentile + 99) / 100
	if position < 1 {
		position = 1
	}
	return sorted[position-1]
}

func samplePeakHeap(peak *atomic.Uint64, stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			updatePeak(peak, stats.HeapAlloc)
		case <-stop:
			return
		}
	}
}

func updatePeak(peak *atomic.Uint64, value uint64) {
	for {
		current := peak.Load()
		if value <= current || peak.CompareAndSwap(current, value) {
			return
		}
	}
}

func bytesToMiB(bytes uint64) float64 {
	return float64(bytes) / (1 << 20)
}
