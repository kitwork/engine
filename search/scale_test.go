//go:build scale

package search

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	segmentScaleSizes          = flag.String("kitwork-segment-sizes", "100000", "comma-separated document counts")
	segmentScaleSamples        = flag.Int("kitwork-segment-samples", 21, "hot query samples per query after warmup")
	indexScaleSegmentDocuments = flag.Int("kitwork-index-segment-documents", 100000, "maximum documents per index segment")
)

const segmentScaleBody = "Sản phẩm được thiết kế cho nhu cầu sử dụng hằng ngày với chất liệu bền đẹp đường may chắc chắn kiểu dáng hiện đại dễ phối đồ phù hợp nhiều hoàn cảnh đóng gói cẩn thận kiểm tra chất lượng trước khi giao hàng"

var segmentScaleTitles = [...]string{
	"Áo thun cotton nam Nike chính hãng %s",
	"Áo khoác nữ công sở thanh lịch %s",
	"Quần jean nam co giãn bền đẹp %s",
	"Giày thể thao Nike nhẹ êm chân %s",
	"Túi xách nữ da mềm cao cấp %s",
	"Đồng hồ nam chống nước hiện đại %s",
}

type segmentQueryMeasurement struct {
	p50            time.Duration
	p95            time.Duration
	maximum        time.Duration
	allocatedBytes uint64
	hits           int
	batch          int
}

type segmentScaleSearcher interface {
	Search(context.Context, MatchQuery, SearchOptions) ([]Hit, error)
}

type segmentScaleQuery struct {
	name string
	text string
}

func TestSegmentScale(t *testing.T) {
	if *segmentScaleSamples < 5 {
		t.Fatal("kitwork-segment-samples must be at least 5")
	}
	for _, size := range parseSegmentScaleSizes(t, *segmentScaleSizes) {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			runSegmentScale(t, size, *segmentScaleSamples)
		})
	}
}

func TestIndexScale(t *testing.T) {
	if *segmentScaleSamples < 5 {
		t.Fatal("kitwork-segment-samples must be at least 5")
	}
	if *indexScaleSegmentDocuments < 1 || uint64(*indexScaleSegmentDocuments) > math.MaxUint32 {
		t.Fatal("kitwork-index-segment-documents must fit a positive uint32")
	}
	for _, size := range parseSegmentScaleSizes(t, *segmentScaleSizes) {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			runIndexScale(t, size, *segmentScaleSamples, *indexScaleSegmentDocuments)
		})
	}
}

func TestIndexLifecycleScale(t *testing.T) {
	if *indexScaleSegmentDocuments < 1 || uint64(*indexScaleSegmentDocuments) > math.MaxUint32 {
		t.Fatal("kitwork-index-segment-documents must fit a positive uint32")
	}
	for _, size := range parseSegmentScaleSizes(t, *segmentScaleSizes) {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			runIndexLifecycleScale(t, size, *indexScaleSegmentDocuments)
		})
	}
}

func TestReplacementScale(t *testing.T) {
	if *indexScaleSegmentDocuments < 1 || uint64(*indexScaleSegmentDocuments) > math.MaxUint32 {
		t.Fatal("kitwork-index-segment-documents must fit a positive uint32")
	}
	for _, size := range parseSegmentScaleSizes(t, *segmentScaleSizes) {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			runReplacementScale(t, size, *indexScaleSegmentDocuments)
		})
	}
}

func TestManagedReplacementScale(t *testing.T) {
	if *indexScaleSegmentDocuments < 1 || uint64(*indexScaleSegmentDocuments) > math.MaxUint32 {
		t.Fatal("kitwork-index-segment-documents must fit a positive uint32")
	}
	for _, size := range parseSegmentScaleSizes(t, *segmentScaleSizes) {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			runManagedReplacementScale(t, size, *indexScaleSegmentDocuments)
		})
	}
}

