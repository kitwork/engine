//go:build bleve_scale

package searchscale

import (
	"flag"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/char/asciifolding"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	indexapi "github.com/blevesearch/bleve_index_api"
)

var (
	bleveScaleSizes   = flag.String("kitwork-bleve-sizes", "100000", "comma-separated document counts")
	bleveScaleSamples = flag.Int("kitwork-bleve-samples", 21, "hot query samples per query after warmup")
	bleveScaleBatch   = flag.Int("kitwork-bleve-batch", 1000, "documents per indexing batch")
)

const (
	kitworkAnalyzer = "kitwork_vietnamese"
	scaleBody       = "Sản phẩm được thiết kế cho nhu cầu sử dụng hằng ngày với chất liệu bền đẹp đường may chắc chắn kiểu dáng hiện đại dễ phối đồ phù hợp nhiều hoàn cảnh đóng gói cẩn thận kiểm tra chất lượng trước khi giao hàng"
)

var scaleTitles = [...]string{
	"Áo thun cotton nam Nike chính hãng %s",
	"Áo khoác nữ công sở thanh lịch %s",
	"Quần jean nam co giãn bền đẹp %s",
	"Giày thể thao Nike nhẹ êm chân %s",
	"Túi xách nữ da mềm cao cấp %s",
	"Đồng hồ nam chống nước hiện đại %s",
}

