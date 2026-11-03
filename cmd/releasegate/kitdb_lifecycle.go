package main

import (
	"runtime"
	"strings"
)

var kitDBDiagnosticOptIns = [...]string{
	"KITDB_SECONDARY_TAIL",
	"KITDB_LIFECYCLE_CAMPAIGN",
	"KITDB_MIXED_CAMPAIGN",
	"KITDB_THREAD_ATTRIBUTION",
	"KITDB_WAL_FORENSIC",
	"KITDB_WAL_LOCK_CAPTURE",
	"KITDB_WAL_CHECKPOINT_FORENSIC",
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
		names = append(names, "TestLifecycleWindowsWALReplaceDenial", "TestWALCheckpointForensicCapture")
		names = append(names, "TestWindowsWALPublicationPhases", "TestWindowsWALPublicationResolutionPhase", "TestWindowsWALPublicationFallbackRefusalPhase")
	}
	command := []string{"go", "test", "-json", "./kitdb/relational"}
	if goos == "windows" { command = append(command, "./kitdb") }
	command = append(command, "-run", "^("+strings.Join(names, "|")+")$", "-count=1", "-timeout=5m")
	step := gateStep{
		Name:    "KitDB lifecycle/WAL contract",
		Command: command,
	}
	if race {
		step.Name += " race"
		step.Command = append(step.Command, "-race")
	}
	for _, name := range names {
		pkg := "github.com/kitwork/engine/kitdb/relational"
		if strings.HasPrefix(name, "TestWindowsWALPublication") { pkg = "github.com/kitwork/engine/kitdb" }
		step.RequiredTests = append(step.RequiredTests, testRequirement{Package: pkg, Test: name})
	}
	if goos == "windows" {
		for _, retry := range []string{"false", "true"} {
			step.RequiredTests = append(step.RequiredTests, testRequirement{Package: "github.com/kitwork/engine/kitdb/relational", Test: "TestLifecycleWindowsWALReplaceDenial/explicit_checkpoint_retry_" + retry})
		}
		for _, phase := range []string{"open-source", "replace-target"} {
			step.RequiredTests = append(step.RequiredTests, testRequirement{Package: "github.com/kitwork/engine/kitdb", Test: "TestWindowsWALPublicationPhases/"+phase})
		}
	}
	return step
}
