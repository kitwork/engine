package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/work"
)

const (
	canarySchemaVersion = 1
	canaryBodyLimit     = 64 << 10
)

var canaryLatencyBounds = [...]time.Duration{
	time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	5 * time.Second,
}

type canaryConfig struct {
	URL            string
	Duration       time.Duration
	Interval       time.Duration
	RequestTimeout time.Duration
	ReloadEvery    time.Duration
	ReportEvery    time.Duration
	Workers        int
	ExpectedStatus int
	Contains       string
	MaxErrorRate   float64
	BundlePath     string
	IncludeHeap    bool
	ReportPath     string
	Quiet          bool
}

func (c canaryConfig) Validate() error {
	if c.Duration <= 0 {
		return errors.New("duration must be positive")
	}
	if c.Interval < 0 {
		return errors.New("interval cannot be negative")
	}
	if c.RequestTimeout <= 0 {
		return errors.New("request-timeout must be positive")
	}
	if c.Workers <= 0 || c.Workers > 1_024 {
		return fmt.Errorf("workers must be between 1 and 1024, got %d", c.Workers)
	}
	if c.ExpectedStatus < 100 || c.ExpectedStatus > 599 {
		return fmt.Errorf("status must be between 100 and 599, got %d", c.ExpectedStatus)
	}
	if c.MaxErrorRate < 0 || c.MaxErrorRate > 1 {
		return fmt.Errorf("max-error-rate must be between 0 and 1, got %f", c.MaxErrorRate)
	}
	if c.URL == "" && c.ReloadEvery <= 0 {
		return errors.New("reload-every must be positive in synthetic mode")
	}
	if c.IncludeHeap && c.BundlePath == "" {
		return errors.New("heap requires a diagnostic bundle path")
	}
	if c.URL != "" && (c.BundlePath != "" || c.IncludeHeap) {
		return errors.New("diagnostic bundles are available only in synthetic mode")
	}
	if c.URL != "" {
		parsed, err := url.Parse(c.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("url must be an absolute http or https URL")
		}
	}
	return nil
}

type canaryLatencyBucket struct {
	UpperBoundMS int64  `json:"upper_bound_ms,omitempty"`
	Overflow     bool   `json:"overflow,omitempty"`
	Count        uint64 `json:"count"`
}

type canarySummary struct {
	SchemaVersion   uint16                   `json:"schema_version"`
	Mode            string                   `json:"mode"`
	Target          string                   `json:"target"`
	StartedAt       time.Time                `json:"started_at"`
	FinishedAt      time.Time                `json:"finished_at"`
	DurationMS      int64                    `json:"duration_ms"`
	Workers         int                      `json:"workers"`
	Requests        uint64                   `json:"requests"`
	Successes       uint64                   `json:"successes"`
	Failures        uint64                   `json:"failures"`
	TransportErrors uint64                   `json:"transport_errors"`
	StatusErrors    uint64                   `json:"status_errors"`
	BodyErrors      uint64                   `json:"body_errors"`
	ReloadWrites    uint64                   `json:"reload_writes"`
	ErrorRate       float64                  `json:"error_rate"`
	MaxLatencyMS    int64                    `json:"max_latency_ms"`
	Latency         []canaryLatencyBucket    `json:"latency"`
	DrainHealthy    bool                     `json:"drain_healthy"`
	Diagnostics     *core.DiagnosticSnapshot `json:"diagnostics,omitempty"`
	Success         bool                     `json:"success"`
	Failure         string                   `json:"failure,omitempty"`
}

type canaryCounters struct {
	requests        atomic.Uint64
	successes       atomic.Uint64
	transportErrors atomic.Uint64
	statusErrors    atomic.Uint64
	bodyErrors      atomic.Uint64
	reloadWrites    atomic.Uint64
	maxLatencyNS    atomic.Int64
	latency         [len(canaryLatencyBounds) + 1]atomic.Uint64
}

type canaryTarget struct {
	mode         string
	url          string
	client       *http.Client
	verifyBody   func([]byte) bool
	reload       func(int) error
	engine       *core.Engine
	server       *httptest.Server
	upstream     *httptest.Server
	root         string
	allowLocal   bool
	restoreLocal bool
}

