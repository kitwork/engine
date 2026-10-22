package relational

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"
)

type lifecycleThreadSample struct {
	Resources         lifecycleResources `json:"resources_after_gc"`
	GoRecordsBefore   int                `json:"go_thread_profile_records_before"`
	GoRecordsAfter    int                `json:"go_thread_profile_records_after"`
	OSThreadsSecond   uint32             `json:"os_threads_second_snapshot"`
	HandlesSecond     uint32             `json:"handles_second_snapshot"`
	HandlesSecondOK   bool               `json:"second_handle_count_available"`
	SamplingElapsedUS int64              `json:"sampling_elapsed_us"`
}

func sampleLifecycleThreads() (lifecycleThreadSample, error) {
	at := time.Now()
	profile := pprof.Lookup("threadcreate")
	if profile == nil {
		return lifecycleThreadSample{}, fmt.Errorf("Go thread creation profile unavailable")
	}
	before := profile.Count()
	point, err := sampleLifecycleResources()
	if err != nil {
		return lifecycleThreadSample{}, err
	}
	second, err := lifecycleProcessThreads()
	if err != nil {
		return lifecycleThreadSample{}, err
	}
	handles, ok := mixedProcessHandles()
	if runtime.GOOS == "windows" && (!point.HandlesOK || !ok || point.Threads == 0 || second == 0) {
		return lifecycleThreadSample{}, fmt.Errorf("incomplete Windows process sample")
	}
	return lifecycleThreadSample{point, before, profile.Count(), second, handles, ok, time.Since(at).Microseconds()}, nil
}

type lifecycleAttributionPoint struct {
	Phase       string                `json:"phase"`
	Index       int                   `json:"index"`
	ElapsedMS   int64                 `json:"elapsed_ms"`
	IdleAfterMS int64                 `json:"requested_idle_after_work_ms"`
	Sample      lifecycleThreadSample `json:"sample"`
}

type lifecycleAttributionReport struct {
	Schema          int                         `json:"schema"`
	Mode            string                      `json:"mode"`
	OS              string                      `json:"os"`
	Arch            string                      `json:"arch"`
	Go              string                      `json:"go"`
	PID             int                         `json:"pid"`
	GOMAXPROCS      int                         `json:"gomaxprocs"`
	WindowMS        int64                       `json:"window_ms"`
	RequestedRounds int                         `json:"requested_rounds"`
	IdleOffsetsMS   []int64                     `json:"idle_offsets_ms"`
	DatabaseCalls   int                         `json:"database_windows_executed"`
	Verified        int                         `json:"verified_database_lifetimes"`
	Acknowledged    uint64                      `json:"acknowledged_transactions"`
	Points          []lifecycleAttributionPoint `json:"points"`
	Completed       bool                        `json:"completed"`
	Failure         string                      `json:"failure,omitempty"`
	Interpretation  string                      `json:"interpretation"`
}

type lifecycleAttributionConfig struct {
	Mode      string
	Rounds    int
	Duration  time.Duration
	Databases int
	Idle      []time.Duration
}

func validateLifecycleAttribution(config lifecycleAttributionConfig) error {
	if (config.Mode != "control" && config.Mode != "database") || config.Rounds < 1 || config.Rounds > 10 ||
		config.Duration <= 0 || config.Duration > 30*time.Second || config.Databases < 2 || config.Databases > 16 || len(config.Idle) < 1 || len(config.Idle) > 6 {
		return fmt.Errorf("invalid bounded thread attribution configuration")
	}
	for index, offset := range config.Idle {
		if offset < 0 || offset > time.Minute || (index == 0 && offset != 0) || (index > 0 && offset <= config.Idle[index-1]) {
			return fmt.Errorf("idle offsets must start at zero and strictly increase within 60 seconds")
		}
	}
	return nil
}

