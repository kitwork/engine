package core

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/work"
)

const (
	defaultRetentionGenerations       = 250
	defaultRetentionRequestsPerGen    = 8
	retentionCheckpointCount          = 5
	retentionPaddingBytes             = 96 << 10
	retentionHeapGrowthAllowance      = 8 << 20
	retentionObjectGrowthAllowance    = 25_000
	retentionGoroutineGrowthAllowance = 8
)

type retentionHeapSample struct {
	Generation  int
	HeapAlloc   uint64
	HeapInuse   uint64
	HeapObjects uint64
	Goroutines  int
}

func TestEngineMemoryRetentionCampaign(t *testing.T) {
	if os.Getenv("KITWORK_RETENTION") != "1" {
		t.Skip("set KITWORK_RETENTION=1 to run the long memory-retention campaign")
	}
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	processBaseline := measureRetentionHeap(0)
	t.Logf(
		"retention process baseline heap_alloc=%s heap_inuse=%s objects=%d goroutines=%d",
		retentionBytes(processBaseline.HeapAlloc),
		retentionBytes(processBaseline.HeapInuse),
		processBaseline.HeapObjects,
		processBaseline.Goroutines,
	)

	savedAllowLocal := work.AllowLocal
	work.AllowLocal = true
	t.Cleanup(func() { work.AllowLocal = savedAllowLocal })

	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		_, _ = writer.Write([]byte("native-ok"))
	}))
	t.Cleanup(upstream.Close)

	root := t.TempDir()
	routerFile := writeRetentionCampaignRoute(
		t,
		root,
		retentionCampaignSource(0, upstream.URL),
	)
	engine := New(root, 1_000_000, true, "localhost")
	t.Cleanup(engine.Close)

	serve := func(wantGeneration int) {
		t.Helper()
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
		)
		want := fmt.Sprintf("generation-%d:11,12,13,14:native-ok", wantGeneration)
		if recorder.Code != http.StatusOK || recorder.Body.String() != want {
			t.Fatalf(
				"generation %d: status=%d body=%q, want %q",
				wantGeneration,
				recorder.Code,
				recorder.Body.String(),
				want,
			)
		}
	}

	serve(0)
	const warmupGenerations = 12
	for generation := 1; generation <= warmupGenerations; generation++ {
		reloadRetentionGeneration(
			t,
			engine,
			routerFile,
			generation,
			upstream.URL,
		)
		serve(generation)
	}

	warmed := measureRetentionHeap(warmupGenerations)
	samples := []retentionHeapSample{warmed}
	t.Logf(
		"retention warmed baseline generation=%d heap_alloc=%s heap_inuse=%s objects=%d goroutines=%d",
		warmed.Generation,
		retentionBytes(warmed.HeapAlloc),
		retentionBytes(warmed.HeapInuse),
		warmed.HeapObjects,
		warmed.Goroutines,
	)
	generations := retentionPositiveEnv(
		t,
		"KITWORK_RETENTION_GENERATIONS",
		defaultRetentionGenerations,
	)
	requestsPerGeneration := retentionPositiveEnv(
		t,
		"KITWORK_RETENTION_REQUESTS",
		defaultRetentionRequestsPerGen,
	)
	checkpointEvery := generations / retentionCheckpointCount
	if checkpointEvery == 0 {
		checkpointEvery = 1
	}
	peakHeapAlloc := warmed.HeapAlloc

	for generation := 1; generation <= generations; generation++ {
		absoluteGeneration := warmupGenerations + generation
		reloadRetentionGeneration(
			t,
			engine,
			routerFile,
			absoluteGeneration,
			upstream.URL,
		)
		for request := 0; request < requestsPerGeneration; request++ {
			serve(absoluteGeneration)
		}
		var live runtime.MemStats
		runtime.ReadMemStats(&live)
		if live.HeapAlloc > peakHeapAlloc {
			peakHeapAlloc = live.HeapAlloc
		}

		if generation%checkpointEvery == 0 || generation == generations {
			sample := measureRetentionHeap(absoluteGeneration)
			samples = append(samples, sample)
			t.Logf(
				"retention checkpoint generation=%d heap_alloc=%s heap_inuse=%s objects=%d goroutines=%d",
				sample.Generation,
				retentionBytes(sample.HeapAlloc),
				retentionBytes(sample.HeapInuse),
				sample.HeapObjects,
				sample.Goroutines,
			)
		}
	}

	health := engine.Health()
	if health.Requests.Inflight != 0 || health.ActiveGenerationLeases != 0 {
		t.Fatalf(
			"campaign ended with active request owners: inflight=%d leases=%d",
			health.Requests.Inflight,
			health.ActiveGenerationLeases,
		)
	}
	if health.LoadedApps != 1 || health.LoadedSites != 1 || health.ActiveGenerations != 1 {
		t.Fatalf(
			"campaign ownership topology: apps=%d sites=%d generations=%d",
			health.LoadedApps,
			health.LoadedSites,
			health.ActiveGenerations,
		)
	}

	writeRetentionHeapProfile(t)
	t.Logf("retention peak heap before checkpoint GC=%s", retentionBytes(peakHeapAlloc))
	assertRetentionPlateau(t, samples)
}

