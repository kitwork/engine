// releasegate runs the repeatable Kitwork engine verification plan.
//
//	go run ./cmd/releasegate --mode verify
//	go run ./cmd/releasegate --mode release --require-clean --report release.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/kitdb"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/work"
)

const releaseReportSchemaVersion = 2

type gateStep struct {
	Name    string
	Command []string
	Env     map[string]string
}

type stepResult struct {
	Name        string            `json:"name"`
	Command     []string          `json:"command"`
	Environment map[string]string `json:"environment,omitempty"`
	PlannedOnly bool              `json:"planned_only,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	DurationMS  int64             `json:"duration_ms"`
	Success     bool              `json:"success"`
	Error       string            `json:"error,omitempty"`
}

type compatibilityReport struct {
	BytecodeVersion        uint16                            `json:"bytecode_version"`
	ProgramEncodingVersion uint16                            `json:"program_encoding_version"`
	ArtifactVersion        uint16                            `json:"artifact_version"`
	CompilerSchemaVersion  uint16                            `json:"compiler_schema_version"`
	InstructionSetChecksum string                            `json:"instruction_set_checksum"`
	CompilerFingerprint    string                            `json:"compiler_fingerprint"`
	RuntimeLimits          kitruntime.LimitsSnapshot         `json:"runtime_limits"`
	KitDBKernel            kitdb.CompatibilityProfile        `json:"kitdb_kernel"`
	KitDBRelational        work.KitDBRelationalCompatibility `json:"kitdb_relational"`
}

type releaseReport struct {
	SchemaVersion   uint16              `json:"schema_version"`
	Mode            string              `json:"mode"`
	DryRun          bool                `json:"dry_run"`
	StartedAt       time.Time           `json:"started_at"`
	FinishedAt      time.Time           `json:"finished_at"`
	DurationMS      int64               `json:"duration_ms"`
	GoVersion       string              `json:"go_version"`
	OS              string              `json:"os"`
	Arch            string              `json:"arch"`
	Compatibility   compatibilityReport `json:"compatibility"`
	Commit          string              `json:"commit,omitempty"`
	Dirty           bool                `json:"dirty"`
	DirtyEntries    int                 `json:"dirty_entries"`
	RepositoryError string              `json:"repository_error,omitempty"`
	Success         bool                `json:"success"`
	Failure         string              `json:"failure,omitempty"`
	Steps           []stepResult        `json:"steps"`
}

func main() {
	mode := flag.String("mode", "verify", "verification mode: verify or release")
	reportPath := flag.String("report", "", "optional JSON report path")
	requireClean := flag.Bool("require-clean", false, "fail before running when git has changes")
	dryRun := flag.Bool("dry-run", false, "print the plan without executing it")
	timeout := flag.Duration("timeout", 45*time.Minute, "overall gate timeout")
	flag.Parse()
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "releasegate: timeout must be positive")
		os.Exit(2)
	}

	root, err := findEngineRoot("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasegate:", err)
		os.Exit(2)
	}
	normalizedMode := strings.ToLower(strings.TrimSpace(*mode))
	steps, err := releasePlan(normalizedMode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasegate:", err)
		os.Exit(2)
	}

	startedAt := time.Now().UTC()
	commit, dirty, dirtyEntries, repositoryErr := gitState(root)
	report := releaseReport{
		SchemaVersion: releaseReportSchemaVersion,
		Mode:          normalizedMode,
		DryRun:        *dryRun,
		StartedAt:     startedAt,
		GoVersion:     goruntime.Version(),
		OS:            goruntime.GOOS,
		Arch:          goruntime.GOARCH,
		Compatibility: currentCompatibilityReport(),
		Commit:        commit,
		Dirty:         dirty,
		DirtyEntries:  dirtyEntries,
	}
	if repositoryErr != nil {
		report.RepositoryError = repositoryErr.Error()
	}

	if *requireClean && (dirty || repositoryErr != nil) {
		if repositoryErr != nil {
			report.Failure = "cannot verify clean working tree: " + repositoryErr.Error()
		} else {
			report.Failure = fmt.Sprintf("working tree has %d dirty entries", dirtyEntries)
		}
		finishReleaseReport(&report, startedAt)
		_ = writeReleaseReport(*reportPath, report)
		fmt.Fprintln(os.Stderr, "releasegate:", report.Failure)
		os.Exit(1)
	}

	if *dryRun {
		for index, step := range steps {
			fmt.Printf("%2d. %-28s %s\n", index+1, step.Name, formatGateCommand(step))
			report.Steps = append(report.Steps, stepResult{
				Name:        step.Name,
				Command:     append([]string(nil), step.Command...),
				Environment: cloneEnvironment(step.Env),
				PlannedOnly: true,
			})
		}
		report.Success = true
		finishReleaseReport(&report, startedAt)
		if err := writeReleaseReport(*reportPath, report); err != nil {
			fmt.Fprintln(os.Stderr, "releasegate:", err)
			os.Exit(1)
		}
		return
	}

	timeoutContext, cancelTimeout := context.WithTimeout(context.Background(), *timeout)
	defer cancelTimeout()
	ctx, stopSignal := signal.NotifyContext(timeoutContext, os.Interrupt)
	defer stopSignal()

	for index, step := range steps {
		fmt.Printf("\n[%d/%d] %s\n", index+1, len(steps), step.Name)
		result := runGateStep(ctx, root, step)
		report.Steps = append(report.Steps, result)
		if !result.Success {
			report.Failure = result.Name + ": " + result.Error
			break
		}
	}
	report.Success = report.Failure == "" && len(report.Steps) == len(steps)
	if ctx.Err() != nil && report.Failure == "" {
		report.Failure = ctx.Err().Error()
		report.Success = false
	}
	finishReleaseReport(&report, startedAt)

	if err := writeReleaseReport(*reportPath, report); err != nil {
		fmt.Fprintln(os.Stderr, "releasegate:", err)
		report.Failure = err.Error()
		report.Success = false
	}
	if !report.Success {
		fmt.Fprintln(os.Stderr, "releasegate: failed:", report.Failure)
		os.Exit(1)
	}
	fmt.Printf("\nreleasegate: %s passed in %s\n", report.Mode, time.Duration(report.DurationMS)*time.Millisecond)
}

func releasePlan(mode string) ([]gateStep, error) {
	verify := []gateStep{
		kitDBCompatibilityStep(),
		{
			Name:    "VM v2 compatibility archive",
			Command: []string{"go", "test", "./compatibility", "-run", "^TestVMV2CompatibilityArchive", "-count=1"},
		},
		{
			Name:    "VM fault gauntlet",
			Command: []string{"go", "test", "./runtime", "-run", "^TestVMFaultGauntlet", "-count=1"},
		},
		{
			Name:    "Language/inspector contracts",
			Command: []string{"go", "test", ".", "./runtime", "./conformance", "-run", "TestInspectFile|TestInspectProgram|TestLanguageConformanceCorpus", "-count=1"},
		},
		{
			Name:    "VM/compiler contracts",
			Command: []string{"go", "test", "./runtime", "./compiler", "-run", "TestVMV2Contract|TestCompilerV3", "-count=1"},
		},
		{
			Name:    "Diagnostics/lifecycle",
			Command: []string{"go", "test", "./core", "-run", "TestEngineDiagnostics|TestEngineDiagnosticBundle|TestEngineLifecycleGauntlet", "-count=1"},
		},
		{
			Name:    "KitDB database journey",
			Command: []string{"go", "test", "./work", "-run", "^(TestKitDBDatabaseReleaseGate|TestKitDBPostgresCopyInWithLibPQIsAtomic)$", "-count=1", "-timeout=5m", "-v"},
			Env:     map[string]string{"KITDB_RELEASE_REPORT": ".artifacts/kitdb-database-gate.json"},
		},
		{
			Name: "KitDB durability/recovery",
			Command: []string{
				"go", "test", "./kitdb", "./work",
				"-run", "^(TestRecoveryTruncatesEveryIncompleteTail|TestCheckpointRecoveryTruncatesIncompleteWALTail|TestBackupAnchorCapturesWALOverlayAndRemainsStandalone|TestRestoreToTransactionFromInsideHistorySegment|TestKitDBAdditiveIndexBuildSurvivesCrashAndInterleavedWrites|TestKitDBPhysicalIndexGenerationReplacesAndCleansAfterHardCrash|TestKitDBPostgresResumableImportHardCrashMatrix)$",
				"-count=1", "-timeout=10m",
			},
		},
		kitDBProjectionRecoveryStep(),
		{Name: "Build", Command: []string{"go", "build", "./..."}},
		{Name: "Full tests", Command: []string{"go", "test", "-count=1", "-timeout=20m", "./..."}},
		{Name: "Vet", Command: []string{"go", "vet", "./..."}},
	}

	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "verify":
		return verify, nil
	case "kitdb-verify":
		return kitDBVerifyPlan(), nil
	case "kitdb-release":
		plan := kitDBVerifyPlan()
		return append(plan, kitDBReleaseCampaigns()...), nil
	case "release":
		plan := append(verify,
			gateStep{
				Name: "Focused race",
				Command: []string{
					"go", "test", "-race", "./runtime", "./compiler", "./conformance", "./compatibility", "./core", "./site", "./app", "./request", "./work", "./utilities/sse",
					"-run", "Test", "-count=1",
				},
			},
			gateStep{
				Name:    "Compiler-to-VM fuzz",
				Command: []string{"go", "test", "./compiler", "-run=^$", "-fuzz=FuzzCompileVerifyExecute", "-fuzztime=10s"},
			},
			gateStep{
				Name:    "VM determinism fuzz",
				Command: []string{"go", "test", "./runtime", "-run=^$", "-fuzz=FuzzVMDeterminism", "-fuzztime=10s"},
			},
			gateStep{
				Name:    "VM pool soak",
				Command: []string{"go", "test", "./runtime", "-run", "TestPooledVM(SoakAcrossPrograms|ReleasesOversizedVerifiedWorkload)", "-count=1", "-timeout=10m"},
				Env:     map[string]string{"KITWORK_SOAK": "1"},
			},
			gateStep{
				Name:    "VM value-pressure campaign",
				Command: []string{"go", "test", "./runtime", "-run", "^TestVMValuePressureCampaign$", "-count=1", "-timeout=10m", "-v"},
				Env: map[string]string{
					"KITWORK_VALUE_PRESSURE":        "1",
					"KITWORK_VALUE_PRESSURE_REPORT": ".artifacts/value-pressure.json",
				},
			},
			gateStep{
				Name:    "Memory retention campaign",
				Command: []string{"go", "test", "./core", "-run", "TestEngineMemoryRetentionCampaign", "-count=1", "-timeout=10m", "-v"},
				Env:     map[string]string{"KITWORK_RETENTION": "1"},
			},
			gateStep{
				Name:    "Restart/recovery campaign",
				Command: []string{"go", "test", "./core", "-run", "^TestEngineRestartRecoveryCampaign$", "-count=1", "-timeout=10m", "-v"},
				Env: map[string]string{
					"KITWORK_RESTART_CAMPAIGN": "1",
					"KITWORK_RESTART_REPORT":   ".artifacts/restart-campaign.json",
				},
			},
			gateStep{
				Name:    "Concurrent cache campaign",
				Command: []string{"go", "test", "./core", "-run", "^TestEngineCacheContentionCampaign$", "-count=1", "-timeout=10m", "-v"},
				Env: map[string]string{
					"KITWORK_CONTENTION_CAMPAIGN": "1",
					"KITWORK_CONTENTION_REPORT":   ".artifacts/cache-contention-campaign.json",
				},
			},
		)
		return append(plan, kitDBReleaseCampaigns()...), nil
	default:
		return nil, fmt.Errorf(
			"unknown mode %q; use verify, release, kitdb-verify, or kitdb-release",
			mode,
		)
	}
}

func kitDBCompatibilityStep() gateStep {
	return gateStep{
		Name: "KitDB 1.x compatibility contract",
		Command: []string{
			"go", "test", "./kitdb", "./work", "./cmd/kitdb",
			"-run", "^(TestKitDBV1CompatibilityProfile|TestKitDBV1FrozenMainFixture|TestKitDBV1RelationalCompatibilityProfile|TestOperatorVersionReportsKitDBV1Contract)$",
			"-count=1",
		},
	}
}

func kitDBProjectionRecoveryStep() gateStep {
	return gateStep{
		Name: "KitDB projection recovery",
		Command: []string{
			"go", "test", "./kitdb/relational",
			"-run", "^(TestColumnarIncrementalProcessExitDuringBuild|TestProjectionSnapshotFreshnessCorruptionAndCancellation|TestAnalyticsRefreshUpgradesBlockDirectoryWithoutRewritingKCOL)$",
			"-count=1", "-timeout=10m",
		},
	}
}

func kitDBVerifyPlan() []gateStep {
	return []gateStep{
		kitDBCompatibilityStep(),
		{
			Name:    "KitDB kernel/node suite",
			Command: []string{"go", "test", "./kitdb/...", "-count=1", "-timeout=20m"},
		},
		{
			Name:    "KitDB search suite",
			Command: []string{"go", "test", "./search", "-count=1", "-timeout=20m"},
		},
		{
			Name:    "KitDB relational suite",
			Command: []string{"go", "test", "./work", "-count=1", "-timeout=20m"},
		},
		{
			Name:    "KitDB operator suite",
			Command: []string{"go", "test", "./cmd/kitdb", "./cmd/kitdbcanary", "./cmd/kitdbimport", "./cmd/kitdbpg", "./cmd/kitdbdist", "-count=1"},
		},
		{
			Name:    "KitDB command build",
			Command: []string{"go", "build", "./cmd/kitdb", "./cmd/kitdbcanary", "./cmd/kitdbimport", "./cmd/kitdbpg", "./cmd/kitdbdist"},
		},
		{
			Name:    "KitDB static analysis",
			Command: []string{"go", "vet", "./kitdb/...", "./search", "./work", "./cmd/kitdb", "./cmd/kitdbcanary", "./cmd/kitdbimport", "./cmd/kitdbpg", "./cmd/kitdbdist"},
		},
		{
			Name: "KitDB durability/recovery",
			Command: []string{
				"go", "test", "./kitdb", "./work",
				"-run", "^(TestRecoveryTruncatesEveryIncompleteTail|TestCheckpointRecoveryTruncatesIncompleteWALTail|TestBackupAnchorCapturesWALOverlayAndRemainsStandalone|TestRestoreToTransactionFromInsideHistorySegment|TestKitDBAdditiveIndexBuildSurvivesCrashAndInterleavedWrites|TestKitDBPhysicalIndexGenerationReplacesAndCleansAfterHardCrash|TestKitDBPostgresResumableImportHardCrashMatrix)$",
				"-count=1", "-timeout=10m",
			},
		},
		kitDBProjectionRecoveryStep(),
		{
			Name:    "KitDB database journey",
			Command: []string{"go", "test", "./work", "-run", "^(TestKitDBDatabaseReleaseGate|TestKitDBPostgresCopyInWithLibPQIsAtomic)$", "-count=1", "-timeout=5m", "-v"},
			Env:     map[string]string{"KITDB_RELEASE_REPORT": ".artifacts/kitdb-database-gate.json"},
		},
	}
}

func kitDBReleaseCampaigns() []gateStep {
	return []gateStep{
		{
			Name:    "KitDB kernel race",
			Command: []string{"go", "test", "-race", "./kitdb/...", "-count=1", "-timeout=20m"},
		},
		{
			Name:    "KitDB search race",
			Command: []string{"go", "test", "-race", "./search", "-count=1", "-timeout=20m"},
		},
		{
			Name:    "KitDB relational race",
			Command: []string{"go", "test", "-race", "./work", "-run", "^TestKitDB", "-count=1", "-timeout=20m"},
		},
		{
			Name:    "KitDB replica hard-crash matrix",
			Command: []string{"go", "test", "./kitdb/node", "-run", "^TestReplicaFileLinkHardCrashMatrix$", "-count=10", "-timeout=20m"},
		},
		{
			Name:    "KitDB catalog hard-crash matrix",
			Command: []string{"go", "test", "./work", "-run", "^TestKitDBNodeCatalog(HardCrashMatrix|EmptyCreateHardCrashMatrix|RenameHardCrashMatrix)$", "-count=10", "-timeout=20m"},
		},
		{
			Name:    "KitDB import hard-crash matrix",
			Command: []string{"go", "test", "./work", "-run", "^TestKitDBPostgresResumableImportHardCrashMatrix$", "-count=10", "-timeout=20m"},
		},
		{
			Name:    "KitDB index hard-crash matrix",
			Command: []string{"go", "test", "./work", "-run", "^TestKitDB(AdditiveIndexBuildSurvivesCrashAndInterleavedWrites|PhysicalIndexGenerationReplacesAndCleansAfterHardCrash)$", "-count=10", "-timeout=20m"},
		},
		{
			Name:    "KitDB analytics hard-crash matrix",
			Command: []string{"go", "test", "./kitdb/relational", "-run", "^TestColumnarIncrementalProcessExitDuringBuild$", "-count=10", "-timeout=20m"},
		},
		{
			Name: "KitDB canary smoke",
			Command: []string{
				"go", "run", "./cmd/kitdbcanary",
				"--duration=5s", "--tenants=16", "--workers=8", "--max-open=4",
				"--checkpoint-every=64", "--verify-every=256",
				"--json=.artifacts/kitdb-canary-smoke.json", "--quiet",
			},
		},
		{
			Name:    "KitDB replica hard-crash soak",
			Command: []string{"go", "test", "./kitdb/node", "-run", "^TestReplicaFileLinkHardCrashSoak$", "-count=1", "-timeout=30m", "-v"},
			Env: map[string]string{
				"KITDB_REPLICA_SOAK_ITERATIONS": "128",
				"KITDB_REPLICA_SOAK_SEED":       "20260828",
			},
		},
	}
}

func runGateStep(ctx context.Context, root string, step gateStep) stepResult {
	startedAt := time.Now().UTC()
	result := stepResult{
		Name:        step.Name,
		Command:     append([]string(nil), step.Command...),
		Environment: cloneEnvironment(step.Env),
		StartedAt:   startedAt,
	}
	if len(step.Command) == 0 {
		result.Error = "empty command"
		return result
	}

	command := exec.CommandContext(ctx, step.Command[0], step.Command[1:]...)
	command.Dir = root
	command.Env = environmentWith(os.Environ(), step.Env)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	err := command.Run()
	result.DurationMS = time.Since(startedAt).Milliseconds()
	result.Success = err == nil
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func environmentWith(base []string, overlay map[string]string) []string {
	result := make([]string, 0, len(base)+len(overlay))
	for _, entry := range base {
		separator := strings.IndexByte(entry, '=')
		key := entry
		if separator >= 0 {
			key = entry[:separator]
		}
		if environmentOverlayContains(overlay, key) {
			continue
		}
		result = append(result, entry)
	}
	keys := make([]string, 0, len(overlay))
	for key := range overlay {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+overlay[key])
	}
	return result
}

func environmentOverlayContains(overlay map[string]string, candidate string) bool {
	for key := range overlay {
		if key == candidate || (goruntime.GOOS == "windows" && strings.EqualFold(key, candidate)) {
			return true
		}
	}
	return false
}

func currentCompatibilityReport() compatibilityReport {
	return compatibilityReport{
		BytecodeVersion:        kitruntime.BytecodeVersion,
		ProgramEncodingVersion: kitruntime.ProgramEncodingVersion,
		ArtifactVersion:        compiler.BytecodeArtifactVersion,
		CompilerSchemaVersion:  compiler.CompilerSchemaVersion,
		InstructionSetChecksum: kitruntime.InstructionSetChecksum(),
		CompilerFingerprint:    compiler.Fingerprint(),
		RuntimeLimits:          kitruntime.Limits(),
		KitDBKernel:            kitdb.CurrentCompatibility(),
		KitDBRelational:        work.CurrentKitDBRelationalCompatibility(),
	}
}

func cloneEnvironment(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func formatGateCommand(step gateStep) string {
	parts := make([]string, 0, len(step.Env)+len(step.Command))
	keys := make([]string, 0, len(step.Env))
	for key := range step.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+step.Env[key])
	}
	parts = append(parts, step.Command...)
	return strings.Join(parts, " ")
}

func findEngineRoot(start string) (string, error) {
	if start == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", err
		}
		start = current
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		moduleFile := filepath.Join(current, "go.mod")
		if data, readErr := os.ReadFile(moduleFile); readErr == nil &&
			strings.Contains(string(data), "module github.com/kitwork/engine") {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("cannot find github.com/kitwork/engine module root")
		}
		current = parent
	}
}

func gitState(root string) (commit string, dirty bool, entries int, err error) {
	commitCommand := exec.Command("git", "rev-parse", "HEAD")
	commitCommand.Dir = root
	output, commandErr := commitCommand.Output()
	if commandErr != nil {
		return "", false, 0, fmt.Errorf("read git commit: %w", commandErr)
	}
	commit = strings.TrimSpace(string(output))
	statusCommand := exec.Command("git", "status", "--porcelain", "--untracked-files=normal")
	statusCommand.Dir = root
	output, commandErr = statusCommand.Output()
	if commandErr != nil {
		return commit, false, 0, fmt.Errorf("read git status: %w", commandErr)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) != "" {
			entries++
		}
	}
	return commit, entries > 0, entries, nil
}

func finishReleaseReport(report *releaseReport, startedAt time.Time) {
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = report.FinishedAt.Sub(startedAt).Milliseconds()
}

func writeReleaseReport(path string, report releaseReport) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	data = append(data, '\n')
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create report directory %s: %w", directory, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write report %s: %w", path, err)
	}
	return nil
}
