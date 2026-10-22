package relational

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

const (
	lifecycleWarmups = 2
	lifecycleRounds  = 8
	lifecycleTail    = 4
)

type lifecycleResources struct {
	Goroutines int    `json:"goroutines"`
	Threads    uint32 `json:"os_threads"`
	Handles    uint32 `json:"process_handles"`
	HandlesOK  bool   `json:"handles_available"`
	Heap       uint64 `json:"heap_after_gc_bytes"`
	RSS        uint64 `json:"rss_bytes"`
}

func sampleLifecycleResources() (lifecycleResources, error) {
	runtime.GC()
	runtime.GC()
	threads, err := lifecycleProcessThreads() // Closes its snapshot before measuring handles.
	if err != nil {
		return lifecycleResources{}, err
	}
	point := shoppingFleetMemoryPointNow()
	handles, handlesOK := mixedProcessHandles()
	return lifecycleResources{runtime.NumGoroutine(), threads, handles, handlesOK, point.heap, point.rss}, nil
}

type lifecycleTrend struct {
	First           int64 `json:"first"`
	Last            int64 `json:"last"`
	Minimum         int64 `json:"minimum"`
	Maximum         int64 `json:"maximum"`
	NetDelta        int64 `json:"net_delta"`
	TailMinimum     int64 `json:"tail_minimum"`
	TailMaximum     int64 `json:"tail_maximum"`
	TailDelta       int64 `json:"tail_delta"`
	ExactTailFlat   bool  `json:"exact_tail_flat"`
	EveryTailRising bool  `json:"every_tail_step_rising"`
}

func lifecycleTrendOf(values []int64) (lifecycleTrend, error) {
	if len(values) < lifecycleTail || len(values) > lifecycleRounds {
		return lifecycleTrend{}, fmt.Errorf("lifecycle trend requires %d..%d samples", lifecycleTail, lifecycleRounds)
	}
	result := lifecycleTrend{First: values[0], Last: values[len(values)-1], Minimum: values[0], Maximum: values[0]}
	for _, value := range values {
		if value < 0 {
			return lifecycleTrend{}, fmt.Errorf("negative resource count")
		}
		result.Minimum, result.Maximum = min(result.Minimum, value), max(result.Maximum, value)
	}
	tail := values[len(values)-lifecycleTail:]
	result.TailMinimum, result.TailMaximum, result.EveryTailRising = tail[0], tail[0], true
	for index, value := range tail {
		result.TailMinimum, result.TailMaximum = min(result.TailMinimum, value), max(result.TailMaximum, value)
		if index > 0 && value <= tail[index-1] {
			result.EveryTailRising = false
		}
	}
	result.NetDelta = result.Last - result.First
	result.TailDelta = tail[len(tail)-1] - tail[0]
	result.ExactTailFlat = result.TailMinimum == result.TailMaximum
	return result, nil
}

type lifecycleRound struct {
	Index     int                `json:"index"`
	Phase     string             `json:"phase"`
	Resources lifecycleResources `json:"after_close_remove_gc"`
	Window    lifecycleSummary   `json:"window"`
}

type lifecycleSummary struct {
	Noisy        bool            `json:"noisy_database_zero"`
	RequestedMS  int64           `json:"requested_ms"`
	ElapsedMS    float64         `json:"elapsed_ms"`
	Acknowledged uint64          `json:"acknowledged_transactions"`
	Verified     int             `json:"verified_databases"`
	ManagerDrain kitdbnode.Stats `json:"manager_drain"`
}

