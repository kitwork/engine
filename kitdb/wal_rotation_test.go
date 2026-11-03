package kitdb

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWALRotationPreparedWriterOwnership(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tenant.wal")
	identity := [16]byte{1, 7}
	var acquired *os.File
	file, err := prepareWALFile(path, identity, 9, 42, func(dir, pattern string) (*os.File, error) {
		var err error
		acquired, err = createWALRotationStaging(dir, pattern)
		return acquired, err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if file != acquired {
		t.Fatal("prepared writer was replaced")
	}
	if offset, err := file.Seek(0, io.SeekCurrent); err != nil || offset != walHeaderSize {
		t.Fatalf("append offset = %d: %v", offset, err)
	}
	if published, err := publishWALFile(file.Name(), path); err != nil || !published {
		t.Fatalf("held writer blocked publication: %t %v", published, err)
	}
	actual, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Stat(path)
	if err != nil || !os.SameFile(actual, target) {
		t.Fatalf("writer did not follow published identity: %v", err)
	}
	header, err := readWALHeader(path, file, walHeaderSize)
	if err != nil || header.identity != identity || header.baseTx != 9 || header.baseChecksum != 42 {
		t.Fatalf("wrong published boundary: %+v %v", header, err)
	}
	frame, err := encodeFrame(10, []operation{{kind: operationPut, key: []byte("key"), value: []byte("value")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeAll(file, frame); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != int64(walHeaderSize+len(frame)) {
		t.Fatalf("writer appended to wrong file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("prepared writer not closed: %v", err)
	}
}

func TestWALRotationPrepareFailureCleansStaging(t *testing.T) {
	root := t.TempDir()
	var acquired *os.File
	_, err := prepareWALFile(filepath.Join(root, "tenant.wal"), [16]byte{1}, 0, 0, func(dir, pattern string) (*os.File, error) {
		file, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		acquired, err = os.Open(file.Name()) // Chmod works, header write must fail.
		return acquired, err
	})
	if err == nil || acquired == nil {
		t.Fatalf("failed preparation accepted: %v", err)
	}
	if _, err := acquired.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("failed preparation retained handle: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed preparation retained staging: %v %v", entries, err)
	}
	called := false
	if _, err := prepareWALFile(filepath.Join(root, "tenant.wal"), [16]byte{1}, 0, 1, func(string, string) (*os.File, error) {
		called = true
		return nil, errors.New("unexpected create")
	}); err == nil || called {
		t.Fatal("invalid boundary created a staging file")
	}
}

func TestWALRotationInstallsPreparedWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	t.Cleanup(func() { _ = db.Close() })
	for i := range 4 {
		previous := db.wal.(*os.File)
		ack := commitPut(t, db, "key", string(rune('a'+i)))
		if tx, err := db.Checkpoint(); err != nil || tx != ack {
			t.Fatalf("checkpoint = %d, want %d: %v", tx, ack, err)
		}
		if _, err := previous.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("previous writer retained: %v", err)
		}
		writer := db.wal.(*os.File)
		info, err := writer.Stat()
		if err != nil {
			t.Fatal(err)
		}
		target, err := os.Stat(databaseWALPath(path))
		if err != nil || !os.SameFile(info, target) || db.walEnd != walHeaderSize || db.walBaseTx != ack {
			t.Fatalf("installed writer/boundary mismatch: %v", err)
		}
	}
	commitPut(t, db, "tail", "durable")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
	if got := string(requireValue(t, db, "tail")); got != "durable" {
		t.Fatal(got)
	}
}
