package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	kitruntime "github.com/kitwork/engine/runtime"
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
	if len(verify) != 8 || len(release) <= len(verify) {
		t.Fatalf("plan sizes: verify=%d release=%d", len(verify), len(release))
	}

	required := map[string]bool{
		"VM v2 compatibility archive":  false,
		"VM fault gauntlet":            false,
		"Language/inspector contracts": false,
		"Focused race":                 false,
		"Compiler-to-VM fuzz":          false,
		"VM determinism fuzz":          false,
		"VM pool soak":                 false,
		"VM value-pressure campaign":   false,
		"Memory retention campaign":    false,
		"Restart/recovery campaign":    false,
		"Concurrent cache campaign":    false,
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
		if step.Name == "VM/compiler contracts" &&
			!containsArgument(step.Command, "TestVMV2Contract|TestCompilerV2") {
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
	}
	for name, found := range required {
		if !found {
			t.Fatalf("release plan omitted %s", name)
		}
	}
	if _, err := releasePlan("unknown"); err == nil {
		t.Fatal("unknown release mode was accepted")
	}
}

func TestCompatibilityReportMatchesRuntime(t *testing.T) {
	report := currentCompatibilityReport()
	if report.BytecodeVersion != 2 ||
		report.ProgramEncodingVersion != 1 ||
		report.ArtifactVersion != 1 ||
		report.CompilerSchemaVersion != 2 ||
		report.InstructionSetChecksum == "" ||
		report.CompilerFingerprint == "" ||
		report.RuntimeLimits != kitruntime.Limits() {
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
