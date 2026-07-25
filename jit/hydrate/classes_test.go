package hydrate

import (
	"strings"
	"testing"
)

// The point of ClassLiterals is that the AUTHOR is not forced into one syntax: whichever shape the
// grammar allows, the CSS JIT must still see every class name. Each case below is a shape someone
// will reasonably write.
func TestClassLiteralsAcrossEveryForm(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`'card'`, []string{"card"}},
		{`'card ring shadow'`, []string{"card", "ring", "shadow"}}, // one literal, several classes
		{`open ? 'text-red' : 'text-green'`, []string{"text-red", "text-green"}},
		{`{ 'is-open': open }`, []string{"is-open"}},
		{`{ 'is-open': open, active: n > 3 }`, []string{"is-open", "active"}},
		{`['card', active ? 'ring' : '']`, []string{"card", "ring"}},
		{`[{ 'a': x }, y ? 'b' : 'c']`, []string{"a", "b", "c"}}, // nested array of object + ternary
		{`open ? 'a' : (big ? 'b' : 'c')`, []string{"a", "b", "c"}},
	}

	for _, tc := range cases {
		got, err := ClassLiterals(tc.expr)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.expr, err)
			continue
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s\n  got  %v\n  want %v", tc.expr, got, tc.want)
		}
	}
}

// Scope reads are variables, not class names. Walking into them would emit CSS for every
// identifier a page mentions, which defeats the point of a JIT that emits only what is used.
func TestClassLiteralsIgnoresVariableNames(t *testing.T) {
	got, err := ClassLiterals(`theme`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a bare variable names no class, got %v", got)
	}

	// The variable must not leak even when it sits beside a real literal.
	got, err = ClassLiterals(`{ 'is-open': isOpen, active: enabled }`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c == "isOpen" || c == "enabled" {
			t.Fatalf("variable name leaked into the class set: %v", got)
		}
	}
}

// A key computed at runtime would be the other way to hide a class name from the JIT. The grammar
// removes the possibility rather than detecting it: object keys are plain names or strings, so
// `{ ['bg-' + shade]: true }` does not compile at all. Pinned here because it is the reason
// HasConstructedClass only has to reason about `+`.
func TestComputedObjectKeyIsRejectedByTheGrammar(t *testing.T) {
	if _, err := Compile(`{ ['bg-' + shade]: true }`); err == nil {
		t.Fatal("a computed object key must not compile — it would hide a class name from the JIT")
	}
}

// A constructed name never appears whole in the source, so the JIT cannot emit it. Detecting this
// is what lets the engine say so instead of shipping a page that is styled only by luck.
func TestHasConstructedClass(t *testing.T) {
	constructed := []string{
		`'text-' + color`,
		`open ? 'text-' + a : 'b'`,
	}
	for _, e := range constructed {
		if !HasConstructedClass(e) {
			t.Errorf("%s: should be reported as constructed", e)
		}
	}

	// CONTROL: arithmetic is not concatenation, and neither is a plain literal. Without these the
	// test would also pass on an implementation that flags every expression containing '+'.
	fine := []string{
		`{ on: n + 1 > 3 }`,
		`open ? 'text-red' : 'text-green'`,
		`'card'`,
		`{ 'is-open': open }`,
	}
	for _, e := range fine {
		if HasConstructedClass(e) {
			t.Errorf("%s: must NOT be reported as constructed", e)
		}
	}
}
