package core

import (
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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/compiler"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/work"
)

const (
	restartCampaignSchemaVersion uint16 = 1
	restartChildSchemaVersion    uint16 = 1

	defaultRestartCampaignCycles = 24
	maxRestartCampaignCycles     = 256
	restartRequestsPerCycle      = 3
	restartAbruptExitCode        = 23

	restartCampaignEnv = "KITWORK_RESTART_CAMPAIGN"
	restartCyclesEnv   = "KITWORK_RESTART_CYCLES"
	restartReportEnv   = "KITWORK_RESTART_REPORT"

	restartChildEnv         = "KITWORK_RESTART_HELPER"
	restartChildRootEnv     = "KITWORK_RESTART_ROOT"
	restartChildCacheEnv    = "KITWORK_RESTART_CACHE"
	restartChildOutputEnv   = "KITWORK_RESTART_OUTPUT"
	restartChildCycleEnv    = "KITWORK_RESTART_CYCLE"
	restartChildRevisionEnv = "KITWORK_RESTART_REVISION"
	restartChildExpectedEnv = "KITWORK_RESTART_EXPECTED"
	restartChildRequestsEnv = "KITWORK_RESTART_REQUESTS"
	restartChildAbruptEnv   = "KITWORK_RESTART_ABRUPT"

	restartScenarioCold              = "cold-start"
	restartScenarioCacheHit          = "cache-hit"
	restartScenarioTruncated         = "truncated-artifact"
	restartScenarioStaleCompiler     = "stale-compiler-artifact"
	restartScenarioCorruptProgram    = "corrupt-program-checksum"
	restartScenarioDeleted           = "deleted-artifact"
	restartScenarioAbrupt            = "abrupt-stop"
	restartScenarioRecoveryAfterStop = "recovery-after-abrupt-stop"

	bytecodeArtifactHeaderSize = 4 + 2 + sha256.Size + sha256.Size + 4
	programChecksumOffset      = 4 + 2 + 2
)

type restartCampaignReport struct {
	SchemaVersion      uint16                          `json:"schema_version"`
	StartedAt          time.Time                       `json:"started_at"`
	FinishedAt         time.Time                       `json:"finished_at"`
	DurationMS         int64                           `json:"duration_ms"`
	GoVersion          string                          `json:"go_version"`
	OS                 string                          `json:"os"`
	Arch               string                          `json:"arch"`
	Compatibility      DiagnosticCompatibilitySnapshot `json:"compatibility"`
	Cycles             int                             `json:"cycles"`
	Revisions          int                             `json:"revisions"`
	Requests           int                             `json:"requests"`
	AbruptStops        int                             `json:"abrupt_stops"`
	Faults             restartFaultCounts              `json:"faults"`
	Artifacts          int                             `json:"artifacts"`
	TemporaryArtifacts int                             `json:"temporary_artifacts"`
	Success            bool                            `json:"success"`
	Failure            string                          `json:"failure,omitempty"`
	Results            []restartCycleReport            `json:"results"`
}

type restartFaultCounts struct {
	Truncated     int `json:"truncated"`
	StaleCompiler int `json:"stale_compiler"`
	Checksum      int `json:"checksum"`
	Deleted       int `json:"deleted"`
}

type restartCycleReport struct {
	Cycle                int    `json:"cycle"`
	Revision             int    `json:"revision"`
	Scenario             string `json:"scenario"`
	Abrupt               bool   `json:"abrupt"`
	Requests             int    `json:"requests"`
	Status               int    `json:"status"`
	ResponseSHA256       string `json:"response_sha256"`
	ArtifactSHA256       string `json:"artifact_sha256"`
	ProgramChecksum      string `json:"program_checksum"`
	ArtifactBytes        int    `json:"artifact_bytes"`
	CacheReadExpected    bool   `json:"cache_read_expected"`
	CacheHitPreserved    bool   `json:"cache_hit_preserved"`
	RepairExpected       bool   `json:"repair_expected"`
	RepairVerified       bool   `json:"repair_verified"`
	ArtifactHashStable   bool   `json:"artifact_hash_stable"`
	DrainHealthy         bool   `json:"drain_healthy"`
	CompatibilityMatched bool   `json:"compatibility_matched"`
	DurationMS           int64  `json:"duration_ms"`
}

