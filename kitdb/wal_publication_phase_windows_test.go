package kitdb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func walPhaseFixture(t *testing.T) (staging, target string) {
	t.Helper()
	target = filepath.Join(t.TempDir(), "phase.wal")
	identity := [16]byte{1, 2}
	if err := createWAL(target, identity, 0, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	staging, err = prepareWAL(target, identity, 7, 42)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(staging) })
	return staging, target
}

func holdWALPhasePath(t *testing.T, path string) *os.File {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		t.Fatal("invalid blocker handle")
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func requireWALPublicationPhase(t *testing.T, err error, phase string, published bool) {
	t.Helper()
	var detail *walPublicationError
	if !errors.As(err, &detail) || detail.phase != phase || detail.published != published {
		t.Fatalf("publication phase=%q published=%t: %v", phase, published, err)
	}
}

func TestWindowsWALPublicationPhases(t *testing.T) {
	for _, source := range []bool{true, false} {
		phase := "replace-target"
		if source {
			phase = "open-source"
		}
		t.Run(phase, func(t *testing.T) {
			staging, target := walPhaseFixture(t)
			old, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			staged, err := os.ReadFile(staging)
			if err != nil {
				t.Fatal(err)
			}
			heldPath := target
			if source {
				heldPath = staging
			}
			blocker := holdWALPhasePath(t, heldPath)
			calls := 0
			var publication *os.File
			published, err := publishWALFileUsing(staging, target, func(file *os.File, path string) error {
				calls++
				publication = file
				return renameWALHandle(file, path)
			})
			if published || (!errors.Is(err, syscall.ERROR_ACCESS_DENIED) && !errors.Is(err, syscall.Errno(32))) {
				t.Fatalf("expected native sharing refusal: %v", err)
			}
			requireWALPublicationPhase(t, err, phase, false)
			var link *os.LinkError
			if !errors.As(err, &link) || link.Old != staging || link.New != target {
				t.Fatalf("link error identity lost: %v", err)
			}
			wantCalls := 1
			if source {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("rename calls=%d want=%d; refusal retried or misclassified", calls, wantCalls)
			}
			if publication != nil {
				if _, err := publication.Write(nil); !errors.Is(err, os.ErrClosed) {
					t.Fatal("publication handle leaked", err)
				}
			}
			for path, want := range map[string][]byte{staging: staged, target: old} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("refusal changed %s: %v", path, err)
				}
			}
			if err := blocker.Close(); err != nil {
				t.Fatal(err)
			}
			// Explicit post-close control, not a production retry of the refusal.
			if ok, err := publishWALFile(staging, target); !ok || err != nil {
				t.Fatalf("closed-blocker control: %v", err)
			}
		})
	}
}

func TestWindowsWALPublicationResolutionPhase(t *testing.T) {
	if published, err := publishWALFile("bad\x00source", "unused"); published || err == nil {
		t.Fatal("invalid source accepted")
	} else {
		requireWALPublicationPhase(t, err, "resolve-source", false)
	}
}

func TestWindowsWALPublicationFallbackRefusalPhase(t *testing.T) {
	staging, target := walPhaseFixture(t)
	blocker := holdWALPhasePath(t, target)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	published, err := publishWALFileUsing(staging, target, func(_ *os.File, _ string) error {
		calls++
		return syscall.Errno(50) // Test-only unsupported native API, not a retry.
	})
	if published || calls != 1 || (!errors.Is(err, syscall.ERROR_ACCESS_DENIED) && !errors.Is(err, syscall.Errno(32))) {
		t.Fatalf("fallback denial was hidden: calls=%d err=%v", calls, err)
	}
	requireWALPublicationPhase(t, err, "fallback-replace", false)
	after, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("fallback refusal changed target", err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
}
