package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/kitdb"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/work"
)

func TestReleasePlanModes(t *testing.T) {
	verify, err := releasePlan("verify")
	if err != nil {
		t.Fatal(err)
	}
	release, err := releasePlan("release")
	if err != nil {
		t.Fatal(err)
	}
	kitDBVerify, err := releasePlan("kitdb-verify")
	if err != nil {
		t.Fatal(err)
	}
	kitDBRelease, err := releasePlan("kitdb-release")
	if err != nil {
		t.Fatal(err)
	}
	if len(verify) < 11 || len(release) <= len(verify) ||
		len(kitDBVerify) < 8 || len(kitDBRelease) <= len(kitDBVerify) {
		t.Fatalf("plan sizes: verify=%d release=%d", len(verify), len(release))
	}

	required := map[string]bool{
		"KitDB 1.x compatibility contract":  false,
		"VM v2 compatibility archive":       false,
		"VM fault gauntlet":                 false,
		"Language/inspector contracts":      false,
		"KitDB database journey":            false,
		"KitDB standalone commerce journey": false,
		"KitDB commerce hard-crash matrix":  false,
		"KitDB durability/recovery":         false,
		"KitDB projection recovery":         false,
		"Focused race":                      false,
		"Compiler-to-VM fuzz":               false,
		"VM determinism fuzz":               false,
		"VM pool soak":                      false,
		"VM value-pressure campaign":        false,
		"Memory retention campaign":         false,
		"Restart/recovery campaign":         false,
		"Concurrent cache campaign":         false,
		"KitDB kernel race":                 false,
		"KitDB search race":                 false,
		"KitDB relational race":             false,
		"KitDB replica hard-crash matrix":   false,
		"KitDB catalog hard-crash matrix":   false,
		"KitDB import hard-crash matrix":    false,
		"KitDB index hard-crash matrix":     false,
		"KitDB analytics hard-crash matrix": false,
		"KitDB canary smoke":                false,
		"KitDB replica hard-crash soak":     false,
	}
	for _, step := range release {
		if _, ok := required[step.Name]; ok {
			required[step.Name] = true
		}
		if step.Name == "VM pool soak" && step.Env["KITWORK_SOAK"] != "1" {
			t.Fatal("VM pool soak omitted KITWORK_SOAK=1")
		}
		if step.Name == "VM value-pressure campaign" {
			if step.Env["KITWORK_VALUE_PRESSURE"] != "1" {
				t.Fatal("value-pressure campaign omitted KITWORK_VALUE_PRESSURE=1")
			}
			if step.Env["KITWORK_VALUE_PRESSURE_REPORT"] != ".artifacts/value-pressure.json" {
				t.Fatal("value-pressure campaign omitted its bounded JSON evidence path")
			}
		}
		if step.Name == "KitDB database journey" {
			if step.Env["KITDB_RELEASE_REPORT"] != ".artifacts/kitdb-database-gate.json" {
				t.Fatal("KitDB database journey omitted its bounded JSON evidence path")
			}
			if !containsArgument(step.Command, "^(TestKitDBDatabaseReleaseGate|TestKitDBPostgresCopyInWithLibPQIsAtomic)$") {
				t.Fatal("KitDB database journey omitted the composed frontend-to-restore oracle")
			}
		}
		if step.Name == "KitDB standalone commerce journey" {
			assertCommerceGate(t, step)
		}
		if step.Name == "KitDB commerce hard-crash matrix" &&
			(!containsArgument(step.Command, "^TestKitDBCommerceNativeJourney$") ||
				step.Env["CGO_ENABLED"] != "0" || step.Env["KITDB_COMMERCE_REPORT"] != "") {
			t.Fatal("commerce campaign omitted native coverage or overwrites single-run evidence")
		}
		if step.Name == "KitDB durability/recovery" {
			if !containsArgument(step.Command, "./kitdb") || !containsArgument(step.Command, "./work") {
				t.Fatal("KitDB recovery gate omitted kernel or relational crash evidence")
			}
			if !containsArgument(step.Command, "^(TestRecoveryTruncatesEveryIncompleteTail|TestCheckpointRecoveryTruncatesIncompleteWALTail|TestBackupAnchorCapturesWALOverlayAndRemainsStandalone|TestRestoreToTransactionFromInsideHistorySegment|TestKitDBAdditiveIndexBuildSurvivesCrashAndInterleavedWrites|TestKitDBPhysicalIndexGenerationReplacesAndCleansAfterHardCrash|TestKitDBPostgresResumableImportHardCrashMatrix)$") {
				t.Fatal("KitDB recovery gate omitted a reviewed crash/backup boundary")
			}
		}
		if step.Name == "KitDB projection recovery" &&
			(!containsArgument(step.Command, "./kitdb/relational") ||
				!containsArgument(step.Command, "^(TestColumnarIncrementalProcessExitDuringBuild|TestProjectionSnapshotFreshnessCorruptionAndCancellation|TestAnalyticsRefreshUpgradesBlockDirectoryWithoutRewritingKCOL)$")) {
			t.Fatal("KitDB projection recovery gate omitted publication, corruption, or metadata-upgrade evidence")
		}
		if step.Name == "KitDB search race" &&
			(!containsArgument(step.Command, "-race") || !containsArgument(step.Command, "./search")) {
			t.Fatal("KitDB search race omitted race detection or the standalone search engine")
		}
		if step.Name == "KitDB 1.x compatibility contract" &&
			!containsArgument(step.Command, "^(TestKitDBV1CompatibilityProfile|TestKitDBV1FrozenMainFixture|TestKitDBV1RelationalCompatibilityProfile|TestOperatorVersionReportsKitDBV1Contract)$") {
			t.Fatal("KitDB compatibility gate omitted the frozen 1.0 main-file fixture")
		}
		if step.Name == "Focused race" &&
			(!containsArgument(step.Command, "./conformance") ||
				!containsArgument(step.Command, "./compatibility")) {
			t.Fatal("focused race gate omitted language or VM compatibility evidence")
		}
		if step.Name == "Memory retention campaign" && step.Env["KITWORK_RETENTION"] != "1" {
			t.Fatal("retention campaign omitted KITWORK_RETENTION=1")
		}
		if step.Name == "Restart/recovery campaign" {
			if step.Env["KITWORK_RESTART_CAMPAIGN"] != "1" {
				t.Fatal("restart campaign omitted KITWORK_RESTART_CAMPAIGN=1")
			}
			if step.Env["KITWORK_RESTART_REPORT"] != ".artifacts/restart-campaign.json" {
				t.Fatal("restart campaign omitted its bounded JSON evidence path")
			}
		}
		if step.Name == "Concurrent cache campaign" {
			if step.Env["KITWORK_CONTENTION_CAMPAIGN"] != "1" {
				t.Fatal("contention campaign omitted KITWORK_CONTENTION_CAMPAIGN=1")
			}
			if step.Env["KITWORK_CONTENTION_REPORT"] != ".artifacts/cache-contention-campaign.json" {
				t.Fatal("contention campaign omitted its bounded JSON evidence path")
			}
		}
		if step.Name == "KitDB replica hard-crash soak" {
			if step.Env["KITDB_REPLICA_SOAK_ITERATIONS"] != "128" ||
				step.Env["KITDB_REPLICA_SOAK_SEED"] != "20260828" {
				t.Fatal("KitDB replica soak omitted its bounded reproducible campaign")
			}
		}
		if step.Name == "KitDB canary smoke" &&
			!containsArgument(step.Command, "--json=.artifacts/kitdb-canary-smoke.json") {
			t.Fatal("KitDB canary smoke omitted its JSON evidence")
		}
		if strings.Contains(step.Name, "hard-crash matrix") &&
			!containsArgument(step.Command, "-count=10") {
			t.Fatalf("%s is not repeated ten times", step.Name)
		}
		if step.Name == "VM/compiler contracts" &&
			!containsArgument(step.Command, "TestVMV2Contract|TestCompilerV3") {
			t.Fatal("contract gate omitted cold-process compiler determinism")
		}
		if step.Name == "Language/inspector contracts" &&
			!containsArgument(step.Command, "TestInspectFile|TestInspectProgram|TestLanguageConformanceCorpus") {
			t.Fatal("contract gate omitted language corpus or bytecode inspector")
		}
		if step.Name == "VM v2 compatibility archive" &&
			!containsArgument(step.Command, "^TestVMV2CompatibilityArchive") {
			t.Fatal("contract gate omitted the frozen VM v2 Program archive")
		}
		if step.Name == "VM fault gauntlet" &&
			!containsArgument(step.Command, "^TestVMFaultGauntlet") {
			t.Fatal("contract gate omitted deterministic VM fault and recovery coverage")
		}
		if step.Name == "Full tests" && !containsArgument(step.Command, "-timeout=20m") {
			t.Fatal("full test gate omitted its explicit package timeout")
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("release plan omitted %s", name)
		}
	}
	for _, expectation := range []struct {
		plan []gateStep
		name string
	}{
		{kitDBVerify, "KitDB projection recovery"},
		{kitDBVerify, "KitDB search suite"},
		{kitDBVerify, "KitDB standalone commerce journey"},
		{kitDBRelease, "KitDB standalone commerce journey"},
		{kitDBRelease, "KitDB commerce hard-crash matrix"},
		{kitDBRelease, "KitDB projection recovery"},
		{kitDBRelease, "KitDB search race"},
		{kitDBRelease, "KitDB analytics hard-crash matrix"},
	} {
		if !containsStep(expectation.plan, expectation.name) {
			t.Fatalf("KitDB plan omitted %s", expectation.name)
		}
	}
	searchSuite, ok := findStep(kitDBVerify, "KitDB search suite")
	if !ok || !containsArgument(searchSuite.Command, "./search") {
		t.Fatal("KitDB search suite omitted the standalone search engine")
	}
	staticAnalysis, ok := findStep(kitDBVerify, "KitDB static analysis")
	if !ok || !containsArgument(staticAnalysis.Command, "./search") {
		t.Fatal("KitDB static analysis omitted the standalone search engine")
	}
	for _, name := range []string{"KitDB operator suite", "KitDB command build", "KitDB static analysis"} {
		step, ok := findStep(kitDBVerify, name)
		if !ok || !containsArgument(step.Command, "./cmd/kitdbdist") {
			t.Fatalf("%s omitted distribution tooling", name)
		}
	}
	if _, err := releasePlan("unknown"); err == nil {
		t.Fatal("unknown release mode was accepted")
	}
}

