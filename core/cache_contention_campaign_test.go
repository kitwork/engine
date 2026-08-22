package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/compiler"
)

const (
	contentionCampaignSchemaVersion uint16 = 1
	contentionChildSchemaVersion    uint16 = 1

	defaultContentionWorkers  = 8
	maxContentionWorkers      = 32
	defaultContentionRequests = 4
	maxContentionRequests     = 32
	contentionRoundCount      = 4
	maxContentionStartSpread  = 3 * time.Second

	contentionCampaignEnv = "KITWORK_CONTENTION_CAMPAIGN"
	contentionWorkersEnv  = "KITWORK_CONTENTION_WORKERS"
	contentionRequestsEnv = "KITWORK_CONTENTION_REQUESTS"
	contentionReportEnv   = "KITWORK_CONTENTION_REPORT"

	contentionChildEnv         = "KITWORK_CONTENTION_HELPER"
	contentionChildRootEnv     = "KITWORK_CONTENTION_ROOT"
	contentionChildCacheEnv    = "KITWORK_CONTENTION_CACHE"
	contentionChildOutputEnv   = "KITWORK_CONTENTION_OUTPUT"
	contentionChildReadyEnv    = "KITWORK_CONTENTION_READY"
	contentionChildReleaseEnv  = "KITWORK_CONTENTION_RELEASE"
	contentionChildScenarioEnv = "KITWORK_CONTENTION_SCENARIO"
	contentionChildWorkerEnv   = "KITWORK_CONTENTION_WORKER"
	contentionChildRevisionEnv = "KITWORK_CONTENTION_REVISION"
	contentionChildExpectedEnv = "KITWORK_CONTENTION_EXPECTED"
	contentionChildRequestsEnv = "KITWORK_CONTENTION_CHILD_REQUESTS"

	contentionScenarioCold     = "cold-cache"
	contentionScenarioWarm     = "warm-cache"
	contentionScenarioCorrupt  = "corrupt-cache"
	contentionScenarioRevision = "source-revision"
)

type contentionCampaignReport struct {
	SchemaVersion      uint16                          `json:"schema_version"`
	StartedAt          time.Time                       `json:"started_at"`
	FinishedAt         time.Time                       `json:"finished_at"`
	DurationMS         int64                           `json:"duration_ms"`
	GoVersion          string                          `json:"go_version"`
	OS                 string                          `json:"os"`
	Arch               string                          `json:"arch"`
	Compatibility      DiagnosticCompatibilitySnapshot `json:"compatibility"`
	WorkersPerRound    int                             `json:"workers_per_round"`
	RequestsPerWorker  int                             `json:"requests_per_worker"`
	Processes          int                             `json:"processes"`
	Requests           int                             `json:"requests"`
	Revisions          int                             `json:"revisions"`
	Artifacts          int                             `json:"artifacts"`
	TemporaryArtifacts int                             `json:"temporary_artifacts"`
	Success            bool                            `json:"success"`
	Failure            string                          `json:"failure,omitempty"`
	Rounds             []contentionRoundReport         `json:"rounds"`
}

type contentionRoundReport struct {
	Scenario                    string                   `json:"scenario"`
	Revision                    int                      `json:"revision"`
	Workers                     int                      `json:"workers"`
	Requests                    int                      `json:"requests"`
	Status                      int                      `json:"status"`
	ResponseSHA256              string                   `json:"response_sha256"`
	ProgramChecksum             string                   `json:"program_checksum"`
	ArtifactSHA256              string                   `json:"artifact_sha256"`
	ArtifactBytes               int                      `json:"artifact_bytes"`
	TemporaryArtifacts          int                      `json:"temporary_artifacts"`
	StartSpreadMicros           int64                    `json:"start_spread_micros"`
	ReleaseToFirstResponse      contentionLatencySummary `json:"release_to_first_response"`
	FirstRequest                contentionLatencySummary `json:"first_request"`
	WorkerLifetime              contentionLatencySummary `json:"worker_lifetime"`
	CacheReadExpected           bool                     `json:"cache_read_expected"`
	CacheHitPreserved           bool                     `json:"cache_hit_preserved"`
	RepairExpected              bool                     `json:"repair_expected"`
	RepairVerified              bool                     `json:"repair_verified"`
	ArtifactHashStable          bool                     `json:"artifact_hash_stable"`
	AllResponsesMatched         bool                     `json:"all_responses_matched"`
	AllDrainsHealthy            bool                     `json:"all_drains_healthy"`
	AllCompatibilityTuplesMatch bool                     `json:"all_compatibility_tuples_match"`
}