type restartChildReport struct {
	SchemaVersion uint16                          `json:"schema_version"`
	Cycle         int                             `json:"cycle"`
	Revision      int                             `json:"revision"`
	Abrupt        bool                            `json:"abrupt"`
	Requests      int                             `json:"requests"`
	Status        int                             `json:"status"`
	Body          string                          `json:"body"`
	Compatibility DiagnosticCompatibilitySnapshot `json:"compatibility"`
	BeforeClose   restartTopology                 `json:"before_close"`
	AfterClose    restartTopology                 `json:"after_close"`
	DrainHealthy  bool                            `json:"drain_healthy"`
}

type restartTopology struct {
	Closed                     bool   `json:"closed"`
	OwnershipSnapshotAvailable bool   `json:"ownership_snapshot_available"`
	LoadedApps                 int    `json:"loaded_apps"`
	LoadedSites                int    `json:"loaded_sites"`
	ActiveGenerations          int    `json:"active_generations"`
	ActiveGenerationLeases     int    `json:"active_generation_leases"`
	InflightRequests           uint64 `json:"inflight_requests"`
	VMsActive                  int64  `json:"vms_active"`
	Programs                   int    `json:"programs"`
	Executions                 uint64 `json:"executions"`
	Successes                  uint64 `json:"successes"`
	Failures                   uint64 `json:"failures"`
	RequestsStarted            uint64 `json:"requests_started"`
	RequestsCompleted          uint64 `json:"requests_completed"`
}

type restartArtifactBefore struct {
	exists  bool
	hash    string
	size    int64
	modTime time.Time
}

func TestEngineRestartRecoveryCampaign(t *testing.T) {
	if os.Getenv(restartCampaignEnv) != "1" {
		t.Skip("set KITWORK_RESTART_CAMPAIGN=1 to run the restart/recovery campaign")
	}

	startedAt := time.Now().UTC()
	report := restartCampaignReport{
		SchemaVersion: restartCampaignSchemaVersion,
		StartedAt:     startedAt,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Compatibility: restartCompatibility(),
	}
	cycles, campaignErr := restartBoundedPositiveEnv(
		restartCyclesEnv,
		defaultRestartCampaignCycles,
		maxRestartCampaignCycles,
	)
	workspace := t.TempDir()
	if campaignErr == nil {
		report.Cycles = cycles
		campaignErr = runRestartRecoveryCampaign(t, workspace, cycles, &report)
	}
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = time.Since(startedAt).Milliseconds()
	report.Success = campaignErr == nil
	if campaignErr != nil {
		report.Failure = restartSanitizeFailure(campaignErr.Error(), workspace)
	}

	if reportPath := strings.TrimSpace(os.Getenv(restartReportEnv)); reportPath != "" {
		if err := writeRestartJSON(restartCampaignReportPath(reportPath), report); err != nil {
			if campaignErr != nil {
				t.Fatalf("%v; write restart report: %v", campaignErr, err)
			}
			t.Fatalf("write restart report: %v", err)
		}
		t.Logf("restart/recovery evidence: %s", reportPath)
	}
	if campaignErr != nil {
		t.Fatal(campaignErr)
	}
	t.Logf(
		"restart/recovery passed: cycles=%d revisions=%d requests=%d abrupt_stops=%d artifacts=%d",
		report.Cycles,
		report.Revisions,
		report.Requests,
		report.AbruptStops,
		report.Artifacts,
	)
}

func TestRestartCampaignCompatibilityMatchesRuntimePolicy(t *testing.T) {
	compatibility := restartCompatibility()
	if compatibility.RuntimeLimits != kitruntime.Limits() {
		t.Fatalf(
			"restart runtime limits = %+v, want %+v",
			compatibility.RuntimeLimits,
			kitruntime.Limits(),
		)
	}
}

