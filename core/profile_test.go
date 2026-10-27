package core

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/work"
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

	write("identity/.shared/math.work", `export const add = (a, b) => a + b;`)
	write("identity/site/router.work", `import * as router from "kitwork/router";
import { add } from "../.shared/math.work";
router.get((context) => context.json({ answer: add(20, 22) }));
`)
	write("identity/site/about/router.work", `import * as router from "kitwork/router";
router.get((context) => context.text("about"));
`)
	write("identity/.schedule/pulse.work", `const pulse = () => 42;`)
	write("identity/.shared/queue/mail.work", `const send = (message) => message;`)
	write("identity/.shared/queue/config.work", "import * as queue from \"kitwork/queue\";\nqueue.parallel(2);\n")
	write("identity/.shared/lib.work", `export const helper = () => 1;`)
	write("identity/.shared/ignored.work", `const helper = () => 1;`)
	write("identity/site/broken/router.work", `const broken = ;`)
	write("test/ignored/router.work", `const ignored = 1;`)

	report := Profile(root)
	if report.Root != root || report.Layout != "auto" {
		t.Fatalf("profile source = %q (%s), want %q (auto)", report.Root, report.Layout, root)
	}
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
		report.Issues[0].File != "identity/site/broken/router.work" {
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
		if program.File == "identity/.shared/ignored.work" {
			t.Fatal("helper module was profiled as an executable entrypoint")
		}
	}
}

func TestProfileWithLayoutProfilesOnlyExecutableRootShape(t *testing.T) {
	t.Run("single app", func(t *testing.T) {
		root := t.TempDir()
		writeProfileSource(t, root, work.UIRouterFileName, profileRouterSource("home"))
		writeProfileSource(t, root, "about/router.work", profileRouterSource("about"))
		writeProfileSource(t, root, ".schedule/pulse.work", `const pulse = () => 1;`)
		writeProfileSource(t, root, "_cron/old.kitwork.js", `const old = () => 1;`)
		writeProfileSource(t, root, "_private/router.work", profileRouterSource("private"))

		report := ProfileWithLayout(root, work.RootLayoutSingle)
		assertProfileFiles(t, report, []string{
			".schedule/pulse.work",
			"about/router.work",
			"router.work",
		})
	})

	t.Run("multi domain app", func(t *testing.T) {
		root := t.TempDir()
		writeProfileSource(t, root, ".shared/queue/mail.work", `const mail = () => 1;`)
		writeProfileSource(t, root, "_queue/old.kitwork.js", `const old = () => 1;`)
		writeProfileSource(t, root, "one.example/router.work", profileRouterSource("one"))
		writeProfileSource(t, root, "one.example/about/router.work", profileRouterSource("about"))
		writeProfileSource(t, root, "identity/nested.example/router.work", profileRouterSource("nested"))
		writeProfileSource(t, root, "sites/legacy.example/router.work", profileRouterSource("legacy"))

		report := ProfileWithLayout(root, work.RootLayoutMultiDomain)
		assertProfileFiles(t, report, []string{
			".shared/queue/mail.work",
			"one.example/about/router.work",
			"one.example/router.work",
		})
	})

	t.Run("multi tenant", func(t *testing.T) {
		root := t.TempDir()
		writeProfileSource(t, root, "first/.schedule/pulse.work", `const pulse = () => 1;`)
		writeProfileSource(t, root, "first/one.example/router.work", profileRouterSource("one"))
		writeProfileSource(t, root, "first/one.example/about/router.work", profileRouterSource("about"))
		writeProfileSource(t, root, "second/two.example/router.work", profileRouterSource("two"))
		writeProfileSource(t, root, "sites/legacy.example/router.work", profileRouterSource("legacy"))

		report := ProfileWithLayout(root, work.RootLayoutMultiTenant)
		assertProfileFiles(t, report, []string{
			"first/.schedule/pulse.work",
			"first/one.example/about/router.work",
			"first/one.example/router.work",
			"second/two.example/router.work",
		})
	})
}

func writeProfileSource(t testing.TB, root, relative, source string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func profileRouterSource(body string) string {
	return `import * as router from "kitwork/router";
router.get((context) => context.text("` + body + `"));`
}

func assertProfileFiles(t testing.TB, report ProfileReport, want []string) {
	t.Helper()
	if !report.OK() {
		t.Fatalf("profile issues = %#v", report.Issues)
	}
	if report.Layout == "" {
		t.Fatal("profile did not expose its root layout")
	}
	got := make([]string, 0, len(report.Programs))
	for _, program := range report.Programs {
		got = append(got, program.File)
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("profile files = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("profile files = %v, want %v", got, want)
		}
	}
	if report.Entrypoints != len(want) || report.ProgramCount != len(want) {
		t.Fatalf(
			"profile counts = %d entrypoints / %d programs, want %d",
			report.Entrypoints,
			report.ProgramCount,
			len(want),
		)
	}
}
