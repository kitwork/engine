package safepath

import (
	"os"
	"os/exec"
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

// A junction (mklink /J, no privilege needed) is a link as much as a symlink: one that leads out of the
// boundary is outside, one that stays in is read as where it leads.
//
// CONTROL: with canonical resolving by filepath.EvalSymlinks (which has not followed a junction since
// Go 1.23), "prepared boundary accepted an escaping junction" failed.
func TestBoundaryResolvesJunctions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows'")
	}
	root := t.TempDir()
	outside := t.TempDir()
	own := filepath.Join(root, "own")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"external": outside, "alias": own} {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, link), target).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v: %s", err, out)
		}
	}
	boundary, err := NewBoundary(root)
	if err != nil {
		t.Fatal(err)
	}
	if inside, err := boundary.Contains(filepath.Join(root, "external", "secret.txt")); err != nil || inside {
		t.Fatalf("prepared boundary accepted an escaping junction: inside=%v err=%v", inside, err)
	}
	if relative, inside, err := boundary.Relative(filepath.Join(root, "alias", "new.txt")); err != nil || !inside || relative != filepath.Join("own", "new.txt") {
		t.Fatalf("a junction inside = %q %v %v, want %q", relative, inside, err, filepath.Join("own", "new.txt"))
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