type lifecycleReport struct {
	Schema          int                `json:"schema"`
	OS              string             `json:"os"`
	Arch            string             `json:"arch"`
	Go              string             `json:"go"`
	PID             int                `json:"pid"`
	GOMAXPROCS      int                `json:"gomaxprocs"`
	WindowMS        int64              `json:"requested_window_ms"`
	Warmups         int                `json:"warmup_rounds"`
	Measured        int                `json:"measured_rounds"`
	RetainFailure   bool               `json:"retain_failure_fixtures"`
	Initial         lifecycleResources `json:"initial"`
	PostWarmup      lifecycleResources `json:"post_warmup"`
	Rounds          []lifecycleRound   `json:"rounds"`
	Handles         *lifecycleTrend    `json:"post_warmup_handles,omitempty"`
	Threads         *lifecycleTrend    `json:"post_warmup_threads,omitempty"`
	Goroutines      lifecycleTrend     `json:"post_warmup_goroutines"`
	Heap            lifecycleTrend     `json:"post_warmup_heap"`
	RSS             lifecycleTrend     `json:"post_warmup_rss"`
	CorrectnessPass bool               `json:"correctness_pass"`
	ReviewRequired  bool               `json:"process_growth_review_required"`
	Interpretation  string             `json:"interpretation"`
	Failure         string             `json:"failure,omitempty"`
}

// Opt-in diagnostics in one process, not a latency benchmark or stable gate.
// Fixed warmup/measured counts prevent shortening the tail until it looks flat.
func TestKitDBLifecycleCampaign(t *testing.T) {
	if os.Getenv("KITDB_LIFECYCLE_CAMPAIGN") != "1" {
		t.Skip("set KITDB_LIFECYCLE_CAMPAIGN=1 for repeated owner lifecycle diagnostics")
	}
	duration, err := lifecycleDuration(os.Getenv("KITDB_LIFECYCLE_SECONDS"))
	if err != nil {
		t.Fatal(err)
	}
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	path := os.Getenv("KITDB_LIFECYCLE_REPORT")
	var output *os.File
	if path != "" {
		output, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := output.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	var raw *os.File
	var rawEncoder *json.Encoder
	if path != "" {
		raw, err = os.OpenFile(path+".windows.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := errors.Join(raw.Sync(), raw.Close()); err != nil {
				t.Error(err)
			}
		}()
		rawEncoder = json.NewEncoder(raw)
	}
	report := lifecycleReport{Schema: 1, OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version(), PID: os.Getpid(),
		GOMAXPROCS: 4, WindowMS: duration.Milliseconds(), Warmups: lifecycleWarmups, Measured: lifecycleRounds,
		Rounds:         make([]lifecycleRound, 0, lifecycleWarmups+lifecycleRounds),
		Interpretation: "Synthetic native SQL diagnostics. Process handles/threads/heap are observations, not KitDB ownership counters or proof of no leak. Full windows stream to JSONL; a preallocated bounded summary stays live. Warmup/profiles are retained. No latency SLA, RSS ceiling, physical power-loss, pgwire or stable qualification."}
	defer func() {
		if output != nil {
			encoder := json.NewEncoder(output)
			encoder.SetIndent("", "  ")
			if err := errors.Join(encoder.Encode(report), output.Sync()); err != nil {
				t.Error(err)
			}
		}
	}()
	fail := func(err error) {
		report.Failure = err.Error()
		t.Fatal(err)
	}
	root, err := lifecycleFixtureRoot(t, os.Getenv("KITDB_LIFECYCLE_ROOT"))
	if err != nil {
		fail(err)
	}
	report.RetainFailure = os.Getenv("KITDB_LIFECYCLE_ROOT") != ""
	report.Initial, err = sampleLifecycleResources()
	if err != nil {
		fail(err)
	}
	var handles, threads, goroutines, heap, rss []int64
	for index := 0; index < lifecycleWarmups+lifecycleRounds; index++ {
		phase := "measured"
		if index < lifecycleWarmups {
			phase = "warmup"
		}
		name := fmt.Sprintf("%02d_%s", index, phase)
		window, err := lifecycleSummarizedWindow(filepath.Join(root, name), mixedConfig{Name: name, Databases: 16, Noisy: index%2 == 1, Duration: duration}, rawEncoder)
		round := lifecycleRound{Index: index, Phase: phase, Window: window}
		var sampleErr error
		round.Resources, sampleErr = sampleLifecycleResources()
		err = errors.Join(err, sampleErr)
		report.Rounds = append(report.Rounds, round)
		if err != nil {
			if path != "" {
				err = errors.Join(err, writeLifecycleProfiles(path+".failure"))
			}
			fail(fmt.Errorf("round %s: %w", name, err))
		}
		t.Logf("round=%s noisy=%t handles=%d threads=%d goroutines=%d heap=%d ack=%d", name, window.Noisy,
			round.Resources.Handles, round.Resources.Threads, round.Resources.Goroutines, round.Resources.Heap, window.Acknowledged)
		if index == lifecycleWarmups-1 {
			report.PostWarmup = round.Resources
			if path != "" {
				if err := writeLifecycleProfiles(path + ".warmup"); err != nil {
					fail(err)
				}
			}
		}
		if phase == "measured" {
			if round.Resources.HandlesOK {
				handles = append(handles, int64(round.Resources.Handles))
			}
			if round.Resources.Threads > 0 {
				threads = append(threads, int64(round.Resources.Threads))
			}
			goroutines = append(goroutines, int64(round.Resources.Goroutines))
			heap = append(heap, int64(round.Resources.Heap))
			rss = append(rss, int64(round.Resources.RSS))
		}
	}
	report.Goroutines, err = lifecycleTrendOf(goroutines)
	if err != nil {
		fail(err)
	}
	report.Heap, err = lifecycleTrendOf(heap)
	if err != nil {
		fail(err)
	}
	report.RSS, err = lifecycleTrendOf(rss)
	if err != nil {
		fail(err)
	}
	if len(handles) != 0 {
		if len(handles) != lifecycleRounds {
			fail(fmt.Errorf("incomplete handle samples"))
		}
		trend, err := lifecycleTrendOf(handles)
		if err != nil {
			fail(err)
		}
		report.Handles = &trend
	}
	if len(threads) != 0 {
		if len(threads) != lifecycleRounds {
			fail(fmt.Errorf("incomplete OS thread samples"))
		}
		trend, err := lifecycleTrendOf(threads)
		if err != nil {
			fail(err)
		}
		report.Threads = &trend
	}
	report.CorrectnessPass = true
	report.ReviewRequired = report.Goroutines.EveryTailRising || report.Heap.EveryTailRising || report.RSS.EveryTailRising ||
		(report.Handles != nil && !report.Handles.ExactTailFlat) || (report.Threads != nil && !report.Threads.ExactTailFlat)
	if path != "" {
		if err := writeLifecycleProfiles(path + ".final"); err != nil {
			fail(err)
		}
	}
	t.Logf("correctness=%t process_review=%t handle_trend=%+v goroutine_trend=%+v", report.CorrectnessPass, report.ReviewRequired, report.Handles, report.Goroutines)
}