type scaleProduct struct {
	Search string `json:"search"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

type queryMeasurement struct {
	p50            time.Duration
	p95            time.Duration
	max            time.Duration
	allocatedBytes uint64
	hits           int
	batch          int
}

func TestBleveScale(t *testing.T) {
	sizes := parseSizes(t, *bleveScaleSizes)
	if *bleveScaleSamples < 5 {
		t.Fatal("kitwork-bleve-samples must be at least 5")
	}
	if *bleveScaleBatch <= 0 {
		t.Fatal("kitwork-bleve-batch must be positive")
	}

	for _, size := range sizes {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			runBleveScale(t, size, *bleveScaleSamples, *bleveScaleBatch)
		})
	}
}

func parseSizes(t *testing.T, input string) []int {
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

func runBleveScale(t *testing.T, count, samples, batchSize int) {
	t.Helper()
	indexPath := filepath.Join(t.TempDir(), "products.bleve")
	mapping := newScaleMapping(t)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineRSS, rssErr := currentProcessRSS()
	if rssErr != nil {
		t.Fatal(rssErr)
	}
	peakHeap := atomic.Uint64{}
	peakHeap.Store(before.HeapAlloc)
	peakRSS := atomic.Uint64{}
	peakRSS.Store(baselineRSS)
	stopSampling := make(chan struct{})
	samplingStopped := make(chan struct{})
	go sampleHeap(&peakHeap, stopSampling, samplingStopped)
	stopRSSSampling := make(chan struct{})
	rssSamplingStopped := make(chan struct{})
	go sampleProcessRSS(&peakRSS, stopRSSSampling, rssSamplingStopped)

	buildStarted := time.Now()
	bleveIndex, err := bleve.New(indexPath, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if err := indexProducts(bleveIndex, count, batchSize); err != nil {
		_ = bleveIndex.Close()
		t.Fatal(err)
	}
	if err := bleveIndex.Close(); err != nil {
		t.Fatal(err)
	}
	buildElapsed := time.Since(buildStarted)

	close(stopSampling)
	<-samplingStopped
	close(stopRSSSampling)
	<-rssSamplingStopped
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	updatePeak(&peakHeap, after.HeapAlloc)
	buildAllocated := after.TotalAlloc - before.TotalAlloc
	runtime.GC()

	indexBytes, fileCount, err := directorySize(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	bleveIndex, err = bleve.Open(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bleveIndex.Close() })
	documentCount, err := bleveIndex.DocCount()
	if err != nil {
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
	measurements := make(map[string]queryMeasurement, len(queries))
	for _, item := range queries {
		measurements[item.name] = measureQuery(t, bleveIndex, item.text, samples)
	}
	queryRSS, err := currentProcessRSS()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("BLEVE_SCALE documents=%d avg_tokens=63.0 build=%s build_docs_per_sec=%.0f index_mb=%.2f files=%d baseline_rss_mb=%.2f peak_build_rss_mb=%.2f query_rss_mb=%.2f baseline_heap_mb=%.2f peak_heap_mb=%.2f build_allocated_gb=%.2f batch=%d",
		documentCount,
		buildElapsed.Round(time.Millisecond),
		float64(documentCount)/buildElapsed.Seconds(),
		bytesToMiB(indexBytes),
		fileCount,
		bytesToMiB(baselineRSS),
		bytesToMiB(peakRSS.Load()),
		bytesToMiB(queryRSS),
		bytesToMiB(before.HeapAlloc),
		bytesToMiB(peakHeap.Load()),
		float64(buildAllocated)/(1<<30),
		batchSize,
	)
	for _, item := range queries {
		measurement := measurements[item.name]
		t.Logf("BLEVE_QUERY documents=%d name=%s text=%q hits=%d batch=%d p50_ms=%.3f p95_ms=%.3f max_ms=%.3f allocated_kb_per_op=%.2f",
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

func newScaleMapping(t *testing.T) mapping.IndexMapping {
	t.Helper()
	mapping := bleve.NewIndexMapping()
	if err := mapping.AddCustomAnalyzer(kitworkAnalyzer, map[string]interface{}{
		"type":          custom.Name,
		"char_filters":  []string{asciifolding.Name},
		"tokenizer":     unicode.Name,
		"token_filters": []string{lowercase.Name},
	}); err != nil {
		t.Fatal(err)
	}
	mapping.ScoringModel = indexapi.BM25Scoring
	mapping.IndexDynamic = false
	mapping.StoreDynamic = false
	mapping.DocValuesDynamic = false

	document := bleve.NewDocumentMapping()
	document.Dynamic = false
	searchField := bleve.NewTextFieldMapping()
	searchField.Analyzer = kitworkAnalyzer
	searchField.Store = false
	searchField.Index = true
	searchField.IncludeTermVectors = false
	searchField.IncludeInAll = false
	searchField.DocValues = false
	document.AddFieldMappingsAt("search", searchField)

	storedField := bleve.NewTextFieldMapping()
	storedField.Store = true
	storedField.Index = false
	storedField.IncludeTermVectors = false
	storedField.IncludeInAll = false
	storedField.DocValues = false
	document.AddFieldMappingsAt("title", storedField)
	document.AddFieldMappingsAt("body", storedField)
	mapping.DefaultMapping = document
	return mapping
}

func indexProducts(index bleve.Index, count, batchSize int) error {
	colors := [...]string{"đen", "trắng", "xanh", "đỏ", "nâu", "xám"}
	sizes := [...]string{"s", "m", "l", "xl", "xxl"}
	batch := index.NewBatch()
	for i := 0; i < count; i++ {
		sku := fmt.Sprintf("sku%09d", i)
		title := fmt.Sprintf(scaleTitles[i%len(scaleTitles)], sku)
		body := fmt.Sprintf("%s Màu %s và kích thước %s. Mã tham chiếu %s.",
			scaleBody, colors[i%len(colors)], sizes[i%len(sizes)], sku)
		product := scaleProduct{Search: title + "\n" + body, Title: title, Body: body}
		if err := batch.Index(sku, product); err != nil {
			return err
		}
		if batch.Size() == batchSize {
			if err := index.Batch(batch); err != nil {
				return err
			}
			batch = index.NewBatch()
		}
	}
	if batch.Size() > 0 {
		return index.Batch(batch)
	}
	return nil
}

func measureQuery(t *testing.T, index bleve.Index, text string, samples int) queryMeasurement {
	t.Helper()
	for range 3 {
		if _, _, err := runQuery(index, text); err != nil {
			t.Fatal(err)
		}
	}

	batch := 1
	for {
		started := time.Now()
		for range batch {
			if _, _, err := runQuery(index, text); err != nil {
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
			hits, consumed, err = runQuery(index, text)
			if err != nil {
				t.Fatal(err)
			}
		}
		durations[i] = time.Since(started) / time.Duration(batch)
	}
	runtime.KeepAlive(consumed)
	runtime.ReadMemStats(&after)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return queryMeasurement{
		p50:            percentile(durations, 50),
		p95:            percentile(durations, 95),
		max:            durations[len(durations)-1],
		allocatedBytes: (after.TotalAlloc - before.TotalAlloc) / uint64(samples*batch),
		hits:           hits,
		batch:          batch,
	}
}

func runQuery(index bleve.Index, text string) (int, int, error) {
	terms := strings.Fields(text)
	conjuncts := make([]query.Query, 0, len(terms))
	for _, term := range terms {
		match := bleve.NewMatchQuery(term)
		match.SetField("search")
		conjuncts = append(conjuncts, match)
	}
	request := bleve.NewSearchRequestOptions(bleve.NewConjunctionQuery(conjuncts...), 20, 0, false)
	request.Fields = []string{"title", "body"}
	result, err := index.Search(request)
	if err != nil {
		return 0, 0, err
	}
	consumed := 0
	for _, hit := range result.Hits {
		if title, ok := hit.Fields["title"].(string); ok {
			consumed += len(title)
		}
		if body, ok := hit.Fields["body"].(string); ok {
			consumed += len(body)
		}
	}
	return len(result.Hits), consumed, nil
}

func percentile(sorted []time.Duration, value int) time.Duration {
	position := (len(sorted)*value + 99) / 100
	if position < 1 {
		position = 1
	}
	return sorted[position-1]
}

func directorySize(path string) (uint64, int, error) {
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

func sampleHeap(peak *atomic.Uint64, stop <-chan struct{}, stopped chan<- struct{}) {
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
