package compiler

import (
	"testing"
)

// A call's place is where the call is (14/10/2026): a method call at its dot, any other call where
// what it calls starts. Until then CALL and INVOKE carried the place of the last thing compiled before
// them, the last argument, so an error of JSON.parse( on one line and its string on the next pointed at
// the string.
func TestCallRecordsWhereTheCallIs(t *testing.T) {
	rows := []struct {
		name, source string
		line, column int32
	}{
		// a native method failing: the dot of .parse, not the string two lines down
		{"invoke-native", "const r = JSON.parse(\n  \"{bad\"\n)", 1, 15},
		// a multi-line chain fails at the dot of the link that failed
		{"invoke-chain", "const r = \"ab\"\n  .repeat(2)\n  .repeat(\n    1e9\n  )", 3, 3},
		// a global function failing: where its name starts
		{"call-native", "const r = fail(\n  \"boom\"\n)", 1, 11},
		{"call-curried", "const make = () => fail\nconst r = make()(\n  \"boom\"\n)", 2, 11},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			_, failure := grammarRun(t, row.source, nil)
			if failure == nil {
				t.Fatalf("%q ran", row.source)
			}
			if failure.Line != row.line || failure.Column != row.column {
				t.Fatalf("%q fails at %d:%d, want %d:%d\n%v", row.source, failure.Line, failure.Column, row.line, row.column, failure)
			}
		})
	}
}

// The frames a failure passes through carry their call's place too: the caller's line is the call of
// the function that failed, not that call's last argument.
func TestStackFramesPointAtTheirCall(t *testing.T) {
	source := "const inner = (x) => fail(\"boom \" + x)\nconst outer = () => inner(\n  1\n)\nconst r = outer()"
	_, failure := grammarRun(t, source, nil)
	if failure == nil {
		t.Fatalf("%q ran", source)
	}
	if len(failure.Stack) < 3 {
		t.Fatalf("stack %v, want inner, outer, main", failure.Stack)
	}
	want := [][2]int32{{1, 22}, {2, 21}, {5, 11}}
	for index, place := range want {
		frame := failure.Stack[index]
		if frame.Line != place[0] || frame.Column != place[1] {
			t.Fatalf("frame %d (%s) at %d:%d, want %d:%d\n%v", index, frame.Function, frame.Line, frame.Column, place[0], place[1], failure)
		}
	}
}
