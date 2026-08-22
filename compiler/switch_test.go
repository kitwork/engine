package compiler

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func wantText(t *testing.T, got value.Value, want, message string) {
	t.Helper()
	if got.K != value.String || got.Text() != want {
		t.Fatalf("%s: got %q (kind %v), want %q", message, got.Text(), got.K, want)
	}
}

func TestSwitchMatchesAndBreaks(t *testing.T) {
	got := runResult(t, `
result = 0;
switch (2) {
case 1:
	result = 10;
	break;
case 2:
	result = 20;
	break;
default:
	result = 30;
}
`)
	wantNum(t, got, 20, "matching case")
}

func TestSwitchDefaultAndFallthrough(t *testing.T) {
	t.Run("no-match-without-default", func(t *testing.T) {
		got := runResult(t, `
result = 7;
switch (9) {
case 1:
	result = 1;
	break;
}
result = result + 1;
`)
		wantNum(t, got, 8, "unmatched switch without default")
	})

	t.Run("default", func(t *testing.T) {
		got := runResult(t, `
result = "";
switch (9) {
case 1:
	result = "one";
	break;
default:
	result = "other";
}
`)
		wantText(t, got, "other", "default clause")
	})

	t.Run("grouped-labels-and-fallthrough", func(t *testing.T) {
		got := runResult(t, `
result = "";
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
`)
		wantText(t, got, "ab", "fallthrough body order")
	})
}

func TestSwitchDefaultMayAppearBeforeLaterCases(t *testing.T) {
	t.Run("no-match-enters-default-and-falls-through", func(t *testing.T) {
		got := runResult(t, `
result = "";
switch (9) {
case 1:
	result = result + "a";
	break;
default:
	result = result + "d";
case 2:
	result = result + "b";
	break;
}
`)
		wantText(t, got, "db", "default fallthrough")
	})

	t.Run("later-match-skips-default", func(t *testing.T) {
		got := runResult(t, `
result = "";
switch (2) {
case 1:
	result = result + "a";
	break;
default:
	result = result + "d";
case 2:
	result = result + "b";
	break;
}
`)
		wantText(t, got, "b", "matched case after default")
	})
}

func TestSwitchEvaluationOrder(t *testing.T) {
	t.Run("discriminant-once", func(t *testing.T) {
		got := runResult(t, `
let calls = 0;
const next = () => {
	calls = calls + 1;
	return 2;
};
result = 0;
switch (next()) {
case 2:
	result = calls;
	break;
}
`)
		wantNum(t, got, 1, "switch discriminant evaluation count")
	})

	t.Run("case-tests-stop-after-match", func(t *testing.T) {
		got := runResult(t, `
result = "";
const mark = (number) => {
	result = result + number;
	return number;
};
switch (2) {
case mark(1):
	break;
case mark(2):
	break;
case mark(3):
	break;
}
`)
		wantText(t, got, "12", "case test evaluation order")
	})
}

func TestBreakExitsNearestBoundedControlFlow(t *testing.T) {
	t.Run("counted-for", func(t *testing.T) {
		got := runResult(t, `
result = 0;
for (let index = 0; index < 10; index++) {
	if (index == 3) { break; }
	result = result + index;
}
result = result + 100;
`)
		wantNum(t, got, 103, "counted for break")
	})

	t.Run("for-of-cleans-iterator-stack", func(t *testing.T) {
		got := runResult(t, `
result = 0;
for (const number of [1, 2, 3, 4]) {
	if (number == 3) { break; }
	result = result + number;
}
result = result + 100;
`)
		wantNum(t, got, 103, "for-of break")
	})

	t.Run("nested-switch", func(t *testing.T) {
		got := runResult(t, `
result = 0;
switch (1) {
case 1:
	switch (2) {
	case 2:
		result = result + 1;
		break;
	default:
		result = 99;
	}
	result = result + 10;
	break;
default:
	result = 1000;
}
`)
		wantNum(t, got, 11, "nearest switch break")
	})
}

func TestSwitchAllowsReturnFromFunction(t *testing.T) {
	got := runResult(t, `
function choose(number) {
	switch (number) {
	case 1:
		return "one";
	default:
		return "other";
	}
}
result = choose(1);
`)
	wantText(t, got, "one", "return in switch")
}

func TestSwitchRejectsInvalidBreakAndClauses(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		mustContain string
	}{
		{name: "break-outside-control-flow", source: `break;`, mustContain: "break"},
		{
			name:        "break-cannot-cross-function",
			source:      `for (let i = 0; i < 1; i++) { const stop = () => { break; }; }`,
			mustContain: "break",
		},
		{
			name:        "duplicate-default",
			source:      `switch (1) { default: result = 1; default: result = 2; }`,
			mustContain: "default",
		},
		{
			name:        "missing-colon",
			source:      `switch (1) { case 1 result = 1; }`,
			mustContain: ":",
		},
		{
			name:        "missing-closing-brace",
			source:      `switch (1) { case 1: result = 1;`,
			mustContain: "}",
		},
		{
			name:        "labeled-break",
			source:      `switch (1) { case 1: break outer; }`,
			mustContain: "labeled break",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wantParseErr(t, test.source, test.mustContain)
		})
	}
}

func TestCompilerRejectsBreakWithoutFrame(t *testing.T) {
	compiler := NewCompiler()
	err := compiler.Compile(&BreakStatement{})
	if err == nil || !strings.Contains(err.Error(), "break outside") {
		t.Fatalf("compiler break error = %v", err)
	}
}

func TestSwitchArtifactRoundTrip(t *testing.T) {
	const source = `
var result = "";
switch (2) {
case 1:
	result = "one";
	break;
case 2:
	result = "two";
	break;
default:
	result = "other";
}
`
	compiled, err := CompileSource(source)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := compiled.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalBytecode(encoded, compiled.SourceFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateArtifact(restored); err != nil {
		t.Fatal(err)
	}
	if restored.Program.Checksum() != compiled.Program.Checksum() {
		t.Fatalf("restored checksum = %q, want %q", restored.Program.Checksum(), compiled.Program.Checksum())
	}

	vm := runtime.New(restored.Program)
	if execution := vm.Run(); execution.K == value.Invalid {
		t.Fatalf("restored switch execution: %v", execution.V)
	}
	if got := vm.Vars["result"].Text(); got != "two" {
		t.Fatalf("restored result = %q, want %q", got, "two")
	}
	if !strings.EqualFold(restored.CompilerFingerprint(), Fingerprint()) {
		t.Fatalf("restored compiler fingerprint = %q, want %q", restored.CompilerFingerprint(), Fingerprint())
	}
}
