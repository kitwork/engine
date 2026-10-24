package compiler

import (
	"encoding/hex"
	"testing"
)

func TestCompilerV22GoldenContract(t *testing.T) {
	if CompilerSchemaVersion != 22 {
		t.Fatalf("CompilerSchemaVersion = %d, want schema v22", CompilerSchemaVersion)
	}
	// Bytecode 3 (14/10/2026) moved the fingerprint and every checksum below with the version alone: each
	// fixture's instructions, constants and debug table are those bytecode 2 wrote (none needs a wide
	// form). A schema-8 artifact of bytecode 2 is not reused. Schema 9 (the same day) moved the fingerprint
	// alone: no fixture before it uses x++ as a value, typeof, continue, or a write to a member or an index.
	// Schema 10 (15/10/2026) moved the fingerprint alone too: no fixture before it updates a member or an
	// item through a call or an update, or breaks a line before ++. Schema 11 on bytecode 4 (15/10/2026,
	// STEP) moved the fingerprint and every checksum with the version; the fixtures with ++ or -- hold
	// STEP where they held PUSH 1; ADD. Schema 12 (22/10/2026, database statements translated when they
	// compile) moved the fingerprint alone: no fixture imports a database. Schema 13 (22/10/2026, the older
	// database spellings removed) moved the fingerprint alone: no fixture imports one. Schema 14 (22/10/2026, a
	// job named by its export) moved the fingerprint alone: no fixture imports a job file or aliases an export.
	// Schema 15 (22/10/2026, a schedule named by its export) moved the fingerprint alone: no fixture imports a
	// file of .schedule/ or writes cron. Schema 16 (22/10/2026, go moves into kitwork/routine) moved the
	// fingerprint alone: no fixture writes go or imports kitwork/routine. Schema 17 (22/10/2026, cache and sse
	// replaced by kitwork/storage) moved the fingerprint alone: no fixture writes cache or sse. Schema 18
	// (22/10/2026, the older words of kitwork/file refused) moved the fingerprint alone: no fixture imports
	// kitwork/file. Schema 19 (22/10/2026, environment read by name) moved the fingerprint alone: no fixture
	// reads env or environment. Schema 20 (23/10/2026, router.view removed) moved the fingerprint alone: no
	// fixture writes router.view. Schema 21 (24/10/2026, a folder's config.work by calls) moved the fingerprint
	// alone: no fixture is a config.work or calls queue.parallel, queue.keep or schedule.keep. Schema 22
	// (24/10/2026, queue.database(db) checked where it is written) moved the fingerprint alone: no fixture is a
	// config.work or calls queue.database.
	const expectedFingerprint = "58dc706d807f0a7e734138dc99273ed41acc619d8ba98e1ca7904daf69a47b93"
	if got := Fingerprint(); got != expectedFingerprint {
		t.Fatalf("compiler fingerprint = %q, want %q", got, expectedFingerprint)
	}
	// schema 21 compiled what a config.work handed queue.database(…) or schedule.database(…) unchecked: its
	// artifacts are not reused
	const schema21 = "15285bc1c3aa7374cd2260fc681dd61d42837f62b4227dc70e66c9b36da7622c"
	if stamped := compilerFingerprintFor(21); hex.EncodeToString(stamped[:]) != schema21 || Fingerprint() == schema21 {
		t.Fatalf("schema 21 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema21)
	}
	// schema 20 compiled a config.work of .shared/queue/ or .schedule/ into an object of its top-level names,
	// which the loader no longer reads: its artifacts are not reused
	const schema20 = "0e2a6963f9bd8c84bf075a9c18fb428054db413e81379e9767f10efd99e6db3e"
	if stamped := compilerFingerprintFor(20); hex.EncodeToString(stamped[:]) != schema20 || Fingerprint() == schema20 {
		t.Fatalf("schema 20 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema20)
	}
	// schema 19 compiled router.view in a router.work, which the router now refuses only where it runs: its
	// artifacts are not reused
	const schema19 = "ede67c227333423db96f7cd76c0913c73308f7db9604d31350aa4d5c3f043cfa"
	if stamped := compilerFingerprintFor(19); hex.EncodeToString(stamped[:]) != schema19 || Fingerprint() == schema19 {
		t.Fatalf("schema 19 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema19)
	}
	// schema 18 compiled env of "kitwork" in an app and environment read whole: its artifacts are not reused
	const schema18 = "14263ce6a384ce77df0a1c5ed405357708c7059fb4a3df38e19a6544df8ac47d"
	if stamped := compilerFingerprintFor(18); hex.EncodeToString(stamped[:]) != schema18 || Fingerprint() == schema18 {
		t.Fatalf("schema 18 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema18)
	}
	// schema 16 compiled cache and sse off "kitwork", which now fail only where they are read: its artifacts
	// are not reused
	const schema16 = "1fd92fc738cb012e76c3a1f08c73bb3a6bf6670c76388972c7470b70d142413b"
	if stamped := compilerFingerprintFor(16); hex.EncodeToString(stamped[:]) != schema16 || Fingerprint() == schema16 {
		t.Fatalf("schema 16 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema16)
	}
	// schema 15 compiled go(fn) bare, go off "kitwork" and a function of routine.go that uses the request: its
	// artifacts are not reused
	const schema15 = "d17e01ab067f55b6e08ac8d15e496fa6757558ab847e20f0a608b256391996af"
	if stamped := compilerFingerprintFor(15); hex.EncodeToString(stamped[:]) != schema15 || Fingerprint() == schema15 {
		t.Fatalf("schema 15 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema15)
	}
	// schema 14 bundled a file of .schedule/ without naming its schedules, and compiled cron: its artifacts
	// are not reused
	const schema14 = "1e759d93d93eab3e52c327ec335937365eea20d8620569395b51ce9b05ec8d5c"
	if stamped := compilerFingerprintFor(14); hex.EncodeToString(stamped[:]) != schema14 || Fingerprint() == schema14 {
		t.Fatalf("schema 14 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema14)
	}
	// schema 13 bundled a job file without naming its jobs, which send() refuses: its artifacts are not reused
	const schema13 = "89008e01476efea5799b15e8c346b46f275806b5aaafff5275763c3b70672c9b"
	if stamped := compilerFingerprintFor(13); hex.EncodeToString(stamped[:]) != schema13 || Fingerprint() == schema13 {
		t.Fatalf("schema 13 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema13)
	}
	// schema 12 still compiled the older spellings of the database module, which now fail only where they
	// are read: its artifacts are not reused
	const schema12 = "9b101149799e891bcf601468edd50f9533c3dbe0896f9ea380220ebcc423abb3"
	if stamped := compilerFingerprintFor(12); hex.EncodeToString(stamped[:]) != schema12 || Fingerprint() == schema12 {
		t.Fatalf("schema 12 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema12)
	}
	// schema 11 on bytecode 4 compiled a statement's lambdas, which the database handle refuses: its
	// artifacts are not reused
	const schema11 = "de39cf778611ad4470b339dda95d37586b7c0e91f6d64989877df3b34e791450"
	if stamped := compilerFingerprintFor(11); hex.EncodeToString(stamped[:]) != schema11 || Fingerprint() == schema11 {
		t.Fatalf("schema 11 on bytecode 4 stamps %x, the current compiler %s: want %s and another", stamped, Fingerprint(), schema11)
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
