//go:build scale

package search

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	runtimemetrics "runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	managerLoadTenants             = flag.Int("kitwork-manager-load-tenants", 8, "number of isolated tenant indexes")
	managerLoadDocumentsPerTenant  = flag.Int("kitwork-manager-load-documents-per-tenant", 12500, "seed documents in each tenant")
	managerLoadConcurrency         = flag.String("kitwork-manager-load-concurrency", "100,1000", "comma-separated concurrent search clients")
	managerLoadDuration            = flag.Duration("kitwork-manager-load-duration", 5*time.Second, "active duration for each concurrency level")
	managerLoadSearchTimeout       = flag.Duration("kitwork-manager-load-search-timeout", 10*time.Second, "deadline for one search including admission")
	managerLoadWritesPerSecond     = flag.Int("kitwork-manager-load-writes-per-second", 20, "target background adds per second across tenants")
	managerLoadSearchSlots         = flag.String("kitwork-manager-load-search-slots", "0", "comma-separated global search slots; zero uses the Manager default")
	managerLoadSearchSlotsPerIndex = flag.Int("kitwork-manager-load-search-slots-per-index", 8, "search slots reserved for one tenant")
)

var managerLoadQueryNames = [...]string{
	"exact_sku",
	"one_common",
	"two_terms",
	"four_terms",
	"unaccented",
}

type managerLoadRun struct {
	root                string
	schema              Schema
	tenantKeys          []string
	documentsPerTenant  int
	concurrency         int
	run                 int
	duration            time.Duration
	searchTimeout       time.Duration
	writesPerSecond     int
	searchSlots         int
	searchSlotsPerIndex int
}

