package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

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
	step := evidenceChildStep(t, "pass")
	planned := unrunStep(step, "planned", "")
	planned.PlannedOnly = true
	if planned.Success || len(planned.Tests) != 1 || planned.Tests[0].Status != "not_run" {
		t.Fatalf("planned step claimed execution: %+v", planned)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := runGatePlan(ctx, t.TempDir(), []gateStep{step})
	if err == "" || len(results) != 1 || results[0].Status != "not_run" || results[0].Success {
		t.Fatalf("cancelled gate: %+v %s", results, err)
	}
}
