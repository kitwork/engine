package css

import (
	"strings"
	"testing"
)

func TestResolveAnimate(t *testing.T) {
	cases := map[string][]string{
		"up":       {"animation-name:animate--up", "var(--animate-duration)", "animation-fill-mode:both"},
		"zoom-out": {"animation-name:animate--zoom-out-enter"}, // class≠keyframe name
		"spin":     {"animation:animate--spin 1s linear infinite;"},
		"spin-ccw": {"animate--spin 1s linear infinite reverse"},
		"wave":     {"animate--wave", "transform-origin:70% 70%", "display:inline-block"},
		"faster":   {"--animate-duration:0.15s;"},
		"slow":     {"--animate-duration:0.85s;"},
		"ease-in":  {"--animate-easing:cubic-bezier(0.4,0,1,1);"},
		"delay-2":  {"--animate-delay:0.16s;"},
		"infinite": {"animation-iteration-count:infinite;"},
		"repeat-3": {"animation-iteration-count:3;"},
		"paused":   {"animation-play-state:paused;"},
	}
	for name, wants := range cases {
		got := resolveAnimate(name)
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("resolveAnimate(%q) = %q, missing %q", name, got, w)
			}
		}
	}
	if got := resolveAnimate("definitely-not-a-thing"); got != "" {
		t.Errorf("unknown name should return \"\", got %q", got)
	}
}

func TestResolveAnimateStagger(t *testing.T) {
	// animate-up-3 == up entrance, delay step 3, self-contained.
	got := resolveAnimate("up-3")
	for _, w := range []string{"animation-name:animate--up", "animation-delay:0.24s", "animation-fill-mode:both"} {
		if !strings.Contains(got, w) {
			t.Errorf("resolveAnimate(up-3) = %q, missing %q", got, w)
		}
	}
	// Unknown base or out-of-range step → nothing.
	if resolveAnimate("nope-2") != "" || resolveAnimate("up-9") != "" {
		t.Error("invalid stagger should resolve to empty")
	}
}

func TestResolveAnimateViaRegistry(t *testing.T) {
	// End-to-end through ResolveCore (registry → buildProp tw-animate).
	css, sel, _ := ResolveCore("animate-shake", nil)
	if !strings.Contains(css, "animate--shake") {
		t.Errorf("ResolveCore(animate-shake) css=%q", css)
	}
	if sel != ".animate-shake" {
		t.Errorf("selector = %q, want .animate-shake", sel)
	}
}

func TestUsedKeyframesOnlyUsed(t *testing.T) {
	// Simulate generated utility CSS that uses ONLY the `up` and `spin` animations.
	css := ".animate-up{animation-name:animate--up;animation-duration:var(--animate-duration)}" +
		".animate-spin{animation:animate--spin 1s linear infinite}"
	out := UsedKeyframes(css, nil)

	if !strings.Contains(out, "@keyframes animate--up{") {
		t.Error("used keyframe animate--up not emitted")
	}
	if !strings.Contains(out, "@keyframes animate--spin{") {
		t.Error("used keyframe animate--spin not emitted")
	}
	// Unused ones must NOT ship.
	for _, unused := range []string{"animate--shake", "animate--wobble", "animate--bounce", "animate--wave"} {
		if strings.Contains(out, "@keyframes "+unused+"{") {
			t.Errorf("unused keyframe %s was emitted", unused)
		}
	}
	// :root vars + reduced-motion guard always accompany used animations.
	if !strings.Contains(out, "--animate-duration:0.5s") {
		t.Error(":root animate vars not emitted")
	}
	if !strings.Contains(out, "prefers-reduced-motion") {
		t.Error("reduced-motion guard not emitted")
	}
}

func TestUsedKeyframesBoundary(t *testing.T) {
	// A page using ONLY fade-out must NOT drag in the plain `fade` keyframe (substring trap).
	css := ".animate-fade-out{animation-name:animate--fade-out;}"
	out := UsedKeyframes(css, nil)
	if !strings.Contains(out, "@keyframes animate--fade-out{") {
		t.Error("animate--fade-out not emitted")
	}
	if strings.Contains(out, "@keyframes animate--fade{") {
		t.Error("plain animate--fade wrongly emitted for a fade-out-only page")
	}
}

func TestUsedKeyframesNoop(t *testing.T) {
	if out := UsedKeyframes(".text-red{color:red}", nil); out != "" {
		t.Errorf("no animation → empty, got %q", out)
	}
}