func runRestartRecoveryCampaign(
	t testing.TB,
	workspace string,
	cycles int,
	report *restartCampaignReport,
) error {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate campaign test executable: %w", err)
	}
	root := filepath.Join(workspace, "apps")
	cacheDirectory := filepath.Join(workspace, "bytecode")
	reportDirectory := filepath.Join(workspace, "reports")
	for _, directory := range []string{root, cacheDirectory, reportDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create campaign directory: %w", err)
		}
	}

	var (
		activeRevision = -1
		expected       *compiler.Bytecode
		expectedBody   string
		artifactPath   string
		baselineHash   string
		baselineBytes  int
		revisionCount  int
	)

	for cycle := 0; cycle < cycles; cycle++ {
		cycleStarted := time.Now()
		revision := cycle / 8
		phase := cycle % 8
		scenario := restartScenario(phase)
		if revision != activeRevision {
			activeRevision = revision
			revisionCount++
			report.Revisions = revisionCount
			baselineHash = ""
			baselineBytes = 0
			expectedBody = fmt.Sprintf("restart-%d:42", revision)
			routerFile, writeErr := writeRestartCampaignRoute(
				root,
				restartCampaignSource(revision),
			)
			if writeErr != nil {
				return fmt.Errorf("revision %d: write route: %w", revision, writeErr)
			}
			expected, err = compiler.CompileFile(routerFile)
			if err != nil {
				return fmt.Errorf("revision %d: compile expected artifact: %w", revision, err)
			}
			artifactPath = filepath.Join(cacheDirectory, expected.CacheKey()+".kwbc")
			if _, statErr := os.Stat(artifactPath); statErr == nil {
				return fmt.Errorf("revision %d: new cache key already exists", revision)
			} else if !os.IsNotExist(statErr) {
				return fmt.Errorf("revision %d: inspect new cache key: %w", revision, statErr)
			}
		}

		before, err := prepareRestartScenario(artifactPath, scenario, &report.Faults)
		if err != nil {
			return fmt.Errorf("cycle %d (%s): %w", cycle, scenario, err)
		}
		abrupt := scenario == restartScenarioAbrupt
		outputPath := filepath.Join(reportDirectory, fmt.Sprintf("cycle-%03d.json", cycle))
		child, err := runRestartChildProcess(
			executable,
			root,
			cacheDirectory,
			outputPath,
			cycle,
			revision,
			expectedBody,
			restartRequestsPerCycle,
			abrupt,
		)
		if err != nil {
			return fmt.Errorf("cycle %d (%s): %w", cycle, scenario, err)
		}
		if err := validateRestartChild(child, cycle, revision, expectedBody, abrupt, report.Compatibility); err != nil {
			return fmt.Errorf("cycle %d (%s): %w", cycle, scenario, err)
		}

		artifact, err := os.ReadFile(artifactPath)
		if err != nil {
			return fmt.Errorf("cycle %d (%s): read repaired artifact: %w", cycle, scenario, err)
		}
		restored, err := compiler.UnmarshalBytecode(artifact, expected.SourceFingerprint())
		if err != nil {
			return fmt.Errorf("cycle %d (%s): validate repaired artifact: %w", cycle, scenario, err)
		}
		if restored.CacheKey() != expected.CacheKey() || restored.Program.Checksum() != expected.Program.Checksum() {
			return fmt.Errorf("cycle %d (%s): repaired artifact identity changed", cycle, scenario)
		}
		artifactDigest := sha256.Sum256(artifact)
		artifactHash := hex.EncodeToString(artifactDigest[:])
		if baselineHash == "" {
			baselineHash = artifactHash
			baselineBytes = len(artifact)
		} else if artifactHash != baselineHash || len(artifact) != baselineBytes {
			return fmt.Errorf("cycle %d (%s): artifact differs from revision baseline", cycle, scenario)
		}

		cacheReadExpected := restartCacheReadExpected(scenario)
		cacheHitPreserved := false
		if cacheReadExpected {
			after, snapshotErr := snapshotRestartArtifact(artifactPath)
			if snapshotErr != nil {
				return fmt.Errorf("cycle %d (%s): inspect cache hit: %w", cycle, scenario, snapshotErr)
			}
			cacheHitPreserved = before.exists &&
				before.hash == after.hash &&
				before.size == after.size &&
				before.modTime.Equal(after.modTime)
			if !cacheHitPreserved {
				return fmt.Errorf("cycle %d (%s): healthy cache artifact was rewritten", cycle, scenario)
			}
		}

		repairExpected := restartRepairExpected(scenario)
		responseDigest := sha256.Sum256([]byte(child.Body))
		result := restartCycleReport{
			Cycle:                cycle,
			Revision:             revision,
			Scenario:             scenario,
			Abrupt:               abrupt,
			Requests:             child.Requests,
			Status:               child.Status,
			ResponseSHA256:       hex.EncodeToString(responseDigest[:]),
			ArtifactSHA256:       artifactHash,
			ProgramChecksum:      restored.Program.Checksum(),
			ArtifactBytes:        len(artifact),
			CacheReadExpected:    cacheReadExpected,
			CacheHitPreserved:    cacheHitPreserved,
			RepairExpected:       repairExpected,
			RepairVerified:       repairExpected && artifactHash == baselineHash,
			ArtifactHashStable:   artifactHash == baselineHash,
			DrainHealthy:         child.DrainHealthy,
			CompatibilityMatched: child.Compatibility == report.Compatibility,
			DurationMS:           time.Since(cycleStarted).Milliseconds(),
		}
		report.Results = append(report.Results, result)
		report.Requests += child.Requests
		if abrupt {
			report.AbruptStops++
		}
		t.Logf(
			"restart cycle=%d revision=%d scenario=%s duration=%s",
			cycle,
			revision,
			scenario,
			time.Since(cycleStarted).Round(time.Millisecond),
		)
	}

	artifacts, err := filepath.Glob(filepath.Join(cacheDirectory, "*.kwbc"))
	if err != nil {
		return fmt.Errorf("list campaign artifacts: %w", err)
	}
	temporaryArtifacts, err := filepath.Glob(filepath.Join(cacheDirectory, ".kitwork-bytecode-*"))
	if err != nil {
		return fmt.Errorf("list temporary campaign artifacts: %w", err)
	}
	report.Revisions = revisionCount
	report.Artifacts = len(artifacts)
	report.TemporaryArtifacts = len(temporaryArtifacts)
	if len(artifacts) != revisionCount {
		return fmt.Errorf("cache contains %d artifacts for %d revisions", len(artifacts), revisionCount)
	}
	if len(temporaryArtifacts) != 0 {
		return fmt.Errorf("cache retained %d temporary artifacts", len(temporaryArtifacts))
	}
	return nil
}

