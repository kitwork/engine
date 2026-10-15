package hydrate

import (
	"runtime/debug"
	"strings"
	"testing"
)

// An expression nested past nestingLimit is an error (14/10/2026), wherever it nests: parentheses, !
// after !, a chain, an operator run, a ternary in a ternary. The parser and every walker of the IR
// recurse as deep as it nests, and a .ui page's expression of nested parentheses overflowed the shared
// host's Go stack while the page compiled. Under a 64 MiB stack the deep forms are a clean error.
// CONTROL: 500 levels compile.
func TestExpressionNestingIsBounded(t *testing.T) {
	saved := debug.SetMaxStack(64 << 20)
	defer debug.SetMaxStack(saved)
	deep := 200000
	for name, source := range map[string]string{
		"parentheses": strings.Repeat("(", deep) + "1" + strings.Repeat(")", deep),
		"not":         strings.Repeat("!", deep) + "a",
		"chain":       "a" + strings.Repeat(".b", deep),
		"sum":         "1" + strings.Repeat(" + 1", deep),
		"ternary":     strings.Repeat("a ? b : ", deep) + "c",
		"arrays":      strings.Repeat("[", deep) + strings.Repeat("]", deep),
	} {
		if _, err := Compile(source); err == nil || !strings.Contains(err.Error(), "biểu thức lồng quá sâu (hơn 1000 tầng") {
			t.Errorf("%s: %v, want the nesting error", name, err)
		}
	}
	control := strings.Repeat("(", 500) + "1" + strings.Repeat(")", 500) + " + a.b.c"
	if _, err := Compile(control); err != nil {
		t.Fatalf("CONTROL: 500 levels = %v", err)
	}
}
