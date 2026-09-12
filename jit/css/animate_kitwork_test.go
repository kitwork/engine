package css

import (
	"strings"
	"testing"
)

// The Kitwork set is the house motion: four groups that say where content comes from, where it
// goes, what changed and what is running. These pin its shape and the few entries that carry more
// than a keyframe.

func TestKitworkSetShape(t *testing.T) {
	groups := KitworkAnimations()
	want := map[string]int{"enter": 10, "exit": 7, "attention": 6, "live": 13}
	if len(groups) != 4 {
		t.Fatalf("want 4 groups, got %d", len(groups))
	}
	total := 0
	for _, g := range groups {
		if want[g.Name] != len(g.Animations) {
			t.Errorf("group %s lists %d animations, want %d", g.Name, len(g.Animations), want[g.Name])
		}
		for _, name := range g.Animations {
			e, ok := animateKitwork[name]
			if !ok || e.group != g.Name {
				t.Errorf("%s listed under %s but the catalog says %+v", name, g.Name, e)
			}
			if resolveAnimate(name) == "" {
				t.Errorf("animate-%s resolves to nothing", name)
			}
			// One name, one catalog: nothing may be in both.
			if _, dup := animateVendored[name]; dup {
				t.Errorf("%s is in both the Kitwork set and animate.css", name)
			}
		}
		total += len(g.Animations)
	}
	if total != len(animateKitwork) {
		t.Errorf("groups list %d animations, catalog has %d", total, len(animateKitwork))
	}
	// Every loop has a shorthand and every run-once has frames; a loop without frames rides another's.
	for name, e := range animateKitwork {
		if e.loop == "" && e.frames == "" {
			t.Errorf("%s: run-once without frames", name)
		}
		if e.frames != "" && !strings.HasPrefix(e.frames, "@keyframes animate--"+name+"{") {
			t.Errorf("%s: frames are not named animate--%s: %.40s", name, name, e.frames)
		}
	}
}

func TestKitworkEntries(t *testing.T) {
	for name, wants := range map[string][]string{
		// enter/exit pairs travel the same distance in opposite directions
		"rise":      {"animation-name:animate--rise;", "animation-duration:var(--animate-duration);", "animation-timing-function:var(--animate-easing, cubic-bezier(0.22, 1, 0.36, 1));", "animation-fill-mode:both;"},
		"sink":      {"animation-name:animate--sink;"},
		"scale-in":  {"animation-name:animate--scale-in;"},
		"scale-out": {"animation-name:animate--scale-out;"},
		"reveal":    {"animation-name:animate--reveal;"},
		"vanish":    {"animation-name:animate--vanish;"},
		// attention: tick swings from its top, highlight's ring must not linger as a shadow
		"tick":      {"animation-name:animate--tick;", "transform-origin:top center;"},
		"highlight": {"animation-name:animate--highlight;", "animation-fill-mode:both;animation-fill-mode:none;"},
		// live: shimmer brings its own gradient, spin-ccw rides spin's frames
		"shimmer":  {"animation:animate--shimmer 1.6s linear infinite;", "background-image:linear-gradient(90deg,transparent 0%,rgba(var(--color-ink),0.06) 50%,transparent 100%);", "background-size:200% 100%;"},
		"spin-ccw": {"animation:animate--spin 1s linear infinite reverse;"},
		"breathe":  {"animation:animate--breathe 3s ease-in-out infinite;"},
		"sweep":    {"animation:animate--sweep 1.4s ease-in-out infinite;"},
		"marquee":  {"animation:animate--marquee 24s linear infinite;"},
		"drift":    {"animation:animate--drift 30s ease-in-out infinite;"},
	} {
		got := resolveAnimate(name)
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("animate-%s = %q, missing %q", name, got, w)
			}
		}
	}
	// The frames themselves, for the entries whose values are the design.
	for name, want := range map[string]string{
		"rise":      "from{opacity:0;transform:translateY(var(--animate-distance)) scale(0.98)}to{opacity:1;transform:translateY(0) scale(1)}",
		"out-left":  "to{opacity:0;transform:translateX(calc(-1 * var(--animate-distance) - 4px))}",
		"reveal":    "from{clip-path:inset(0 100% 0 0)}to{clip-path:inset(0 0 0 0)}",
		"highlight": "from{box-shadow:0 0 0 0 rgba(var(--color-brand),0.45)}to{box-shadow:0 0 0 12px rgba(var(--color-brand),0)}",
		"pop":       "0%,100%{transform:scale(1)}40%{transform:scale(1.06)}",
	} {
		if !strings.Contains(animateKitwork[name].frames, want) {
			t.Errorf("%s frames = %s\nwant to contain %s", name, animateKitwork[name].frames, want)
		}
	}
}

func TestKitworkEmitOnlyUsed(t *testing.T) {
	cfg := DefaultConfig
	// A page with a skeleton (shimmer), a live dot (breathe), a counter-clockwise spinner and a
	// staggered rise: four utilities, four keyframes — spin-ccw pulls in spin's block, not its own.
	css := GenerateJIT(`<div class="animate-shimmer"></div><i class="animate-breathe"></i><i class="animate-spin-ccw"></i><p class="animate-rise-2"></p>`, &cfg)
	out := UsedKeyframes(css, &cfg)
	for _, w := range []string{"@keyframes animate--shimmer{", "@keyframes animate--breathe{", "@keyframes animate--spin{", "@keyframes animate--rise{"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	if n := strings.Count(out, "@keyframes "); n != 4 {
		t.Errorf("emitted %d @keyframes blocks, want 4:\n%s", n, out)
	}
	if strings.Contains(out, "animate--spin-ccw") {
		t.Errorf("spin-ccw must ride spin's frames, not emit its own:\n%s", out)
	}
	if !strings.Contains(css, "animation-delay:0.16s") {
		t.Errorf("rise-2 stagger not applied:\n%s", css)
	}
}
