package compiler

import (
	"strings"
	"testing"
)

// A counted loop `for (let i = 0; i < n; i++)` runs bounded, in order.
func TestForCountedSum(t *testing.T) {
	got := runResult(t, `
		result = 0
		for (let i = 0; i < 5; i++) { result = result + i }
	`)
	wantNum(t, got, 10, "0+1+2+3+4") // 0,1,2,3,4
}

// The counter drives array indexing (bound = arr.length, evaluated by the condition each pass).
func TestForCountedOverArray(t *testing.T) {
	got := runResult(t, `
		let arr = [10, 20, 30]
		result = 0
		for (let i = 0; i < arr.length; i++) { result = result + arr[i] }
	`)
	wantNum(t, got, 60, "10+20+30")
}

// Countdown with i-- terminates.
func TestForCountdown(t *testing.T) {
	got := runResult(t, `
		result = 0
		for (let i = 3; i > 0; i--) { result = result + i }
	`)
	wantNum(t, got, 6, "3+2+1")
}

// Custom step with i += 2.
func TestForStep(t *testing.T) {
	got := runResult(t, `
		result = 0
		for (let i = 0; i < 10; i += 2) { result = result + 1 }
	`)
	wantNum(t, got, 5, "0,2,4,6,8 → 5 iterations")
}

// for…of iterates a collection (compiled to ITER) — bounded by the array length.
func TestForOf(t *testing.T) {
	got := runResult(t, `
		let arr = [1, 2, 3, 4]
		result = 0
		for (const x of arr) { result = result + x }
	`)
	wantNum(t, got, 10, "1+2+3+4")
}

// Nested counted loops.
func TestForNested(t *testing.T) {
	got := runResult(t, `
		result = 0
		for (let i = 0; i < 3; i++) {
			for (let j = 0; j < 3; j++) { result = result + 1 }
		}
	`)
	wantNum(t, got, 9, "3x3")
}

// --- The guardrails: shapes that are `while` in disguise must be REJECTED at parse time. ---

func wantParseErr(t *testing.T, src, mustContain string) {
	t.Helper()
	_, err := parseProgram(src)
	if err == nil {
		t.Fatalf("expected a parse error for:\n%s", src)
	}
	if mustContain != "" && !strings.Contains(err.Error(), mustContain) {
		t.Fatalf("error %q should mention %q", err.Error(), mustContain)
	}
}

func TestForInfiniteRejected(t *testing.T) {
	wantParseErr(t, `for (;;) { }`, "while")
}

func TestForConditionOnlyRejected(t *testing.T) {
	wantParseErr(t, `for (; x < 3 ;) { }`, "while")
}

func TestForConditionMustReferenceCounter(t *testing.T) {
	// A condition that ignores the counter is an arbitrary loop = while in disguise.
	wantParseErr(t, `for (let i = 0; ready(); i++) { }`, "biến đếm")
}

func TestForUpdateMustMutateCounter(t *testing.T) {
	wantParseErr(t, `for (let i = 0; i < n; other = other + 1) { }`, "biến đếm")
}

func TestForCounterMustBeDeclared(t *testing.T) {
	wantParseErr(t, `for (i = 0; i < 3; i++) { }`, "let")
}

// `while` stays banned entirely (unchanged).
func TestWhileStillBanned(t *testing.T) {
	wantParseErr(t, `while (true) { }`, "while")
}
