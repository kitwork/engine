package engine

import (
	"os"
	"path/filepath"
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