func TestAnimateDirectionAndIteration(t *testing.T) {
	cases := map[string]string{
		"reverse":           "animation-direction:reverse;",
		"alternate":         "animation-direction:alternate;",
		"alternate-reverse": "animation-direction:alternate-reverse;",
		"once":              "animation-iteration-count:1;",
	}
	for name, want := range cases {
		if got := resolveAnimate(name); got != want {
			t.Errorf("resolveAnimate(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestAnimateArbitrary(t *testing.T) {
	// animate-[wiggle_1s_ease-in-out_infinite] → animation shorthand with underscores→spaces.
	css, sel, _ := ResolveCore("animate-[wiggle_1s_ease-in-out_infinite]", nil)
	if css != "animation: wiggle 1s ease-in-out infinite;" {
		t.Errorf("arbitrary animate css = %q", css)
	}
	if sel != `.animate-\[wiggle_1s_ease-in-out_infinite\]` {
		t.Errorf("arbitrary selector = %q", sel)
	}
}

func TestAnimateOnHover(t *testing.T) {
	// The paused rule targets descendants via ` *`.
	css, sel, _ := ResolveCore("animate-on-hover", nil)
	if css != "animation-play-state: paused;" {
		t.Errorf("on-hover css = %q", css)
	}
	if sel != ".animate-on-hover *" {
		t.Errorf("on-hover selector = %q, want '.animate-on-hover *'", sel)
	}
	// UsedKeyframes appends the :hover-runs counterpart when the class is present.
	out := UsedKeyframes(".animate-on-hover *{animation-play-state: paused;} .animate-spin{animation:animate--spin 1s linear infinite}", nil)
	if !strings.Contains(out, ".animate-on-hover:hover *{animation-play-state:running}") {
		t.Errorf("on-hover running counterpart not emitted: %q", out)
	}
}

func TestUsedKeyframesExtend(t *testing.T) {
	// theme.extend.keyframes are emitted when referenced, and animation shorthand comes from
	// theme.extend.animation via cfg.Animations (tw-animate).
	cfg := &Config{
		Animations: map[string]string{"wiggle": "wiggle 1s ease-in-out infinite"},
		Keyframes:  map[string]string{"wiggle": "0%,100%{transform:rotate(-3deg)}50%{transform:rotate(3deg)}"},
	}
	body := resolveAnimateOrConfig("wiggle", cfg)
	if !strings.Contains(body, "animation: wiggle 1s ease-in-out infinite;") {
		t.Errorf("config animation not used: %q", body)
	}
	css := ".animate-wiggle{animation: wiggle 1s ease-in-out infinite;}"
	out := UsedKeyframes(css, cfg)
	if !strings.Contains(out, "@keyframes wiggle{") {
		t.Errorf("extend keyframe not emitted: %q", out)
	}
}

func TestAnimatePipeline(t *testing.T) {
	// Full server path: scan HTML for classes → utility CSS (GenerateJITCached), then the
	// keyframes/statics render prepends (UsedKeyframes).
	html := `<div class="animate-on-hover">` +
		`<i class="animate-spin animate-reverse"></i>` +
		`<span class="animate-up animate-up-2"></span>` +
		`<b class="animate-[wiggle_1s_infinite]"></b></div>`
	cfg := &Config{Keyframes: map[string]string{"wiggle": "0%,100%{transform:rotate(-3deg)}50%{transform:rotate(3deg)}"}}

	css := GenerateJITCached(html, cfg)
	full := UsedKeyframes(css, cfg) + css

	wants := []string{
		"animation:animate--spin 1s linear infinite",   // loop
		"animation-direction:reverse",                  // modifier composes
		"animation-name:animate--up",                   // entrance
		"animation-delay:0.16s",                        // stagger up-2
		"animation: wiggle 1s infinite",                // arbitrary
		"@keyframes animate--spin{",                    // only-used keyframe (loop)
		"@keyframes animate--up{",                       // only-used keyframe (entrance)
		"@keyframes wiggle{",                            // extend keyframe
		".animate-on-hover * { animation-play-state: paused; }", // paused descendants
		".animate-on-hover:hover *{animation-play-state:running}", // hover-runs counterpart
		"prefers-reduced-motion",                       // a11y guard
	}
	for _, w := range wants {
		if !strings.Contains(full, w) {
			t.Errorf("pipeline output missing %q", w)
		}
	}
	// Only-used: an unrelated keyframe must not appear.
	if strings.Contains(full, "@keyframes animate--jello{") {
		t.Error("unused keyframe leaked into pipeline output")
	}
}

// resolveAnimateOrConfig mirrors the tw-animate precedence for testing (config wins, then catalog).
func resolveAnimateOrConfig(name string, cfg *Config) string {
	if cfg != nil && cfg.Animations != nil {
		if r, ok := cfg.Animations[name]; ok {
			return "animation: " + r + ";"
		}
	}
	return resolveAnimate(name)
}