func executeCanary(ctx context.Context, config canaryConfig) (canarySummary, error) {
	target, err := openCanaryTarget(config)
	if err != nil {
		return canarySummary{}, err
	}
	startedAt := time.Now().UTC()
	summary := canarySummary{
		SchemaVersion: canarySchemaVersion,
		Mode:          target.mode,
		Target:        publicCanaryTarget(target),
		StartedAt:     startedAt,
		Workers:       config.Workers,
	}

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	counters := &canaryCounters{}
	controlErrors := make(chan error, 1)
	var wait sync.WaitGroup

	if target.reload != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runCanaryReloads(runContext, cancel, config.ReloadEvery, target, counters, controlErrors)
		}()
	}
	if !config.Quiet && config.ReportEvery > 0 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runCanaryReporter(runContext, config.ReportEvery, counters)
		}()
	}
	for worker := 0; worker < config.Workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runCanaryWorker(runContext, config, target, counters)
		}()
	}

	<-runContext.Done()
	cancel()
	wait.Wait()

	if errors.Is(ctx.Err(), context.Canceled) {
		summary.Failure = "canary interrupted before the configured duration"
	}
	select {
	case controlErr := <-controlErrors:
		summary.Failure = controlErr.Error()
	default:
	}

	drainHealthy, closeErr := target.Close()
	summary.DrainHealthy = drainHealthy
	if closeErr != nil && summary.Failure == "" {
		summary.Failure = closeErr.Error()
	}
	if target.engine != nil {
		diagnostics := target.engine.Diagnostics()
		summary.Diagnostics = &diagnostics
		if config.BundlePath != "" {
			if bundleErr := writeCanaryBundle(config.BundlePath, target.engine, config.IncludeHeap); bundleErr != nil && summary.Failure == "" {
				summary.Failure = bundleErr.Error()
			}
		}
	}

	finishCanarySummary(&summary, counters, startedAt, config.MaxErrorRate)
	if !summary.Success {
		return summary, errors.New(summary.Failure)
	}
	return summary, nil
}

func runCanaryWorker(
	ctx context.Context,
	config canaryConfig,
	target *canaryTarget,
	counters *canaryCounters,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		startedAt := time.Now()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
		if err != nil {
			counters.requests.Add(1)
			counters.transportErrors.Add(1)
			return
		}
		response, requestErr := target.client.Do(request)
		if requestErr != nil {
			if ctx.Err() != nil {
				return
			}
			counters.recordLatency(time.Since(startedAt))
			counters.requests.Add(1)
			counters.transportErrors.Add(1)
		} else {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, canaryBodyLimit))
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if readErr != nil && ctx.Err() != nil {
				return
			}
			counters.recordLatency(time.Since(startedAt))
			counters.requests.Add(1)
			switch {
			case readErr != nil:
				counters.transportErrors.Add(1)
			case response.StatusCode != config.ExpectedStatus:
				counters.statusErrors.Add(1)
			case target.verifyBody != nil && !target.verifyBody(body):
				counters.bodyErrors.Add(1)
			default:
				counters.successes.Add(1)
			}
		}

		if config.Interval == 0 {
			continue
		}
		timer := time.NewTimer(config.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func runCanaryReloads(
	ctx context.Context,
	cancel context.CancelFunc,
	interval time.Duration,
	target *canaryTarget,
	counters *canaryCounters,
	errors chan<- error,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	revision := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			revision++
			if err := target.reload(revision); err != nil {
				select {
				case errors <- fmt.Errorf("rewrite generation %d: %w", revision, err):
				default:
				}
				cancel()
				return
			}
			counters.reloadWrites.Add(1)
		}
	}
}

func runCanaryReporter(ctx context.Context, interval time.Duration, counters *canaryCounters) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requests := counters.requests.Load()
			successes := counters.successes.Load()
			failures := counters.transportErrors.Load() +
				counters.statusErrors.Load() +
				counters.bodyErrors.Load()
			fmt.Printf(
				"canary: requests=%d successes=%d failures=%d reload_writes=%d max_latency=%s\n",
				requests,
				successes,
				failures,
				counters.reloadWrites.Load(),
				time.Duration(counters.maxLatencyNS.Load()),
			)
		}
	}
}

