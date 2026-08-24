package compiler

import "testing"

func TestCompilerV3GoldenContract(t *testing.T) {
	if CompilerSchemaVersion != 3 {
		t.Fatalf("CompilerSchemaVersion = %d, want schema v3", CompilerSchemaVersion)
	}
	const expectedFingerprint = "f0c6c9255af80f7d2109348a23f49281ca50935b9bb8e41cbcf4970d7fcd4c07"
	if got := Fingerprint(); got != expectedFingerprint {
		t.Fatalf("compiler fingerprint = %q, want %q", got, expectedFingerprint)
	}

	fixtures := []struct {
		name     string
		source   string
		checksum string
	}{
		{
			name:     "arithmetic",
			source:   `var result = (40 + 4) / 2;`,
			checksum: "d3194cab2db755a50851ea1fb82537d55d0fbb5815661c03ce9ec20227ff0beb",
		},
		{
			name: "closures-and-callbacks",
			source: `
const make = (base) => (number) => base + number;
var result = [1, 2, 3].map((item) => make(10)(item)).join(",");
`,
			checksum: "2ddc9462ad8313e3a0069906afe9af97690fa0bfa954fff96c19c2c78235edb7",
		},
		{
			name: "bounded-control-flow",
			source: `
var result = 0;
for (let index = 0; index < 8; index++) {
	if (index > 3) { result = result + index; }
}
`,
			checksum: "ec157a3a5a8d0eb82627835b45ce79979477d8c941e2078963f3790e42627266",
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
			checksum: "7c13741c452fce0678e536786e4f550c4beb190c57a4e697c6d4a4782c12356a",
		},
		{
			name:     "safe-evaluator",
			source:   `var result = { answer: 42 }.safe().value.answer;`,
			checksum: "668d4f58dc1d0cbfd3f7dee431dd6e2f1ef055842e60f4585c305984e55cc7d8",
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