func writeRetentionCampaignRoute(t testing.TB, root, source string) string {
	t.Helper()
	directory := filepath.Join(root, "retention", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	routerFile := filepath.Join(directory, work.RouterFileName)
	if err := os.WriteFile(routerFile, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return routerFile
}

func retentionCampaignSource(generation int, upstream string) string {
	padding := strings.Repeat("x", retentionPaddingBytes)
	return fmt.Sprintf(`
import { router, http } from "kitwork";

const generation = "generation-%d";
const retirementProbe = %q;
const make = (base) => (number) => base + number;

router.get((ctx) => {
	const functions = [1, 2, 3, 4].map((base) => make(base));
	const rendered = functions.map((fn) => fn(10)).join(",");
	const response = http.get(%q);
	return ctx.text(generation + ":" + rendered + ":" + response.text());
});
`, generation, padding, upstream)
}

func reloadRetentionGeneration(
	t testing.TB,
	engine *Engine,
	routerFile string,
	generation int,
	upstream string,
) {
	t.Helper()
	entry := engine.cache["localhost"]
	if entry == nil || entry.current() == nil {
		t.Fatal("retention campaign has no active tenant")
	}
	oldGeneration := entry.current().SiteGeneration()

	if err := os.WriteFile(
		routerFile,
		[]byte(retentionCampaignSource(generation, upstream)),
		0o644,
	); err != nil {
		t.Fatalf("write generation %d: %v", generation, err)
	}
	now := time.Now().Add(time.Duration(generation) * time.Second)
	if err := os.Chtimes(routerFile, now, now); err != nil {
		t.Fatalf("touch generation %d: %v", generation, err)
	}
	entry.mu.Lock()
	entry.lastChecked = time.Time{}
	entry.mu.Unlock()

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"activate generation %d: status=%d body=%q",
			generation,
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if !oldGeneration.Retired() || oldGeneration.RouteGraph() != nil {
		t.Fatalf("generation %d retained its retired route graph", generation-1)
	}
}

func measureRetentionHeap(generation int) retentionHeapSample {
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return retentionHeapSample{
		Generation:  generation,
		HeapAlloc:   stats.HeapAlloc,
		HeapInuse:   stats.HeapInuse,
		HeapObjects: stats.HeapObjects,
		Goroutines:  runtime.NumGoroutine(),
	}
}

func assertRetentionPlateau(t testing.TB, samples []retentionHeapSample) {
	t.Helper()
	if len(samples) < 2 {
		t.Fatal("retention campaign produced fewer than two heap samples")
	}
	first := samples[0]
	last := samples[len(samples)-1]
	heapGrowth := positiveRetentionDelta(last.HeapAlloc, first.HeapAlloc)
	objectGrowth := positiveRetentionDelta(last.HeapObjects, first.HeapObjects)
	goroutineGrowth := last.Goroutines - first.Goroutines
	heapSlope := retentionSlope(samples, func(sample retentionHeapSample) float64 {
		return float64(sample.HeapAlloc)
	})
	projectedGrowth := heapSlope * float64(last.Generation-first.Generation)

	t.Logf(
		"retention plateau generations=%d..%d heap_growth=%s heap_slope=%.2f KiB/generation object_growth=%d goroutine_growth=%d",
		first.Generation,
		last.Generation,
		retentionBytes(heapGrowth),
		heapSlope/(1<<10),
		objectGrowth,
		goroutineGrowth,
	)
	if heapGrowth > retentionHeapGrowthAllowance {
		t.Fatalf(
			"post-GC heap grew by %s; allowance is %s",
			retentionBytes(heapGrowth),
			retentionBytes(retentionHeapGrowthAllowance),
		)
	}
	if objectGrowth > retentionObjectGrowthAllowance {
		t.Fatalf(
			"post-GC heap objects grew by %d; allowance is %d",
			objectGrowth,
			retentionObjectGrowthAllowance,
		)
	}
	if heapSlope > 16<<10 && projectedGrowth > 4<<20 {
		t.Fatalf(
			"post-GC heap has a linear slope of %.2f KiB/generation (projected growth %s)",
			heapSlope/(1<<10),
			retentionBytes(uint64(projectedGrowth)),
		)
	}
	if goroutineGrowth > retentionGoroutineGrowthAllowance {
		t.Fatalf(
			"goroutines grew by %d; allowance is %d",
			goroutineGrowth,
			retentionGoroutineGrowthAllowance,
		)
	}
}

func retentionSlope(
	samples []retentionHeapSample,
	valueOf func(retentionHeapSample) float64,
) float64 {
	if len(samples) < 2 {
		return 0
	}
	var sumX, sumY float64
	for _, sample := range samples {
		sumX += float64(sample.Generation)
		sumY += valueOf(sample)
	}
	meanX := sumX / float64(len(samples))
	meanY := sumY / float64(len(samples))
	var numerator, denominator float64
	for _, sample := range samples {
		x := float64(sample.Generation) - meanX
		numerator += x * (valueOf(sample) - meanY)
		denominator += x * x
	}
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func positiveRetentionDelta(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}

func retentionBytes(bytes uint64) string {
	return fmt.Sprintf("%.2f MiB", float64(bytes)/(1<<20))
}

func retentionPositiveEnv(t testing.TB, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", name, raw)
	}
	return parsed
}

func writeRetentionHeapProfile(t testing.TB) {
	t.Helper()
	path := os.Getenv("KITWORK_HEAP_PROFILE")
	if path == "" {
		return
	}
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create heap profile directory: %v", err)
		}
	}
	profile, err := os.Create(path)
	if err != nil {
		t.Fatalf("create heap profile: %v", err)
	}
	if err := pprof.WriteHeapProfile(profile); err != nil {
		_ = profile.Close()
		t.Fatalf("write heap profile: %v", err)
	}
	if err := profile.Close(); err != nil {
		t.Fatalf("close heap profile: %v", err)
	}
	t.Logf("heap profile written to %s", path)
}
