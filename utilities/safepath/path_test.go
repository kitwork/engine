package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

var boundaryBenchmarkResult bool

func TestResolveRejectsTraversalAndSymlinkParents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if _, err := Resolve(root, "..", filepath.Base(outside), "secret.txt"); err == nil {
		t.Fatal("lexical traversal was accepted")
	}

	link := filepath.Join(root, "external")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Resolve(root, "external", "new.txt"); err == nil {
		t.Fatal("write through an escaping symlink parent was accepted")
	}
}

func TestBoundaryRejectsEscapingSymlinkAndMissingChild(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	boundary, err := NewBoundary(root)
	if err != nil {
		t.Fatal(err)
	}
	inside, err := boundary.Contains(filepath.Join(root, "future", "file.txt"))
	if err != nil || !inside {
		t.Fatalf("missing child inside boundary: inside=%v err=%v", inside, err)
	}

	link := filepath.Join(root, "external")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	inside, err = boundary.Contains(filepath.Join(link, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if inside {
		t.Fatal("prepared boundary accepted an escaping symlink")
	}
}

func BenchmarkContains(b *testing.B) {
	root := b.TempDir()
	target := filepath.Join(root, "asset.txt")
	if err := os.WriteFile(target, []byte("asset"), 0o644); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inside, err := Contains(root, target)
		if err != nil {
			b.Fatal(err)
		}
		boundaryBenchmarkResult = inside
	}
}

func BenchmarkPreparedBoundaryContains(b *testing.B) {
	root := b.TempDir()
	target := filepath.Join(root, "asset.txt")
	if err := os.WriteFile(target, []byte("asset"), 0o644); err != nil {
		b.Fatal(err)
	}
	boundary, err := NewBoundary(root)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inside, containsErr := boundary.Contains(target)
		if containsErr != nil {
			b.Fatal(containsErr)
		}
		boundaryBenchmarkResult = inside
	}
}
