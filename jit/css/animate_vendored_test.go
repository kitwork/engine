package css

import (
	"strings"
	"testing"
)

// The animate.css library is vendored whole by vendor_animate.py; a page still ships only what it
// uses. These tests pin the contract: the catalog is complete and grouped like animate.style, the
// utility names are the upstream names in kebab, upstream's per-animation extras ride along, the
// motion runs at animate.css's pace with the browser's `ease`, modifiers and stagger reach it, and
// nothing unused is emitted.

func TestVendoredCatalogComplete(t *testing.T) {
	// animate.css 4.1.1 ships 98 animations; bounce and pulse are Tailwind's names and are skipped.
	if got := len(animateVendored); got != 96 {
		t.Fatalf("vendored catalog has %d animations, want 96", got)
	}
	groups := VendoredAnimations()
	if len(groups) != 16 || groups[0].Name != "attention_seekers" || groups[15].Name != "sliding_exits" {
		t.Fatalf("groups are not animate.style's 16 sections in order: %+v", groups)
	}
	total := 0
	for _, g := range groups {
		for _, name := range g.Animations {
			e, ok := animateVendored[name]
			if !ok || e.group != g.Name {
				t.Fatalf("%s listed under %s but the catalog says %+v", name, g.Name, e)
			}
		}
		total += len(g.Animations)
	}
	if total != 96 {
		t.Fatalf("groups list %d animations, catalog has 96", total)
	}
	for _, skipped := range []string{"bounce", "pulse"} {
		if _, ok := animateVendored[skipped]; ok {
			t.Fatalf("%s must stay Tailwind's, not animate.css's", skipped)
		}
	}
}

func TestVendoredFramesAreUpstream(t *testing.T) {
	// A byte-exact sample (minified upstream source, keyframe renamed) — the transcription is not
	// by hand any more, and a regeneration that drifted would show here.
	want := "@keyframes animate--fade-in-up{from{opacity:0;transform:translate3d(0,100%,0)}to{opacity:1;transform:translate3d(0,0,0)}}"
	if got := animateVendored["fade-in-up"].frames; got != want {
		t.Fatalf("fade-in-up frames\n got %s\nwant %s", got, want)
	}
	// The extras upstream puts on the class rule, not in the keyframes.
	for name, want := range map[string]animateEntry{
		"hinge":                {factor: "2", extra: "transform-origin:top left"},
		"bounce-in":            {factor: "0.75"},
		"flip":                 {extra: "backface-visibility:visible"},
		"flip-out-x":           {factor: "0.75", extra: "backface-visibility:visible !important"},
		"light-speed-in-right": {extra: "animation-timing-function:ease-out"},
		"swing":                {extra: "transform-origin:top center"},
		"head-shake":           {extra: "animation-timing-function:ease-in-out"},
	} {
		got := animateVendored[name]
		if got.factor != want.factor || got.extra != want.extra {
			t.Errorf("%s: factor=%q extra=%q, want factor=%q extra=%q", name, got.factor, got.extra, want.factor, want.extra)
		}
	}
}

func TestVendoredUtility(t *testing.T) {
	// animate-fade-in-up: keyframe by kebab name, animate.css's pace (twice the shared duration →
	// 1s by default), the browser's ease unless a modifier sets --animate-easing, fill:both.
	got := resolveAnimate("fade-in-up")
	for _, w := range []string{
		"animation-name:animate--fade-in-up;",
		"animation-duration:calc(var(--animate-duration) * 2);",
		"animation-timing-function:var(--animate-easing, ease);",
		"animation-delay:var(--animate-delay);",
		"animation-fill-mode:both;",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("resolveAnimate(fade-in-up) = %q, missing %q", got, w)
		}
	}
	// Per-animation factor and extras land on the rule.
	got = resolveAnimate("hinge")
	for _, w := range []string{"animation-duration:calc(var(--animate-duration) * 2 * 2);", "transform-origin:top left;"} {
		if !strings.Contains(got, w) {
			t.Errorf("resolveAnimate(hinge) = %q, missing %q", got, w)
		}
	}
	// Stagger reaches the vendored family too.
	if got := resolveAnimate("fade-in-up-3"); !strings.Contains(got, "animation-name:animate--fade-in-up;") || !strings.Contains(got, "animation-delay:0.24s;") {
		t.Errorf("resolveAnimate(fade-in-up-3) = %q", got)
	}
	// The short set keeps its spring curve as the fallback of the same var, so one ease-* modifier
	// steers both families.
	if got := resolveAnimate("up"); !strings.Contains(got, "animation-timing-function:var(--animate-easing, cubic-bezier(0.22, 1, 0.36, 1));") {
		t.Errorf("resolveAnimate(up) = %q", got)
	}
	if strings.Contains(animateRootVars, "--animate-easing") {
		t.Errorf("--animate-easing must not have a :root value (each family falls back to its own): %s", animateRootVars)
	}
	// Not a name in either family.
	if got := resolveAnimate("fade-in-sideways"); got != "" {
		t.Errorf("unknown vendored-looking name resolved to %q", got)
	}
}

func TestVendoredEmitOnlyUsed(t *testing.T) {
	cfg := DefaultConfig
	css := GenerateJIT(`<div class="animate-fade-in-up animate-slow"><i class="animate-tada"></i></div>`, &cfg)
	out := UsedKeyframes(css, &cfg)
	for _, w := range []string{"@keyframes animate--fade-in-up{", "@keyframes animate--tada{", "prefers-reduced-motion"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	// Siblings, prefixes and the other 94 stay home.
	for _, unused := range []string{"animate--fade-in{", "animate--fade-in-down{", "animate--fade-in-up-big{", "animate--hinge{", "animate--jello{"} {
		if strings.Contains(out, "@keyframes "+unused) {
			t.Errorf("unused keyframe %s emitted", unused)
		}
	}
	if n := strings.Count(out, "@keyframes "); n != 2 {
		t.Errorf("emitted %d @keyframes blocks, want 2:\n%s", n, out)
	}
}

func TestVendoredConfigWins(t *testing.T) {
	// theme.extend.animation with an animate.css name overrides the library, like Tailwind.
	cfg := &Config{Animations: map[string]string{"fade-in-up": "rise 300ms ease-out"}}
	css, _, _ := ResolveCore("animate-fade-in-up", cfg)
	if !strings.Contains(css, "animation: rise 300ms ease-out;") || strings.Contains(css, "animate--fade-in-up") {
		t.Errorf("config did not win over the vendored library: %q", css)
	}
}
