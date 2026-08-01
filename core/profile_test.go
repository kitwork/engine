package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
)

func TestProfileCompilesOnlyExecutableEntrypoints(t *testing.T) {
	root := t.TempDir()
	write := func(relative, source string) {
		t.Helper()
		file := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("identity/site/_core/math.kitwork.js", `export const add = (a, b) => a + b;`)
	write("identity/site/router.kitwork.js", `
import { router } from "kitwork";
import { add } from "./_core/math.kitwork.js";
router.get((ctx) => ctx.json({ answer: add(20, 22) }));
`)
	write("identity/site/about/router.kitwork.js", `
import { router } from "kitwork";
router.get((ctx) => ctx.text("about"));
`)
	write("identity/_cron/pulse.kitwork.js", `const pulse = () => 42;`)
	write("identity/_queue/mail.kitwork.js", `const send = (message) => message;`)
	write("identity/site/_core/ignored.kitwork.js", `const helper = () => 1;`)
	write("identity/site/broken/router.kitwork.js", `const broken = ;`)
	write("test/ignored/router.kitwork.js", `const ignored = 1;`)

	report := Profile(root)
	if report.Entrypoints != 5 {
		t.Fatalf("entrypoints = %d, want 5", report.Entrypoints)
	}
	if report.ProgramCount != 4 || len(report.Programs) != 4 {
		t.Fatalf("program count = %d (%d profiles), want 4", report.ProgramCount, len(report.Programs))
	}
	if report.CompatiblePrograms != report.ProgramCount ||
		report.BytecodeVersion != runtime.BytecodeVersion ||
		report.ProgramEncodingVersion != runtime.ProgramEncodingVersion ||
		report.ArtifactVersion != compiler.BytecodeArtifactVersion ||
		report.CompilerSchemaVersion != compiler.CompilerSchemaVersion ||
		report.CompilerFingerprint == "" ||
		report.InstructionSetChecksum == "" {
		t.Fatalf("incomplete compatibility contract = %#v", report)
	}
	if len(report.Issues) != 1 ||
		report.Issues[0].File != "identity/site/broken/router.kitwork.js" {
		t.Fatalf("issues = %#v", report.Issues)
	}
	if report.BytecodeBytes == 0 ||
		report.Instructions == 0 ||
		report.Constants == 0 ||
		report.EntryPoints == 0 ||
		report.MaxStackDepth == 0 ||
		len(report.Opcodes) == 0 {
		t.Fatalf("incomplete report = %#v", report)
	}
	if report.Programs[0].Profile.BytecodeBytes <
		report.Programs[len(report.Programs)-1].Profile.BytecodeBytes {
		t.Fatal("programs are not sorted by descending bytecode size")
	}
	for _, program := range report.Programs {
		if program.File == "identity/site/_core/ignored.kitwork.js" {
			t.Fatal("helper module was profiled as an executable entrypoint")
		}
	}
}
