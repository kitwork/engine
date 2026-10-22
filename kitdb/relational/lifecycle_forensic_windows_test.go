package relational

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type lifecycleForensicRound struct {
	Window            mixedWindow               `json:"window"`
	Resources         lifecycleResources        `json:"after_close"`
	Lock              *lifecycleLockObservation `json:"lock_observation,omitempty"`
	CapturedError     string                    `json:"captured_error,omitempty"`
	Digests           []lifecycleFileDigest     `json:"original_files,omitempty"`
	CopyVerified      int                       `json:"copy_verified_databases"`
	OriginalUnchanged bool                      `json:"original_unchanged_after_verification"`
	ForensicError     string                    `json:"forensic_error,omitempty"`
}

// One opt-in diagnostic with 2+8 x 30s workload and 12m test timeout.
// Failed fixtures survive; recovery/verification opens copies, never originals.
func TestKitDBWALForensicCampaign(t *testing.T) {
	if os.Getenv("KITDB_WAL_FORENSIC") != "1" {
		t.Skip("opt-in WAL forensic diagnostic")
	}
	root, reportPath := os.Getenv("KITDB_WAL_ROOT"), os.Getenv("KITDB_WAL_REPORT")
	if err := validateLifecycleForensicPaths(root, reportPath); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(reportPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			t.Error(err)
		}
	}()
	encoder := json.NewEncoder(file)
	if _, err := lifecycleFixtureRoot(t, root); err != nil {
		t.Fatal(err)
	}
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	for index := 0; index < lifecycleWarmups+lifecycleRounds; index++ {
		phase := "measured"
		if index < lifecycleWarmups {
			phase = "warmup"
		}
		name := fmt.Sprintf("%02d_%s", index, phase)
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		var round lifecycleForensicRound
		config := mixedConfig{Name: name, Databases: 16, Noisy: index%2 == 1, Duration: 30 * time.Second}
		config.ObserveFailure = func(id int, operation string, cause error) {
			round.CapturedError = fmt.Sprintf("database %d %s: %v", id, operation, cause)
			var link *os.LinkError
			paths := []string{filepath.Join(path, fmt.Sprintf("tenant_%02d.kitdb.wal", id))}
			if errors.As(cause, &link) {
				paths = []string{link.Old, link.New}
			}
			observation := lifecycleLockUsers(paths)
			round.Lock = &observation
			// Persist before owner cleanup, even if later cleanup or verification fails.
			lockFile, err := os.OpenFile(reportPath+".lock.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err == nil {
				err = errors.Join(json.NewEncoder(lockFile).Encode(observation), lockFile.Sync(), lockFile.Close())
			}
			if err != nil {
				round.ForensicError = err.Error()
			}
		}
		window, runErr := runMixedWindow(path, config)
		round.Window = window
		round.Resources, err = sampleLifecycleResources()
		runErr = errors.Join(runErr, err)
		if runErr == nil && (!window.Success || len(window.Tenants) != 16 || window.RequestedMS != 30000 || window.ElapsedMS < 30000 || window.ManagerDrain == nil || !mixedManagerDrained(*window.ManagerDrain)) {
			runErr = fmt.Errorf("incomplete window")
		}
		if runErr != nil {
			// No copy until every owner has released its leases and open handles.
			if window.PostCloseDrain == nil || !mixedManagerReleased(*window.PostCloseDrain) {
				round.ForensicError += "; owners not released; original retained without copy"
			} else {
				copyPath := filepath.Join(root, name+"_verification_copy")
				round.Digests, err = lifecycleCopyFixture(path, copyPath)
				if err == nil {
					round.CopyVerified, err = lifecycleVerifyCopy(copyPath, window)
				}
				if err != nil {
					round.ForensicError += "; " + err.Error()
				}
				after, digestErr := lifecycleDigests(path)
				round.OriginalUnchanged = digestErr == nil && len(round.Digests) > 0 && reflect.DeepEqual(round.Digests, after)
				if digestErr != nil {
					round.ForensicError += "; " + digestErr.Error()
				}
			}
			if err := encoder.Encode(round); err != nil {
				t.Error(err)
			}
			t.Fatal(runErr)
		}
		if err := encoder.Encode(round); err != nil {
			t.Fatal(err)
		}
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		t.Logf("round=%s ACK=%d reopened=%d handles=%d threads=%d", name, window.All.Commit.Count, len(window.Tenants), round.Resources.Handles, round.Resources.Threads)
	}
}

func validateLifecycleForensicPaths(root, report string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(report) {
		return fmt.Errorf("explicit fresh absolute fixture root and absolute report path required")
	}
	relative, err := filepath.Rel(root, report)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("diagnostic report must be outside fixture root")
	}
	return nil
}

func TestLifecycleForensicConfiguration(t *testing.T) {
	parent := t.TempDir()
	root, report := filepath.Join(parent, "fresh-fixtures"), filepath.Join(parent, "report.jsonl")
	if err := validateLifecycleForensicPaths(root, report); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][2]string{{"relative", report}, {root, "relative"}, {root, filepath.Join(root, "report.jsonl")}, {root, root}} {
		if err := validateLifecycleForensicPaths(paths[0], paths[1]); err == nil {
			t.Fatalf("unsafe diagnostic paths accepted: %v", paths)
		}
	}
}