func runSegmentScale(t *testing.T, count, samples int) {
	t.Helper()
	schema, err := NewSchema(Text("search", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{
		FlushThresholdBytes: math.MaxInt64,
		MaxDocuments:        uint32(count),
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineRSS, err := currentSegmentScaleRSS()
	if err != nil {
		t.Fatal(err)
	}
	peakHeap := atomic.Uint64{}
	peakHeap.Store(before.HeapAlloc)
	peakRSS := atomic.Uint64{}
	peakRSS.Store(baselineRSS)
	stopHeap := make(chan struct{})
	heapStopped := make(chan struct{})
	go sampleSegmentScaleHeap(&peakHeap, stopHeap, heapStopped)
	stopRSS := make(chan struct{})
	rssStopped := make(chan struct{})
	go sampleSegmentScaleRSS(&peakRSS, stopRSS, rssStopped)

	colors := [...]string{"đen", "trắng", "xanh", "đỏ", "nâu", "xám"}
	sizes := [...]string{"s", "m", "l", "xl", "xxl"}
	buildStarted := time.Now()
	for index := 0; index < count; index++ {
		sku := fmt.Sprintf("sku%09d", index)
		title := fmt.Sprintf(segmentScaleTitles[index%len(segmentScaleTitles)], sku)
		body := fmt.Sprintf("%s Màu %s và kích thước %s. Mã tham chiếu %s.",
			segmentScaleBody, colors[index%len(colors)], sizes[index%len(sizes)], sku)
		if err := builder.Add(Document{ID: sku, Fields: map[string]string{"search": title + "\n" + body}}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "products.ks")
	info, err := builder.Write(context.Background(), path)
	buildElapsed := time.Since(buildStarted)
	close(stopHeap)
	<-heapStopped
	close(stopRSS)
	<-rssStopped
	if err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	updateSegmentScalePeak(&peakHeap, after.HeapAlloc)
	buildAllocated := after.TotalAlloc - before.TotalAlloc

	builder = nil
	runtime.GC()
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = segment.Close() })

	queries := newSegmentScaleQueries(count)
	measurements := make(map[string]segmentQueryMeasurement, len(queries))
	for _, query := range queries {
		measurements[query.name] = measureSegmentScaleQuery(t, segment, query.text, samples)
	}
	queryRSS, err := currentSegmentScaleRSS()
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("SEGMENT_SCALE documents=%d avg_tokens=63.0 build=%s build_docs_per_sec=%.0f index_mb=%.2f baseline_rss_mb=%.2f peak_build_rss_mb=%.2f query_rss_mb=%.2f baseline_heap_mb=%.2f peak_heap_mb=%.2f build_allocated_gb=%.2f info_bytes=%d",
		count,
		buildElapsed.Round(time.Millisecond),
		float64(count)/buildElapsed.Seconds(),
		segmentScaleMiB(uint64(fileInfo.Size())),
		segmentScaleMiB(baselineRSS),
		segmentScaleMiB(peakRSS.Load()),
		segmentScaleMiB(queryRSS),
		segmentScaleMiB(before.HeapAlloc),
		segmentScaleMiB(peakHeap.Load()),
		float64(buildAllocated)/(1<<30),
		info.Bytes,
	)
	for _, query := range queries {
		measurement := measurements[query.name]
		t.Logf("SEGMENT_QUERY documents=%d name=%s text=%q hits=%d batch=%d p50_ms=%.3f p95_ms=%.3f max_ms=%.3f allocated_kb_per_op=%.2f",
			count,
			query.name,
			query.text,
			measurement.hits,
			measurement.batch,
			float64(measurement.p50)/float64(time.Millisecond),
			float64(measurement.p95)/float64(time.Millisecond),
			float64(measurement.maximum)/float64(time.Millisecond),
			float64(measurement.allocatedBytes)/(1<<10),
		)
	}
}

func runIndexScale(t *testing.T, count, samples, segmentDocuments int) {
	t.Helper()
	schema, err := NewSchema(Text("search", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{
		FlushThresholdBytes: math.MaxInt64,
		MaxDocuments:        uint32(min(count, segmentDocuments)),
	}})
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineRSS, err := currentSegmentScaleRSS()
	if err != nil {
		t.Fatal(err)
	}
	peakHeap := atomic.Uint64{}
	peakHeap.Store(before.HeapAlloc)
	peakRSS := atomic.Uint64{}
	peakRSS.Store(baselineRSS)
	stopHeap := make(chan struct{})
	heapStopped := make(chan struct{})
	go sampleSegmentScaleHeap(&peakHeap, stopHeap, heapStopped)
	stopRSS := make(chan struct{})
	rssStopped := make(chan struct{})
	go sampleSegmentScaleRSS(&peakRSS, stopRSS, rssStopped)

	colors := [...]string{"đen", "trắng", "xanh", "đỏ", "nâu", "xám"}
	sizes := [...]string{"s", "m", "l", "xl", "xxl"}
	buildStarted := time.Now()
	for document := 0; document < count; document++ {
		sku := fmt.Sprintf("sku%09d", document)
		title := fmt.Sprintf(segmentScaleTitles[document%len(segmentScaleTitles)], sku)
		body := fmt.Sprintf("%s Màu %s và kích thước %s. Mã tham chiếu %s.",
			segmentScaleBody, colors[document%len(colors)], sizes[document%len(sizes)], sku)
		if err := writer.Add(context.Background(), Document{ID: sku, Fields: map[string]string{"search": title + "\n" + body}}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := writer.Commit(context.Background())
	buildElapsed := time.Since(buildStarted)
	close(stopHeap)
	<-heapStopped
	close(stopRSS)
	<-rssStopped
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	updateSegmentScalePeak(&peakHeap, after.HeapAlloc)
	buildAllocated := after.TotalAlloc - before.TotalAlloc

	writer = nil
	runtime.GC()
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })

	queries := newSegmentScaleQueries(count)
	measurements := make(map[string]segmentQueryMeasurement, len(queries))
	for _, query := range queries {
		measurements[query.name] = measureSegmentScaleQuery(t, index, query.text, samples)
	}
	queryRSS, err := currentSegmentScaleRSS()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("INDEX_SCALE documents=%d avg_tokens=63.0 segments=%d segment_documents=%d build=%s build_docs_per_sec=%.0f index_mb=%.2f baseline_rss_mb=%.2f peak_build_rss_mb=%.2f query_rss_mb=%.2f baseline_heap_mb=%.2f peak_heap_mb=%.2f build_allocated_gb=%.2f info_bytes=%d",
		count,
		info.Segments,
		segmentDocuments,
		buildElapsed.Round(time.Millisecond),
		float64(count)/buildElapsed.Seconds(),
		segmentScaleMiB(uint64(info.Bytes)),
		segmentScaleMiB(baselineRSS),
		segmentScaleMiB(peakRSS.Load()),
		segmentScaleMiB(queryRSS),
		segmentScaleMiB(before.HeapAlloc),
		segmentScaleMiB(peakHeap.Load()),
		float64(buildAllocated)/(1<<30),
		info.Bytes,
	)
	for _, query := range queries {
		measurement := measurements[query.name]
		t.Logf("INDEX_QUERY documents=%d segments=%d name=%s text=%q hits=%d batch=%d p50_ms=%.3f p95_ms=%.3f max_ms=%.3f allocated_kb_per_op=%.2f",
			count,
			info.Segments,
			query.name,
			query.text,
			measurement.hits,
			measurement.batch,
			float64(measurement.p50)/float64(time.Millisecond),
			float64(measurement.p95)/float64(time.Millisecond),
			float64(measurement.maximum)/float64(time.Millisecond),
			float64(measurement.allocatedBytes)/(1<<10),
		)
	}
}

func runReplacementScale(t *testing.T, count, segmentDocuments int) {
	t.Helper()
	schema, err := NewSchema(Text("search", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewIndexWriter(t.TempDir(), schema, WriterOptions{Segment: BuildOptions{
		FlushThresholdBytes: math.MaxInt64,
		MaxDocuments:        uint32(min(count, segmentDocuments)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	replacement, err := writer.BeginReplacement()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Abort()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineRSS, err := currentSegmentScaleRSS()
	if err != nil {
		t.Fatal(err)
	}
	peakHeap := atomic.Uint64{}
	peakHeap.Store(before.HeapAlloc)
	peakRSS := atomic.Uint64{}
	peakRSS.Store(baselineRSS)
	stopHeap := make(chan struct{})
	heapStopped := make(chan struct{})
	go sampleSegmentScaleHeap(&peakHeap, stopHeap, heapStopped)
	stopRSS := make(chan struct{})
	rssStopped := make(chan struct{})
	go sampleSegmentScaleRSS(&peakRSS, stopRSS, rssStopped)

	colors := [...]string{"đen", "trắng", "xanh", "đỏ", "nâu", "xám"}
	sizes := [...]string{"s", "m", "l", "xl", "xxl"}
	ingestStarted := time.Now()
	for document := 0; document < count; document++ {
		sku := fmt.Sprintf("sku%09d", document)
		title := fmt.Sprintf(segmentScaleTitles[document%len(segmentScaleTitles)], sku)
		body := fmt.Sprintf("%s Màu %s và kích thước %s. Mã tham chiếu %s.",
			segmentScaleBody, colors[document%len(colors)], sizes[document%len(sizes)], sku)
		if err := replacement.Add(context.Background(), Document{
			ID: sku, Fields: map[string]string{"search": title + "\n" + body},
		}); err != nil {
			t.Fatal(err)
		}
		if document&8191 == 0 && len(writer.pendingIDs) != 0 {
			t.Fatalf("streaming replacement retained %d global identifiers", len(writer.pendingIDs))
		}
	}
	ingestElapsed := time.Since(ingestStarted)
	commitStarted := time.Now()
	info, err := replacement.Commit(context.Background())
	commitElapsed := time.Since(commitStarted)
	close(stopHeap)
	<-heapStopped
	close(stopRSS)
	<-rssStopped
	if err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	updateSegmentScalePeak(&peakHeap, after.HeapAlloc)
	if info.Documents != uint64(count) {
		t.Fatalf("replacement documents = %d, want %d", info.Documents, count)
	}
	t.Logf("REPLACEMENT_SCALE documents=%d segment_documents=%d segments=%d ingest=%s ingest_docs_per_sec=%.0f commit=%s index_mb=%.2f baseline_rss_mb=%.2f peak_rss_mb=%.2f baseline_heap_mb=%.2f peak_heap_mb=%.2f allocated_gb=%.2f",
		count,
		segmentDocuments,
		info.Segments,
		ingestElapsed.Round(time.Millisecond),
		float64(count)/ingestElapsed.Seconds(),
		commitElapsed.Round(time.Millisecond),
		segmentScaleMiB(uint64(info.Bytes)),
		segmentScaleMiB(baselineRSS),
		segmentScaleMiB(peakRSS.Load()),
		segmentScaleMiB(before.HeapAlloc),
		segmentScaleMiB(peakHeap.Load()),
		float64(after.TotalAlloc-before.TotalAlloc)/(1<<30),
	)
}

func runManagedReplacementScale(t *testing.T, count, segmentDocuments int) {
	t.Helper()
	schema, err := NewSchema(Text("search", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), ManagerOptions{
		Writer: WriterOptions{Segment: BuildOptions{
			FlushThresholdBytes: math.MaxInt64,
			MaxDocuments:        uint32(min(count, segmentDocuments)),
		}},
		DisableAutoCompact: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	replacement, err := manager.BeginReplacement(context.Background(), "tenant", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Abort()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineRSS, err := currentSegmentScaleRSS()
	if err != nil {
		t.Fatal(err)
	}
	peakHeap := atomic.Uint64{}
	peakHeap.Store(before.HeapAlloc)
	peakRSS := atomic.Uint64{}
	peakRSS.Store(baselineRSS)
	stopHeap := make(chan struct{})
	heapStopped := make(chan struct{})
	go sampleSegmentScaleHeap(&peakHeap, stopHeap, heapStopped)
	stopRSS := make(chan struct{})
	rssStopped := make(chan struct{})
	go sampleSegmentScaleRSS(&peakRSS, stopRSS, rssStopped)

	colors := [...]string{"đen", "trắng", "xanh", "đỏ", "nâu", "xám"}
	sizes := [...]string{"s", "m", "l", "xl", "xxl"}
	ingestStarted := time.Now()
	for document := 0; document < count; document++ {
		sku := fmt.Sprintf("sku%09d", document)
		title := fmt.Sprintf(segmentScaleTitles[document%len(segmentScaleTitles)], sku)
		body := fmt.Sprintf("%s Màu %s và kích thước %s. Mã tham chiếu %s.",
			segmentScaleBody, colors[document%len(colors)], sizes[document%len(sizes)], sku)
		if err := replacement.Add(context.Background(), Document{
			ID: sku, Fields: map[string]string{"search": title + "\n" + body},
		}); err != nil {
			t.Fatal(err)
		}
		if document&8191 == 0 {
			stats, exists := manager.IndexStats("tenant")
			if !exists || stats.PendingReplacementDocs != 0 || stats.PendingReplacementBytes != 0 {
				t.Fatalf("managed replacement retained pending payloads: %#v", stats)
			}
		}
	}
	ingestElapsed := time.Since(ingestStarted)
	commitStarted := time.Now()
	info, err := replacement.Commit(context.Background())
	commitElapsed := time.Since(commitStarted)
	close(stopHeap)
	<-heapStopped
	close(stopRSS)
	<-rssStopped
	if err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	updateSegmentScalePeak(&peakHeap, after.HeapAlloc)
	if info.Documents != uint64(count) {
		t.Fatalf("managed replacement documents = %d, want %d", info.Documents, count)
	}
	stats := manager.Stats()
	if stats.PendingReplacementBytes != 0 || stats.PendingWriteBytes != 0 || stats.ActiveReplacements != 0 {
		t.Fatalf("managed replacement resources did not drain: %#v", stats)
	}
	t.Logf("MANAGED_REPLACEMENT_SCALE documents=%d segment_documents=%d segments=%d ingest=%s ingest_docs_per_sec=%.0f commit=%s index_mb=%.2f baseline_rss_mb=%.2f peak_rss_mb=%.2f baseline_heap_mb=%.2f peak_heap_mb=%.2f allocated_gb=%.2f",
		count,
		segmentDocuments,
		info.Segments,
		ingestElapsed.Round(time.Millisecond),
		float64(count)/ingestElapsed.Seconds(),
		commitElapsed.Round(time.Millisecond),
		segmentScaleMiB(uint64(info.Bytes)),
		segmentScaleMiB(baselineRSS),
		segmentScaleMiB(peakRSS.Load()),
		segmentScaleMiB(before.HeapAlloc),
		segmentScaleMiB(peakHeap.Load()),
		float64(after.TotalAlloc-before.TotalAlloc)/(1<<30),
	)
}

func runIndexLifecycleScale(t *testing.T, count, segmentDocuments int) {
	t.Helper()
	schema, err := NewSchema(Text("search", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{
		FlushThresholdBytes: math.MaxInt64,
		MaxDocuments:        uint32(min(count, segmentDocuments)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for document := 0; document < count; document++ {
		sku := fmt.Sprintf("sku%09d", document)
		title := fmt.Sprintf(segmentScaleTitles[document%len(segmentScaleTitles)], sku)
		if err := writer.Add(context.Background(), Document{
			ID: sku, Fields: map[string]string{"search": title + "\n" + segmentScaleBody},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	mutationStarted := time.Now()
	updates, deletes := 0, 0
	for document := 0; document < count; document += 100 {
		sku := fmt.Sprintf("sku%09d", document)
		if (document/100)&1 == 0 {
			if err := writer.Update(context.Background(), Document{
				ID: sku, Fields: map[string]string{"search": "Sản phẩm đã cập nhật cotton premium " + sku},
			}); err != nil {
				t.Fatal(err)
			}
			updates++
			continue
		}
		deleted, err := writer.Delete(context.Background(), sku)
		if err != nil || !deleted {
			t.Fatalf("delete %s = %v, %v", sku, deleted, err)
		}
		deletes++
	}
	mutated, err := writer.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mutationElapsed := time.Since(mutationStarted)

	compactStarted := time.Now()
	compacted, merged, err := writer.Compact(context.Background(), CompactOptions{
		Force: true, MaximumInputSegments: maximumCompactInputSegments,
		MaximumInputDocuments: uint64(count + updates),
	})
	if err != nil || !merged {
		t.Fatalf("compact = %#v, %v, %v", compacted, merged, err)
	}
	compactElapsed := time.Since(compactStarted)
	gcStarted := time.Now()
	garbage, err := writer.GarbageCollect(context.Background(), GarbageCollectOptions{
		OldestRetainedGeneration: compacted.Generation,
	})
	if err != nil {
		t.Fatal(err)
	}
	gcElapsed := time.Since(gcStarted)

	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if compacted.Documents != uint64(count-deletes) || compacted.PhysicalDocuments != compacted.Documents || compacted.Deleted != 0 {
		t.Fatalf("compacted info = %#v", compacted)
	}
	t.Logf("INDEX_LIFECYCLE documents=%d initial_segments=%d updates=%d deletes=%d mutation=%s mutation_ops_per_sec=%.0f before_physical=%d before_deleted=%d compact=%s compacted_segments=%d compacted_mb=%.2f gc=%s reclaimed_mb=%.2f",
		count,
		mutated.Segments,
		updates,
		deletes,
		mutationElapsed.Round(time.Millisecond),
		float64(updates+deletes)/mutationElapsed.Seconds(),
		mutated.PhysicalDocuments,
		mutated.Deleted,
		compactElapsed.Round(time.Millisecond),
		compacted.Segments,
		segmentScaleMiB(uint64(compacted.Bytes)),
		gcElapsed.Round(time.Millisecond),
		segmentScaleMiB(uint64(garbage.ReclaimedBytes)),
	)
}

func newSegmentScaleQueries(count int) []segmentScaleQuery {
	return []segmentScaleQuery{
		{name: "exact_sku", text: fmt.Sprintf("sku%09d", count-1)},
		{name: "one_common", text: "thun"},
		{name: "two_terms", text: "áo nike"},
		{name: "four_terms", text: "áo thun cotton nam"},
		{name: "unaccented", text: "ao khoac nu cong so"},
	}
}

func measureSegmentScaleQuery(t *testing.T, segment segmentScaleSearcher, text string, samples int) segmentQueryMeasurement {
	t.Helper()
	query := MatchQuery{Field: "search", Text: text}
	options := SearchOptions{Limit: 20}
	for range 3 {
		if _, err := segment.Search(context.Background(), query, options); err != nil {
			t.Fatal(err)
		}
	}

	batch := 1
	for {
		started := time.Now()
		for range batch {
			if _, err := segment.Search(context.Background(), query, options); err != nil {
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
	for index := range durations {
		started := time.Now()
		for range batch {
			result, err := segment.Search(context.Background(), query, options)
			if err != nil {
				t.Fatal(err)
			}
			hits = len(result)
			for _, hit := range result {
				consumed += len(hit.ID)
			}
		}
		durations[index] = time.Since(started) / time.Duration(batch)
	}
	runtime.KeepAlive(consumed)
	runtime.ReadMemStats(&after)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return segmentQueryMeasurement{
		p50:            segmentScalePercentile(durations, 50),
		p95:            segmentScalePercentile(durations, 95),
		maximum:        durations[len(durations)-1],
		allocatedBytes: (after.TotalAlloc - before.TotalAlloc) / uint64(samples*batch),
		hits:           hits,
		batch:          batch,
	}
}

func parseSegmentScaleSizes(t *testing.T, input string) []int {
	t.Helper()
	parts := strings.Split(input, ",")
	sizes := make([]int, 0, len(parts))
	for _, part := range parts {
		size, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || size <= 0 || uint64(size) > math.MaxUint32 {
			t.Fatalf("invalid scale size %q", part)
		}
		sizes = append(sizes, size)
	}
	return sizes
}

func segmentScalePercentile(sorted []time.Duration, percentile int) time.Duration {
	position := (len(sorted)*percentile + 99) / 100
	if position < 1 {
		position = 1
	}
	return sorted[position-1]
}

func sampleSegmentScaleHeap(peak *atomic.Uint64, stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			updateSegmentScalePeak(peak, stats.HeapAlloc)
		case <-stop:
			return
		}
	}
}

func updateSegmentScalePeak(peak *atomic.Uint64, value uint64) {
	for {
		current := peak.Load()
		if value <= current || peak.CompareAndSwap(current, value) {
			return
		}
	}
}

func segmentScaleMiB(bytes uint64) float64 {
	return float64(bytes) / (1 << 20)
}
