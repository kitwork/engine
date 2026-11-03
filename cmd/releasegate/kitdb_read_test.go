package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestPreparedReadGatesCannotBeOmitted(t *testing.T) {
	for _, mode := range []string{"verify", "release", "kitdb-verify", "kitdb-release", "kitdb-read"} {
		plan, err := releasePlan(mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, config := range []struct{ race, profile bool }{{}, {race: true}, {profile: true}} {
			expected := kitDBReadStep(config.race, config.profile)
			step, found := findStep(plan, expected.Name)
			required := !config.race || strings.HasSuffix(mode, "release") || mode == "kitdb-read"
			if found != required {
				t.Fatalf("mode=%s step=%s found=%t", mode, expected.Name, found)
			}
			if !required {
				continue
			}
			if !hasArgument(step.Command, "-json") || !hasArgument(step.Command, "-count=1") || !hasArgument(step.Command, "-timeout=5m") || hasArgument(step.Command, "-race") != config.race || hasArgument(step.Command, "-tags=kitdb_profile") != config.profile {
				t.Fatal("incomplete read gate", step)
			}
			if len(step.RequiredTests) != len(expected.RequiredTests) {
				t.Fatal("lost required read evidence", step.Name)
			}
			for i, requirement := range expected.RequiredTests {
				if requirement != step.RequiredTests[i] {
					t.Fatal("changed required read evidence", step.Name, requirement)
				}
			}
			for _, name := range kitDBDiagnosticOptIns {
				if step.Env[name] != "0" {
					t.Fatal("inherited opt-in can enter read gate", name)
				}
			}
		}
	}
	plan, _ := releasePlan("kitdb-read")
	if len(plan) != 3 {
		t.Fatal("read mode must contain exactly normal, race and profile contracts")
	}
}

func TestPreparedReadGateSelection(t *testing.T) {
	for _, profile := range []bool{false, true} {
		step := kitDBReadStep(false, profile)
		var pattern string
		for i, argument := range step.Command {
			if argument == "-run" {
				pattern = step.Command[i+1]
			}
		}
		matcher, err := regexp.Compile(pattern)
		if err != nil || pattern == "" {
			t.Fatal(pattern, err)
		}
		seen := make(map[string]bool)
		for _, requirement := range step.RequiredTests {
			root := strings.SplitN(requirement.Test, "/", 2)[0]
			if seen[requirement.Test] || requirement.Package != "github.com/kitwork/engine/kitdb/relational" || !matcher.MatchString(root) || matcher.MatchString(root+"Other") {
				t.Fatal("invalid read requirement", requirement)
			}
			seen[requirement.Test] = true
		}
		want := 33
		if profile {
			want = 36
		}
		if len(seen) != want {
			t.Fatal("read evidence cardinality", len(seen), want)
		}
		for _, name := range []string{
			"TestPreparedPrimarySelectFallbackCommit/row",
			"TestPreparedPrimarySelectLayoutHandoff/generation",
			"TestPreparedPrimarySelectReadErrors/fence",
			"TestPreparedPrimarySelectCancelledHandoff/fence",
			"TestPreparedPrimarySelectCaptureFailure",
			"TestPreparedPrimarySelectStorageReadFailure",
			"TestPreparedPrimarySelectAllocationEnvelope/primary",
			"TestPreparedPrimarySelectAllocationEnvelope/fallback",
			"TestSecondaryScanOwnedBuffers",
			"TestSecondaryReadPolicies/warm",
			"TestSecondaryReadPolicies/disabled",
			"TestSecondaryReadConcurrentCheckpoint",
			"TestSecondaryReadRangeDependency",
			"TestSQLPointReaderTransactionScope",
			"TestSQLPointReaderWarmIndex",
		} {
			if !seen[name] {
				t.Fatal("critical requirement omitted", name)
			}
		}
		if profile && (!seen["TestPreparedPrimarySelectSingleReservation/id"] || !seen["TestPreparedPrimarySelectSingleReservation/bucket"]) {
			t.Fatal("single-reservation proof omitted")
		}
		for _, name := range []string{"TestCheckpointAttributionCampaign", "TestKitDBMixedCampaign", "TestKitDBLifecycleCampaign"} {
			if matcher.MatchString(name) {
				t.Fatal("read gate selected a benchmark/diagnostic", name)
			}
		}
	}
}

func TestPreparedReadRequiredEvidence(t *testing.T) {
	for _, profile := range []bool{false, true} {
		step := kitDBReadStep(false, profile)
		for _, target := range step.RequiredTests {
			for _, mode := range []string{"pass", "missing", "skip", "fail"} {
				observer := newTestObserver(step.RequiredTests)
				encoder := json.NewEncoder(observer)
				for _, requirement := range step.RequiredTests {
					action := "pass"
					if requirement == target {
						if mode == "missing" {
							continue
						}
						if mode != "pass" {
							action = mode
						}
					}
					if err := encoder.Encode(map[string]string{"Action": action, "Package": requirement.Package, "Test": requirement.Test}); err != nil {
						t.Fatal(err)
					}
				}
				if err := observer.finish(); (err == nil) != (mode == "pass") {
					t.Fatalf("target=%s mode=%s error=%v", target.Test, mode, err)
				}
			}
		}
	}
}
