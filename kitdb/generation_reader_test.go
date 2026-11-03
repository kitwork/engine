package kitdb

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendedGenerationReaderOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	t.Cleanup(func() { _ = db.Close() })
	commitPut(t, db, "value", "before")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// Populate a page that must remain reusable at the same physical index.
	if got := string(requireValue(t, db, "value")); got != "before" {
		t.Fatal(got)
	}
	cache := db.main.cache
	oldFile := db.main.file
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snapshot.Close() })
	commitPut(t, db, "value", "after")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if db.main.file == oldFile || db.main.file == snapshot.main.file || db.main.cache != cache {
		t.Fatal("generation must own its handle and retain only the immutable-page cache")
	}
	if _, err := oldFile.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("previous reader not closed: %v", err)
	}
	if value, found, err := snapshot.Get([]byte("value")); err != nil || !found || string(value) != "before" {
		t.Fatalf("old snapshot: %q/%t/%v", value, found, err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if got := string(requireValue(t, db, "value")); got != "after" {
		t.Fatal(got)
	}
	reader := db.main.file
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("appended reader not closed: %v", err)
	}
	db = mustOpen(t, path)
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
	if got := string(requireValue(t, db, "value")); got != "after" {
		t.Fatal(got)
	}
}

func TestAppendedGenerationUsesPinnedFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "generation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := file.Truncate(128); err != nil {
		t.Fatal(err)
	}
	previous := &mainImage{cache: newRowPageCache(4096), blocks: []mainBlock{{offset: 12}}}
	// A non-existent path proves construction uses the already-pinned file.
	image, err := newAppendedGeneration(filepath.Join(t.TempDir(), "absent"), file, previous, &generationAppendResult{fileEnd: 128, blocks: []mainBlock{{offset: 64}}})
	if err != nil {
		t.Fatal(err)
	}
	if image.file != file || image.cache != previous.cache || len(image.blocks) != 2 {
		t.Fatal("pinned handle/cache or appended metadata lost")
	}
	image.blocks[0].offset = 99
	if previous.blocks[0].offset != 12 {
		t.Fatal("mutable metadata slice shared with previous image")
	}
	if err := image.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("image did not own file: %v", err)
	}
}

func TestAppendedGenerationValidatesPinnedFile(t *testing.T) {
	for _, mode := range []string{"short", "closed", "directory"} {
		t.Run(mode, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "generation-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = file.Close() })
			if mode == "closed" || mode == "directory" {
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "directory" {
				file, err = os.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
			}
			image, err := newAppendedGeneration(file.Name(), file, &mainImage{}, &generationAppendResult{fileEnd: 1})
			if image != nil || err == nil {
				t.Fatal("invalid pinned file accepted")
			}
			if mode == "short" || mode == "directory" {
				if !errors.Is(err, ErrCorrupt) {
					t.Fatalf("expected corruption, got %v", err)
				}
			} else if errors.Is(err, ErrCorrupt) {
				t.Fatalf("stat failure misclassified as corruption: %v", err)
			}
		})
	}
}
