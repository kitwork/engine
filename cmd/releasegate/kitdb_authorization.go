package main

import "strings"

type testRequirement struct {
	Package string `json:"package"`
	Test    string `json:"test"`
}

func kitDBAuthorizationStep(race bool) gateStep {
	groups := []struct {
		pkg   string
		tests []string
	}{
		{"managed", []string{"TestSessionAccountIncarnation", "TestRootLiveAccountCommands", "TestRootAlterDropAccountsAndAdminProtection"}},
		{"relational", []string{"TestAuthorizationLifecycleWriteCommitAndKill", "TestAuthorizationLifecycleRoleRecreation", "TestAuthorizationLifecycleUserRecreation", "TestAuthorizationLifecycleAdministratorRecreation", "TestLocalRolesPortableDatabaseRequiresMapping", "TestLocalRoleHardExitRecovery"}},
		{"kitsql", []string{"TestDatabaseLocalRolesAcrossAdapters", "TestLocalRoleWritesAcrossAdapters", "TestLocalRoleInsertSelectAcrossAdapters", "TestLocalRoleUpsertAcrossAdapters"}},
	}
	step := gateStep{Name: "KitDB authorization contract", Command: []string{"go", "test", "-json"}}
	if race {
		step.Name += " race"
		step.Command = append(step.Command, "-race")
	}
	var names []string
	for _, group := range groups {
		step.Command = append(step.Command, "./kitdb/"+group.pkg)
		for _, name := range group.tests {
			names = append(names, name)
			step.RequiredTests = append(step.RequiredTests, testRequirement{Package: "github.com/kitwork/engine/kitdb/" + group.pkg, Test: name})
		}
	}
	step.Command = append(step.Command, "-run", "^("+strings.Join(names, "|")+")$", "-count=1", "-timeout=15m")
	return step
}

func kitDBWriteCampaignStep(race bool) gateStep {
	step := gateStep{
		Name:          "KitDB restricted write campaign",
		Command:       []string{"go", "test", "-json", "./kitdb/relational", "-run", "^TestRestrictedWriteCampaignSeeded$", "-count=1", "-timeout=5m"},
		RequiredTests: []testRequirement{{Package: "github.com/kitwork/engine/kitdb/relational", Test: "TestRestrictedWriteCampaignSeeded"}},
	}
	if race {
		step.Name += " race"
		step.Command = append(step.Command, "-race")
	}
	return step
}

func kitDBProcessCrashStep(race bool) gateStep {
	step := gateStep{
		Name:        "KitDB standalone authorization crash",
		Command:     []string{"go", "test", "-json", "./cmd/kitdbpg", "-run", "^TestStandaloneRestrictedWriteCrashRecovery$", "-count=1", "-timeout=8m"},
		Env:         map[string]string{"KITDB_PROCESS_CRASH": "1", "KITDB_PROCESS_CRASH_RACE": "0"},
		CrashReport: ".artifacts/kitdb-authorization-crash.json",
	}
	if race {
		step.Name += " race"
		step.Command = append(step.Command, "-race")
		step.Env["KITDB_PROCESS_CRASH_RACE"] = "1"
		step.CrashReport = ".artifacts/kitdb-authorization-crash-race.json"
	}
	step.Env["KITDB_PROCESS_CRASH_REPORT"] = step.CrashReport
	for _, test := range append([]string{"TestStandaloneRestrictedWriteCrashRecovery"}, crashCaseTests()...) {
		step.RequiredTests = append(step.RequiredTests, testRequirement{Package: "github.com/kitwork/engine/cmd/kitdbpg", Test: test})
	}
	return step
}

func crashCaseTests() []string {
	var tests []string
	for _, name := range []string{"commit_ack", "staged_batch", "grant_ack", "revoke_ack", "commit_not_forwarded", "commit_reply_withheld"} {
		tests = append(tests, "TestStandaloneRestrictedWriteCrashRecovery/"+name)
	}
	return tests
}

func kitDBSQLAuthorizationCanaryStep(race bool) gateStep {
	step := gateStep{
		Name:    "KitDB SQL authorization canary",
		Command: []string{"go", "test", "-json", "./kitdb/kitsql", "-run", "^(TestSQLCanaryAuthorization|TestSQLAuthCanaryOracles|TestExecutionErrorClassification)$", "-count=1", "-timeout=8m"},
		Env:     map[string]string{"KITDB_SQL_CANARY_AUTH": "1", "KITDB_SQL_AUTH_PHASE_SECONDS": "6", "KITDB_SQL_AUTH_ROUNDS": "1", "KITDB_SQL_AUTH_REPORT": ""},
		RequiredTests: []testRequirement{
			{Package: "github.com/kitwork/engine/kitdb/kitsql", Test: "TestSQLCanaryAuthorization"},
			{Package: "github.com/kitwork/engine/kitdb/kitsql", Test: "TestSQLAuthCanaryOracles"},
			{Package: "github.com/kitwork/engine/kitdb/kitsql", Test: "TestExecutionErrorClassification"},
		},
	}
	if race {
		step.Name += " race"
		step.Command = append(step.Command, "-race")
	}
	return step
}