type contentionLatencySummary struct {
	Count     int   `json:"count"`
	P50Micros int64 `json:"p50_micros"`
	P95Micros int64 `json:"p95_micros"`
	MaxMicros int64 `json:"max_micros"`
}

type contentionChildReport struct {
	SchemaVersion                uint16                          `json:"schema_version"`
	Scenario                     string                          `json:"scenario"`
	Worker                       int                             `json:"worker"`
	Revision                     int                             `json:"revision"`
	Requests                     int                             `json:"requests"`
	Status                       int                             `json:"status"`
	Body                         string                          `json:"body"`
	RequestStartedUnixNano       int64                           `json:"request_started_unix_nano"`
	ReleaseToFirstResponseMicros int64                           `json:"release_to_first_response_micros"`
	FirstRequestMicros           int64                           `json:"first_request_micros"`
	WorkerLifetimeMicros         int64                           `json:"worker_lifetime_micros"`
	Compatibility                DiagnosticCompatibilitySnapshot `json:"compatibility"`
	BeforeClose                  restartTopology                 `json:"before_close"`
	AfterClose                   restartTopology                 `json:"after_close"`
	DrainHealthy                 bool                            `json:"drain_healthy"`
}

type contentionRoundConfig struct {
	scenario          string
	revision          int
	cacheReadExpected bool
	repairExpected    bool
}

type runningContentionChild struct {
	worker     int
	readyPath  string
	reportPath string
	command    *exec.Cmd
	output     bytes.Buffer
	done       chan error
	finished   bool
	waitErr    error
}

func TestEngineCacheContentionCampaign(t *testing.T) {
	if os.Getenv(contentionCampaignEnv) != "1" {
		t.Skip("set KITWORK_CONTENTION_CAMPAIGN=1 to run the cache-contention campaign")
	}

	startedAt := time.Now().UTC()
	report := contentionCampaignReport{
		SchemaVersion: contentionCampaignSchemaVersion,
		StartedAt:     startedAt,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Compatibility: restartCompatibility(),
	}
	workers, campaignErr := restartBoundedPositiveEnv(
		contentionWorkersEnv,
		defaultContentionWorkers,
		maxContentionWorkers,
	)
	requests := 0
	if campaignErr == nil {
		requests, campaignErr = restartBoundedPositiveEnv(
			contentionRequestsEnv,
			defaultContentionRequests,
			maxContentionRequests,
		)
	}
	workspace := t.TempDir()
	if campaignErr == nil {
		report.WorkersPerRound = workers
		report.RequestsPerWorker = requests
		campaignErr = runCacheContentionCampaign(t, workspace, workers, requests, &report)
	}
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = time.Since(startedAt).Milliseconds()
	report.Success = campaignErr == nil
	if campaignErr != nil {
		report.Failure = restartSanitizeFailure(campaignErr.Error(), workspace)
	}

	if reportPath := strings.TrimSpace(os.Getenv(contentionReportEnv)); reportPath != "" {
		if err := writeRestartJSON(restartCampaignReportPath(reportPath), report); err != nil {
			if campaignErr != nil {
				t.Fatalf("%v; write contention report: %v", campaignErr, err)
			}
			t.Fatalf("write contention report: %v", err)
		}
		t.Logf("cache-contention evidence: %s", reportPath)
	}
	if campaignErr != nil {
		t.Fatal(campaignErr)
	}
	t.Logf(
		"cache contention passed: rounds=%d processes=%d requests=%d revisions=%d artifacts=%d",
		len(report.Rounds),
		report.Processes,
		report.Requests,
		report.Revisions,
		report.Artifacts,
	)
}