func prepareRestartScenario(
	artifactPath string,
	scenario string,
	faults *restartFaultCounts,
) (restartArtifactBefore, error) {
	before, err := snapshotRestartArtifact(artifactPath)
	if scenario == restartScenarioCold {
		if err == nil && before.exists {
			return restartArtifactBefore{}, fmt.Errorf("cold-start artifact already exists")
		}
		if err != nil && !os.IsNotExist(err) {
			return restartArtifactBefore{}, err
		}
		return restartArtifactBefore{}, nil
	}
	if err != nil {
		return restartArtifactBefore{}, fmt.Errorf("read scenario artifact: %w", err)
	}

	switch scenario {
	case restartScenarioTruncated:
		data, readErr := os.ReadFile(artifactPath)
		if readErr != nil {
			return restartArtifactBefore{}, readErr
		}
		limit := 12
		if len(data) < limit {
			limit = len(data) / 2
		}
		if limit == 0 {
			return restartArtifactBefore{}, fmt.Errorf("artifact is empty")
		}
		if writeErr := os.WriteFile(artifactPath, data[:limit], 0o644); writeErr != nil {
			return restartArtifactBefore{}, writeErr
		}
		faults.Truncated++
	case restartScenarioStaleCompiler:
		if mutateErr := mutateRestartArtifact(artifactPath, 6); mutateErr != nil {
			return restartArtifactBefore{}, mutateErr
		}
		faults.StaleCompiler++
	case restartScenarioCorruptProgram:
		offset := bytecodeArtifactHeaderSize + programChecksumOffset
		if mutateErr := mutateRestartArtifact(artifactPath, offset); mutateErr != nil {
			return restartArtifactBefore{}, mutateErr
		}
		faults.Checksum++
	case restartScenarioDeleted:
		if removeErr := os.Remove(artifactPath); removeErr != nil {
			return restartArtifactBefore{}, removeErr
		}
		faults.Deleted++
	}
	return before, nil
}

