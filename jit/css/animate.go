package css

import (
	"sort"
	"strings"
)

// animate.go — jit animate: the whole animation library lives here, but a page ships ONLY the
// @keyframes it actually uses (see UsedKeyframes). Two families share one utility surface,
// `animate-<name>`, resolved by the tw-animate case in buildProp via resolveAnimate:
//
//	Kitwork's short set — subtle, product-UI motion at 16px / 0.5s with a spring curve:
//	    run-once: up down left right fade flip-x flip-y heartbeat rubber out-up out-down out-left
//	    out-right — consume the shared --animate-* vars so modifiers COMPOSE regardless of
//	    class order (custom properties cascade per-element), with fill:both so the final frame sticks.
//	    loop (infinite): spin spin-ccw pulse bounce float ping blink wave — each ships its own timing.
//	    spin, ping, pulse and bounce are Tailwind's names and carry Tailwind's exact motion.
//	animate.css — the library, vendored whole by vendor_animate.py into animate_catalog_gen.go
//	    (96 animations: fade-in-up, bounce-in, zoom-out, tada, hinge, …; upstream camelCase → kebab).
//	    They run at animate.css's pace — twice --animate-duration, so 1s by default — and with the
//	    browser's `ease` unless a modifier says otherwise, so they look like animate.style.
//	modifiers: faster fast slow slower · ease-linear ease-in ease-out ease-bounce ease-spring ·
//	    delay-1..8 · infinite repeat-2 repeat-3 paused running. Duration/easing/delay set the VAR
//	    (order-independent) and reach both families; iteration/play-state set the property.
//	stagger: animate-<run-once>-<N> == that animation with delay step N, for either family.
//
// Extend like Tailwind: router.jitcss({ theme:{ extend:{ animation, keyframes } } }) still wins —
// cfg.Animations is consulted first in tw-animate, and cfg.Keyframes are emitted by UsedKeyframes.

// animateEntry is one vendored animation: its group, its @keyframes block (renamed animate--<name>),
// the duration factor its upstream class rule applies ("" for 1), and the extra declarations that
// rule carries beyond animation-name (transform-origin, backface-visibility, a timing function).
type animateEntry struct {
	group  string
	frames string
	factor string
	extra  string
}

// AnimationGroup is one section of the vendored library, in animate.style's order — for galleries.
type AnimationGroup struct {
	Name       string
	Animations []string
}

// VendoredAnimations returns the animate.css library by group, for docs and galleries.
func VendoredAnimations() []AnimationGroup { return animateVendoredGroups }

// animateOnce maps a run-once utility name → its @keyframes name.
var animateOnce = map[string]string{
	"up": "animate--up", "down": "animate--down", "left": "animate--left", "right": "animate--right",
	"fade": "animate--fade", "flip-x": "animate--flip-x", "flip-y": "animate--flip-y",
	"heartbeat": "animate--heartbeat", "rubber": "animate--rubber",
	"out-up": "animate--out-up", "out-down": "animate--out-down", "out-left": "animate--out-left",
	"out-right": "animate--out-right",
}

// animateLoop maps a loop utility name → its full `animation` shorthand (own timing + infinite).
// spin, ping, pulse and bounce are Tailwind's names and carry Tailwind's exact values (shorthand and
// keyframes) — an existing name must keep its meaning; the rest are ours.
var animateLoop = map[string]string{
	"spin":     "animate--spin 1s linear infinite",
	"spin-ccw": "animate--spin 1s linear infinite reverse",
	"pulse":    "animate--pulse 2s cubic-bezier(0.4,0,0.6,1) infinite",
	"bounce":   "animate--bounce 1s infinite",
	"float":    "animate--float 3.5s ease-in-out infinite",
	"ping":     "animate--ping 1s cubic-bezier(0,0,0.2,1) infinite",
	"blink":    "animate--blink 1.2s step-start infinite",
	"wave":     "animate--wave 2.5s ease-in-out infinite",
}

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

