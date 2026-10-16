package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

var nativeRequiredSubtests = []string{"mixed-application", "commerce-objects", "authorization-portability"}

var nativeAuthorizationChecks = []string{
	"offline-root-bootstrap", "sql-users-roles-mappings", "wrong-password-28P01",
	"read-write-schema-permissions-42501", "restart-retains-authorization",
	"independent-verified-restore", "root-login-does-not-transfer",
	"portable-role-without-mapping-denied", "explicit-remapping-and-writable-restore",
	"processes-reaped-and-offline-ownership-reacquired",
}

type nativeEvidence struct {
	Format              string   `json:"format"`
	Version             string   `json:"version"`
	Commit              string   `json:"commit"`
	Platform            string   `json:"platform"`
	Manifest            artifact `json:"manifest"`
	PassedSubtests      []string `json:"passed_subtests"`
	AuthorizationChecks []string `json:"authorization_checks"`
	Success             bool     `json:"success"`
	StableQualified     bool     `json:"stable_release_qualified"`
}

func (e *nativeEvidence) validate() error {
	if !reflect.DeepEqual(e.PassedSubtests, nativeRequiredSubtests) {
		return fmt.Errorf("mandatory native subtests incomplete: %v", e.PassedSubtests)
	}
	if !reflect.DeepEqual(e.AuthorizationChecks, nativeAuthorizationChecks) {
		return fmt.Errorf("mandatory authorization checks incomplete: %v", e.AuthorizationChecks)
	}
	return nil
}

func beginNativeEvidence(t *testing.T, report manifest, manifestPath string) *nativeEvidence {
	t.Helper()
	digest, err := digestFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	e := &nativeEvidence{Format: "kitdb-native-journey/v1", Version: report.Version,
		Commit: report.Commit, Platform: runtime.GOOS + "/" + runtime.GOARCH, Manifest: digest}
	t.Cleanup(func() {
		if !t.Failed() {
			if err := e.validate(); err != nil {
				t.Error(err)
			}
		}
		e.Success = !t.Failed() && !t.Skipped()
		data, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		t.Log(string(data))
		if path := os.Getenv("KITDB_NATIVE_REPORT"); path != "" {
			if !filepath.IsAbs(path) {
				path = filepath.Join("../..", path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Error(err)
				return
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				t.Error(err)
				return
			}
			_, writeErr := file.Write(append(data, '\n'))
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				t.Errorf("write native evidence: %v; close: %v", writeErr, closeErr)
			}
		}
	})
	return e
}

func runNativeSubtest(t *testing.T, e *nativeEvidence, name string, exercise func(*testing.T)) {
	t.Helper()
	completed := false
	passed := t.Run(name, func(child *testing.T) {
		exercise(child)
		completed = !child.Failed() && !child.Skipped()
	})
	if !passed || !completed {
		t.Fatalf("mandatory native subtest %s did not pass (filtered and skipped tests do not qualify)", name)
	}
	e.PassedSubtests = append(e.PassedSubtests, name)
}

func TestNativeEvidenceRequiresEveryMandatoryCheck(t *testing.T) {
	valid := func() nativeEvidence {
		return nativeEvidence{PassedSubtests: append([]string(nil), nativeRequiredSubtests...),
			AuthorizationChecks: append([]string(nil), nativeAuthorizationChecks...)}
	}
	e := valid()
	if err := e.validate(); err != nil {
		t.Fatal(err)
	}
	for i := range nativeRequiredSubtests {
		e := valid()
		e.PassedSubtests = append(e.PassedSubtests[:i], e.PassedSubtests[i+1:]...)
		if e.validate() == nil {
			t.Fatalf("accepted missing subtest %s", nativeRequiredSubtests[i])
		}
	}
	for i := range nativeAuthorizationChecks {
		e := valid()
		e.AuthorizationChecks = append(e.AuthorizationChecks[:i], e.AuthorizationChecks[i+1:]...)
		if e.validate() == nil {
			t.Fatalf("accepted missing authorization check %s", nativeAuthorizationChecks[i])
		}
	}
}

func TestNativeQualificationFailureProcess(t *testing.T) {
	switch os.Getenv("KITDB_NATIVE_FAILURE_PROBE") {
	case "missing-directory":
		t.Setenv("KITDB_DIST_DIRECTORY", "")
		t.Setenv("KITDB_NATIVE_REPORT", filepath.Join(t.TempDir(), "report.json"))
		TestKitDBDistributionNativeJourney(t)
	case "skipped-subtest":
		runNativeSubtest(t, &nativeEvidence{}, "required", func(child *testing.T) { child.Skip("fixture skip") })
	case "filtered-subtest":
		runNativeSubtest(t, &nativeEvidence{}, "required", func(child *testing.T) { child.Fatal("filtered callback ran") })
	case "transport-denial":
		requireAuthorizationState(t, errors.New("connection lost; SQLSTATE 42501"), "42501")
	case "nil-denial":
		requireAuthorizationState(t, nil, "42501")
	case "existing-report":
		path := os.Getenv("KITDB_NATIVE_COLLISION_PATH")
		t.Setenv("KITDB_NATIVE_REPORT", path)
		e := beginNativeEvidence(t, manifest{}, path)
		e.PassedSubtests = append([]string(nil), nativeRequiredSubtests...)
		e.AuthorizationChecks = append([]string(nil), nativeAuthorizationChecks...)
	}
}

func TestNativeQualificationRejectsIncompleteRuns(t *testing.T) {
	for _, probe := range []struct{ mode, message string }{
		{"missing-directory", "qualification cannot skip"},
		{"skipped-subtest", "mandatory native subtest required did not pass"},
		{"filtered-subtest", "mandatory native subtest required did not pass"},
		{"transport-denial", "expected SQLSTATE 42501"},
		{"nil-denial", "expected SQLSTATE 42501"},
		{"existing-report", "FAIL"},
	} {
		t.Run(probe.mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "existing-report.json")
			const original = "retained earlier evidence\n"
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			pattern := "^TestNativeQualificationFailureProcess$"
			if probe.mode == "filtered-subtest" {
				pattern += "/^not-required$"
			}
			command := exec.CommandContext(ctx, os.Args[0], "-test.run="+pattern, "-test.count=1")
			command.Env = buildEnvironment(os.Environ(), map[string]string{
				"KITDB_NATIVE_FAILURE_PROBE": probe.mode, "KITDB_NATIVE_COLLISION_PATH": path,
			})
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), probe.message) {
				t.Fatalf("failure probe %s: %v: %s", probe.mode, err, output)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != original {
				t.Fatalf("earlier report overwritten: %q: %v", data, err)
			}
		})
	}
}
