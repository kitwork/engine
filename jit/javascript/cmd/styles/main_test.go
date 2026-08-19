package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteStylesheetPublishesImmutableBytesAtomically(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "kitjs.examples.hash.css")
	source := []byte(".block { display: block; }\n")

	if err := writeStylesheet(path, source, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source) {
		t.Fatalf("published stylesheet = %q, want %q", got, source)
	}
	if err := writeStylesheet(path, append([]byte(nil), source...), true); err != nil {
		t.Fatalf("identical immutable rewrite: %v", err)
	}
	if err := writeStylesheet(path, []byte("different"), true); err == nil {
		t.Fatal("different bytes replaced an immutable stylesheet")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("stylesheet directory contains temporary output: %#v", entries)
	}
}