// animateFrames holds every @keyframes block, keyed by its @-name. UsedKeyframes emits only the ones
// a page references. Minified transcription of the animate.css library.
var animateFrames = map[string]string{
	"animate--up":        "@keyframes animate--up{from{opacity:0;transform:translateY(var(--animate-distance))}to{opacity:1;transform:translateY(0)}}",
	"animate--down":      "@keyframes animate--down{from{opacity:0;transform:translateY(calc(-1 * var(--animate-distance)))}to{opacity:1;transform:translateY(0)}}",
	"animate--left":      "@keyframes animate--left{from{opacity:0;transform:translateX(calc(var(--animate-distance) + 4px))}to{opacity:1;transform:translateX(0)}}",
	"animate--right":     "@keyframes animate--right{from{opacity:0;transform:translateX(calc(-1 * var(--animate-distance) - 4px))}to{opacity:1;transform:translateX(0)}}",
	"animate--fade":      "@keyframes animate--fade{from{opacity:0}to{opacity:1}}",
	"animate--flip-x":    "@keyframes animate--flip-x{from{opacity:0;transform:perspective(400px) rotateX(-80deg)}60%{opacity:1;transform:perspective(400px) rotateX(10deg)}80%{transform:perspective(400px) rotateX(-5deg)}to{transform:perspective(400px) rotateX(0deg)}}",
	"animate--flip-y":    "@keyframes animate--flip-y{from{opacity:0;transform:perspective(400px) rotateY(-80deg)}60%{opacity:1;transform:perspective(400px) rotateY(10deg)}80%{transform:perspective(400px) rotateY(-5deg)}to{transform:perspective(400px) rotateY(0deg)}}",
	"animate--heartbeat": "@keyframes animate--heartbeat{0%,100%{transform:scale(1)}14%{transform:scale(1.15)}28%{transform:scale(1)}42%{transform:scale(1.15)}70%{transform:scale(1)}}",
	"animate--rubber":    "@keyframes animate--rubber{0%{transform:scaleX(1) scaleY(1)}30%{transform:scaleX(1.3) scaleY(0.75)}40%{transform:scaleX(0.75) scaleY(1.25)}50%{transform:scaleX(1.15) scaleY(0.85)}65%{transform:scaleX(0.95) scaleY(1.05)}75%{transform:scaleX(1.05) scaleY(0.95)}to{transform:scaleX(1) scaleY(1)}}",
	"animate--out-up":    "@keyframes animate--out-up{from{opacity:1;transform:translateY(0)}to{opacity:0;transform:translateY(calc(-1 * var(--animate-distance)))}}",
	"animate--out-down":  "@keyframes animate--out-down{from{opacity:1;transform:translateY(0)}to{opacity:0;transform:translateY(var(--animate-distance))}}",
	"animate--out-left":  "@keyframes animate--out-left{from{opacity:1;transform:translateX(0)}to{opacity:0;transform:translateX(-24px)}}",
	"animate--out-right": "@keyframes animate--out-right{from{opacity:1;transform:translateX(0)}to{opacity:0;transform:translateX(24px)}}",
	"animate--spin":      "@keyframes animate--spin{from{transform:rotate(0deg)}to{transform:rotate(360deg)}}",
	"animate--pulse":     "@keyframes animate--pulse{50%{opacity:.5}}",
	"animate--bounce":    "@keyframes animate--bounce{0%,100%{transform:translateY(-25%);animation-timing-function:cubic-bezier(0.8,0,1,1)}50%{transform:none;animation-timing-function:cubic-bezier(0,0,0.2,1)}}",
	"animate--float":     "@keyframes animate--float{0%,100%{transform:translateY(0)}50%{transform:translateY(-8px)}}",
	"animate--ping":      "@keyframes animate--ping{75%,100%{transform:scale(2);opacity:0}}",
	"animate--blink":     "@keyframes animate--blink{0%,100%{opacity:1}50%{opacity:0}}",
	"animate--wave":      "@keyframes animate--wave{0%{transform:rotate(0deg)}15%{transform:rotate(14deg)}30%{transform:rotate(-8deg)}40%{transform:rotate(14deg)}50%{transform:rotate(-4deg)}60%{transform:rotate(10deg)}70%{transform:rotate(0deg)}100%{transform:rotate(0deg)}}",
}

// animateRootVars are the library defaults; the run-once utilities read them via var(), so modifiers
// (which rewrite the vars on the element) compose regardless of class order. Easing has no root
// value on purpose: each family falls back to its own curve (spring for the short set, `ease` for
// animate.css) and an ease-* modifier overrides both by setting --animate-easing on the element.
const animateRootVars = ":root{--animate-duration:0.5s;--animate-delay:0s;--animate-distance:16px}"

const (
	animateShortEasing    = "var(--animate-easing, cubic-bezier(0.22, 1, 0.36, 1))"
	animateVendoredEasing = "var(--animate-easing, ease)"
)

// runOnce is the body of a run-once utility: keyframe + the shared vars, fill:both so the final
// frame sticks. delay is the var by default, or a stagger step; the vendored family passes its own
// duration and easing plus the extra declarations its upstream class rule carries.
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

// runOnceFor resolves a run-once base name from either family with the given delay, or "".
func runOnceFor(base, delay string) string {
	if kf, ok := animateOnce[base]; ok {
		return runOnce(kf, "var(--animate-duration)", animateShortEasing, delay, "")
	}
	if e, ok := animateVendored[base]; ok {
		return runOnce("animate--"+base, vendoredDuration(e), animateVendoredEasing, delay, e.extra)
	}
	return ""
}

// animateReducedMotion honours the OS setting: near-instant, single-run — the accessible default
// (matches the library's own @media guard). Content still ends in its final frame (fill:both).
const animateReducedMotion = `@media (prefers-reduced-motion:reduce){[class*="animate-"]{animation-duration:.01ms!important;animation-iteration-count:1!important}}`

// resolveAnimate returns the CSS body for an `animate-<name>` utility, or "" if unknown.
func resolveAnimate(name string) string {
	if css := runOnceFor(name, "var(--animate-delay)"); css != "" {
		return css
	}
	if sh, ok := animateLoop[name]; ok {
		css := "animation:" + sh + ";"
		if name == "wave" {
			css += "transform-origin:70% 70%;display:inline-block;"
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
	var names []string
	for kf := range animateFrames {
		if referenced(css, kf) {
			names = append(names, kf)
		}
	}
	for name := range animateVendored {
		if kf := "animate--" + name; referenced(css, kf) {
			names = append(names, kf)
		}
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
			if frames, ok := animateFrames[kf]; ok {
				b.WriteString(frames)
			} else {
				b.WriteString(animateVendored[strings.TrimPrefix(kf, "animate--")].frames)
			}
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
