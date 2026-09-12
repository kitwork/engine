package css

import (
	"sort"
	"strings"
)

// animate.go — jit animate: the whole animation library lives in the binary, but a page ships ONLY
// the @keyframes it actually uses (see UsedKeyframes). Two catalogs of the same shape share one
// utility surface, `animate-<name>`, resolved by the tw-animate case in buildProp via resolveAnimate:
//
//	Kitwork's set (animate_kitwork.go) — quiet product-UI motion at 16px / 0.5s with a spring curve,
//	    in four groups: enter (fade up down left right scale-in rise flip-x flip-y reveal), exit
//	    (vanish out-* scale-out sink), attention (pop nudge tick highlight heartbeat rubber) and live
//	    loops (spin ping pulse bounce float blink wave breathe shimmer marquee sweep drift).
//	    spin, ping, pulse and bounce are Tailwind's names and carry Tailwind's exact motion.
//	animate.css (animate_catalog_gen.go, vendored whole by vendor_animate.py) — the library under
//	    its own names in kebab: fade-in-up, bounce-in, zoom-out, tada, hinge, … (96). It runs at
//	    animate.css's pace — twice --animate-duration, so 1s — and with the browser's `ease`, so it
//	    looks like animate.style.
//	modifiers: faster fast slow slower · ease-linear ease-in ease-out ease-bounce ease-spring ·
//	    delay-1..8 · infinite once repeat-2 repeat-3 paused running reverse alternate. Duration,
//	    easing and delay set the VAR (order-independent, so modifiers compose regardless of class
//	    order — custom properties cascade per element) and reach both catalogs; iteration,
//	    play-state and direction set the property.
//	stagger: animate-<run-once>-<N> == that animation with delay step N, from either catalog.
//
// Extend like Tailwind: router.jitcss({ theme:{ extend:{ animation, keyframes } } }) still wins —
// cfg.Animations is consulted first in tw-animate, and cfg.Keyframes are emitted by UsedKeyframes.

// animateEntry is one animation in either catalog: its group, its @keyframes block (named
// animate--<name>; empty when it rides another entry's, like spin-ccw on spin), the loop shorthand
// for loops (empty for run-once), the duration factor a vendored class rule applies ("" for 1), and
// the extra declarations appended to the utility rule (transform-origin, a gradient, a fill-mode).
type animateEntry struct {
	group  string
	frames string
	loop   string
	factor string
	extra  string
}

// AnimationGroup is one section of a catalog, in reading order — for galleries and docs.
type AnimationGroup struct {
	Name       string
	Animations []string
}

// KitworkAnimations returns the Kitwork set by group: enter, exit, attention, live.
func KitworkAnimations() []AnimationGroup { return animateKitworkGroups }

// VendoredAnimations returns the animate.css library by group, in animate.style's order.
func VendoredAnimations() []AnimationGroup { return animateVendoredGroups }

// animateDelay maps delay-N → animation-delay value (set on the shared var).
var animateDelay = map[string]string{
	"delay-1": "0.08s", "delay-2": "0.16s", "delay-3": "0.24s", "delay-4": "0.32s",
	"delay-5": "0.44s", "delay-6": "0.58s", "delay-7": "0.72s", "delay-8": "0.90s",
}

// animateEase maps ease-<name> → timing function (set on the shared var).
var animateEase = map[string]string{
	"ease-linear": "linear",
	"ease-in":     "cubic-bezier(0.4,0,1,1)",
	"ease-out":    "cubic-bezier(0,0,0.2,1)",
	"ease-bounce": "cubic-bezier(0.34,1.56,0.64,1)",
	"ease-spring": "cubic-bezier(0.22,1,0.36,1)",
}

// animateDuration maps the named speeds → duration (set on the shared var).
var animateDuration = map[string]string{
	"faster": "0.15s", "fast": "0.25s", "slow": "0.85s", "slower": "1.4s",
}

// animateRootVars are the defaults; the run-once utilities read them via var(), so modifiers (which
// rewrite the vars on the element) compose regardless of class order. Easing has no root value on
// purpose: each catalog falls back to its own curve (the spring for Kitwork's set, `ease` for
// animate.css) and an ease-* modifier overrides both by setting --animate-easing on the element.
const animateRootVars = ":root{--animate-duration:0.5s;--animate-delay:0s;--animate-distance:16px}"

const (
	animateKitworkEasing  = "var(--animate-easing, cubic-bezier(0.22, 1, 0.36, 1))"
	animateVendoredEasing = "var(--animate-easing, ease)"
)

// animateReducedMotion honours the OS setting: near-instant, single-run — the accessible default
// (matches animate.css's own @media guard). Content still ends in its final frame (fill:both).
const animateReducedMotion = `@media (prefers-reduced-motion:reduce){[class*="animate-"]{animation-duration:.01ms!important;animation-iteration-count:1!important}}`