func lifecycleDuration(source string) (time.Duration, error) {
	if source == "" {
		return 5 * time.Second, nil
	}
	seconds, err := strconv.Atoi(source)
	if err != nil || seconds < 1 || seconds > 30 {
		return 0, fmt.Errorf("KITDB_LIFECYCLE_SECONDS must be 1..30")
	}
	return time.Duration(seconds) * time.Second, nil
}

func lifecycleFixtureRoot(t *testing.T, requested string) (string, error) {
	if requested == "" {
		return t.TempDir(), nil
	}
	root, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	// Never reuse a caller's existing directory or register retained data for automatic cleanup.
	if err := os.Mkdir(root, 0700); err != nil {
		return "", err
	}
	return root, nil
}

func lifecycleWindow(root string, config mixedConfig) (mixedWindow, error) {
	if err := os.Mkdir(root, 0700); err != nil {
		return mixedWindow{}, err
	}
	window, err := runMixedWindow(root, config)
	if err != nil {
		return window, err
	}
	if !window.Success || window.ElapsedMS < float64(config.Duration)/float64(time.Millisecond) ||
		window.ManagerDrain == nil || !mixedManagerDrained(*window.ManagerDrain) || len(window.Tenants) != config.Databases {
		return window, fmt.Errorf("incomplete lifecycle window: success=%t elapsed_ms=%.6f requested_ms=%d tenants=%d/%d manager=%+v",
			window.Success, window.ElapsedMS, config.Duration.Milliseconds(), len(window.Tenants), config.Databases, window.ManagerDrain)
	}
	for _, tenant := range window.Tenants {
		if !tenant.ReopenVerified || tenant.Acknowledged == 0 || tenant.CommitSyncs != tenant.Acknowledged || tenant.Drain != (mixedDrain{}) {
			return window, fmt.Errorf("incomplete tenant reopen/sync/drain oracle")
		}
	}
	// The loop retains only bounded reports, not open databases or old fixture trees.
	return window, os.RemoveAll(root)
}

