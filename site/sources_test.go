package site_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
)

func TestSourceManifestDetectsContentWithoutTimestampChange(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "router.kitwork.js")
	if err := os.WriteFile(filename, []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}

	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site(root, "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.Sources().WatchFile(filename); err != nil {
		t.Fatal(err)
	}
	if _, err := siteRuntime.ActivateGeneration(generation); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filename, []byte("bravo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filename, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := generation.Sources().Changed()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("same-size, same-timestamp content change was not detected")
	}
	if err := generation.Sources().WatchFile(filename); err == nil {
		t.Fatal("activated generation accepted a source mutation")
	}
	siteRuntime.Close()
}

func TestSourceManifestWatchesAlreadySnapshottedBytes(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "component.js")
	if err := os.WriteFile(filename, []byte("raw-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	appRuntime := app.NewRuntime("identity-a")
	t.Cleanup(appRuntime.Close)
	siteRuntime, _ := appRuntime.Site(root, "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	consumed := []byte("raw-v1")
	if err := generation.Sources().WatchFileContent(filename, consumed); err != nil {
		t.Fatal(err)
	}
	consumed[0] = 'X'
	if err := generation.Sources().WatchFileContent(filename, []byte("raw-v2")); err == nil {
		t.Fatal("manifest accepted two different preparation snapshots for one file")
	}
	generation.Sources().Freeze()
	if err := os.WriteFile(filename, []byte("raw-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := generation.Sources().Changed(); err != nil || !changed {
		t.Fatalf("snapshotted source edit: changed=%v err=%v", changed, err)
	}
}

func TestSourceManifestConfinedWatchRejectsAuthoredSymlinkRetargetBeforeRead(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.js")
	content := []byte(`kit.component("tenant-test", {});`)
	if err := os.WriteFile(inside, content, 0o644); err != nil {
		t.Fatal(err)
	}
	authored := filepath.Join(root, "component.js")
	if err := os.Symlink(inside, authored); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(authored)
	if err != nil {
		t.Fatal(err)
	}

	appRuntime := app.NewRuntime("identity-a")
	t.Cleanup(appRuntime.Close)
	siteRuntime, _ := appRuntime.Site(root, "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.Sources().WatchConfinedFileContent(authored, root, resolved, content); err != nil {
		t.Fatal(err)
	}
	generation.Sources().Freeze()

	outsideDirectory := t.TempDir()
	if err := os.Remove(authored); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDirectory, authored); err != nil {
		t.Skipf("symlink retarget unavailable: %v", err)
	}
	if changed, err := generation.Sources().Changed(); err != nil || !changed {
		t.Fatalf("outside authored-link retarget: changed=%v err=%v", changed, err)
	}
}

func TestSourceManifestConfinedWatchRejectsParentLinkRetargetBeforeRead(t *testing.T) {
	root := makeShortTestTempDir(t)
	insideDirectory := filepath.Join(root, "components")
	if err := os.Mkdir(insideDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`kit.component("tenant-test", {});`)
	inside := filepath.Join(insideDirectory, "component.js")
	if err := os.WriteFile(inside, content, 0o644); err != nil {
		t.Fatal(err)
	}
	authored := filepath.Join(insideDirectory, "component.js")
	resolved, err := filepath.EvalSymlinks(authored)
	if err != nil {
		t.Fatal(err)
	}

	appRuntime := app.NewRuntime("identity-a")
	t.Cleanup(appRuntime.Close)
	siteRuntime, _ := appRuntime.Site(root, "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.Sources().WatchConfinedFileContent(authored, root, resolved, content); err != nil {
		t.Fatal(err)
	}
	generation.Sources().Freeze()

	outsideDirectory := makeShortTestTempDir(t)
	if err := os.Mkdir(filepath.Join(outsideDirectory, "component.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	preparedDirectory := filepath.Join(root, "prepared-components")
	if err := os.Rename(insideDirectory, preparedDirectory); err != nil {
		t.Fatal(err)
	}
	linkKind := makeTestDirectoryLink(t, outsideDirectory, insideDirectory)
	if changed, err := generation.Sources().Changed(); err != nil || !changed {
		t.Fatalf("outside parent-%s retarget: changed=%v err=%v", linkKind, changed, err)
	}
}

func makeShortTestTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "kw-confined-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove test directory %q: %v", directory, err)
		}
	})
	return directory
}

