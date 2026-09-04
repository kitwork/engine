package snapshotfile

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotPublicationAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.columnar")
	write := func(value string) *Writer {
		t.Helper()
		w, err := Create(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.Close() })
		if err := w.Add("table", func(out io.Writer) error { _, err := io.WriteString(out, value); return err }); err != nil {
			t.Fatal(err)
		}
		return w
	}
	w := write("first")
	if err := w.Publish(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	w = write("second")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Publish(canceled, "two"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	w.Close()
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	section, err := r.Section("table")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(section)
	if err != nil || string(data) != "first" {
		t.Fatalf("old publication: %q %v", data, err)
	}
	r.Close()
	w = write("replacement")
	if err := w.Publish(context.Background(), "three"); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata string
	if err := r.Metadata(&metadata); err != nil || metadata != "three" {
		t.Fatal(metadata, err)
	}
	r.Close()
	w = write("unpublished")
	if err := w.Add("broken", func(io.Writer) error { return io.ErrShortWrite }); err == nil {
		t.Fatal("missing write failure")
	}
	if err := w.Publish(context.Background(), "four"); err == nil {
		t.Fatal("failed writer published")
	}
	w.Close()
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary cleanup: %v %v", files, err)
	}
}

func TestSnapshotCorruptAndTruncatedDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Publish(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, position := range []int{0, 12, len(data) - 1} {
		bad := append([]byte(nil), data...)
		bad[position] ^= 1
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if r, err := Open(path); err == nil {
			r.Close()
			t.Fatalf("accepted corruption at %d", position)
		}
	}
	if err := os.WriteFile(path, data[:len(data)-1], 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(path); err == nil {
		r.Close()
		t.Fatal("accepted truncation")
	}
}