func runCacheContentionCampaign(
	t testing.TB,
	workspace string,
	workers int,
	requests int,
	report *contentionCampaignReport,
) error {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate contention test executable: %w", err)
	}
	root := filepath.Join(workspace, "apps")
	cacheDirectory := filepath.Join(workspace, "bytecode")
	evidenceDirectory := filepath.Join(workspace, "evidence")
	for _, directory := range []string{root, cacheDirectory, evidenceDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create contention directory: %w", err)
		}
	}

	rounds := []contentionRoundConfig{
		{scenario: contentionScenarioCold, revision: 0},
		{scenario: contentionScenarioWarm, revision: 0, cacheReadExpected: true},
		{scenario: contentionScenarioCorrupt, revision: 0, repairExpected: true},
		{scenario: contentionScenarioRevision, revision: 1},
	}
	report.Revisions = 2
	var routerFile string
	for _, round := range rounds {
		if round.scenario == contentionScenarioCold || round.scenario == contentionScenarioRevision {
			routerFile, err = writeRestartCampaignRoute(
				root,
				restartCampaignSource(round.revision),
			)
			if err != nil {
				return fmt.Errorf("%s: write route: %w", round.scenario, err)
			}
		}
		expected, err := compiler.CompileFile(routerFile)
		if err != nil {
			return fmt.Errorf("%s: compile expected artifact: %w", round.scenario, err)
		}
		expectedArtifact, err := expected.MarshalBinary()
		if err != nil {
			return fmt.Errorf("%s: encode expected artifact: %w", round.scenario, err)
		}
		expectedDigest := sha256.Sum256(expectedArtifact)
		expectedHash := hex.EncodeToString(expectedDigest[:])
		artifactPath := filepath.Join(cacheDirectory, expected.CacheKey()+".kwbc")

		before := restartArtifactBefore{}
		switch round.scenario {
		case contentionScenarioCold, contentionScenarioRevision:
			if _, statErr := os.Stat(artifactPath); statErr == nil {
				return fmt.Errorf("%s: new cache key already exists", round.scenario)
			} else if !os.IsNotExist(statErr) {
				return fmt.Errorf("%s: inspect cache key: %w", round.scenario, statErr)
			}
		case contentionScenarioWarm:
			before, err = snapshotRestartArtifact(artifactPath)
			if err != nil {
				return fmt.Errorf("%s: inspect warm artifact: %w", round.scenario, err)
			}
		case contentionScenarioCorrupt:
			before, err = snapshotRestartArtifact(artifactPath)
			if err != nil {
				return fmt.Errorf("%s: inspect repair artifact: %w", round.scenario, err)
			}
			if err := mutateRestartArtifact(
				artifactPath,
				bytecodeArtifactHeaderSize+programChecksumOffset,
			); err != nil {
				return fmt.Errorf("%s: inject checksum fault: %w", round.scenario, err)
			}
			corruptArtifact, readErr := os.ReadFile(artifactPath)
			if readErr != nil {
				return fmt.Errorf("%s: read checksum fault: %w", round.scenario, readErr)
			}
			if _, decodeErr := compiler.UnmarshalBytecode(
				corruptArtifact,
				expected.SourceFingerprint(),
			); decodeErr == nil {
				return fmt.Errorf("%s: injected artifact remained valid", round.scenario)
			}
		}

		expectedBody := fmt.Sprintf("restart-%d:42", round.revision)
		children, err := runContentionChildren(
			executable,
			root,
			cacheDirectory,
			evidenceDirectory,
			round,
			expectedBody,
			workers,
			requests,
		)
		if err != nil {
			return err
		}
		roundReport, err := validateContentionRound(
			children,
			round,
			expectedBody,
			requests,
			expected,
			expectedHash,
			artifactPath,
			cacheDirectory,
			before,
			report.Compatibility,
		)
		if err != nil {
			return err
		}
		report.Rounds = append(report.Rounds, roundReport)
		report.Processes += workers
		report.Requests += workers * requests
		t.Logf(
			"contention scenario=%s workers=%d requests=%d first_response_p50=%s p95=%s start_spread=%s",
			round.scenario,
			workers,
			workers*requests,
			time.Duration(roundReport.ReleaseToFirstResponse.P50Micros)*time.Microsecond,
			time.Duration(roundReport.ReleaseToFirstResponse.P95Micros)*time.Microsecond,
			time.Duration(roundReport.StartSpreadMicros)*time.Microsecond,
		)
	}

	artifacts, err := filepath.Glob(filepath.Join(cacheDirectory, "*.kwbc"))
	if err != nil {
		return fmt.Errorf("list contention artifacts: %w", err)
	}
	temporaryArtifacts, err := filepath.Glob(filepath.Join(cacheDirectory, ".kitwork-bytecode-*"))
	if err != nil {
		return fmt.Errorf("list contention temporary artifacts: %w", err)
	}
	report.Artifacts = len(artifacts)
	report.TemporaryArtifacts = len(temporaryArtifacts)
	if len(artifacts) != report.Revisions {
		return fmt.Errorf("cache contains %d artifacts for %d revisions", len(artifacts), report.Revisions)
	}
	if len(temporaryArtifacts) != 0 {
		return fmt.Errorf("cache retained %d temporary artifacts", len(temporaryArtifacts))
	}
	return nil
}