func makeTestDirectoryLink(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return "symlink"
	} else if runtime.GOOS != "windows" {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if strings.ContainsAny(link+target, " \t&|<>^\"") {
		t.Skipf("directory junction fallback requires simple test paths: link=%q target=%q", link, target)
	}
	command := "mklink /J " + link + " " + target
	output, err := exec.Command("cmd", "/c", command).CombinedOutput()
	if err != nil {
		t.Skipf("directory junctions unavailable for %q: %v: %s", command, err, output)
	}
	return "junction"
}

func TestSourceManifestDetectsRouterAndDirectoryAppearance(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "api", "router.kitwork.js")
	if err := os.MkdirAll(filepath.Dir(missing), 0o755); err != nil {
		t.Fatal(err)
	}

	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site(root, "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.Sources().WatchFile(missing); err != nil {
		t.Fatal(err)
	}
	if err := generation.Sources().WatchDirectory(root); err != nil {
		t.Fatal(err)
	}
	generation.Sources().Freeze()

	if err := os.WriteFile(missing, []byte("const x = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := generation.Sources().Changed()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("missing router appearance was not detected")
	}
	generation.Retire()

	directoryGeneration, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := directoryGeneration.Sources().WatchDirectory(root); err != nil {
		t.Fatal(err)
	}
	directoryGeneration.Sources().Freeze()

	if err := os.WriteFile(filepath.Join(root, "page.kitwork.html"), []byte("updated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".persist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if changed, err := directoryGeneration.Sources().Changed(); err != nil || changed {
		t.Fatalf("template/runtime data changed route graph: changed=%v err=%v", changed, err)
	}

	if err := os.Mkdir(filepath.Join(root, "fresh"), 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err = directoryGeneration.Sources().Changed()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("new route directory was not detected")
	}
	directoryGeneration.Retire()
}

func TestSourceManifestDetectsModuleDirectoryMembership(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "first.kitwork.js"), []byte("const x = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}

	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site(root, "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.Sources().WatchModuleDirectory(root); err != nil {
		t.Fatal(err)
	}
	generation.Sources().Freeze()

	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := generation.Sources().Changed(); err != nil || changed {
		t.Fatalf("non-module file changed module graph: changed=%v err=%v", changed, err)
	}
	if err := os.WriteFile(filepath.Join(root, "second.kitwork.js"), []byte("const y = 2;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := generation.Sources().Changed(); err != nil || !changed {
		t.Fatalf("new module was not detected: changed=%v err=%v", changed, err)
	}
	generation.Retire()
}

func TestSourceManifestDetectsTemplateTreeChanges(t *testing.T) {
	root := t.TempDir()
	page := filepath.Join(root, "page.kitwork.html")
	if err := os.WriteFile(page, []byte("template-v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site(root, "example.com")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.Sources().WatchTemplateTree(root); err != nil {
		t.Fatal(err)
	}
	generation.Sources().Freeze()

	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := generation.Sources().Changed(); err != nil || changed {
		t.Fatalf("non-template changed template tree: changed=%v err=%v", changed, err)
	}
	if err := os.WriteFile(page, []byte("template-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := generation.Sources().Changed(); err != nil || !changed {
		t.Fatalf("template edit was not detected: changed=%v err=%v", changed, err)
	}
	generation.Retire()

	addition, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := addition.Sources().WatchTemplateTree(root); err != nil {
		t.Fatal(err)
	}
	addition.Sources().Freeze()
	if err := os.WriteFile(filepath.Join(root, "sidebar.kitwork.html"), []byte("sidebar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := addition.Sources().Changed(); err != nil || !changed {
		t.Fatalf("new template was not detected: changed=%v err=%v", changed, err)
	}
	addition.Retire()
}
