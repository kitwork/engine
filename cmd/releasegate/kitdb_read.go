package main

import "strings"

func kitDBReadStep(race, profile bool) gateStep {
	names := []string{
		"TestPreparedPrimarySelectEquivalence",
		"TestPreparedPrimarySelectCommitFence",
		"TestPreparedPrimarySelectSchemaRebind",
		"TestPreparedPrimarySelectCancelledPreparation",
		"TestPreparedPrimarySelectAdmission",
		"TestPreparedPrimarySelectConcurrentCheckpoint",
		"TestPreparedPrimarySelectFallbackCommit",
		"TestPreparedPrimarySelectLayoutHandoff",
		"TestPreparedPrimarySelectReadErrors",
		"TestPreparedPrimarySelectCancelledHandoff",
		"TestPreparedPrimarySelectCaptureFailure",
		"TestPreparedPrimarySelectStorageReadFailure",
		"TestPreparedPrimarySelectAllocationEnvelope",
		"TestSecondaryScanOwnedBuffers",
		"TestSecondaryReadPolicies",
		"TestSecondaryReadConcurrentCheckpoint",
		"TestSecondaryReadRangeDependency",
		"TestSQLPointReaderTransactionScope",
		"TestSQLPointReaderWarmIndex",
	}
	step := gateStep{Name: "KitDB prepared-read contract"}
	if profile {
		step.Name += " profile"
		names = append(names, "TestPreparedPrimarySelectSingleReservation")
	}
	step.Command = []string{"go", "test", "-json", "./kitdb/relational", "-run", "^(" + strings.Join(names, "|") + ")$", "-count=1", "-timeout=5m"}
	if race {
		step.Name += " race"
		step.Command = append(step.Command, "-race")
	}
	if profile {
		step.Command = append(step.Command, "-tags=kitdb_profile")
	}
	const pkg = "github.com/kitwork/engine/kitdb/relational"
	for _, name := range names {
		step.RequiredTests = append(step.RequiredTests, testRequirement{Package: pkg, Test: name})
	}
	for _, root := range []string{"FallbackCommit", "LayoutHandoff", "ReadErrors", "CancelledHandoff"} {
		points := []string{"generation", "row"}
		if root == "ReadErrors" || root == "CancelledHandoff" {
			points = append(points, "fence")
		}
		for _, point := range points {
			step.RequiredTests = append(step.RequiredTests, testRequirement{Package: pkg, Test: "TestPreparedPrimarySelect" + root + "/" + point})
		}
	}
	for _, name := range []string{"primary", "fallback"} {
		step.RequiredTests = append(step.RequiredTests, testRequirement{Package: pkg, Test: "TestPreparedPrimarySelectAllocationEnvelope/" + name})
	}
	for _, name := range []string{"warm", "disabled"} {
		step.RequiredTests = append(step.RequiredTests, testRequirement{Package: pkg, Test: "TestSecondaryReadPolicies/" + name})
	}
	if profile {
		for _, name := range []string{"id", "bucket"} {
			step.RequiredTests = append(step.RequiredTests, testRequirement{Package: pkg, Test: "TestPreparedPrimarySelectSingleReservation/" + name})
		}
	}
	return step
}