func runContentionChildren(
	executable string,
	root string,
	cacheDirectory string,
	evidenceDirectory string,
	round contentionRoundConfig,
	expectedBody string,
	workers int,
	requests int,
) ([]contentionChildReport, error) {
	roundDirectory := filepath.Join(evidenceDirectory, round.scenario)
	if err := os.MkdirAll(roundDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("%s: create barrier directory: %w", round.scenario, err)
	}
	releasePath := filepath.Join(roundDirectory, "release")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	processes := make([]*runningContentionChild, 0, workers)
	for worker := 0; worker < workers; worker++ {
		process := &runningContentionChild{
			worker:     worker,
			readyPath:  filepath.Join(roundDirectory, fmt.Sprintf("ready-%03d", worker)),
			reportPath: filepath.Join(roundDirectory, fmt.Sprintf("worker-%03d.json", worker)),
			done:       make(chan error, 1),
		}
		process.command = exec.CommandContext(
			ctx,
			executable,
			"-test.run=^TestEngineCacheContentionChild$",
			"-test.count=1",
			"-test.timeout=50s",
		)
		process.command.Env = restartChildEnvironment(map[string]string{
			contentionChildEnv:         "1",
			contentionChildRootEnv:     root,
			contentionChildCacheEnv:    cacheDirectory,
			contentionChildOutputEnv:   process.reportPath,
			contentionChildReadyEnv:    process.readyPath,
			contentionChildReleaseEnv:  releasePath,
			contentionChildScenarioEnv: round.scenario,
			contentionChildWorkerEnv:   strconv.Itoa(worker),
			contentionChildRevisionEnv: strconv.Itoa(round.revision),
			contentionChildExpectedEnv: expectedBody,
			contentionChildRequestsEnv: strconv.Itoa(requests),
		})
		process.command.Stdout = &process.output
		process.command.Stderr = &process.output
		if err := process.command.Start(); err != nil {
			cancel()
			finishContentionChildren(processes)
			return nil, fmt.Errorf("%s: start worker %d: %w", round.scenario, worker, err)
		}
		processes = append(processes, process)
		go func(child *runningContentionChild) {
			child.done <- child.command.Wait()
		}(process)
	}

	if err := waitContentionChildrenReady(ctx, processes); err != nil {
		cancel()
		finishContentionChildren(processes)
		return nil, fmt.Errorf("%s: %w", round.scenario, err)
	}
	releasedAt := time.Now().UTC()
	if err := publishContentionBarrier(releasePath, releasedAt); err != nil {
		cancel()
		finishContentionChildren(processes)
		return nil, fmt.Errorf("%s: publish release barrier: %w", round.scenario, err)
	}
	finishContentionChildren(processes)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s: child campaign timed out: %w", round.scenario, ctx.Err())
	}

	reports := make([]contentionChildReport, 0, workers)
	for _, process := range processes {
		if process.waitErr != nil {
			return nil, fmt.Errorf(
				"%s: worker %d: %w output=%s",
				round.scenario,
				process.worker,
				process.waitErr,
				restartBoundedOutput(process.output.Bytes()),
			)
		}
		data, err := os.ReadFile(process.reportPath)
		if err != nil {
			return nil, fmt.Errorf("%s: read worker %d evidence: %w", round.scenario, process.worker, err)
		}
		var report contentionChildReport
		if err := json.Unmarshal(data, &report); err != nil {
			return nil, fmt.Errorf("%s: decode worker %d evidence: %w", round.scenario, process.worker, err)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func waitContentionChildrenReady(
	ctx context.Context,
	processes []*runningContentionChild,
) error {
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	ready := make([]bool, len(processes))
	for {
		allReady := true
		for index, process := range processes {
			if !ready[index] {
				if _, err := os.Stat(process.readyPath); err == nil {
					ready[index] = true
				} else if !os.IsNotExist(err) {
					return fmt.Errorf("inspect worker %d readiness: %w", process.worker, err)
				}
			}
			if !ready[index] {
				allReady = false
			}
			if !process.finished {
				select {
				case process.waitErr = <-process.done:
					process.finished = true
					return fmt.Errorf(
						"worker %d exited before release: %v output=%s",
						process.worker,
						process.waitErr,
						restartBoundedOutput(process.output.Bytes()),
					)
				default:
				}
			}
		}
		if allReady {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func finishContentionChildren(processes []*runningContentionChild) {
	for _, process := range processes {
		if process.finished {
			continue
		}
		process.waitErr = <-process.done
		process.finished = true
	}
}

func publishContentionBarrier(path string, releasedAt time.Time) error {
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(
		temporaryPath,
		[]byte(strconv.FormatInt(releasedAt.UnixNano(), 10)),
		0o644,
	); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func validateContentionRound(
	children []contentionChildReport,
	round contentionRoundConfig,
	expectedBody string,
	expectedRequests int,
	expected *compiler.Bytecode,
	expectedHash string,
	artifactPath string,
	cacheDirectory string,
	before restartArtifactBefore,
	compatibility DiagnosticCompatibilitySnapshot,
) (contentionRoundReport, error) {
	if len(children) == 0 {
		return contentionRoundReport{}, fmt.Errorf("%s: no worker evidence", round.scenario)
	}
	requestStarts := make([]int64, 0, len(children))
	releaseToFirst := make([]int64, 0, len(children))
	firstRequests := make([]int64, 0, len(children))
	workerLifetimes := make([]int64, 0, len(children))
	for worker, child := range children {
		if child.SchemaVersion != contentionChildSchemaVersion ||
			child.Scenario != round.scenario ||
			child.Worker != worker ||
			child.Revision != round.revision {
			return contentionRoundReport{}, fmt.Errorf("%s: worker %d evidence identity mismatch", round.scenario, worker)
		}
		if child.Requests != expectedRequests || child.Status != http.StatusOK || child.Body != expectedBody {
			return contentionRoundReport{}, fmt.Errorf(
				"%s: worker %d response mismatch: requests=%d status=%d body=%q",
				round.scenario,
				worker,
				child.Requests,
				child.Status,
				child.Body,
			)
		}
		if child.Compatibility != compatibility {
			return contentionRoundReport{}, fmt.Errorf("%s: worker %d compatibility mismatch", round.scenario, worker)
		}
		if err := validateRestartLiveTopology(child.BeforeClose, child.Requests); err != nil {
			return contentionRoundReport{}, fmt.Errorf("%s: worker %d: %w", round.scenario, worker, err)
		}
		if !child.DrainHealthy {
			return contentionRoundReport{}, fmt.Errorf("%s: worker %d did not drain cleanly", round.scenario, worker)
		}
		if err := validateRestartClosedTopology(child.AfterClose); err != nil {
			return contentionRoundReport{}, fmt.Errorf("%s: worker %d: %w", round.scenario, worker, err)
		}
		if child.RequestStartedUnixNano <= 0 ||
			child.ReleaseToFirstResponseMicros < 0 ||
			child.FirstRequestMicros < 0 ||
			child.WorkerLifetimeMicros < child.FirstRequestMicros {
			return contentionRoundReport{}, fmt.Errorf("%s: worker %d has invalid timing evidence", round.scenario, worker)
		}
		requestStarts = append(requestStarts, child.RequestStartedUnixNano)
		releaseToFirst = append(releaseToFirst, child.ReleaseToFirstResponseMicros)
		firstRequests = append(firstRequests, child.FirstRequestMicros)
		workerLifetimes = append(workerLifetimes, child.WorkerLifetimeMicros)
	}

	sort.Slice(requestStarts, func(left, right int) bool {
		return requestStarts[left] < requestStarts[right]
	})
	startSpread := time.Duration(requestStarts[len(requestStarts)-1] - requestStarts[0])
	if startSpread > maxContentionStartSpread {
		return contentionRoundReport{}, fmt.Errorf(
			"%s: barrier start spread %s exceeds %s",
			round.scenario,
			startSpread,
			maxContentionStartSpread,
		)
	}

	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return contentionRoundReport{}, fmt.Errorf("%s: read final artifact: %w", round.scenario, err)
	}
	restored, err := compiler.UnmarshalBytecode(artifact, expected.SourceFingerprint())
	if err != nil {
		return contentionRoundReport{}, fmt.Errorf("%s: validate final artifact: %w", round.scenario, err)
	}
	digest := sha256.Sum256(artifact)
	artifactHash := hex.EncodeToString(digest[:])
	if artifactHash != expectedHash || restored.Program.Checksum() != expected.Program.Checksum() {
		return contentionRoundReport{}, fmt.Errorf("%s: final artifact identity changed", round.scenario)
	}
	temporaryArtifacts, err := filepath.Glob(filepath.Join(cacheDirectory, ".kitwork-bytecode-*"))
	if err != nil {
		return contentionRoundReport{}, fmt.Errorf("%s: list temporary artifacts: %w", round.scenario, err)
	}
	if len(temporaryArtifacts) != 0 {
		return contentionRoundReport{}, fmt.Errorf("%s: retained %d temporary artifacts", round.scenario, len(temporaryArtifacts))
	}

	cacheHitPreserved := false
	if round.cacheReadExpected {
		after, err := snapshotRestartArtifact(artifactPath)
		if err != nil {
			return contentionRoundReport{}, fmt.Errorf("%s: inspect cache hit: %w", round.scenario, err)
		}
		cacheHitPreserved = before.exists &&
			before.hash == after.hash &&
			before.size == after.size &&
			before.modTime.Equal(after.modTime)
		if !cacheHitPreserved {
			return contentionRoundReport{}, fmt.Errorf("%s: healthy cache artifact was rewritten", round.scenario)
		}
	}

	responseDigest := sha256.Sum256([]byte(expectedBody))
	return contentionRoundReport{
		Scenario:                    round.scenario,
		Revision:                    round.revision,
		Workers:                     len(children),
		Requests:                    contentionChildRequestTotal(children),
		Status:                      http.StatusOK,
		ResponseSHA256:              hex.EncodeToString(responseDigest[:]),
		ProgramChecksum:             restored.Program.Checksum(),
		ArtifactSHA256:              artifactHash,
		ArtifactBytes:               len(artifact),
		TemporaryArtifacts:          len(temporaryArtifacts),
		StartSpreadMicros:           startSpread.Microseconds(),
		ReleaseToFirstResponse:      summarizeContentionLatency(releaseToFirst),
		FirstRequest:                summarizeContentionLatency(firstRequests),
		WorkerLifetime:              summarizeContentionLatency(workerLifetimes),
		CacheReadExpected:           round.cacheReadExpected,
		CacheHitPreserved:           cacheHitPreserved,
		RepairExpected:              round.repairExpected,
		RepairVerified:              round.repairExpected && artifactHash == expectedHash,
		ArtifactHashStable:          artifactHash == expectedHash,
		AllResponsesMatched:         true,
		AllDrainsHealthy:            true,
		AllCompatibilityTuplesMatch: true,
	}, nil
}

func contentionChildRequestTotal(children []contentionChildReport) int {
	total := 0
	for _, child := range children {
		total += child.Requests
	}
	return total
}

func summarizeContentionLatency(values []int64) contentionLatencySummary {
	if len(values) == 0 {
		return contentionLatencySummary{}
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left] < ordered[right]
	})
	return contentionLatencySummary{
		Count:     len(ordered),
		P50Micros: contentionPercentile(ordered, 50),
		P95Micros: contentionPercentile(ordered, 95),
		MaxMicros: ordered[len(ordered)-1],
	}
}

func contentionPercentile(ordered []int64, percentile int) int64 {
	if len(ordered) == 0 {
		return 0
	}
	index := (len(ordered)*percentile+99)/100 - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}

func TestContentionLatencySummary(t *testing.T) {
	summary := summarizeContentionLatency([]int64{80, 10, 40, 20, 70, 50, 30, 60})
	if summary.Count != 8 || summary.P50Micros != 40 || summary.P95Micros != 80 || summary.MaxMicros != 80 {
		t.Fatalf("latency summary = %+v", summary)
	}
}

func TestEngineCacheContentionChild(t *testing.T) {
	if os.Getenv(contentionChildEnv) != "1" {
		t.Skip("cache-contention child process")
	}
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(previousLogger)

	root := restartRequiredEnv(t, contentionChildRootEnv)
	cacheDirectory := restartRequiredEnv(t, contentionChildCacheEnv)
	outputPath := restartRequiredEnv(t, contentionChildOutputEnv)
	readyPath := restartRequiredEnv(t, contentionChildReadyEnv)
	releasePath := restartRequiredEnv(t, contentionChildReleaseEnv)
	scenario := restartRequiredEnv(t, contentionChildScenarioEnv)
	expectedBody := restartRequiredEnv(t, contentionChildExpectedEnv)
	worker := contentionChildInteger(t, contentionChildWorkerEnv, true, maxContentionWorkers-1)
	revision := contentionChildInteger(t, contentionChildRevisionEnv, true, contentionRoundCount)
	requests := contentionChildInteger(t, contentionChildRequestsEnv, false, maxContentionRequests)

	engine := New(root, 1_000_000, false, "localhost")
	engine.SetBytecodeCache(cacheDirectory)
	server := httptest.NewServer(engine)
	client := server.Client()
	client.Timeout = 10 * time.Second
	closed := false
	t.Cleanup(func() {
		if closed {
			return
		}
		client.CloseIdleConnections()
		server.Close()
		engine.Close()
	})

	if err := os.WriteFile(readyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("publish worker readiness: %v", err)
	}
	releasedAt, err := waitContentionRelease(releasePath, 35*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	requestStartedAt := time.Now()
	report := contentionChildReport{
		SchemaVersion:          contentionChildSchemaVersion,
		Scenario:               scenario,
		Worker:                 worker,
		Revision:               revision,
		RequestStartedUnixNano: requestStartedAt.UnixNano(),
	}
	for requestIndex := 0; requestIndex < requests; requestIndex++ {
		requestStarted := time.Now()
		response, requestErr := client.Get(server.URL + "/")
		if requestErr != nil {
			t.Fatalf("request %d: %v", requestIndex, requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			t.Fatalf("read request %d: %v", requestIndex, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close request %d: %v", requestIndex, closeErr)
		}
		report.Requests++
		report.Status = response.StatusCode
		report.Body = string(body)
		if requestIndex == 0 {
			report.FirstRequestMicros = time.Since(requestStarted).Microseconds()
			report.ReleaseToFirstResponseMicros = time.Since(releasedAt).Microseconds()
		}
		if response.StatusCode != http.StatusOK || report.Body != expectedBody {
			t.Fatalf(
				"request %d: status=%d body=%q, want %q",
				requestIndex,
				response.StatusCode,
				report.Body,
				expectedBody,
			)
		}
	}

	live := engine.Diagnostics()
	report.Compatibility = live.Compatibility
	report.BeforeClose = restartTopologyFrom(live)
	client.CloseIdleConnections()
	server.Close()
	engine.Close()
	closed = true
	closedSnapshot := engine.Diagnostics()
	report.AfterClose = restartTopologyFrom(closedSnapshot)
	report.DrainHealthy = validateRestartClosedTopology(report.AfterClose) == nil
	report.WorkerLifetimeMicros = time.Since(requestStartedAt).Microseconds()
	if err := writeRestartJSON(outputPath, report); err != nil {
		t.Fatal(err)
	}
}

func waitContentionRelease(path string, timeout time.Duration) (time.Time, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var lastReadError error
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			nanoseconds, parseErr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if parseErr != nil || nanoseconds <= 0 {
				return time.Time{}, fmt.Errorf("decode release barrier")
			}
			return time.Unix(0, nanoseconds), nil
		}
		// Windows can briefly return a sharing violation while the parent
		// atomically renames the marker. Until the deadline, every read error
		// means the barrier is not observable yet.
		lastReadError = err
		select {
		case <-deadline.C:
			return time.Time{}, fmt.Errorf("release barrier timed out: %w", lastReadError)
		case <-ticker.C:
		}
	}
}

func contentionChildInteger(
	t testing.TB,
	name string,
	allowZero bool,
	maximum int,
) int {
	t.Helper()
	value, err := strconv.Atoi(restartRequiredEnv(t, name))
	if err != nil || value < 0 || (!allowZero && value == 0) || value > maximum {
		t.Fatalf("%s must be a bounded integer", name)
	}
	return value
}
