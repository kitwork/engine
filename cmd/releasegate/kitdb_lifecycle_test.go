package main

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestLifecycleGatesCannotBeOmitted(t *testing.T) {
	for _, mode := range []string{"verify", "release", "kitdb-verify", "kitdb-release", "kitdb-lifecycle"} {
		plan, err := releasePlan(mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, race := range []bool{false, true} {
			expected := kitDBLifecycleStep(race)
			step, found := findStep(plan, expected.Name)
			required := !race || strings.HasSuffix(mode, "release") || mode == "kitdb-lifecycle"
			if found != required {
				t.Fatalf("mode=%s race=%t found=%t", mode, race, found)
			}
			if !required {
				continue
			}
			if len(step.RequiredTests) != len(expected.RequiredTests) || !hasArgument(step.Command, "-json") || !hasArgument(step.Command, "-count=1") || !hasArgument(step.Command, "-timeout=5m") || hasArgument(step.Command, "-race") != race {
				t.Fatalf("incomplete lifecycle gate: %+v", step)
			}
			for i, requirement := range expected.RequiredTests {
				if requirement != step.RequiredTests[i] {
					t.Fatal("changed lifecycle test requirement", step.Name)
				}
			}
		}
	}
	plan, _ := releasePlan("kitdb-lifecycle")
	if len(plan) != 2 {
		t.Fatal("focused lifecycle mode must contain only normal/race contracts")
	}
}

func TestLifecycleGatePlatformRequirements(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		step := kitDBLifecycleStepForOS(false, goos)
		pattern := ""
		for i, value := range step.Command {
			if value == "-run" && i+1 < len(step.Command) {
				pattern = step.Command[i+1]
			}
		}
		matcher, err := regexp.Compile(pattern)
		if err != nil || pattern == "" {
			t.Fatal("missing anchored test selection", err)
		}
		want := 8
		if goos == "windows" {
			want = 11 // Nine roots plus both real-denial recovery subcases.
		}
		if len(step.RequiredTests) != want {
			t.Fatalf("%s requirements=%d want=%d", goos, len(step.RequiredTests), want)
		}
		seen := make(map[testRequirement]bool)
		for _, test := range step.RequiredTests {
			root := strings.SplitN(test.Test, "/", 2)[0]
			if seen[test] || test.Package != "github.com/kitwork/engine/kitdb/relational" || !matcher.MatchString(root) || matcher.MatchString(root+"Other") {
				t.Fatal("duplicate, unselected or unanchored test", test)
			}
			seen[test] = true
		}
		for _, diagnostic := range []string{"TestKitDBLifecycleCampaign", "TestKitDBMixedCampaign", "TestKitDBThreadAttribution", "TestKitDBWALForensicCampaign", "TestLifecycleWindowsLockCapture"} {
			if matcher.MatchString(diagnostic) {
				t.Fatal("gate selected an opt-in diagnostic", diagnostic)
			}
		}
	}
}

func TestLifecycleRequiredEvidenceRejectsMissingAndSkipped(t *testing.T) {
	step := kitDBLifecycleStepForOS(false, "windows")
	for _, mode := range []string{"pass", "missing", "skip"} {
		observer := newTestObserver(step.RequiredTests)
		encoder := json.NewEncoder(observer)
		for i, requirement := range step.RequiredTests {
			action := "pass"
			if i == len(step.RequiredTests)-1 {
				if mode == "missing" {
					continue
				}
				if mode == "skip" {
					action = "skip"
				}
			}
			if err := encoder.Encode(map[string]string{"Action": action, "Package": requirement.Package, "Test": requirement.Test}); err != nil {
				t.Fatal(err)
			}
		}
		if err := observer.finish(); (err == nil) != (mode == "pass") {
			t.Fatalf("mode=%s error=%v", mode, err)
		}
	}
}

func TestGatePlansDisableManualKitDBDiagnostics(t *testing.T) {
	for _, mode := range []string{"verify", "release", "kitdb-verify", "kitdb-release", "kitdb-auth", "kitdb-lifecycle"} {
		plan, err := releasePlan(mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range plan {
			for _, name := range kitDBDiagnosticOptIns {
				if step.Env[name] != "0" {
					t.Fatal("inherited diagnostic can escape gate budget", mode, step.Name, name)
				}
			}
		}
	}
	original := map[string]string{"KITDB_WAL_FORENSIC": "1", "KITDB_PROCESS_CRASH": "1"}
	safe := withoutKitDBDiagnostics(original)
	if safe["KITDB_WAL_FORENSIC"] != "0" || safe["KITDB_PROCESS_CRASH"] != "1" || original["KITDB_WAL_FORENSIC"] != "1" {
		t.Fatal("diagnostic isolation mutated source or another gate's opt-in")
	}
}

func TestLifecycleGateEnvironmentChild(t *testing.T) {
	if os.Getenv("KITDB_GATE_ENV_CHILD") != "1" {
		return
	}
	for _, name := range kitDBDiagnosticOptIns {
		if os.Getenv(name) != "0" {
			os.Exit(7)
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]string{"Action": "pass", "Package": "lab", "Test": "TestDiagnosticsDisabled"})
	os.Exit(0)
}

func TestLifecycleGateBlocksInheritedDiagnostics(t *testing.T) {
	for _, name := range kitDBDiagnosticOptIns {
		t.Setenv(name, "1")
		if runtime.GOOS == "windows" {
			t.Setenv(strings.ToLower(name), "1")
		}
	}
	plan, err := releasePlan("kitdb-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	step := plan[0]
	step.Command = []string{executable, "-test.run=^TestLifecycleGateEnvironmentChild$", "--", "-json"}
	step.Env["KITDB_GATE_ENV_CHILD"] = "1"
	step.RequiredTests = []testRequirement{{Package: "lab", Test: "TestDiagnosticsDisabled"}}
	result := runGateStep(context.Background(), t.TempDir(), step)
	if !result.Success {
		t.Fatalf("diagnostic opt-ins leaked to child: %+v", result)
	}
	for _, name := range kitDBDiagnosticOptIns {
		if result.Environment[name] != "0" {
			t.Fatal("reported environment differs from isolation policy", name)
		}
	}
}