// runOnce is the body of a run-once utility: keyframe + the shared vars, fill:both so the final
// frame sticks. delay is the var by default, or a stagger step. extra comes last so an entry can
// override a default (highlight sets fill-mode:none — its ring must not linger as a shadow).
func runOnce(kf, duration, easing, delay, extra string) string {
	css := "animation-name:" + kf + ";animation-duration:" + duration + ";" +
		"animation-timing-function:" + easing + ";animation-delay:" + delay + ";animation-fill-mode:both;"
	if extra != "" {
		css += extra + ";"
	}
	return css
}

// vendoredDuration is animate.css's pace: twice --animate-duration (1s by default, upstream's
// default) times the per-animation factor its class rule applies (hinge ×2, bounceIn ×0.75, …).
func vendoredDuration(e animateEntry) string {
	if e.factor == "" {
		return "calc(var(--animate-duration) * 2)"
	}
	return "calc(var(--animate-duration) * 2 * " + e.factor + ")"
}

// runOnceFor resolves a run-once base name from either catalog with the given delay, or "".
func runOnceFor(base, delay string) string {
	if e, ok := animateKitwork[base]; ok && e.loop == "" {
		return runOnce("animate--"+base, "var(--animate-duration)", animateKitworkEasing, delay, e.extra)
	}
	if e, ok := animateVendored[base]; ok {
		return runOnce("animate--"+base, vendoredDuration(e), animateVendoredEasing, delay, e.extra)
	}
	return ""
}

// resolveAnimate returns the CSS body for an `animate-<name>` utility, or "" if unknown.
func resolveAnimate(name string) string {
	if css := runOnceFor(name, "var(--animate-delay)"); css != "" {
		return css
	}
	if e, ok := animateKitwork[name]; ok && e.loop != "" {
		css := "animation:" + e.loop + ";"
		if e.extra != "" {
			css += e.extra + ";"
		}
		return css
	}
	if v, ok := animateDuration[name]; ok {
		return "--animate-duration:" + v + ";"
	}
	if v, ok := animateEase[name]; ok {
		return "--animate-easing:" + v + ";"
	}
	if v, ok := animateDelay[name]; ok {
		return "--animate-delay:" + v + ";"
	}
	switch name {
	case "infinite":
		return "animation-iteration-count:infinite;"
	case "once":
		return "animation-iteration-count:1;"
	case "repeat-2":
		return "animation-iteration-count:2;"
	case "repeat-3":
		return "animation-iteration-count:3;"
	case "paused":
		return "animation-play-state:paused;"
	case "running":
		return "animation-play-state:running;"
	case "reverse":
		return "animation-direction:reverse;"
	case "alternate":
		return "animation-direction:alternate;"
	case "alternate-reverse":
		return "animation-direction:alternate-reverse;"
	}
	// Staggered entrance shorthand: animate-<once>-<N> == the run-once animation with delay step N
	// (self-contained, so `animate-up-3` works alone or alongside `animate-up`). Enables list
	// stagger: each item a higher N. Only valid for a known run-once base + delay 1..8.
	if i := strings.LastIndex(name, "-"); i > 0 {
		if d, ok := animateDelay["delay-"+name[i+1:]]; ok {
			return runOnceFor(name[:i], d)
		}
	}
	return ""
}

// referenced reports whether a keyframe @-name is actually used in the generated CSS — matched at a
// boundary (`;`, ` `, `,`) so animate--fade doesn't spuriously match inside animate--fade-out.
func referenced(css, name string) bool {
	return strings.Contains(css, name+";") || strings.Contains(css, name+" ") || strings.Contains(css, name+",")
}

// UsedKeyframes returns the @keyframes + :root vars + reduced-motion guard for ONLY the animations
// referenced in the already-generated utility CSS — the emit-only-used half of jit animate. Empty
// when the page animates nothing. cfg.Keyframes (theme.extend) are included when referenced.
func UsedKeyframes(css string, cfg *Config) string {
	frames := map[string]string{}
	for _, catalog := range []map[string]animateEntry{animateKitwork, animateVendored} {
		for name, e := range catalog {
			if kf := "animate--" + name; e.frames != "" && referenced(css, kf) {
				frames[kf] = e.frames
			}
		}
	}
	names := make([]string, 0, len(frames))
	for kf := range frames {
		names = append(names, kf)
	}
	var extra []string
	if cfg != nil {
		for name := range cfg.Keyframes {
			if referenced(css, name) {
				extra = append(extra, name)
			}
		}
	}
	// animate-on-hover: the paused rule ships as a normal utility (with the ` *` child selector); the
	// :hover-runs counterpart can't be a single utility rule, so it's emitted here as a static helper.
	hasHover := strings.Contains(css, "animate-on-hover")

	if len(names) == 0 && len(extra) == 0 && !hasHover {
		return ""
	}
	sort.Strings(names) // deterministic output → stable cache signature
	sort.Strings(extra)

	var b strings.Builder
	if len(names) > 0 || len(extra) > 0 {
		b.WriteString(animateRootVars)
		for _, kf := range names {
			b.WriteString(frames[kf])
		}
		for _, name := range extra {
			b.WriteString("@keyframes " + name + "{" + cfg.Keyframes[name] + "}")
		}
		b.WriteString(animateReducedMotion)
	}
	if hasHover {
		b.WriteString(".animate-on-hover:hover *{animation-play-state:running}")
	}
	return b.String()
}