// Do not keep per-tenant result slices alive across a resource sample.
func lifecycleSummarizedWindow(root string, config mixedConfig, output *json.Encoder) (lifecycleSummary, error) {
	window, err := lifecycleWindow(root, config)
	if output != nil {
		err = errors.Join(err, output.Encode(window))
	}
	if err != nil {
		return lifecycleSummary{}, err
	}
	return lifecycleSummary{Noisy: window.Noisy, RequestedMS: window.RequestedMS, ElapsedMS: window.ElapsedMS,
		Acknowledged: window.All.Commit.Count, Verified: len(window.Tenants), ManagerDrain: *window.ManagerDrain}, nil
}

func writeLifecycleProfiles(prefix string) error {
	for _, suffix := range []string{".goroutines.txt", ".heap"} {
		file, err := os.OpenFile(prefix+suffix, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if suffix == ".heap" {
			err = pprof.WriteHeapProfile(file)
		} else {
			err = pprof.Lookup("goroutine").WriteTo(file, 2)
		}
		if err := errors.Join(err, file.Sync(), file.Close()); err != nil {
			return err
		}
	}
	return nil
}

func TestLifecycleTrend(t *testing.T) {
	for _, test := range []struct {
		name         string
		values       []int64
		flat, rising bool
		delta        int64
	}{
		{"flat", []int64{240, 240, 240, 240}, true, false, 0},
		{"leak", []int64{240, 241, 242, 243}, false, true, 3},
		{"warmup_then_flat", []int64{200, 240, 240, 240, 240}, true, false, 40},
		{"noise", []int64{240, 241, 240, 241}, false, false, 1},
		{"falling", []int64{243, 242, 241, 240}, false, false, -3},
	} {
		t.Run(test.name, func(t *testing.T) {
			trend, err := lifecycleTrendOf(test.values)
			if err != nil || trend.ExactTailFlat != test.flat || trend.EveryTailRising != test.rising || trend.NetDelta != test.delta {
				t.Fatalf("trend=%+v error=%v", trend, err)
			}
		})
	}
	for _, values := range [][]int64{nil, {1, 2, 3}, {-1, 0, 1, 2}, make([]int64, lifecycleRounds+1)} {
		if _, err := lifecycleTrendOf(values); err == nil {
			t.Fatal("invalid trend accepted")
		}
	}
}

func TestLifecycleConfiguration(t *testing.T) {
	for _, value := range []string{"", "1", "30"} {
		if _, err := lifecycleDuration(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"0", "31", "-1", "forever"} {
		if _, err := lifecycleDuration(value); err == nil {
			t.Fatal("unbounded duration accepted")
		}
	}
}

func TestLifecycleProcessSampling(t *testing.T) {
	for range 8 {
		point, err := sampleLifecycleResources()
		if err != nil || point.Goroutines == 0 || point.Heap == 0 || (runtime.GOOS == "windows" && (!point.HandlesOK || point.Handles == 0 || point.Threads == 0)) {
			t.Fatalf("resource sample=%+v error=%v", point, err)
		}
	}
}

func TestLifecycleManagerDrain(t *testing.T) {
	clean := kitdbnode.Stats{Closed: true}
	if !mixedManagerDrained(clean) {
		t.Fatal("closed manager rejected")
	}
	for _, mutate := range []func(*kitdbnode.Stats){
		func(s *kitdbnode.Stats) { s.Closed = false }, func(s *kitdbnode.Stats) { s.ManagedDatabases = 1 },
		func(s *kitdbnode.Stats) { s.OpenDatabases = 1 }, func(s *kitdbnode.Stats) { s.OpeningDatabases = 1 },
		func(s *kitdbnode.Stats) { s.ClosingDatabases = 1 }, func(s *kitdbnode.Stats) { s.ActiveLeases = 1 },
		func(s *kitdbnode.Stats) { s.WaitingAcquisitions = 1 }, func(s *kitdbnode.Stats) { s.ReservedPageCacheBytes = 1 },
		func(s *kitdbnode.Stats) { s.WarmReservedPageCacheBytes = 1 }, func(s *kitdbnode.Stats) { s.QueuedMaintenance = 1 },
		func(s *kitdbnode.Stats) { s.RunningMaintenance = 1 }, func(s *kitdbnode.Stats) { s.MultiDatabaseMaintenanceRunning = true },
		func(s *kitdbnode.Stats) { s.OpenFailures = 1 }, func(s *kitdbnode.Stats) { s.MaintenanceFailures = 1 },
		func(s *kitdbnode.Stats) { s.LastCloseError = "close failure" },
	} {
		stats := clean
		mutate(&stats)
		if mixedManagerDrained(stats) {
			t.Fatalf("retained manager accepted: %+v", stats)
		}
	}
}

func TestLifecycleOwnerSmoke(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 4; index++ {
		path := filepath.Join(root, fmt.Sprint(index))
		var raw bytes.Buffer
		summary, err := lifecycleSummarizedWindow(path, mixedConfig{Name: "lifecycle_smoke", Databases: 2, Noisy: index%2 == 1, Duration: 350 * time.Millisecond}, json.NewEncoder(&raw))
		if err != nil {
			t.Fatal(err)
		}
		var window mixedWindow
		if err := json.Unmarshal(raw.Bytes(), &window); err != nil {
			t.Fatal(err)
		}
		if summary.Acknowledged != window.All.Commit.Count || summary.Verified != 2 || !mixedManagerDrained(summary.ManagerDrain) || len(window.Tenants) != 2 {
			t.Fatal("streamed window or retained summary incomplete")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fixture still present: %v", err)
		}
	}
}

func TestLifecycleProfilesExclusive(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "profiles")
	if err := writeLifecycleProfiles(prefix); err != nil {
		t.Fatal(err)
	}
	if err := writeLifecycleProfiles(prefix); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite accepted: %v", err)
	}
}