func waitLifecycleUntil(ctx context.Context, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(max(time.Duration(0), time.Until(at)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runLifecycleAttribution(ctx context.Context, root string, config lifecycleAttributionConfig, events, windows *json.Encoder) (report lifecycleAttributionReport, resultErr error) {
	if err := validateLifecycleAttribution(config); err != nil {
		return report, err
	}
	if ctx == nil {
		return report, fmt.Errorf("nil attribution context")
	}
	report = lifecycleAttributionReport{Schema: 1, Mode: config.Mode, OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version(), PID: os.Getpid(),
		GOMAXPROCS: runtime.GOMAXPROCS(0), WindowMS: config.Duration.Milliseconds(), RequestedRounds: config.Rounds,
		Points:         make([]lifecycleAttributionPoint, 0, 1+config.Rounds+len(config.Idle)),
		Interpretation: "Read-only process diagnostics. Go thread-profile records are not a current native-thread count. Two OS snapshots bracket only part of sampling, not an atomic ownership proof. Control runs the memory sampler/timers/GC but no KitDB workload. No release promotion or waiver of earlier thread/WAL failures."}
	for _, offset := range config.Idle {
		report.IdleOffsetsMS = append(report.IdleOffsetsMS, offset.Milliseconds())
	}
	defer func() {
		if resultErr != nil {
			report.Completed, report.Failure = false, resultErr.Error()
		}
	}()
	started := time.Now()
	sample := func(phase string, index int, idle time.Duration) error {
		point, err := sampleLifecycleThreads()
		if err != nil {
			return err
		}
		entry := lifecycleAttributionPoint{phase, index, time.Since(started).Milliseconds(), idle.Milliseconds(), point}
		report.Points = append(report.Points, entry)
		if events != nil {
			return events.Encode(entry)
		}
		return nil
	}
	if err := sample("initial", -1, 0); err != nil {
		return report, err
	}
	for index := 0; index < config.Rounds; index++ {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if config.Mode == "database" {
			config := mixedConfig{Name: fmt.Sprintf("window_%02d", index), Databases: config.Databases, Noisy: index%2 == 1, Duration: config.Duration}
			summary, err := lifecycleSummarizedWindow(filepath.Join(root, config.Name), config, windows)
			report.DatabaseCalls++
			if err != nil {
				return report, errors.Join(err, sample("failure_after_cleanup", index, 0))
			}
			report.Verified += summary.Verified
			report.Acknowledged += summary.Acknowledged
		} else {
			// Match the measurement worker, not the concurrent database/OS I/O pressure.
			runtime.GC()
			stopMemory := startShoppingFleetMemorySampler()
			err := waitLifecycleUntil(ctx, time.Now().Add(config.Duration))
			stopMemory()
			if err != nil {
				return report, err
			}
		}
		if err := sample("after_window", index, 0); err != nil {
			return report, err
		}
	}
	idleStart := time.Now()
	for index, offset := range config.Idle {
		if err := waitLifecycleUntil(ctx, idleStart.Add(offset)); err != nil {
			return report, err
		}
		if err := sample("idle", index, offset); err != nil {
			return report, err
		}
	}
	report.Completed = true
	return report, nil
}

func TestKitDBThreadAttribution(t *testing.T) {
	if os.Getenv("KITDB_THREAD_ATTRIBUTION") != "1" {
		t.Skip("set KITDB_THREAD_ATTRIBUTION=1 for bounded idle/control diagnostics")
	}
	config := lifecycleAttributionConfig{Mode: os.Getenv("KITDB_THREAD_MODE"), Rounds: 10, Duration: 30 * time.Second, Databases: 16,
		Idle: []time.Duration{0, time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}}
	if err := validateLifecycleAttribution(config); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("KITDB_THREAD_REPORT")
	if path == "" {
		t.Fatal("KITDB_THREAD_REPORT must name a fresh output file")
	}
	open := func(suffix string) *os.File {
		t.Helper()
		file, err := os.OpenFile(path+suffix, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := errors.Join(file.Sync(), file.Close()); err != nil {
				t.Error(err)
			}
		})
		return file
	}
	output, events, windows := open(""), open(".points.jsonl"), open(".windows.jsonl")
	root := ""
	if config.Mode == "database" {
		var err error
		if os.Getenv("KITDB_THREAD_ROOT") == "" {
			t.Fatal("database mode requires a fresh retained KITDB_THREAD_ROOT")
		}
		root, err = lifecycleFixtureRoot(t, os.Getenv("KITDB_THREAD_ROOT"))
		if err != nil {
			t.Fatal(err)
		}
	}
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
	defer cancel()
	report, err := runLifecycleAttribution(ctx, root, config, json.NewEncoder(events), json.NewEncoder(windows))
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if writeErr := encoder.Encode(report); writeErr != nil {
		t.Error(writeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mode=%s pid=%d windows=%d verified=%d ack=%d observations=%d", report.Mode, report.PID, report.RequestedRounds, report.Verified, report.Acknowledged, len(report.Points))
}

func TestLifecycleAttributionSampling(t *testing.T) {
	point, err := sampleLifecycleThreads()
	if err != nil || point.GoRecordsBefore <= 0 || point.GoRecordsAfter <= 0 || point.Resources.Goroutines == 0 {
		t.Fatalf("sample=%+v error=%v", point, err)
	}
}

func TestLifecycleAttributionBounds(t *testing.T) {
	valid := lifecycleAttributionConfig{Mode: "control", Rounds: 10, Duration: 30 * time.Second, Databases: 16, Idle: []time.Duration{0, time.Minute}}
	if err := validateLifecycleAttribution(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*lifecycleAttributionConfig){
		func(c *lifecycleAttributionConfig) { c.Mode = "other" }, func(c *lifecycleAttributionConfig) { c.Rounds = 0 },
		func(c *lifecycleAttributionConfig) { c.Rounds = 11 }, func(c *lifecycleAttributionConfig) { c.Duration = 31 * time.Second },
		func(c *lifecycleAttributionConfig) { c.Duration = 0 }, func(c *lifecycleAttributionConfig) { c.Databases = 1 },
		func(c *lifecycleAttributionConfig) { c.Databases = 17 }, func(c *lifecycleAttributionConfig) { c.Idle = nil },
		func(c *lifecycleAttributionConfig) { c.Idle = []time.Duration{time.Second} },
		func(c *lifecycleAttributionConfig) { c.Idle = []time.Duration{0, time.Second, time.Second} },
		func(c *lifecycleAttributionConfig) { c.Idle = []time.Duration{0, 61 * time.Second} },
	} {
		config := valid
		mutate(&config)
		if err := validateLifecycleAttribution(config); err == nil {
			t.Fatal("unbounded attribution accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitLifecycleUntil(ctx, time.Now().Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait ignored cancellation: %v", err)
	}
}

func TestLifecycleAttributionSmoke(t *testing.T) {
	for _, mode := range []string{"control", "database"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "fixtures")
			if mode == "database" {
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			duration := 5 * time.Millisecond
			if mode == "database" {
				duration = 350 * time.Millisecond
			}
			report, err := runLifecycleAttribution(ctx, root, lifecycleAttributionConfig{Mode: mode, Rounds: 2, Duration: duration, Databases: 2, Idle: []time.Duration{0, 5 * time.Millisecond}}, nil, nil)
			if err != nil || !report.Completed || len(report.Points) != 5 {
				t.Fatalf("report=%+v error=%v", report, err)
			}
			if mode == "control" {
				if report.DatabaseCalls != 0 || report.Verified != 0 || report.Acknowledged != 0 {
					t.Fatal("control invoked database workload")
				}
				if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("control created a fixture: %v", err)
				}
			} else if report.DatabaseCalls != 2 || report.Verified != 4 || report.Acknowledged == 0 {
				t.Fatal("database workload incomplete")
			}
		})
	}
}
