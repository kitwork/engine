package javascript

import (
	"strings"
	"testing"
)

func TestStagedComponentSourceRequiresLexicallyContainedFunctionBody(t *testing.T) {
	valid := []string{
		`;kit.component("tenant-x", { text: "});", value: total / count });` + "\n",
		`;var await = 8; kit.component("tenant-x", { value: (await) / 2 });` + "\n",
		";kit.component(\"tenant-x\", { pattern: /^[})]+$/, value: `raw }); ${({ nested: true }).nested}` });\n",
		";kit.component(\"tenant-x\", { value: 1 }); /* }); ignored */\n",
		";kit.component(\"tenant-x\", { value: 1 }); // }); ignored\n",
		";async function collect(items) { for await (var item of items) /x/.test(item); }\n" +
			"kit.component(\"tenant-x\", {});\n",
		";class Matcher extends /x/.constructor {}\nkit.component(\"tenant-x\", {});\n",
		";if\u2028(true) /x/.test(\"x\");\nkit.component(\"tenant-x\", {});\n",
		";function decide(π) { π\u2028return /x/.test(\"x\"); }\nkit.component(\"tenant-x\", {});\n",
		";function decide(name) { name\u2028return /x/.test(name); }\nkit.component(\"tenant-x\", {});\n",
		";function decide(name) { /x/\u2028return /y/.test(name); }\nkit.component(\"tenant-x\", {});\n",
		";for (;;) { break\n/x/.test(\"x\"); }\nkit.component(\"tenant-x\", {});\n",
		";for (;;) { continue\n/x/.test(\"x\"); }\nkit.component(\"tenant-x\", {});\n",
		";for (;;) { break\nif (true) /x/.test(\"x\"); }\nkit.component(\"tenant-x\", {});\n",
		";for (;;) { break /*\n*/ if (true) /x/.test(\"x\"); }\nkit.component(\"tenant-x\", {});\n",
		";loop: for (;;) { break loop; /x/.test(\"x\"); }\nkit.component(\"tenant-x\", {});\n",
	}
	for index, source := range valid {
		if err := validateStagedComponentSource("tenant-x", []byte(source)); err != nil {
			t.Fatalf("valid source %d rejected: %v", index, err)
		}
	}

	invalid := []string{
		";});\n",
		";var await = 1; await / 1; }); /marker/\n",
		";// comment ends at carriage return\r});\n",
		";var of = 1; of / 1;\n",
		";loop: for (;;) { break loop\n/x/.test(\"x\"); }\n",
		";loop: for (;;) { continue loop /*\n*/ /x/.test(\"x\"); }\n",
		";loop: for (;;) { break \\u006c\\u006f\\u006f\\u0070; }\n",
		";debugger\n/x/.test(\"x\");\n",
		";kit.component(\"tenant-x\", { value: 1 );\n",
		";kit.component(\"tenant-x\", { value: `unterminated });\n",
		";kit.component(\"tenant-x\", { value: /unterminated });\n",
		";kit.component(\"tenant-x\", {}); /*\n",
		";(function () {\n",
	}
	for index, source := range invalid {
		if err := validateStagedComponentSource("tenant-x", []byte(source)); err == nil {
			t.Fatalf("non-contained source %d was accepted", index)
		}
	}
}

func TestBuildStagedRejectsNonContainedComponentBeforeArtifactAssembly(t *testing.T) {
	for index, source := range []string{
		";});\n",
		";var await = 1; await / 1; }); /marker/\n",
	} {
		_, err := BuildStaged(StagedBuildOptions{
			Profile: ProfileKit,
			Components: []ComponentPackage{{
				Name: "tenant-x", Version: "1.0.0", Source: []byte(source),
			}},
		})
		if err == nil || !strings.Contains(err.Error(), "not lexically contained") {
			t.Fatalf("BuildStaged source %d error = %v", index, err)
		}
	}
}

func TestStagedPackageSuffixAccepts128BytesAndRejects129(t *testing.T) {
	name128 := "a" + strings.Repeat("b", MaxStagedPackageSuffixBytes-1)
	source128 := []byte(`;kit.component("` + name128 + `", {});` + "\n")
	assembly, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileKit,
		Components: []ComponentPackage{{
			Name: name128, Version: "1.0.0", Source: source128,
		}},
	})
	if err != nil {
		t.Fatalf("128-byte component name rejected: %v", err)
	}
	if len(assembly.Components) != 1 || assembly.Components[0].Suffix() != name128 {
		t.Fatalf("128-byte component artifact = %#v", assembly.Components)
	}

	name129 := name128 + "c"
	_, err = BuildStaged(StagedBuildOptions{
		Profile: ProfileKit,
		Components: []ComponentPackage{{
			Name: name129, Version: "1.0.0", Source: []byte(`;kit.component("x", {});` + "\n"),
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be represented by a staged asset suffix") {
		t.Fatalf("129-byte component error = %v", err)
	}
}
