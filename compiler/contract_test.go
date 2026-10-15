package compiler

import (
	"encoding/hex"
	"testing"
)

func TestCompilerV9GoldenContract(t *testing.T) {
	if CompilerSchemaVersion != 9 {
		t.Fatalf("CompilerSchemaVersion = %d, want schema v9", CompilerSchemaVersion)
	}
	// Bytecode 3 (14/10/2026) moved the fingerprint and every checksum below with the version alone: each
	// fixture's instructions, constants and debug table are those bytecode 2 wrote (none needs a wide
	// form). A schema-8 artifact of bytecode 2 is not reused. Schema 9 (the same day) moved the fingerprint
	// alone: no fixture before it uses x++ as a value, typeof, continue, or a write to a member or an index.
	const expectedFingerprint = "a7a9f8ffe16e9318e7356a4944f3d92a057da79e0b61b530e136038986c9e2b6"
	if got := Fingerprint(); got != expectedFingerprint {
		t.Fatalf("compiler fingerprint = %q, want %q", got, expectedFingerprint)
	}
	// schema 8 on bytecode 3 gave x++ the new value: its artifacts are not reused
	const schema8 = "5fbf4f10a82a5a6d4a7f3c1f2a5c727ccef765a7947729a5b2430e0a8964db55"
	if stamped := compilerFingerprintFor(8); hex.EncodeToString(stamped[:]) != schema8 || Fingerprint() == schema8 {
		t.Fatalf("schema 8 on bytecode 3 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema8)
	}
	const schema8OnBytecode2 = "be7e49394fb709e1b0adeba3731263930c05631b609763361a95ccd89a276ef9"
	if stamped := bytecode2FingerprintFor(8); hex.EncodeToString(stamped[:]) != schema8OnBytecode2 || Fingerprint() == schema8OnBytecode2 {
		t.Fatalf("schema 8 on bytecode 2 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema8OnBytecode2)
	}

	fixtures := []struct {
		name     string
		source   string
		checksum string
	}{
		{
			name:     "arithmetic",
			source:   `var result = (40 + 4) / 2;`,
			checksum: "733480ea18d10518f868bfd3a4f6e8eb4c4dc820674da018690e87c7d070ee1b",
		},
		{
			name: "closures-and-callbacks",
			source: `
const make = (base) => (number) => base + number;
var result = [1, 2, 3].map((item) => make(10)(item)).join(",");
`,
			checksum: "2cd9d9cbbffb8b5b2bb0dd0dfd87f78c8efedd1c7d3619b8131c80565344bd71",
		},
		{
			name: "bounded-control-flow",
			source: `
var result = 0;
for (let index = 0; index < 8; index++) {
	if (index > 3) { result = result + index; }
}
`,
			// Schema 6: the counter lives in the loop's block (slot index#N).
			checksum: "239055553636276f74a2d35e00d5bc620e9d2cacd63ce3effc3128a9052b2a09",
		},
		{
			name: "switch-fallthrough",
			source: `
var result = "";
switch (2) {
case 1:
case 2:
	result = result + "a";
case 3:
	result = result + "b";
	break;
default:
	result = "wrong";
}
`,
			checksum: "14a7d153ec30d7bb0d39f7173f5e77e80c6c5223043f92c7ca9c09f4b9ba4d2a",
		},
		{
			// Schema 7: safe is a global function the VM calls like any other; { answer: 42 }.safe() was
			// a lowering into a hidden evaluator lambda, and is a compile error now.
			name:     "safe-global",
			source:   `var result = safe(() => ({ answer: 42 })).value.answer;`,
			checksum: "b26c0152c54f3ce0a515e34d0d3df7ac6846a9134f17973fca0259b018366bc7",
		},
		{
			// Schema 7: a failure inside safe is { value: null, error: { message, code, at } }.
			name:     "safe-failure",
			source:   "const check = safe(() => fail(\"hết hàng\"));\nvar result = [check.value, check.error.code, check.error.message, check.error.at];",
			checksum: "4186bfc371aeffc87406628dde04ff0548faf1fc40d4c4812602aa5036158d38",
		},
		{
			// Schema 4: a reserved word is a property name, after a dot and as a key. Gives 10;
			// schema 3 read o.undefined as the key "null" and gave 13.
			name: "reserved-word-keys",
			source: `
const o = { undefined: 1, true: 2, false: 3, null: 4 };
var result = o.undefined + o.true + o["false"] + o.null;
`,
			checksum: "e99da29985bd83d035eead0ac4c4d11c9144a46234c33b022c05ba9aac379e85",
		},
		{
			// Schema 5: && and || run their right side only when needed (a constant or a name on the
			// right keeps AND/OR), a condition branches on them, ?? and ?. test for null, and CALL/INVOKE
			// sit where the call is.
			name: "short-circuit-and-null",
			source: `
let calls = 0;
const touch = () => { calls = calls + 1; return calls; };
const o = { a: { b: 2 } };
const none = null;
var value = (false && touch()) || none?.a.b || (o?.a?.["b"] ?? touch());
if (value && touch() || none) { calls = calls + 10; }
var result = [value, calls, none ?? "x", 0 ?? 1, o.a.missing?.(), true && "plain"];
`,
			checksum: "cae90845c161448afe98cd3fa791567f3d226f7491768a5fa6bf16faf5e54c0e",
		},
		{
			// Schema 5: renaming, defaults (missing or null), nested patterns and assignment patterns.
			name: "destructuring",
			source: `
const { a: first, b = 2, c: { d } = { d: 4 }, "e-f": ef = null } = { a: 1, c: null };
let x = 0;
let y = 0;
[x, y = 9] = [5];
var result = [first, b, d, ef, x, y];
`,
			checksum: "390d6e28cea7035d7c72588871764cd4129ded8f221ed72ccf66a4f2c2f1bd97",
		},
		{
			// Schema 6: a const of a block is that block's (x#N), a nested function's declaration and an
			// assigned parameter stay in their function (error@N, copied as the function starts), so the
			// outer variables keep their values. Gives [1, 2, ["outer", "p!", "inner"]].
			name: "block-scope",
			source: `
let x = 1;
let seen = 0;
if (x > 0) {
	const x = 2;
	seen = x;
}
const outer = () => {
	let error = "outer";
	const inner = (error) => { error = error + "!"; return error; };
	const save = () => { const { error } = { error: "inner" }; return error; };
	return [error, inner("p"), save()];
};
var result = [x, seen, outer()];
`,
			checksum: "8e87b00c8ea5acf6c21e5e55262b3d66068c8fcf0bbb4e4b3d4ced36dfdfb23b",
		},
		{
			// Schema 6: a closure made in a loop keeps its iteration's variable (an object per iteration,
			// handed in by a call). Gives ["a", "b", 0, 1].
			name: "closures-per-iteration",
			source: `
const made = [];
for (const item of ["a", "b"]) {
	made.push(() => item);
}
for (let index = 0; index < 2; index++) {
	made.push(() => index);
}
var result = made.map((read) => read());
`,
			checksum: "842950c09b3fc7790bff5bbc26e5b3526e250b5c8172619e4d6bbcab6f72a015",
		},
		{
			// Schema 8: a template that starts with two values starts from "", so it joins them as
			// text ("12"); an operator, a member read and an array literal record their own place.
			name:     "template-text",
			source:   "const a = 1;\nconst b = 2;\nvar result = [`${a}${b}`, [a, b].length - 1];",
			checksum: "cea3b73739bf43697f8d9061a2debd81317deab1043d26150cef47cf3ee1a1e9",
		},
		{
			// Schema 9: count++ as a value is the old count (read, update, POP), ++count the new one,
			// continue jumps to for…of's next item, typeof is PUSH "#typeof"; INVOKE 0, and -1 is true.
			// Gives [0, 2, "number", "undefined", [1, 3], -1].
			name:     "update-continue-typeof",
			source:   "let count = 0;\nconst next = () => count++;\nconst seen = [];\nfor (const item of [1, 2, 3, 4]) {\n\tif (item % 2 == 0) { continue; }\n\tseen.push(item);\n}\nvar result = [next(), ++count, typeof count, typeof missing, seen, -1 || 5];\n",
			checksum: "c03f86cbe4bbefa1df69dfda1f5f5df87e04f6b4c16a4d3f09a194d2a9f9f4d1",
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			compiled, err := CompileSource(fixture.source)
			if err != nil {
				t.Fatal(err)
			}
			if got := compiled.Program.Checksum(); got != fixture.checksum {
				t.Fatalf("Program checksum = %q, want %q", got, fixture.checksum)
			}
			if err := ValidateArtifact(compiled); err != nil {
				t.Fatalf("golden artifact compatibility: %v", err)
			}
		})
	}
}