func TestLifecycleFailureCleanupEvidence(t *testing.T) {
	window, err := runMixedWindow(t.TempDir(), mixedConfig{Name: "invalid_profile", Databases: 2, Duration: 350 * time.Millisecond, ProfileMode: "invalid"})
	if err == nil || window.Success || window.Failure == "" || window.PostCloseDrain == nil || !mixedManagerReleased(*window.PostCloseDrain) {
		t.Fatalf("failure lost cleanup evidence: error=%v report=%+v", err, window)
	}
}

func TestLifecycleRetainedFailureFixture(t *testing.T) {
	requested := filepath.Join(t.TempDir(), "retained")
	root, err := lifecycleFixtureRoot(t, requested)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycleFixtureRoot(t, requested); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing root accepted: %v", err)
	}
	path := filepath.Join(root, "failed")
	if _, err := lifecycleWindow(path, mixedConfig{Name: "retained_failure", Databases: 1, Duration: 350 * time.Millisecond, ProfileMode: "invalid"}); err == nil {
		t.Fatal("expected profile failure")
	}
	if _, err := os.Stat(filepath.Join(path, "tenant_00.kitdb")); err != nil {
		t.Fatalf("failed fixture lost: %v", err)
	}
	if _, err := lifecycleFixtureRoot(t, ""); err != nil {
		t.Fatal(err)
	}
}
