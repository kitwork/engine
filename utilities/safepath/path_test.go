package safepath

import (
	"os"
	"path/filepath"
	"runtime"
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

// Relative is the target's path from the boundary as the disk holds it (a name of the code zone of
// kitwork/file is checked on it, KITWORK_FILE.md mục 1): "" for the root itself, a child that does not
// exist yet as written, a name written in another case read in the disk's case (Windows), a link read as
// where it leads (elsewhere), and nothing for a target outside.
//
// CONTROL: with Relative taking the target as written (canonical skipped), SITE/new.txt stayed in the case
// it was written in on Windows and the test failed.
func TestBoundaryRelative(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Site"), 0o755); err != nil {
		t.Fatal(err)
	}
	boundary, err := NewBoundary(root)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		root:                                    "",
		filepath.Join(root, "Site", "new.txt"):  filepath.Join("Site", "new.txt"),
		filepath.Join(root, "Site", "a", "b.c"): filepath.Join("Site", "a", "b.c"),
	}
	if runtime.GOOS == "windows" {
		cases[filepath.Join(root, "SITE", "new.txt")] = filepath.Join("Site", "new.txt")
	} else if err := os.Symlink(filepath.Join(root, "Site"), filepath.Join(root, "alias")); err == nil {
		cases[filepath.Join(root, "alias", "new.txt")] = filepath.Join("Site", "new.txt")
	}
	for target, want := range cases {
		relative, inside, err := boundary.Relative(target)
		if err != nil || !inside || relative != want {
			t.Errorf("Relative(%q) = %q %v %v, want %q", target, relative, inside, err, want)
		}
	}
	if relative, inside, err := boundary.Relative(filepath.Join(filepath.Dir(root), "x")); inside || relative != "" || err != nil {
		t.Errorf("a target outside = %q %v %v, want not inside", relative, inside, err)
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
