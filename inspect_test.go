package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
)

func TestInspectFileCrossesNativeArtifactBoundary(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "router.kitwork.js")
	helper := filepath.Join(root, "helper.kitwork.js")
	if err := os.WriteFile(helper, []byte(`export const twice = (number) => number * 2;`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(`
import { twice } from "./helper.kitwork.js";
let result = twice(21);
`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := InspectFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if report.ArtifactVersion != compiler.BytecodeArtifactVersion ||
		report.CompilerSchemaVersion != compiler.CompilerSchemaVersion ||
		report.CompilerFingerprint != compiler.Fingerprint() ||
		report.ArtifactBytes == 0 ||
		report.SourceFingerprint == "" ||
		report.CacheKey == "" {
		t.Fatalf("inspection compatibility = %+v", report)
	}
	if len(report.Sources) != 2 || report.Sources[0] != "router.kitwork.js" || report.Sources[1] != "helper.kitwork.js" {
		t.Fatalf("inspection sources = %#v", report.Sources)
	}
	if report.Program.BytecodeVersion != runtime.BytecodeVersion ||
		report.Program.ProgramEncodingVersion != runtime.ProgramEncodingVersion ||
		report.Program.Checksum == "" ||
		len(report.Program.Instructions) == 0 {
		t.Fatalf("program inspection = %+v", report.Program)
	}
}

func TestInspectFileRejectsMissingAndNonKitworkSources(t *testing.T) {
	if _, err := InspectFile(""); err == nil {
		t.Fatal("empty source path was accepted")
	}
	if _, err := InspectFile("router.js"); err == nil {
		t.Fatal("ordinary JavaScript source was accepted")
	}
	if _, err := InspectFile(filepath.Join(t.TempDir(), "missing.kitwork.js")); err == nil {
		t.Fatal("missing source was accepted")
	}
}

// A file of an app resolves its `app/…` imports from the app's folder under inspect as at boot
// (22/10/2026): apps/<identity>/ under the host's apps/, or the host's app/, never a folder named apps
// above the host; its cache key is the name the host's bytecode cache gives it. A file of no app says so.
func TestInspectFileResolvesAppImports(t *testing.T) {
	// the host stands in a folder named apps: counted from the host, that folder is not an app
	host := filepath.Join(t.TempDir(), "apps", "project")
	loose := t.TempDir()
	importing := `import { report } from "app/.shared/report.work";` + "\nlet result = report();"
	for relative, content := range map[string]string{
		"apps/acme/.shared/report.work":    `export const report = () => "acme";`,
		"apps/acme/_cron/daily.kitwork.js": importing,
		"app/.shared/report.work":          `export const report = () => "app";`,
		"app/_cron/mail.kitwork.js":        importing,
	} {
		full := filepath.Join(host, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(loose, "daily.kitwork.js"), []byte(importing), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(host)

	for _, c := range []struct{ entry, app string }{
		{"apps/acme/_cron/daily.kitwork.js", "apps/acme"},
		{"app/_cron/mail.kitwork.js", "app"},
	} {
		entry := filepath.Join(host, filepath.FromSlash(c.entry))
		appRoot := filepath.Join(host, filepath.FromSlash(c.app))
		report, err := InspectFile(entry)
		if err != nil {
			t.Fatalf("%s: %v", c.entry, err)
		}
		if len(report.Sources) != 2 || report.Sources[1] != "../.shared/report.work" {
			t.Fatalf("%s: sources = %#v, want the app's .shared/report.work", c.entry, report.Sources)
		}
		compiled, err := compiler.CompileFileInApp(appRoot, entry)
		if err != nil {
			t.Fatal(err)
		}
		if report.CacheKey != compiled.CacheKeyInApp(appRoot) {
			t.Fatalf("%s: cache key = %q, want the host cache's name %q", c.entry, report.CacheKey, compiled.CacheKeyInApp(appRoot))
		}
	}

	_, err := InspectFile(filepath.Join(loose, "daily.kitwork.js"))
	want := `daily.kitwork.js:1:24: import "app/.shared/report.work": file này không thuộc app nào nên không có app/`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("no app: error = %v, want %q", err, want)
	}
}
