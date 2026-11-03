package relational

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

type checkpointForensicRound struct {
	Controlled        bool                      `json:"controlled_blocker"`
	Acknowledged      uint64                    `json:"acknowledged"`
	Checkpoints       int                       `json:"completed_checkpoints"`
	Operation         string                    `json:"operation"`
	CapturedError     string                    `json:"captured_error,omitempty"`
	Errno             uint32                    `json:"windows_errno,omitempty"`
	Lock              *lifecycleLockObservation `json:"resource_users_after_checkpoint_return,omitempty"`
	ObservationLimit  string                    `json:"observation_limit,omitempty"`
	OwnersClosed      bool                      `json:"owners_closed"`
	CopyVerified      bool                      `json:"copy_verified"`
	OriginalUnchanged bool                      `json:"original_unchanged"`
	Digests           []lifecycleFileDigest     `json:"original_files,omitempty"`
	ForensicError     string                    `json:"forensic_error,omitempty"`
}

// Use the kernel directly, not SQL. Persist a failure before closing the owner;
// then recover only a verified copy. Never retry the failed checkpoint.
func runCheckpointForensicRound(root string, controlled bool, persist func(checkpointForensicRound) error) (round checkpointForensicRound, resultErr error) {
	round.Controlled = controlled
	if err := os.Mkdir(root, 0700); err != nil {
		return round, err
	}
	path := filepath.Join(root, "compact.kitdb")
	db, err := kitdbengine.Open(path)
	if err != nil {
		return round, err
	}
	defer db.Close()
	blocker := syscall.InvalidHandle
	defer func() {
		if blocker != syscall.InvalidHandle {
			_ = syscall.CloseHandle(blocker)
		}
	}()
	for step := 1; step <= 64; step++ {
		round.Operation = "commit"
		tx, err := db.Begin()
		if err == nil {
			err = tx.Put([]byte("key"), []byte(fmt.Sprintf("value-%d", step)))
			if err == nil {
				var ack uint64
				ack, err = tx.Commit()
				if err == nil {
					round.Acknowledged = ack
				}
			}
			if err != nil {
				_ = tx.Rollback()
			}
		}
		if err != nil {
			resultErr = err
			break
		}
		if controlled && step == 33 {
			name, err := syscall.UTF16PtrFromString(path + ".wal")
			if err == nil {
				blocker, err = syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
			}
			if err != nil {
				return round, err
			}
		}
		round.Operation = "checkpoint"
		if _, resultErr = db.Checkpoint(); resultErr != nil {
			break
		}
		round.Checkpoints++
	}
	if resultErr != nil {
		round.CapturedError = resultErr.Error()
		var errno syscall.Errno
		if errors.As(resultErr, &errno) {
			round.Errno = uint32(errno)
		}
		paths := []string{path + ".wal"}
		var link *os.LinkError
		if errors.As(resultErr, &link) {
			paths = []string{link.Old, link.New}
		}
		lock := lifecycleLockUsers(paths)
		round.Lock = &lock
		round.ObservationLimit = "After Checkpoint returned: the old WAL may already have been reopened and staging removed. Our PID can be the recovery handle, not the original blocker. Restart Manager reports users, not per-handle share flags."
		if err := persist(round); err != nil {
			round.ForensicError = err.Error()
		}
	}
	if blocker != syscall.InvalidHandle {
		if err := syscall.CloseHandle(blocker); err != nil {
			return round, errors.Join(resultErr, err)
		}
		blocker = syscall.InvalidHandle
	}
	if err := db.Close(); err != nil {
		return round, errors.Join(resultErr, err)
	}
	round.OwnersClosed = true
	copyRoot := root + "_verification_copy"
	round.Digests, err = lifecycleCopyFixture(root, copyRoot)
	if err == nil {
		var copyDB *kitdbengine.DB
		copyDB, err = kitdbengine.Open(filepath.Join(copyRoot, "compact.kitdb"))
		if err == nil {
			err = copyDB.Verify()
			if err == nil {
				var value []byte
				var found bool
				value, found, err = copyDB.Get([]byte("key"))
				if err == nil && (!found || string(value) != fmt.Sprintf("value-%d", round.Acknowledged)) {
					err = fmt.Errorf("copy does not match independent ACK model: %q found=%t", value, found)
				}
			}
			err = errors.Join(err, copyDB.Close())
		}
	}
	round.CopyVerified = err == nil
	after, digestErr := lifecycleDigests(root)
	round.OriginalUnchanged = digestErr == nil && len(round.Digests) > 0 && reflect.DeepEqual(round.Digests, after)
	if err != nil || digestErr != nil || !round.OriginalUnchanged {
		round.ForensicError += fmt.Sprintf("; copy=%v digests=%v unchanged=%t", err, digestErr, round.OriginalUnchanged)
	}
	return round, resultErr
}

