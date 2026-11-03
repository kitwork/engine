//go:build kitdb_waldiag

package kitdb

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb/internal/waldiag"
)

type walPhaseCapture struct {
	Phase     string `json:"phase"`
	Published bool   `json:"published"`
	PID       int    `json:"observer_pid"`
	Source    struct {
		Exists bool `json:"exists"`
	} `json:"source_state"`
	Target struct {
		Exists bool `json:"exists"`
	} `json:"target_state"`
	Handle struct {
		Present bool   `json:"present"`
		Open    bool   `json:"open"`
		FileID  uint64 `json:"file_id"`
	} `json:"publication_handle"`
	Users []struct {
		PID int `json:"pid"`
	} `json:"resource_users"`
	ResourceError string `json:"resource_error"`
}

func prepareWALPhaseCapture(t *testing.T, staging string) string {
	t.Helper()
	root := filepath.Dir(staging)
	report := filepath.Join(t.TempDir(), "failure.json")
	if err := waldiag.Prepare(root, report); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KITDB_WAL_DIAG_ROOT", root)
	t.Setenv("KITDB_WAL_DIAG_REPORT", report)
	return report
}

func readWALPhaseCapture(t *testing.T, path string) walPhaseCapture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var capture walPhaseCapture
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	return capture
}

func TestWindowsWALPublicationCaptureBeforeCleanup(t *testing.T) {
	for _, source := range []bool{true, false} {
		phase := "replace-target"
		if source {
			phase = "open-source"
		}
		t.Run(phase, func(t *testing.T) {
			staging, target := walPhaseFixture(t)
			report := prepareWALPhaseCapture(t, staging)
			path := target
			if source {
				path = staging
			}
			blocker := holdWALPhasePath(t, path)
			if published, err := publishWALFile(staging, target); published || err == nil {
				t.Fatal("missing real sharing refusal")
			} else {
				requireWALPublicationPhase(t, err, phase, false)
			}
			capture := readWALPhaseCapture(t, report)
			if capture.Phase != phase || capture.Published || capture.PID != os.Getpid() || !capture.Source.Exists || !capture.Target.Exists || capture.ResourceError != "" {
				t.Fatalf("capture=%+v", capture)
			}
			if capture.Handle.Present != !source || capture.Handle.Open != !source || (!source && capture.Handle.FileID == 0) {
				t.Fatalf("publication was observed after handle cleanup: %+v", capture)
			}
			ownPID := false
			for _, user := range capture.Users {
				ownPID = ownPID || user.PID == os.Getpid()
			}
			if !ownPID {
				t.Fatal("known resource user absent", capture)
			}
			before, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			// The one-shot report is immutable even when another refusal occurs.
			if _, err := publishWALFile(staging, target); err == nil {
				t.Fatal("second control unexpectedly published")
			}
			after, err := os.ReadFile(report)
			if err != nil || string(before) != string(after) {
				t.Fatal("failure report overwritten", err)
			}
			if err := blocker.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Fatal(err)
			}
		})
	}
}
