package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Exercise source and target sharing separately: ACCESS_DENIED alone cannot
// identify which file, process, or handle caused a real publication failure.
func TestWindowsWALPublicationSharing(t *testing.T) {
	for _, source := range []bool{false, true} {
		for _, deleteShare := range []bool{false, true} {
			t.Run(fmt.Sprintf("source_%t_delete_share_%t", source, deleteShare), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "sharing.wal")
				identity := [16]byte{1, 2, 3}
				if err := createWAL(path, identity, 0, 0); err != nil {
					t.Fatal(err)
				}
				staging, err := prepareWAL(path, identity, 1, 42)
				if err != nil {
					t.Fatal(err)
				}
				defer os.Remove(staging)
				heldPath := path
				if source {
					heldPath = staging
				}
				name, err := syscall.UTF16PtrFromString(heldPath)
				if err != nil {
					t.Fatal(err)
				}
				share := uint32(syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE)
				if deleteShare {
					share |= syscall.FILE_SHARE_DELETE
				}
				handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, share, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
				file := os.NewFile(uintptr(handle), heldPath)
				if file == nil {
					_ = syscall.CloseHandle(handle)
					t.Fatal("invalid test handle")
				}
				defer file.Close()
				publicationErr := replaceFile(staging, path)
				if !deleteShare && publicationErr == nil {
					t.Fatal("replacement bypassed a real no-delete-share handle")
				}
				// Legacy MoveFileEx can also refuse an open destination with delete
				// sharing. Accept that platform outcome only if neither file changed;
				// a source with delete sharing must still support the move.
				if publicationErr != nil {
					if source && deleteShare {
						t.Fatal(publicationErr)
					}
					var link *os.LinkError
					if !errors.As(publicationErr, &link) || link.Old != staging || link.New != path ||
						(!errors.Is(publicationErr, syscall.ERROR_ACCESS_DENIED) && !errors.Is(publicationErr, syscall.Errno(32))) {
						t.Fatalf("expected real sharing denial with both paths: %v", publicationErr)
					}
					for name, want := range map[string][]byte{path: encodeWALHeader(identity, 0, 0), staging: encodeWALHeader(identity, 1, 42)} {
						got, err := os.ReadFile(name)
						if err != nil || !bytes.Equal(got, want) {
							t.Fatalf("denial changed %s: %v", name, err)
						}
					}
					t.Logf("controlled_source=%t delete_share=%t errno=%v old_and_new_headers_unchanged=true", source, deleteShare, publicationErr)
					if err := file.Close(); err != nil {
						t.Fatal(err)
					}
					// A separate, explicit post-close control; the refused publication
					// above remains observed and is never retried inside production.
					if err := replaceFile(staging, path); err != nil {
						t.Fatal("closed-handle control failed", err)
					}
					got, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(got, encodeWALHeader(identity, 1, 42)) {
						t.Fatalf("closed-handle replacement header: %v", err)
					}
					return
				}
				if publicationErr != nil {
					t.Fatal(publicationErr)
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, encodeWALHeader(identity, 1, 42)) {
					t.Fatalf("replacement header: %v", err)
				}
				if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("source name remains: %v", err)
				}
				heldBytes, err := io.ReadAll(file)
				want := encodeWALHeader(identity, 0, 0)
				if source {
					want = encodeWALHeader(identity, 1, 42)
				}
				if err != nil || !bytes.Equal(heldBytes, want) {
					t.Fatalf("held handle changed file identity: %v", err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestWindowsWALCheckpointDenialRecovery(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for _, history := range []bool{false, true} {
			t.Run(fmt.Sprintf("compact_%t_history_%t", compact, history), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "recovery.kitdb")
				options := OpenOptions{RetainHistory: history}
				db, err := OpenWithOptions(path, options)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if compact {
					for range maxMainSegments {
						commitPut(t, db, "key", "previous")
						if _, err := db.Checkpoint(); err != nil {
							t.Fatal(err)
						}
					}
				}
				ack := commitPut(t, db, "key", "acknowledged")
				name, err := syscall.UTF16PtrFromString(databaseWALPath(path))
				if err != nil {
					t.Fatal(err)
				}
				handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if handle != syscall.InvalidHandle {
						_ = syscall.CloseHandle(handle)
					}
				}()
				_, publicationErr := db.Checkpoint()
				if !errors.Is(publicationErr, syscall.ERROR_ACCESS_DENIED) && !errors.Is(publicationErr, syscall.Errno(32)) {
					t.Fatalf("expected real replacement denial: %v", publicationErr)
				}
				if errors.Is(publicationErr, ErrDurabilityUncertain) || errors.Is(publicationErr, ErrUnavailable) != history {
					t.Fatalf("wrong publication policy: %v", publicationErr)
				}
				if db.checkpointTx != ack || db.walBaseTx >= ack || db.wal == nil {
					t.Fatal("expected published main boundary and restored old WAL")
				}
				wal, ok := db.wal.(*os.File)
				if !ok {
					t.Fatal("restored WAL is not a file")
				}
				if _, err := wal.Stat(); err != nil {
					t.Fatal("restored WAL handle is closed", err)
				}
				value, found, readErr := db.Get([]byte("key"))
				if history {
					if !errors.Is(readErr, ErrUnavailable) {
						t.Fatalf("history failure should fail closed: %v", readErr)
					}
				} else if readErr != nil || !found || string(value) != "acknowledged" {
					t.Fatalf("ack lost after refusal: %q %v %v", value, found, readErr)
				}
				if err := syscall.CloseHandle(handle); err != nil {
					t.Fatal(err)
				}
				handle = syscall.InvalidHandle
				// No checkpoint retry: a fresh open must reconcile the old WAL with
				// the already-published main boundary without losing the ACK.
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := OpenWithOptions(path, options)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if err := reopened.Verify(); err != nil {
					t.Fatal(err)
				}
				if got := string(requireValue(t, reopened, "key")); got != "acknowledged" {
					t.Fatalf("reopen lost ACK: %q", got)
				}
				if reopened.lastTx != ack || reopened.walBaseTx != ack {
					t.Fatal("reopen did not reconcile exact checkpoint boundary")
				}
				commitPut(t, reopened, "later", "committed")
				if err := reopened.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