func mutateRestartArtifact(path string, offset int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if offset < 0 || offset >= len(data) {
		return fmt.Errorf("artifact is too short for fault offset %d", offset)
	}
	data[offset] ^= 0xff
	return os.WriteFile(path, data, 0o644)
}

func snapshotRestartArtifact(path string) (restartArtifactBefore, error) {
	info, err := os.Stat(path)
	if err != nil {
		return restartArtifactBefore{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return restartArtifactBefore{}, err
	}
	digest := sha256.Sum256(data)
	return restartArtifactBefore{
		exists:  true,
		hash:    hex.EncodeToString(digest[:]),
		size:    info.Size(),
		modTime: info.ModTime(),
	}, nil
}

func runRestartChildProcess(
	executable string,
	root string,
	cacheDirectory string,
	outputPath string,
	cycle int,
	revision int,
	expectedBody string,
	requests int,
	abrupt bool,
) (restartChildReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	command := exec.CommandContext(
		ctx,
		executable,
		"-test.run=^TestEngineRestartRecoveryChild$",
		"-test.count=1",
		"-test.timeout=25s",
	)
	command.Env = restartChildEnvironment(map[string]string{
		restartChildEnv:         "1",
		restartChildRootEnv:     root,
		restartChildCacheEnv:    cacheDirectory,
		restartChildOutputEnv:   outputPath,
		restartChildCycleEnv:    strconv.Itoa(cycle),
		restartChildRevisionEnv: strconv.Itoa(revision),
		restartChildExpectedEnv: expectedBody,
		restartChildRequestsEnv: strconv.Itoa(requests),
		restartChildAbruptEnv:   strconv.FormatBool(abrupt),
	})
	output, commandErr := command.CombinedOutput()
	contextErr := ctx.Err()
	cancel()
	if contextErr != nil {
		return restartChildReport{}, fmt.Errorf("child process timed out: %w", contextErr)
	}
	exitCode := 0
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	if abrupt {
		if commandErr == nil || exitCode != restartAbruptExitCode {
			return restartChildReport{}, fmt.Errorf(
				"abrupt child exit=%d error=%v output=%s",
				exitCode,
				commandErr,
				restartBoundedOutput(output),
			)
		}
	} else if commandErr != nil {
		return restartChildReport{}, fmt.Errorf(
			"child exit=%d: %w output=%s",
			exitCode,
			commandErr,
			restartBoundedOutput(output),
		)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		return restartChildReport{}, fmt.Errorf("read child evidence: %w", err)
	}
	var report restartChildReport
	if err := json.Unmarshal(data, &report); err != nil {
		return restartChildReport{}, fmt.Errorf("decode child evidence: %w", err)
	}
	return report, nil
}

func validateRestartChild(
	report restartChildReport,
	cycle int,
	revision int,
	expectedBody string,
	abrupt bool,
	compatibility DiagnosticCompatibilitySnapshot,
) error {
	if report.SchemaVersion != restartChildSchemaVersion ||
		report.Cycle != cycle ||
		report.Revision != revision ||
		report.Abrupt != abrupt {
		return fmt.Errorf("child evidence identity mismatch")
	}
	if report.Requests != restartRequestsPerCycle ||
		report.Status != http.StatusOK ||
		report.Body != expectedBody {
		return fmt.Errorf(
			"response mismatch: requests=%d status=%d body=%q",
			report.Requests,
			report.Status,
			report.Body,
		)
	}
	if report.Compatibility != compatibility {
		return fmt.Errorf("runtime compatibility tuple changed")
	}
	if err := validateRestartLiveTopology(report.BeforeClose, report.Requests); err != nil {
		return err
	}
	if abrupt {
		if report.DrainHealthy {
			return fmt.Errorf("abrupt child unexpectedly reported a graceful drain")
		}
		return nil
	}
	if !report.DrainHealthy {
		return fmt.Errorf("normal child did not drain cleanly")
	}
	return validateRestartClosedTopology(report.AfterClose)
}

func validateRestartLiveTopology(topology restartTopology, requests int) error {
	if topology.Closed || !topology.OwnershipSnapshotAvailable ||
		topology.LoadedApps != 1 || topology.LoadedSites != 1 ||
		topology.ActiveGenerations != 1 || topology.ActiveGenerationLeases != 0 ||
		topology.InflightRequests != 0 || topology.VMsActive != 0 {
		return fmt.Errorf("invalid live ownership topology: %+v", topology)
	}
	if topology.Programs == 0 ||
		topology.Executions < uint64(requests) ||
		topology.Successes < uint64(requests) ||
		topology.Failures != 0 ||
		topology.RequestsStarted != uint64(requests) ||
		topology.RequestsCompleted != uint64(requests) {
		return fmt.Errorf("invalid live execution health: %+v", topology)
	}
	return nil
}

func validateRestartClosedTopology(topology restartTopology) error {
	if !topology.Closed || !topology.OwnershipSnapshotAvailable ||
		topology.LoadedApps != 0 || topology.LoadedSites != 0 ||
		topology.ActiveGenerations != 0 || topology.ActiveGenerationLeases != 0 ||
		topology.InflightRequests != 0 || topology.VMsActive != 0 {
		return fmt.Errorf("invalid closed ownership topology: %+v", topology)
	}
	return nil
}

func TestEngineRestartRecoveryChild(t *testing.T) {
	if os.Getenv(restartChildEnv) != "1" {
		t.Skip("restart/recovery child process")
	}
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(previousLogger)

	root := restartRequiredEnv(t, restartChildRootEnv)
	cacheDirectory := restartRequiredEnv(t, restartChildCacheEnv)
	outputPath := restartRequiredEnv(t, restartChildOutputEnv)
	expectedBody := restartRequiredEnv(t, restartChildExpectedEnv)
	cycle := restartChildPositiveInt(t, restartChildCycleEnv, true)
	revision := restartChildPositiveInt(t, restartChildRevisionEnv, true)
	requests := restartChildPositiveInt(t, restartChildRequestsEnv, false)
	abrupt, err := strconv.ParseBool(restartRequiredEnv(t, restartChildAbruptEnv))
	if err != nil {
		t.Fatalf("parse abrupt flag: %v", err)
	}

	engine := New(root, 1_000_000, false, "localhost")
	engine.SetBytecodeCache(cacheDirectory)
	server := httptest.NewServer(engine)
	client := server.Client()
	client.Timeout = 5 * time.Second
	closed := false
	t.Cleanup(func() {
		if closed {
			return
		}
		client.CloseIdleConnections()
		server.Close()
		engine.Close()
	})

	report := restartChildReport{
		SchemaVersion: restartChildSchemaVersion,
		Cycle:         cycle,
		Revision:      revision,
		Abrupt:        abrupt,
	}
	for requestIndex := 0; requestIndex < requests; requestIndex++ {
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
	if abrupt {
		if err := writeRestartJSON(outputPath, report); err != nil {
			t.Fatal(err)
		}
		os.Exit(restartAbruptExitCode)
	}

	client.CloseIdleConnections()
	server.Close()
	engine.Close()
	closed = true
	closedSnapshot := engine.Diagnostics()
	report.AfterClose = restartTopologyFrom(closedSnapshot)
	report.DrainHealthy = validateRestartClosedTopology(report.AfterClose) == nil
	if err := writeRestartJSON(outputPath, report); err != nil {
		t.Fatal(err)
	}
}

func restartTopologyFrom(snapshot DiagnosticSnapshot) restartTopology {
	health := snapshot.Health
	return restartTopology{
		Closed:                     snapshot.Engine.Closed,
		OwnershipSnapshotAvailable: health.OwnershipSnapshotAvailable,
		LoadedApps:                 health.LoadedApps,
		LoadedSites:                health.LoadedSites,
		ActiveGenerations:          health.ActiveGenerations,
		ActiveGenerationLeases:     health.ActiveGenerationLeases,
		InflightRequests:           health.Requests.Inflight,
		VMsActive:                  health.VMPool.Active,
		Programs:                   health.Programs,
		Executions:                 health.Executions,
		Successes:                  health.Successes,
		Failures:                   health.Failures,
		RequestsStarted:            health.Requests.Started,
		RequestsCompleted:          health.Requests.Completed,
	}
}

func restartCompatibility() DiagnosticCompatibilitySnapshot {
	return DiagnosticCompatibilitySnapshot{
		BytecodeVersion:        kitruntime.BytecodeVersion,
		ProgramEncodingVersion: kitruntime.ProgramEncodingVersion,
		ArtifactVersion:        compiler.BytecodeArtifactVersion,
		CompilerSchemaVersion:  compiler.CompilerSchemaVersion,
		CompilerFingerprint:    compiler.Fingerprint(),
		InstructionSetChecksum: kitruntime.InstructionSetChecksum(),
		RuntimeLimits:          kitruntime.Limits(),
	}
}

func writeRestartCampaignRoute(root, source string) (string, error) {
	directory := filepath.Join(root, "restart", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	routerFile := filepath.Join(directory, work.RouterFileName)
	if err := os.WriteFile(routerFile, []byte(source), 0o644); err != nil {
		return "", err
	}
	return routerFile, nil
}

func restartCampaignSource(revision int) string {
	return fmt.Sprintf(`
import { router } from "kitwork";

const revision = "restart-%d";
const make = (base) => (number) => base + number;

router.get((ctx) => ctx.text(revision + ":" + make(40)(2)));
`, revision)
}

func restartScenario(phase int) string {
	switch phase {
	case 0:
		return restartScenarioCold
	case 1:
		return restartScenarioCacheHit
	case 2:
		return restartScenarioTruncated
	case 3:
		return restartScenarioStaleCompiler
	case 4:
		return restartScenarioCorruptProgram
	case 5:
		return restartScenarioDeleted
	case 6:
		return restartScenarioAbrupt
	default:
		return restartScenarioRecoveryAfterStop
	}
}

func restartCacheReadExpected(scenario string) bool {
	return scenario == restartScenarioCacheHit ||
		scenario == restartScenarioAbrupt ||
		scenario == restartScenarioRecoveryAfterStop
}

func restartRepairExpected(scenario string) bool {
	return scenario == restartScenarioTruncated ||
		scenario == restartScenarioStaleCompiler ||
		scenario == restartScenarioCorruptProgram ||
		scenario == restartScenarioDeleted
}

func restartChildEnvironment(overlay map[string]string) []string {
	keys := make(map[string]struct{}, len(overlay))
	for key := range overlay {
		keys[strings.ToUpper(key)] = struct{}{}
	}
	result := make([]string, 0, len(os.Environ())+len(overlay))
	for _, entry := range os.Environ() {
		key := entry
		if separator := strings.IndexByte(entry, '='); separator >= 0 {
			key = entry[:separator]
		}
		if _, replaced := keys[strings.ToUpper(key)]; !replaced {
			result = append(result, entry)
		}
	}
	for key, value := range overlay {
		result = append(result, key+"="+value)
	}
	return result
}

func restartRequiredEnv(t testing.TB, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func restartChildPositiveInt(t testing.TB, name string, allowZero bool) int {
	t.Helper()
	value, err := strconv.Atoi(restartRequiredEnv(t, name))
	if err != nil || value < 0 || (!allowZero && value == 0) {
		t.Fatalf("%s must be a valid bounded integer", name)
	}
	if value > maxRestartCampaignCycles {
		t.Fatalf("%s exceeds %d", name, maxRestartCampaignCycles)
	}
	return value
}

func restartBoundedPositiveEnv(name string, fallback, maximum int) (int, error) {
	text := strings.TrimSpace(os.Getenv(name))
	if text == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(text)
	if err != nil || value <= 0 || value > maximum {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
	}
	return value, nil
}

func writeRestartJSON(path string, report any) error {
	directory := filepath.Dir(path)
	if directory != "." && directory != "" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func restartCampaignReportPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return path
	}
	return filepath.Join(filepath.Dir(filepath.Dir(sourceFile)), path)
}

func restartBoundedOutput(output []byte) string {
	const maximum = 4 << 10
	if len(output) > maximum {
		output = output[len(output)-maximum:]
	}
	return strconv.Quote(strings.TrimSpace(string(output)))
}

func restartSanitizeFailure(message string, sensitive ...string) string {
	for _, value := range sensitive {
		if value == "" {
			continue
		}
		message = strings.ReplaceAll(message, value, "<campaign-workspace>")
		message = strings.ReplaceAll(message, filepath.ToSlash(value), "<campaign-workspace>")
	}
	return message
}