func TestManagerConcurrentLoad(t *testing.T) {
	if *managerLoadTenants < 1 || *managerLoadTenants > maximumManagerOpenIndexes {
		t.Fatal("kitwork-manager-load-tenants is out of range")
	}
	if *managerLoadDocumentsPerTenant < 120 || uint64(*managerLoadDocumentsPerTenant) > math.MaxUint32 {
		t.Fatal("kitwork-manager-load-documents-per-tenant must be between 120 and MaxUint32")
	}
	if *managerLoadDuration <= 0 || *managerLoadSearchTimeout <= 0 {
		t.Fatal("manager load durations must be positive")
	}
	if *managerLoadWritesPerSecond < 0 {
		t.Fatal("kitwork-manager-load-writes-per-second cannot be negative")
	}
	if *managerLoadSearchSlotsPerIndex < 1 || *managerLoadSearchSlotsPerIndex > maximumManagerConcurrentSearches {
		t.Fatal("kitwork-manager-load-search-slots-per-index is out of range")
	}

	concurrencyLevels := parseSegmentScaleSizes(t, *managerLoadConcurrency)
	searchSlotLevels := parseManagerLoadSearchSlots(t, *managerLoadSearchSlots)
	schema, err := NewSchema(Text("search", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	tenantKeys := make([]string, *managerLoadTenants)
	for tenant := range tenantKeys {
		tenantKeys[tenant] = fmt.Sprintf("tenant-%03d", tenant)
	}

	setupStarted := time.Now()
	seedManagerLoadIndexes(t, root, schema, tenantKeys, *managerLoadDocumentsPerTenant)
	seedBytes, err := managerLoadDirectoryBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	totalDocuments := len(tenantKeys) * *managerLoadDocumentsPerTenant
	t.Logf("MANAGER_LOAD_SETUP tenants=%d documents_per_tenant=%d total_documents=%d build=%s index_mb=%.2f",
		len(tenantKeys),
		*managerLoadDocumentsPerTenant,
		totalDocuments,
		time.Since(setupStarted).Round(time.Millisecond),
		segmentScaleMiB(seedBytes),
	)

	run := 0
	for _, concurrency := range concurrencyLevels {
		for _, searchSlots := range searchSlotLevels {
			concurrency, searchSlots, currentRun := concurrency, searchSlots, run
			run++
			name := fmt.Sprintf("clients_%d_slots_%d", concurrency, searchSlots)
			if searchSlots == 0 {
				name = fmt.Sprintf("clients_%d_slots_default", concurrency)
			}
			t.Run(name, func(t *testing.T) {
				runRoot := t.TempDir()
				cloneStarted := time.Now()
				if err := cloneManagerLoadTree(root, runRoot); err != nil {
					t.Fatal(err)
				}
				t.Logf("MANAGER_LOAD_CLONE index_mb=%.2f elapsed=%s", segmentScaleMiB(seedBytes), time.Since(cloneStarted).Round(time.Millisecond))
				runManagerLoad(t, managerLoadRun{
					root: runRoot, schema: schema, tenantKeys: tenantKeys,
					documentsPerTenant: *managerLoadDocumentsPerTenant,
					concurrency:        concurrency, run: currentRun,
					duration: *managerLoadDuration, searchTimeout: *managerLoadSearchTimeout,
					writesPerSecond:     *managerLoadWritesPerSecond,
					searchSlots:         searchSlots,
					searchSlotsPerIndex: *managerLoadSearchSlotsPerIndex,
				})
			})
		}
	}
}

func seedManagerLoadIndexes(t *testing.T, root string, schema Schema, tenantKeys []string, documents int) {
	t.Helper()
	colors := [...]string{"đen", "trắng", "xanh", "đỏ", "nâu", "xám"}
	sizes := [...]string{"s", "m", "l", "xl", "xxl"}
	for _, key := range tenantKeys {
		writer, err := NewIndexWriter(managedIndexDirectory(root, key), schema, WriterOptions{Segment: BuildOptions{
			FlushThresholdBytes: math.MaxInt64,
			MaxDocuments:        uint32(documents),
		}})
		if err != nil {
			t.Fatal(err)
		}
		for document := 0; document < documents; document++ {
			sku := fmt.Sprintf("sku%09d", document)
			title := fmt.Sprintf(segmentScaleTitles[document%len(segmentScaleTitles)], sku)
			body := fmt.Sprintf("%s Màu %s và kích thước %s. Mã tham chiếu %s.",
				segmentScaleBody, colors[document%len(colors)], sizes[document%len(sizes)], sku)
			if err := writer.Add(context.Background(), Document{
				ID: sku, Fields: map[string]string{"search": title + "\n" + body},
			}); err != nil {
				_ = writer.Close()
				t.Fatal(err)
			}
		}
		if _, err := writer.Commit(context.Background()); err != nil {
			_ = writer.Close()
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func runManagerLoad(t *testing.T, load managerLoadRun) {
	t.Helper()
	globalSlots := load.searchSlots
	if globalSlots == 0 {
		globalSlots = defaultManagerConcurrentSearches(runtime.GOMAXPROCS(0))
	}
	perIndexSlots := min(load.searchSlotsPerIndex, globalSlots)
	manager, err := NewManager(load.root, ManagerOptions{
		MaxOpenIndexes:                len(load.tenantKeys),
		MaxConcurrentSearches:         globalSlots,
		MaxConcurrentSearchesPerIndex: perIndexSlots,
		MaxConcurrentMutationBatches:  min(len(load.tenantKeys), max(1, runtime.GOMAXPROCS(0))),
		MaxConcurrentMaintenance:      min(len(load.tenantKeys), 2),
		MutationQueueSize:             256, MutationBatchSize: 64, MutationBatchDelay: 2 * time.Millisecond,
		Writer: WriterOptions{Segment: BuildOptions{MaxDocuments: 64}},
		Compact: CompactOptions{
			MaximumSegments: 8, MaximumInputSegments: 8,
			MaximumInputDocuments: uint64(load.documentsPerTenant*2 + 10000),
		},
		AutoGarbageCollect: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = manager.Close()
		}
	}()

	initialDocuments := make([]uint64, len(load.tenantKeys))
	for tenant, key := range load.tenantKeys {
		hits, err := manager.Search(context.Background(), key, load.schema,
			MatchQuery{Field: "search", Text: "áo nike"}, SearchOptions{Limit: 20})
		if err != nil || len(hits) != 20 {
			t.Fatalf("warm tenant %q hits=%d: %v", key, len(hits), err)
		}
		stats, exists := manager.IndexStats(key)
		if !exists || stats.Failed {
			t.Fatalf("warm tenant %q stats = %#v, exists = %v", key, stats, exists)
		}
		initialDocuments[tenant] = stats.Documents
	}
	baselineStats := manager.Stats().Searches

	runtime.GC()
	var memoryBefore runtime.MemStats
	runtime.ReadMemStats(&memoryBefore)
	baselineRSS, err := currentSegmentScaleRSS()
	if err != nil {
		t.Fatal(err)
	}
	peakRSS := atomic.Uint64{}
	peakRSS.Store(baselineRSS)
	stopRSS := make(chan struct{})
	rssStopped := make(chan struct{})
	go sampleSegmentScaleRSS(&peakRSS, stopRSS, rssStopped)
	cpuBefore := managerLoadCPUSeconds()
	bytesBefore, err := managerLoadDirectoryBytes(load.root)
	if err != nil {
		close(stopRSS)
		<-rssStopped
		t.Fatal(err)
	}

	start := make(chan struct{})
	stop := make(chan struct{})
	searchErrors := make(chan error, load.concurrency)
	measurements := [len(managerLoadQueryNames)]searchLatencyHistogram{}
	tenantSearches := make([]atomic.Uint64, len(load.tenantKeys))
	var searchWait sync.WaitGroup
	for worker := 0; worker < load.concurrency; worker++ {
		worker := worker
		searchWait.Add(1)
		go func() {
			defer searchWait.Done()
			<-start
			iteration := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				tenant := (worker + iteration) % len(load.tenantKeys)
				kind := (worker*3 + iteration) % len(managerLoadQueryNames)
				query, expected := managerLoadQuery(kind, worker, iteration, load.documentsPerTenant)
				ctx, cancel := context.WithTimeout(context.Background(), load.searchTimeout)
				started := time.Now()
				hits, err := manager.Search(ctx, load.tenantKeys[tenant], load.schema,
					MatchQuery{Field: "search", Text: query}, SearchOptions{Limit: 20})
				measurements[kind].record(time.Since(started))
				cancel()
				if err != nil {
					searchErrors <- fmt.Errorf("worker %d tenant %d query %s: %w", worker, tenant, managerLoadQueryNames[kind], err)
					return
				}
				if len(hits) != expected {
					searchErrors <- fmt.Errorf("worker %d tenant %d query %s returned %d hits, want %d",
						worker, tenant, managerLoadQueryNames[kind], len(hits), expected)
					return
				}
				tenantSearches[tenant].Add(1)
				iteration++
			}
		}()
	}

	var writeSequence atomic.Uint64
	writesByTenant := make([]atomic.Uint64, len(load.tenantKeys))
	writerErrors := make(chan error, max(1, min(load.writesPerSecond, len(load.tenantKeys))))
	var writerWait sync.WaitGroup
	startManagerLoadWriters(manager, load, start, stop, &writeSequence, writesByTenant, writerErrors, &writerWait)

	loadStarted := time.Now()
	close(start)
	timer := time.NewTimer(load.duration)
	<-timer.C
	close(stop)
	searchWait.Wait()
	writerWait.Wait()
	elapsed := time.Since(loadStarted)
	close(searchErrors)
	close(writerErrors)
	close(stopRSS)
	<-rssStopped

	for err := range searchErrors {
		t.Error(err)
	}
	for err := range writerErrors {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	var memoryAfter runtime.MemStats
	runtime.ReadMemStats(&memoryAfter)
	cpuSeconds := managerLoadCPUSeconds() - cpuBefore
	afterStats := manager.Stats()
	searches := subtractSearchRuntimeStats(afterStats.Searches, baselineStats)
	bytesAfter, err := managerLoadDirectoryBytes(load.root)
	if err != nil {
		t.Fatal(err)
	}
	if searches.Completed == 0 || searches.Completed != searches.Succeeded ||
		searches.Failed != 0 || searches.Canceled != 0 || searches.Overloaded != 0 ||
		searches.Active != 0 || searches.Waiting != 0 {
		t.Fatalf("manager load search stats = %#v", searches)
	}
	if load.writesPerSecond > 0 && writeSequence.Load() == 0 {
		t.Fatal("background writer made no progress")
	}
	minimumTenantSearches, maximumTenantSearches := managerLoadTenantRange(tenantSearches)
	if minimumTenantSearches == 0 {
		t.Fatalf("tenant search distribution starved: min=%d max=%d", minimumTenantSearches, maximumTenantSearches)
	}
	indexGrowth := int64(bytesAfter) - int64(bytesBefore)

	allocatedPerSearch := uint64(0)
	if searches.Completed != 0 {
		allocatedPerSearch = (memoryAfter.TotalAlloc - memoryBefore.TotalAlloc) / searches.Completed
	}
	t.Logf("MANAGER_LOAD clients=%d tenants=%d global_slots=%d tenant_slots=%d duration=%s searches=%d qps=%.0f successes=%d failures=%d canceled=%d overloaded=%d max_active=%d max_waiting=%d queue_p50_upper_ms=%.3f queue_p95_upper_ms=%.3f queue_p99_upper_ms=%.3f execution_p50_upper_ms=%.3f execution_p95_upper_ms=%.3f execution_p99_upper_ms=%.3f total_p95_upper_ms=%.3f total_p99_upper_ms=%.3f cpu_seconds=%.3f cpu_cores=%.2f baseline_rss_mb=%.2f peak_rss_mb=%.2f heap_after_mb=%.2f allocated_kb_per_search=%.2f tenant_search_min=%d tenant_search_max=%d writes=%d commits=%d compactions=%d gc=%d index_growth_mb=%.2f",
		load.concurrency,
		len(load.tenantKeys),
		globalSlots,
		perIndexSlots,
		elapsed.Round(time.Millisecond),
		searches.Completed,
		float64(searches.Completed)/elapsed.Seconds(),
		searches.Succeeded,
		searches.Failed,
		searches.Canceled,
		searches.Overloaded,
		searches.MaxActive,
		searches.MaxWaiting,
		managerLoadMilliseconds(managerLoadPercentile(searches.QueueLatency, 50)),
		managerLoadMilliseconds(managerLoadPercentile(searches.QueueLatency, 95)),
		managerLoadMilliseconds(managerLoadPercentile(searches.QueueLatency, 99)),
		managerLoadMilliseconds(managerLoadPercentile(searches.ExecutionLatency, 50)),
		managerLoadMilliseconds(managerLoadPercentile(searches.ExecutionLatency, 95)),
		managerLoadMilliseconds(managerLoadPercentile(searches.ExecutionLatency, 99)),
		managerLoadMilliseconds(managerLoadPercentile(searches.TotalLatency, 95)),
		managerLoadMilliseconds(managerLoadPercentile(searches.TotalLatency, 99)),
		cpuSeconds,
		cpuSeconds/elapsed.Seconds(),
		segmentScaleMiB(baselineRSS),
		segmentScaleMiB(peakRSS.Load()),
		segmentScaleMiB(memoryAfter.HeapAlloc),
		float64(allocatedPerSearch)/(1<<10),
		minimumTenantSearches,
		maximumTenantSearches,
		writeSequence.Load(),
		afterStats.Commits,
		afterStats.Compactions,
		afterStats.GarbageCollections,
		float64(indexGrowth)/(1<<20),
	)
	for kind, name := range managerLoadQueryNames {
		measurement := measurements[kind].snapshot()
		t.Logf("MANAGER_LOAD_QUERY clients=%d name=%s searches=%d p50_upper_ms=%.3f p95_upper_ms=%.3f p99_upper_ms=%.3f max_ms=%.3f",
			load.concurrency,
			name,
			measurement.Count,
			managerLoadMilliseconds(managerLoadPercentile(measurement, 50)),
			managerLoadMilliseconds(managerLoadPercentile(measurement, 95)),
			managerLoadMilliseconds(managerLoadPercentile(measurement, 99)),
			float64(measurement.MaxNanoseconds)/float64(time.Millisecond),
		)
	}

	for tenant, key := range load.tenantKeys {
		if err := manager.Maintain(context.Background(), key, load.schema); err != nil {
			t.Fatalf("final maintenance for %s: %v", key, err)
		}
		stats, exists := manager.IndexStats(key)
		wantDocuments := initialDocuments[tenant] + writesByTenant[tenant].Load()
		if !exists || stats.Failed || stats.Documents != wantDocuments || stats.ActiveSearches != 0 || stats.WaitingSearches != 0 {
			t.Fatalf("final tenant %s stats = %#v, want documents=%d", key, stats, wantDocuments)
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
}

func startManagerLoadWriters(
	manager *Manager,
	load managerLoadRun,
	start, stop <-chan struct{},
	sequence *atomic.Uint64,
	writesByTenant []atomic.Uint64,
	errorsFound chan<- error,
	wait *sync.WaitGroup,
) {
	if load.writesPerSecond == 0 {
		return
	}
	workers := min(load.writesPerSecond, len(load.tenantKeys))
	interval := time.Second * time.Duration(workers) / time.Duration(load.writesPerSecond)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			write := func() bool {
				current := sequence.Add(1)
				tenant := int(current-1) % len(load.tenantKeys)
				identifier := fmt.Sprintf("live-%03d-%012d", load.run, current)
				ctx, cancel := context.WithTimeout(context.Background(), load.searchTimeout)
				_, err := manager.Add(ctx, load.tenantKeys[tenant], load.schema, Document{
					ID:     identifier,
					Fields: map[string]string{"search": "Sản phẩm common cotton được thêm khi đang chịu tải " + identifier},
				})
				cancel()
				if err != nil {
					errorsFound <- fmt.Errorf("background writer %d tenant %d: %w", worker, tenant, err)
					return false
				}
				writesByTenant[tenant].Add(1)
				return true
			}
			if !write() {
				return
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					if !write() {
						return
					}
				}
			}
		}()
	}
}

func managerLoadQuery(kind, worker, iteration, documents int) (string, int) {
	switch kind {
	case 0:
		document := (worker*104729 + iteration*7919) % documents
		return fmt.Sprintf("sku%09d", document), 1
	case 1:
		return "thun", 20
	case 2:
		return "áo nike", 20
	case 3:
		return "áo thun cotton nam", 20
	default:
		return "ao khoac nu cong so", 20
	}
}

func subtractSearchRuntimeStats(after, before SearchRuntimeStats) SearchRuntimeStats {
	return SearchRuntimeStats{
		Started:          after.Started - before.Started,
		Completed:        after.Completed - before.Completed,
		Succeeded:        after.Succeeded - before.Succeeded,
		Failed:           after.Failed - before.Failed,
		Canceled:         after.Canceled - before.Canceled,
		Overloaded:       after.Overloaded - before.Overloaded,
		Waiting:          after.Waiting,
		MaxWaiting:       after.MaxWaiting,
		Active:           after.Active,
		MaxActive:        after.MaxActive,
		TotalLatency:     subtractSearchLatencyStats(after.TotalLatency, before.TotalLatency),
		QueueLatency:     subtractSearchLatencyStats(after.QueueLatency, before.QueueLatency),
		ExecutionLatency: subtractSearchLatencyStats(after.ExecutionLatency, before.ExecutionLatency),
	}
}

func subtractSearchLatencyStats(after, before SearchLatencyStats) SearchLatencyStats {
	delta := SearchLatencyStats{
		Count:            after.Count - before.Count,
		TotalNanoseconds: after.TotalNanoseconds - before.TotalNanoseconds,
		MaxNanoseconds:   after.MaxNanoseconds,
		Buckets:          make([]SearchLatencyBucket, len(after.Buckets)),
		Overflow:         after.Overflow - before.Overflow,
	}
	for index := range delta.Buckets {
		delta.Buckets[index] = SearchLatencyBucket{
			UpperBoundMicroseconds: after.Buckets[index].UpperBoundMicroseconds,
			Count:                  after.Buckets[index].Count - before.Buckets[index].Count,
		}
	}
	return delta
}

func managerLoadPercentile(stats SearchLatencyStats, percentile uint64) time.Duration {
	if stats.Count == 0 {
		return 0
	}
	target := (stats.Count*percentile + 99) / 100
	cumulative := uint64(0)
	for _, bucket := range stats.Buckets {
		cumulative += bucket.Count
		if cumulative >= target {
			return time.Duration(bucket.UpperBoundMicroseconds) * time.Microsecond
		}
	}
	return time.Duration(stats.MaxNanoseconds)
}

func managerLoadMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func managerLoadTenantRange(counts []atomic.Uint64) (uint64, uint64) {
	minimum := ^uint64(0)
	maximum := uint64(0)
	for index := range counts {
		count := counts[index].Load()
		minimum = min(minimum, count)
		maximum = max(maximum, count)
	}
	return minimum, maximum
}

func managerLoadCPUSeconds() float64 {
	samples := []runtimemetrics.Sample{
		{Name: "/cpu/classes/user:cpu-seconds"},
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/cpu/classes/scavenge/total:cpu-seconds"},
	}
	runtimemetrics.Read(samples)
	total := 0.0
	for _, sample := range samples {
		if sample.Value.Kind() == runtimemetrics.KindFloat64 {
			total += sample.Value.Float64()
		}
	}
	return total
}

func managerLoadDirectoryBytes(root string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() > 0 {
				total += uint64(info.Size())
			}
		}
		return nil
	})
	return total, err
}

func parseManagerLoadSearchSlots(t *testing.T, input string) []int {
	t.Helper()
	parts := strings.Split(input, ",")
	slots := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 0 || value > maximumManagerConcurrentSearches {
			t.Fatalf("invalid manager load search slots %q", part)
		}
		slots = append(slots, value)
	}
	return slots
}

func cloneManagerLoadTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		inputCloseErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return inputCloseErr
	})
}
