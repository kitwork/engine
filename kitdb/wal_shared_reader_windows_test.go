package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestWindowsWALPublicationPaths(t *testing.T) {
	if got, want := unsafe.Offsetof(walRenameInformation{}.name), 2*unsafe.Sizeof(uintptr(0))+4; got != want {
		t.Fatalf("FILE_RENAME_INFO ABI: name offset %d want %d", got, want)
	}
	root := t.TempDir()
	for range 3 {
		root = filepath.Join(root, strings.Repeat("segment", 12))
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "wal-\u0111\u01b0\u1eddng-\U0001f680.wal")
	identity := [16]byte{1, 2}
	if len(path) <= 260 {
		t.Fatal("fixture did not exercise an extended path")
	}
	if err := createWAL(path, identity, 0, 0); err != nil {
		t.Fatal(err)
	}
	staging, err := prepareWAL(path, identity, 7, 42)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(staging)
	if published, err := publishWALFile(staging, path); err != nil || !published {
		t.Fatalf("Unicode/extended publication: published=%t err=%v", published, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, encodeWALHeader(identity, 7, 42)) {
		t.Fatalf("published header differs: %v", err)
	}
}

func TestWindowsWALPublicationFallback(t *testing.T) {
	for _, code := range []syscall.Errno{1, 50, 87, 120, 127, 5, 32} {
		t.Run(fmt.Sprintf("errno_%d", code), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fallback.wal")
			identity := [16]byte{1}
			if err := createWAL(path, identity, 0, 0); err != nil {
				t.Fatal(err)
			}
			staging, err := prepareWAL(path, identity, 1, 42)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(staging)
			var held *os.File
			calls := 0
			published, err := publishWALFileUsing(staging, path, func(file *os.File, target string) error {
				held = file
				calls++
				return code // Test-only unsupported/denial classification, not an OS observation.
			})
			unsupported := code != 5 && code != 32
			if calls != 1 || published != unsupported || (err == nil) != unsupported {
				t.Fatalf("calls=%d published=%t err=%v", calls, published, err)
			}
			if !unsupported && !errors.Is(err, code) {
				t.Fatal("denial was replaced or retried", err)
			}
			if !unsupported {
				requireWALPublicationPhase(t, err, "replace-target", false)
			}
			if _, err := held.Write(nil); !errors.Is(err, os.ErrClosed) {
				t.Fatal("publication leaked its source handle", err)
			}
			want := encodeWALHeader(identity, 0, 0)
			if unsupported {
				want = encodeWALHeader(identity, 1, 42)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("target changed incorrectly: %v", err)
			}
			if !unsupported {
				got, err := os.ReadFile(staging)
				if err != nil || !bytes.Equal(got, encodeWALHeader(identity, 1, 42)) {
					t.Fatalf("denial changed staging: %v", err)
				}
			}
		})
	}
}

func TestWindowsWALCheckpointSharedReader(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for _, history := range []bool{false, true} {
			t.Run(fmt.Sprintf("compact_%t_history_%t", compact, history), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "reader.kitdb")
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
				walPath := databaseWALPath(path)
				oldBytes, err := os.ReadFile(walPath)
				if err != nil {
					t.Fatal(err)
				}
				name, err := syscall.UTF16PtrFromString(walPath)
				if err != nil {
					t.Fatal(err)
				}
				handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
				reader := os.NewFile(uintptr(handle), walPath)
				if reader == nil {
					_ = syscall.CloseHandle(handle)
					t.Fatal("invalid test handle")
				}
				defer reader.Close()
				if got, err := db.Checkpoint(); err != nil || got != ack {
					t.Fatalf("delete-sharing reader blocked checkpoint at ACK %d: tx=%d err=%v", ack, got, err)
				}
				if db.walBaseTx != ack || db.walEnd != walHeaderSize {
					t.Fatal("new WAL does not start at the exact checkpoint boundary")
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := OpenWithOptions(path, options)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if got := string(requireValue(t, reopened, "key")); got != "acknowledged" {
					t.Fatalf("lost ACK: %q", got)
				}
				commitPut(t, reopened, "later", "new-file")
				if err := reopened.Verify(); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, len(oldBytes))
				if _, err := reader.ReadAt(got, 0); err != nil || !bytes.Equal(got, oldBytes) {
					t.Fatalf("held reader no longer refers to its old WAL bytes: %v", err)
				}
				if err := reopened.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