func assertCommerceGate(t *testing.T, step gateStep) {
	t.Helper()
	if !containsArgument(step.Command, "./cmd/kitdbdist") ||
		!containsArgument(step.Command, "^TestKitDBCommerceNativeJourney$") ||
		!containsArgument(step.Command, "-count=1") || !containsArgument(step.Command, "-timeout=5m") ||
		step.Env["KITDB_COMMERCE_REPORT"] != ".artifacts/kitdb-commerce-gate.json" || step.Env["CGO_ENABLED"] != "0" {
		t.Fatalf("incomplete standalone commerce gate: %+v", step)
	}
}

func TestKitDBCommerceGateCannotBeOmitted(t *testing.T) {
	for _, mode := range []string{"verify", "release", "kitdb-verify", "kitdb-release"} {
		plan, err := releasePlan(mode)
		if err != nil {
			t.Fatal(err)
		}
		step, found := findStep(plan, "KitDB standalone commerce journey")
		if !found {
			t.Fatalf("%s omitted commerce journey", mode)
		}
		assertCommerceGate(t, step)
	}
}

func TestCompatibilityReportMatchesRuntime(t *testing.T) {
	report := currentCompatibilityReport()
	if report.BytecodeVersion != 2 ||
		report.ProgramEncodingVersion != 1 ||
		report.ArtifactVersion != compiler.BytecodeArtifactVersion ||
		report.CompilerSchemaVersion != compiler.CompilerSchemaVersion ||
		report.InstructionSetChecksum == "" ||
		report.CompilerFingerprint != compiler.Fingerprint() ||
		report.RuntimeLimits != kitruntime.Limits() ||
		report.KitDBKernel != kitdb.CurrentCompatibility() ||
		report.KitDBRelational != work.CurrentKitDBRelationalCompatibility() {
		t.Fatalf("compatibility report = %+v", report)
	}
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func containsStep(steps []gateStep, expected string) bool {
	_, ok := findStep(steps, expected)
	return ok
}

func findStep(steps []gateStep, expected string) (gateStep, bool) {
	for _, step := range steps {
		if step.Name == expected {
			return step, true
		}
	}
	return gateStep{}, false
}

func TestDryRunStepShape(t *testing.T) {
	step := gateStep{
		Name:    "Contract",
		Command: []string{"go", "test", "./runtime"},
		Env:     map[string]string{"KITWORK_SOAK": "1"},
	}
	result := stepResult{
		Name:        step.Name,
		Command:     append([]string(nil), step.Command...),
		Environment: cloneEnvironment(step.Env),
		PlannedOnly: true,
	}
	if !result.PlannedOnly || result.Name != step.Name || result.Environment["KITWORK_SOAK"] != "1" {
		t.Fatalf("dry-run result = %+v", result)
	}
}

func TestFindEngineRootFromCommandDirectory(t *testing.T) {
	root, err := findEngineRoot("")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("engine go.mod is empty")
	}
	if commit, _, _, err := gitState(root); err != nil || commit == "" {
		t.Fatalf("git state commit = %q, err = %v", commit, err)
	}
}

func TestWriteReleaseReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports", "release.json")
	report := releaseReport{
		SchemaVersion: releaseReportSchemaVersion,
		Mode:          "verify",
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		Compatibility: currentCompatibilityReport(),
		Success:       true,
	}
	if err := writeReleaseReport(path, report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatal("release report is empty or missing its final newline")
	}
	var restored releaseReport
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Compatibility != report.Compatibility {
		t.Fatalf(
			"restored compatibility = %+v, want %+v",
			restored.Compatibility,
			report.Compatibility,
		)
	}
}

func TestEnvironmentWithReplacesOverlay(t *testing.T) {
	base := []string{"UNCHANGED=yes", "KITWORK_SOAK=old"}
	got := environmentWith(base, map[string]string{"KITWORK_SOAK": "1"})
	if len(got) != 2 || got[0] != "UNCHANGED=yes" || got[1] != "KITWORK_SOAK=1" {
		t.Fatalf("environment = %#v", got)
	}
}