func (c *canaryCounters) recordLatency(elapsed time.Duration) {
	index := len(canaryLatencyBounds)
	for current, upper := range canaryLatencyBounds {
		if elapsed <= upper {
			index = current
			break
		}
	}
	c.latency[index].Add(1)
	nanoseconds := elapsed.Nanoseconds()
	for {
		current := c.maxLatencyNS.Load()
		if nanoseconds <= current || c.maxLatencyNS.CompareAndSwap(current, nanoseconds) {
			return
		}
	}
}

func finishCanarySummary(
	summary *canarySummary,
	counters *canaryCounters,
	startedAt time.Time,
	maxErrorRate float64,
) {
	summary.FinishedAt = time.Now().UTC()
	summary.DurationMS = summary.FinishedAt.Sub(startedAt).Milliseconds()
	summary.Requests = counters.requests.Load()
	summary.Successes = counters.successes.Load()
	summary.TransportErrors = counters.transportErrors.Load()
	summary.StatusErrors = counters.statusErrors.Load()
	summary.BodyErrors = counters.bodyErrors.Load()
	summary.ReloadWrites = counters.reloadWrites.Load()
	summary.Failures = summary.TransportErrors + summary.StatusErrors + summary.BodyErrors
	summary.MaxLatencyMS = time.Duration(counters.maxLatencyNS.Load()).Milliseconds()
	if summary.Requests > 0 {
		summary.ErrorRate = float64(summary.Failures) / float64(summary.Requests)
	}
	for index := range counters.latency {
		bucket := canaryLatencyBucket{Count: counters.latency[index].Load()}
		if index < len(canaryLatencyBounds) {
			bucket.UpperBoundMS = canaryLatencyBounds[index].Milliseconds()
		} else {
			bucket.Overflow = true
		}
		summary.Latency = append(summary.Latency, bucket)
	}

	switch {
	case summary.Failure != "":
	case summary.Requests == 0:
		summary.Failure = "canary completed without requests"
	case summary.Requests != summary.Successes+summary.Failures:
		summary.Failure = fmt.Sprintf(
			"request accounting mismatch: requests=%d successes=%d failures=%d",
			summary.Requests,
			summary.Successes,
			summary.Failures,
		)
	case summary.ErrorRate > maxErrorRate:
		summary.Failure = fmt.Sprintf(
			"error rate %.6f exceeded %.6f",
			summary.ErrorRate,
			maxErrorRate,
		)
	case summary.Mode == "synthetic" && !summary.DrainHealthy:
		summary.Failure = "synthetic engine did not drain cleanly"
	}
	summary.Success = summary.Failure == ""
}

func openCanaryTarget(config canaryConfig) (*canaryTarget, error) {
	if config.URL != "" {
		verify := func(body []byte) bool { return true }
		if config.Contains != "" {
			verify = func(body []byte) bool { return bytesContain(body, config.Contains) }
		}
		return &canaryTarget{
			mode:       "external",
			url:        config.URL,
			client:     &http.Client{Timeout: config.RequestTimeout},
			verifyBody: verify,
		}, nil
	}
	return openSyntheticCanary(config)
}

