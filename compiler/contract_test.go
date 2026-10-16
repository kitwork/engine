package compiler

import (
	"encoding/hex"
	"testing"
)

func TestCompilerV11GoldenContract(t *testing.T) {
	if CompilerSchemaVersion != 11 {
		t.Fatalf("CompilerSchemaVersion = %d, want schema v11", CompilerSchemaVersion)
	}
	// Bytecode 3 (14/10/2026) moved the fingerprint and every checksum below with the version alone: each
	// fixture's instructions, constants and debug table are those bytecode 2 wrote (none needs a wide
	// form). A schema-8 artifact of bytecode 2 is not reused. Schema 9 (the same day) moved the fingerprint
	// alone: no fixture before it uses x++ as a value, typeof, continue, or a write to a member or an index.
	// Schema 10 (15/10/2026) moved the fingerprint alone too: no fixture before it updates a member or an
	// item through a call or an update, or breaks a line before ++. Schema 11 on bytecode 4 (15/10/2026,
	// STEP) moved the fingerprint and every checksum with the version; the fixtures with ++ or -- hold
	// STEP where they held PUSH 1; ADD.
	const expectedFingerprint = "de39cf778611ad4470b339dda95d37586b7c0e91f6d64989877df3b34e791450"
	if got := Fingerprint(); got != expectedFingerprint {
		t.Fatalf("compiler fingerprint = %q, want %q", got, expectedFingerprint)
	}
	// schema 10 on bytecode 3 made x++ x = x + 1, which joins "5"++ into "51": its artifacts are not reused
	const schema10 = "308eee4d8c849346f04f0b247289f1327411c01686ee1e12c5da91bc27a3eeb9"
	if stamped := bytecode3FingerprintFor(10); hex.EncodeToString(stamped[:]) != schema10 || Fingerprint() == schema10 {
		t.Fatalf("schema 10 on bytecode 3 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema10)
	}
	// schema 9 on bytecode 3 evaluated the object and key of a[i++]++ again: its artifacts are not reused
	const schema9 = "a7a9f8ffe16e9318e7356a4944f3d92a057da79e0b61b530e136038986c9e2b6"
	if stamped := bytecode3FingerprintFor(9); hex.EncodeToString(stamped[:]) != schema9 || Fingerprint() == schema9 {
		t.Fatalf("schema 9 on bytecode 3 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema9)
	}
	// schema 8 on bytecode 3 gave x++ the new value: its artifacts are not reused
	const schema8 = "5fbf4f10a82a5a6d4a7f3c1f2a5c727ccef765a7947729a5b2430e0a8964db55"
	if stamped := bytecode3FingerprintFor(8); hex.EncodeToString(stamped[:]) != schema8 || Fingerprint() == schema8 {
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
			checksum: "81be0d64632556c7bf522f741f4423aaffe10f34ba97e1b01ca82fcfbea3a329",
		},
		{
			name: "closures-and-callbacks",
			source: `
const make = (base) => (number) => base + number;
var result = [1, 2, 3].map((item) => make(10)(item)).join(",");
`,
			checksum: "3afb981716cda7ab96a083403590f68b019e08ba8f66f6e3e2e897a1917d13aa",
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
			checksum: "d61250aeeafe5ccfea87cd4fd4be8f470a2feaf47ec6e49a787a560befb0a4bf",
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
			checksum: "385681132fe07924cc017124cd017c4f3b4cbdc8e2f746bfba9e8d36c52866ba",
		},
		{
			// Schema 7: safe is a global function the VM calls like any other; { answer: 42 }.safe() was
			// a lowering into a hidden evaluator lambda, and is a compile error now.
			name:     "safe-global",
			source:   `var result = safe(() => ({ answer: 42 })).value.answer;`,
			checksum: "eb639fe395978be26e7f1e7ac7d9ccbfeb8340bc3622256f48cf96e71b0e68ff",
		},
		{
			// Schema 7: a failure inside safe is { value: null, error: { message, code, at } }.
			name:     "safe-failure",
			source:   "const check = safe(() => fail(\"hết hàng\"));\nvar result = [check.value, check.error.code, check.error.message, check.error.at];",
			checksum: "cd91e0a6752c7e161ba5c56aed6bf75971d11ddd57aef1d37e5f5e1134c02a85",
		},
		{
			// Schema 4: a reserved word is a property name, after a dot and as a key. Gives 10;
			// schema 3 read o.undefined as the key "null" and gave 13.
			name: "reserved-word-keys",
			source: `
const o = { undefined: 1, true: 2, false: 3, null: 4 };
var result = o.undefined + o.true + o["false"] + o.null;
`,
			checksum: "4ea26f0ae24b88b33c062ff5bc39c9f44443c101300ba0035e1647f913822924",
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
			checksum: "9f31b38f80cf19848f7892d870ac2f837bceee9ad349b44265bbe826d957590e",
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
			checksum: "295950b881194fcfcf76e03c08b4ec60412ddc88d5f67d6101dc7f04eabee581",
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
			checksum: "2e9cbaf0b268cd16ccbcaf4ff32dbeb3c5e1314c47d22895e80d3ebba2c1e32e",
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
			checksum: "6d201ea9d9e64873ec7ccab33848c913def584adb613d935069983ff5a6e2959",
		},
		{
			// Schema 8: a template that starts with two values starts from "", so it joins them as
			// text ("12"); an operator, a member read and an array literal record their own place.
			name:     "template-text",
			source:   "const a = 1;\nconst b = 2;\nvar result = [`${a}${b}`, [a, b].length - 1];",
			checksum: "25d7b197dab28e7bea92b12107defdd0e5c7d0027ce2ae271ebbbae8539f5fcd",
		},
		{
			// Schema 9: count++ as a value is the old count (read, update, POP), ++count the new one,
			// continue jumps to for…of's next item, typeof is PUSH "#typeof"; INVOKE 0, and -1 is true.
			// Gives [0, 2, "number", "undefined", [1, 3], -1].
			name:     "update-continue-typeof",
			source:   "let count = 0;\nconst next = () => count++;\nconst seen = [];\nfor (const item of [1, 2, 3, 4]) {\n\tif (item % 2 == 0) { continue; }\n\tseen.push(item);\n}\nvar result = [next(), ++count, typeof count, typeof missing, seen, -1 || 5];\n",
			checksum: "74b5ff6aba147bb8b2a8db497dbb3d84b37ad49da0ad67843708aa4ce28e87d3",
		},
		{
			// Schema 10: the object and key of an update or a compound assignment that do more than read
			// run once, into hidden slots, and a line break before ++ ends the expression. Gives
			// [[2, 2, 3], 1, 1, 1, [2, 6]].
			name:     "update-once-line-break",
			source:   "const a = [1, 2, 3];\nlet i = 0;\na[i++]++;\nlet calls = 0;\nconst pick = () => { calls = calls + 1; return a; };\npick()[1] += 0;\nlet x = 1;\nlet y = 5;\nx\n++y\nvar result = [a, i, calls, x, [i + 1, y]];\n",
			checksum: "97495a333d35dca31d8e8161d626d4776224df68ecce0ef699bb68bb071a47b6",
		},
		{
			// Schema 11 (bytecode 4): ++ and -- make a number of what they step (STEP), the old value of
			// x++ too, and a division or a remainder by zero is Infinity or NaN. Gives
			// [5, 6, -1, 2, Infinity, NaN].
			name:     "step-and-division-by-zero",
			source:   "let text = \"5\";\nconst old = text++;\nlet missing = null;\nmissing--;\nlet flag = true;\n++flag;\nvar result = [old, text, missing, flag, 1 / 0, 7 % 0];\n",
			checksum: "67ad8f58e71a9b8577d730d74e2211f7abe7d5f71531c594026c924c9b5ee410",
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
