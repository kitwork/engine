package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAuthorizationGatesCannotBeOmitted(t *testing.T) {
	for _, mode := range []string{"verify", "release", "kitdb-verify", "kitdb-release", "kitdb-auth"} {
		plan, err := releasePlan(mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []gateStep{kitDBAuthorizationStep(false), kitDBWriteCampaignStep(false), kitDBProcessCrashStep(false), kitDBSQLAuthorizationCanaryStep(false)} {
			step, ok := findStep(plan, expected.Name)
			if !ok || len(step.RequiredTests) != len(expected.RequiredTests) || !hasArgument(step.Command, "-json") || !hasArgument(step.Command, "-count=1") {
				t.Fatalf("%s omitted required test evidence: %s", mode, expected.Name)
			}
			for i, required := range expected.RequiredTests {
				if step.RequiredTests[i] != required {
					t.Fatal("required test changed", step.Name, required)
				}
			}
		}
		if strings.HasSuffix(mode, "release") || mode == "kitdb-auth" {
			for _, expected := range []gateStep{kitDBAuthorizationStep(true), kitDBWriteCampaignStep(true), kitDBProcessCrashStep(true), kitDBSQLAuthorizationCanaryStep(true)} {
				step, ok := findStep(plan, expected.Name)
				if !ok || !hasArgument(step.Command, "-race") {
					t.Fatal("missing race step", mode, expected.Name)
				}
			}
		}
	}
	for _, race := range []bool{false, true} {
		canary := kitDBSQLAuthorizationCanaryStep(race)
		if canary.Env["KITDB_SQL_CANARY_AUTH"] != "1" || canary.Env["KITDB_SQL_AUTH_PHASE_SECONDS"] != "6" || canary.Env["KITDB_SQL_AUTH_ROUNDS"] != "1" {
			t.Fatal("canary skipped or inherited unbounded budget")
		}
		step := kitDBProcessCrashStep(race)
		if step.Env["KITDB_PROCESS_CRASH"] != "1" || (step.Env["KITDB_PROCESS_CRASH_RACE"] == "1") != race || step.CrashReport == "" || step.Env["KITDB_PROCESS_CRASH_REPORT"] != step.CrashReport || len(step.RequiredTests) != 7 {
			t.Fatalf("crash opt-in/evidence incomplete: %+v", step)
		}
	}
}

func TestRequiredTestEvidence(t *testing.T) {
	for _, tc := range []struct{ name, events, status string }{
		{"pass", `{"Action":"pass","Package":"lab","Test":"TestRequired"}`, "passed"},
		{"skip", `{"Action":"skip","Package":"lab","Test":"TestRequired"}`, "skipped"},
		{"child_skip", "{\"Action\":\"skip\",\"Package\":\"lab\",\"Test\":\"TestRequired/pg\"}\n{\"Action\":\"pass\",\"Package\":\"lab\",\"Test\":\"TestRequired\"}", "skipped"},
		{"failure", `{"Action":"fail","Package":"lab","Test":"TestRequired"}`, "failed"},
		{"wrong_package", `{"Action":"pass","Package":"other","Test":"TestRequired"}`, "not_run"},
		{"missing", "", "not_run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newTestObserver([]testRequirement{{Package: "lab", Test: "TestRequired"}})
			// Exercise arbitrary pipe fragmentation, including split JSON tokens.
			for _, b := range []byte(tc.events) {
				o.Write([]byte{b})
			}
			err := o.finish()
			if o.tests[0].Status != tc.status || (err == nil) != (tc.status == "passed") {
				t.Fatalf("evidence=%+v error=%v", o.tests, err)
			}
		})
	}
	for _, invalid := range []string{"not JSON", strings.Repeat("x", (1<<20)+1)} {
		o := newTestObserver(nil)
		o.Write([]byte(invalid))
		if o.finish() == nil {
			t.Fatal("accepted malformed or oversized evidence")
		}
	}
}

func TestReleaseGateEvidenceChild(t *testing.T) {
	mode := os.Getenv("RELEASE_GATE_EVIDENCE_CHILD")
	if mode == "" {
		return
	}
	if mode != "missing" {
		action := "pass"
		if mode == "skip" {
			action = "skip"
		}
		json.NewEncoder(os.Stdout).Encode(map[string]string{"Action": action, "Package": "lab", "Test": "TestRequired"})
	}
	if mode == "fail" {
		os.Exit(3)
	}
	os.Exit(0)
}

func evidenceChildStep(t *testing.T, mode string) gateStep {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return gateStep{Name: mode, Command: []string{executable, "-test.run=^TestReleaseGateEvidenceChild$", "--", "-json"}, Env: map[string]string{"RELEASE_GATE_EVIDENCE_CHILD": mode}, RequiredTests: []testRequirement{{Package: "lab", Test: "TestRequired"}}}
}

func TestGateRejectsSkippedMissingAndFailedCommand(t *testing.T) {
	for _, mode := range []string{"pass", "skip", "missing", "fail"} {
		result := runGateStep(context.Background(), t.TempDir(), evidenceChildStep(t, mode))
		if result.Success != (mode == "pass") {
			t.Fatalf("%s: %+v", mode, result)
		}
	}
	results, failure := runGatePlan(context.Background(), t.TempDir(), []gateStep{evidenceChildStep(t, "skip"), evidenceChildStep(t, "pass")})
	if failure == "" || len(results) != 2 || results[0].Status != "skipped" || results[1].Status != "not_run" || results[1].Success {
		t.Fatalf("results=%+v failure=%s", results, failure)
	}
}

func TestPlannedAndCancelledStepsAreNotSuccess(t *testing.T) {
	step := kitDBProcessCrashStep(false)
	planned := unrunStep(step, "planned", "")
	planned.PlannedOnly = true
	if planned.Success || len(planned.Tests) != 7 || planned.Tests[0].Status != "not_run" {
		t.Fatalf("planned step claimed execution: %+v", planned)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := runGatePlan(ctx, t.TempDir(), []gateStep{step})
	if err == "" || len(results) != 1 || results[0].Status != "not_run" || results[0].Success {
		t.Fatalf("cancelled gate: %+v %s", results, err)
	}
}

func TestCrashEvidenceValidationAndFreshness(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "crash.json")
	report := crashEvidence{SchemaVersion: 1, OS: runtime.GOOS, Arch: runtime.GOARCH, BinarySHA256: strings.Repeat("a", 64), Success: true}
	for _, name := range crashCaseTests() {
		report.Cases = append(report.Cases, crashCaseEvidence{Name: strings.TrimPrefix(name, "TestStandaloneRestrictedWriteCrashRecovery/"), Success: true})
	}
	write := func() {
		data, _ := json.Marshal(report)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := readCrashEvidence(path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := readCrashEvidence(path, true); err == nil {
		t.Fatal("accepted normal binary as race evidence")
	}
	step := evidenceChildStep(t, "pass")
	step.CrashReport = "crash.json"
	result := runGateStep(context.Background(), root, step)
	if result.Success {
		t.Fatal("stale successful artifact accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale report not cleared", err)
	}
	report.Cases = report.Cases[:5]
	write()
	if _, err := readCrashEvidence(path, false); err == nil {
		t.Fatal("accepted missing crash scenario")
	}
	for _, name := range []string{".", "../outside.json", filepath.Join(root, "absolute.json")} {
		if _, err := confinedReportPath(root, name); err == nil {
			t.Fatal("unconfined report path accepted", name)
		}
	}
}
