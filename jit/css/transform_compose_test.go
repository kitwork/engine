package css

import (
	"strings"
	"testing"
)

// translate/rotate/scale/skew each emitted a bare `transform:`, so two of them on one element meant
// the second rule replaced the first — same specificity, last one wins. `hover:-translate-y-1
// hover:-rotate-2` lifted without tilting and reported no error anywhere. Each now fills its own
// slot and restates the whole chain, the way ring, shadow and the filters already did.
func TestTransformsCompose(t *testing.T) {
	cfg := DefaultConfig
	for _, c := range []struct{ cls, slot string }{
		{"translate-x-4", "--kitwork-translate-x: 1rem"},
		{"-translate-y-1.5", "--kitwork-translate-y: -0.375rem"},
		{"rotate-45", "--kitwork-rotate: 45deg"},
		{"-rotate-2", "--kitwork-rotate: -2deg"},
		{"scale-105", "--kitwork-scale-x: 1.05; --kitwork-scale-y: 1.05"},
		{"scale-x-50", "--kitwork-scale-x: 0.5"},
		{"skew-y-3", "--kitwork-skew-y: 3deg"},
	} {
		css, _, _ := ResolveCore(c.cls, &cfg)
		if !strings.Contains(css, c.slot) {
			t.Errorf("%s → %q, want slot %q", c.cls, css, c.slot)
		}
		if !strings.Contains(css, "transform: "+transformChain+";") {
			t.Errorf("%s → %q, want it to restate the whole transform chain", c.cls, css)
		}
	}

	// The point of the change: two transform utilities on one element must not cancel each other.
	// Each writes a DIFFERENT custom property, so the cascade keeps both.
	lift, _, _ := ResolveCore("-translate-y-1", &cfg)
	tilt, _, _ := ResolveCore("-rotate-2", &cfg)
	if strings.Contains(lift, "--kitwork-rotate:") || strings.Contains(tilt, "--kitwork-translate-y:") {
		t.Fatal("a utility wrote a slot that is not its own; stacking would still clobber")
	}

	// Every slot the chain names must have a value in Preflight. A transform function handed an
	// empty var is invalid and the browser drops the WHOLE declaration — which is how a chain that
	// looks right renders nothing. Translate/rotate/skew seed 0, the scales seed 1.
	pre := buildJITCSS([]string{"rotate-45"}, nil)
	for _, v := range strings.Split(transformChain, " ") {
		start := strings.Index(v, "var(")
		if start < 0 {
			continue
		}
		name := v[start+len("var(") : strings.Index(v[start:], ")")+start]
		want := name + ": 0;"
		if strings.Contains(name, "scale") {
			want = name + ": 1;"
		}
		if !strings.Contains(pre, want) {
			t.Errorf("Preflight does not seed %q; the transform declaration would be dropped", want)
		}
	}
}