func openSyntheticCanary(config canaryConfig) (*canaryTarget, error) {
	allowLocal := work.AllowLocal
	work.AllowLocal = true
	cleanupAllowLocal := true
	defer func() {
		if cleanupAllowLocal {
			work.AllowLocal = allowLocal
		}
	}()

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("native-ok"))
	}))
	root, err := os.MkdirTemp("", "kitwork-canary-*")
	if err != nil {
		upstream.Close()
		return nil, err
	}
	directory := filepath.Join(root, "canary", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		upstream.Close()
		_ = os.RemoveAll(root)
		return nil, err
	}
	routerFile := filepath.Join(directory, work.RouterFileName)
	writeSource := func(revision int) error {
		if err := os.WriteFile(
			routerFile,
			[]byte(syntheticCanarySource(revision, upstream.URL)),
			0o644,
		); err != nil {
			return err
		}
		now := time.Now().Add(time.Duration(revision) * time.Second)
		return os.Chtimes(routerFile, now, now)
	}
	if err := writeSource(0); err != nil {
		upstream.Close()
		_ = os.RemoveAll(root)
		return nil, err
	}

	engine := core.New(root, 1_000_000, true, "localhost")
	server := httptest.NewServer(engine)
	client := server.Client()
	client.Timeout = config.RequestTimeout
	cleanupAllowLocal = false
	return &canaryTarget{
		mode:         "synthetic",
		url:          server.URL + "/",
		client:       client,
		verifyBody:   verifySyntheticCanaryBody,
		reload:       writeSource,
		engine:       engine,
		server:       server,
		upstream:     upstream,
		root:         root,
		allowLocal:   allowLocal,
		restoreLocal: true,
	}, nil
}

func syntheticCanarySource(revision int, upstream string) string {
	return fmt.Sprintf(`
import { router, http } from "kitwork";

const generation = "generation-%d";
const retentionProbe = %q;
const make = (base) => (number) => base + number;

router.get((ctx) => {
	const functions = [1, 2, 3, 4].map((base) => make(base));
	const rendered = functions.map((fn) => fn(10)).join(",");
	const response = http.get(%q);
	return ctx.text(generation + ":" + rendered + ":" + response.text());
});
`, revision, strings.Repeat("x", 16<<10), upstream)
}

func verifySyntheticCanaryBody(body []byte) bool {
	text := string(body)
	if !strings.HasPrefix(text, "generation-") ||
		!strings.HasSuffix(text, ":11,12,13,14:native-ok") {
		return false
	}
	separator := strings.IndexByte(text, ':')
	if separator <= len("generation-") {
		return false
	}
	_, err := strconv.Atoi(text[len("generation-"):separator])
	return err == nil
}

func bytesContain(body []byte, expected string) bool {
	return strings.Contains(string(body), expected)
}

func publicCanaryTarget(target *canaryTarget) string {
	if target.mode == "synthetic" {
		return "synthetic://kitwork"
	}
	parsed, err := url.Parse(target.url)
	if err != nil {
		return "external://invalid"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func (target *canaryTarget) Close() (bool, error) {
	if target == nil {
		return true, nil
	}
	if target.client != nil {
		target.client.CloseIdleConnections()
	}
	if target.server != nil {
		target.server.Close()
	}
	if target.engine != nil {
		target.engine.Close()
	}
	drained := true
	if target.engine != nil {
		health := target.engine.Health()
		drained = health.LoadedApps == 0 &&
			health.LoadedSites == 0 &&
			health.ActiveGenerations == 0 &&
			health.ActiveGenerationLeases == 0 &&
			health.VMPool.Active == 0 &&
			health.Requests.Inflight == 0
	}
	if target.upstream != nil {
		target.upstream.Close()
	}
	if target.restoreLocal {
		work.AllowLocal = target.allowLocal
	}
	if target.root != "" {
		if err := os.RemoveAll(target.root); err != nil {
			return drained, err
		}
	}
	return drained, nil
}

func writeCanaryBundle(path string, engine *core.Engine, includeHeap bool) error {
	if engine == nil {
		return errors.New("canary has no local engine for a diagnostic bundle")
	}
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writeErr := engine.WriteDiagnosticBundle(
		file,
		core.DiagnosticBundleOptions{IncludeHeapProfile: includeHeap},
	)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func writeCanaryReport(path string, summary canarySummary) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func printCanarySummary(summary canarySummary) {
	fmt.Printf(
		"canary: mode=%s duration=%s requests=%d successes=%d failures=%d error_rate=%.6f reload_writes=%d max_latency=%s drain=%v success=%v\n",
		summary.Mode,
		time.Duration(summary.DurationMS)*time.Millisecond,
		summary.Requests,
		summary.Successes,
		summary.Failures,
		summary.ErrorRate,
		summary.ReloadWrites,
		time.Duration(summary.MaxLatencyMS)*time.Millisecond,
		summary.DrainHealthy,
		summary.Success,
	)
}