func checkpointForensicPersist(path string, round checkpointForensicRound) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(file).Encode(round), file.Sync(), file.Close())
}

func TestWALCheckpointForensicCapture(t *testing.T) {
	root := t.TempDir()
	failure := filepath.Join(root, "failure.json")
	round, err := runCheckpointForensicRound(filepath.Join(root, "original"), true, func(round checkpointForensicRound) error {
		return checkpointForensicPersist(failure, round)
	})
	if !errors.Is(err, syscall.ERROR_ACCESS_DENIED) && !errors.Is(err, syscall.Errno(32)) {
		t.Fatalf("expected real controlled refusal: %v", err)
	}
	if round.Checkpoints != 32 || round.Acknowledged != 33 || round.Operation != "checkpoint" ||
		!round.OwnersClosed || !round.CopyVerified || !round.OriginalUnchanged || round.ForensicError != "" {
		t.Fatalf("incomplete control: %+v", round)
	}
	if round.Lock == nil || round.Lock.Error != "" {
		t.Fatalf("resource observation failed: %+v", round.Lock)
	}
	found := false
	for _, user := range round.Lock.Users {
		found = found || user.PID == uint32(os.Getpid())
	}
	if !found {
		t.Fatalf("known resource user not observed: %+v", round.Lock)
	}
	data, err := os.ReadFile(failure)
	var beforeClose checkpointForensicRound
	if err != nil || json.Unmarshal(data, &beforeClose) != nil || beforeClose.OwnersClosed || beforeClose.CapturedError == "" {
		t.Fatal("missing pre-cleanup failure evidence", err)
	}
	t.Logf("controlled_errno=%d ACK=%d completed_checkpoints=%d pre_close_capture=true original_unchanged=true copy_verified=true", round.Errno, round.Acknowledged, round.Checkpoints)
}

func TestWALCheckpointForensicCampaign(t *testing.T) {
	if os.Getenv("KITDB_WAL_CHECKPOINT_FORENSIC") != "1" {
		t.Skip("opt-in bounded kernel checkpoint diagnostic")
	}
	root, report := os.Getenv("KITDB_WAL_ROOT"), os.Getenv("KITDB_WAL_REPORT")
	if err := validateLifecycleForensicPaths(root, report); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycleFixtureRoot(t, root); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		round, err := runCheckpointForensicRound(filepath.Join(root, fmt.Sprintf("round-%02d", index)), false, func(round checkpointForensicRound) error {
			return checkpointForensicPersist(report+".failure.json", round)
		})
		if writeErr := checkpointForensicPersist(fmt.Sprintf("%s.round-%02d.json", report, index), round); writeErr != nil {
			t.Fatal(writeErr)
		}
		if err != nil || round.CapturedError != "" || round.ForensicError != "" || !round.OwnersClosed ||
			!round.CopyVerified || !round.OriginalUnchanged || round.Acknowledged != 64 || round.Checkpoints != 64 {
			t.Fatalf("round %d incomplete: %+v err=%v", index, round, err)
		}
		t.Logf("round=%d ACK=64 checkpoints=64 copy_verified=true original_unchanged=true at=%s", index, time.Now().UTC().Format(time.RFC3339Nano))
	}
}
