package kitdb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWindowsWALRotationHeldWriterFallback(t *testing.T) {
	for _, code := range []syscall.Errno{1, 50, 87, 120, 127, 5, 32} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tenant.wal")
			if err := createWAL(path, [16]byte{1}, 0, 0); err != nil {
				t.Fatal(err)
			}
			file, err := prepareWALFile(path, [16]byte{1}, 1, 42, createWALRotationStaging)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			published, err := publishWALFileUsing(file.Name(), path, func(*os.File, string) error { return code })
			unsupported := code != 5 && code != 32
			if published != unsupported || (err == nil) != unsupported {
				t.Fatalf("held-writer fallback: %t %v", published, err)
			}
			if !unsupported && !errors.Is(err, code) {
				t.Fatal("denial was changed", err)
			}
			name := path
			if !unsupported {
				name = file.Name()
			}
			actual, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			held, err := file.Stat()
			if err != nil || !os.SameFile(actual, held) {
				t.Fatalf("publication changed the pinned writer: %v", err)
			}
		})
	}
}

func TestWindowsWALRotationExtendedPath(t *testing.T) {
	root := t.TempDir()
	for range 3 {
		root = filepath.Join(root, strings.Repeat("segment", 12))
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "wal-\u0111\u01b0\u1eddng.wal")
	file, err := prepareWALFile(path, [16]byte{1}, 7, 42, createWALRotationStaging)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if published, err := publishWALFile(file.Name(), path); err != nil || !published {
		t.Fatalf("extended-path held-writer publication: %t %v", published, err)
	}
}
