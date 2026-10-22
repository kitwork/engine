package main

import (
	"runtime"
	"strings"
)

var kitDBDiagnosticOptIns = [...]string{
	"KITDB_LIFECYCLE_CAMPAIGN",
	"KITDB_MIXED_CAMPAIGN",
	"KITDB_THREAD_ATTRIBUTION",
	"KITDB_WAL_FORENSIC",
	"KITDB_WAL_LOCK_CAPTURE",
}

// Diagnostic campaigns run directly, never through a qualification plan. Keep
// this explicit in planned/executed evidence, including broad package suites.
func withoutKitDBDiagnostics(environment map[string]string) map[string]string {
	result := cloneEnvironment(environment)
	if result == nil {
		result = make(map[string]string, len(kitDBDiagnosticOptIns))
	}
	for _, name := range kitDBDiagnosticOptIns {
		result[name] = "0"
	}
	return result
}

func kitDBLifecycleStep(race bool) gateStep {
	return kitDBLifecycleStepForOS(race, runtime.GOOS)
}

func kitDBLifecycleStepForOS(race bool, goos string) gateStep {
	names := []string{
		"TestLifecycleManagerDrain",
		"TestLifecycleOwnerSmoke",
		"TestLifecycleFailureCleanupEvidence",
		"TestLifecycleRetainedFailureFixture",
		"TestLifecycleACKModel",
		"TestLifecycleForensicCopy",
		"TestMixedCampaignSmoke",
		"TestMixedVisibleOracleRejectsCorruption",
	}
	if goos == "windows" {
		names = append(names, "TestLifecycleWindowsWALReplaceDenial")
	}
	step := gateStep{
		Name:    "KitDB lifecycle/WAL contract",
		Command: []string{"go", "test", "-json", "./kitdb/relational", "-run", "^(" + strings.Join(names, "|") + ")$", "-count=1", "-timeout=5m"},
	}
	if race {
		step.Name += " race"
		step.Command = append(step.Command, "-race")
	}
	for _, name := range names {
		step.RequiredTests = append(step.RequiredTests, testRequirement{Package: "github.com/kitwork/engine/kitdb/relational", Test: name})
	}
	if goos == "windows" {
		for _, retry := range []string{"false", "true"} {
			step.RequiredTests = append(step.RequiredTests, testRequirement{Package: "github.com/kitwork/engine/kitdb/relational", Test: "TestLifecycleWindowsWALReplaceDenial/explicit_checkpoint_retry_" + retry})
		}
	}
	return step
}
