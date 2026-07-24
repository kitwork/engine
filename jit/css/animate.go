package css

import (
	"sort"
	"strings"
)

// animate.go — the JIT animation catalog, vendored from the hand-written animate.css library
// (Pure CSS Animation Library, MIT). It is the animation counterpart to jiticons/jitfonts: the whole
// library lives here, but a page ships ONLY the @keyframes it actually uses (see UsedKeyframes).
//
// Utility surface — `animate-<name>`, resolved by the tw-animate case in buildProp via resolveAnimate:
//
//	run-once (entrance/attention/exit): up down left right fade zoom-in zoom-out flip-x flip-y
//	    roll-in shake wobble heartbeat jello rubber tada fade-out out-up out-down out-left out-right
//	    zoom-out-exit  — consume the shared --animate-* vars so modifiers COMPOSE regardless of class
//	    order (custom properties cascade per-element), with fill:both so the final frame sticks.
//	loop (infinite): spin spin-ccw pulse bounce float ping blink wave — each ships its own timing.
//	modifiers: faster fast slow slower · ease-linear ease-in ease-out ease-bounce ease-spring ·
//	    delay-1..8 · infinite repeat-2 repeat-3 paused running. Duration/easing/delay set the VAR
//	    (order-independent); iteration/play-state set the property.
//
// Extend like Tailwind: router.jitcss({ theme:{ extend:{ animation, keyframes } } }) still wins —
// cfg.Animations is consulted first in tw-animate, and cfg.Keyframes are emitted by UsedKeyframes.

// animateOnce maps a run-once utility name → its @keyframes name.
var animateOnce = map[string]string{
	"up": "animate--up", "down": "animate--down", "left": "animate--left", "right": "animate--right",
	"fade": "animate--fade", "zoom-in": "animate--zoom-in", "zoom-out": "animate--zoom-out-enter",
	"flip-x": "animate--flip-x", "flip-y": "animate--flip-y", "roll-in": "animate--roll-in",
	"shake": "animate--shake", "wobble": "animate--wobble", "heartbeat": "animate--heartbeat",
	"jello": "animate--jello", "rubber": "animate--rubber", "tada": "animate--tada",
	"fade-out": "animate--fade-out", "out-up": "animate--out-up", "out-down": "animate--out-down",
	"out-left": "animate--out-left", "out-right": "animate--out-right", "zoom-out-exit": "animate--zoom-out-exit",
}

// animateLoop maps a loop utility name → its full `animation` shorthand (own timing + infinite).
var animateLoop = map[string]string{
	"spin":     "animate--spin 1s linear infinite",
	"spin-ccw": "animate--spin 1s linear infinite reverse",
	"pulse":    "animate--pulse 2s ease-in-out infinite",
	"bounce":   "animate--bounce 1s infinite",
	"float":    "animate--float 3.5s ease-in-out infinite",
	"ping":     "animate--ping 1.4s cubic-bezier(0,0,0.2,1) infinite",
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
	"animate--up":             "@keyframes animate--up{from{opacity:0;transform:translateY(var(--animate-distance))}to{opacity:1;transform:translateY(0)}}",
	"animate--down":           "@keyframes animate--down{from{opacity:0;transform:translateY(calc(-1 * var(--animate-distance)))}to{opacity:1;transform:translateY(0)}}",
	"animate--left":           "@keyframes animate--left{from{opacity:0;transform:translateX(calc(var(--animate-distance) + 4px))}to{opacity:1;transform:translateX(0)}}",
	"animate--right":          "@keyframes animate--right{from{opacity:0;transform:translateX(calc(-1 * var(--animate-distance) - 4px))}to{opacity:1;transform:translateX(0)}}",
	"animate--fade":           "@keyframes animate--fade{from{opacity:0}to{opacity:1}}",
	"animate--zoom-in":        "@keyframes animate--zoom-in{from{opacity:0;transform:scale(0.88)}to{opacity:1;transform:scale(1)}}",
	"animate--zoom-out-enter": "@keyframes animate--zoom-out-enter{from{opacity:0;transform:scale(1.12)}to{opacity:1;transform:scale(1)}}",
	"animate--flip-x":         "@keyframes animate--flip-x{from{opacity:0;transform:perspective(400px) rotateX(-80deg)}60%{opacity:1;transform:perspective(400px) rotateX(10deg)}80%{transform:perspective(400px) rotateX(-5deg)}to{transform:perspective(400px) rotateX(0deg)}}",
	"animate--flip-y":         "@keyframes animate--flip-y{from{opacity:0;transform:perspective(400px) rotateY(-80deg)}60%{opacity:1;transform:perspective(400px) rotateY(10deg)}80%{transform:perspective(400px) rotateY(-5deg)}to{transform:perspective(400px) rotateY(0deg)}}",
	"animate--roll-in":        "@keyframes animate--roll-in{from{opacity:0;transform:translateX(-100%) rotate(-120deg)}to{opacity:1;transform:translateX(0) rotate(0deg)}}",
	"animate--shake":          "@keyframes animate--shake{0%,100%{transform:translateX(0)}10%,30%,50%,70%,90%{transform:translateX(-8px)}20%,40%,60%,80%{transform:translateX(8px)}}",
	"animate--wobble":         "@keyframes animate--wobble{0%{transform:translateX(0)}15%{transform:translateX(-20px) rotate(-5deg)}30%{transform:translateX(15px) rotate(4deg)}45%{transform:translateX(-10px) rotate(-3deg)}60%{transform:translateX(7px) rotate(2deg)}75%{transform:translateX(-4px) rotate(-1deg)}to{transform:translateX(0)}}",
	"animate--heartbeat":      "@keyframes animate--heartbeat{0%,100%{transform:scale(1)}14%{transform:scale(1.15)}28%{transform:scale(1)}42%{transform:scale(1.15)}70%{transform:scale(1)}}",
	"animate--jello":          "@keyframes animate--jello{0%,11%,100%{transform:skewX(0deg) skewY(0deg)}22%{transform:skewX(-12deg) skewY(-12deg)}33%{transform:skewX(10deg) skewY(10deg)}44%{transform:skewX(-6deg) skewY(-6deg)}55%{transform:skewX(4deg) skewY(4deg)}66%{transform:skewX(-2deg) skewY(-2deg)}77%{transform:skewX(1deg) skewY(1deg)}88%{transform:skewX(-0.5deg) skewY(-0.5deg)}}",
	"animate--rubber":         "@keyframes animate--rubber{0%{transform:scaleX(1) scaleY(1)}30%{transform:scaleX(1.3) scaleY(0.75)}40%{transform:scaleX(0.75) scaleY(1.25)}50%{transform:scaleX(1.15) scaleY(0.85)}65%{transform:scaleX(0.95) scaleY(1.05)}75%{transform:scaleX(1.05) scaleY(0.95)}to{transform:scaleX(1) scaleY(1)}}",
	"animate--tada":           "@keyframes animate--tada{0%,100%{transform:scale(1) rotate(0deg)}10%,20%{transform:scale(0.9) rotate(-3deg)}30%,50%,70%,90%{transform:scale(1.1) rotate(3deg)}40%,60%,80%{transform:scale(1.1) rotate(-3deg)}}",
	"animate--fade-out":       "@keyframes animate--fade-out{from{opacity:1}to{opacity:0}}",
	"animate--out-up":         "@keyframes animate--out-up{from{opacity:1;transform:translateY(0)}to{opacity:0;transform:translateY(calc(-1 * var(--animate-distance)))}}",
	"animate--out-down":       "@keyframes animate--out-down{from{opacity:1;transform:translateY(0)}to{opacity:0;transform:translateY(var(--animate-distance))}}",
	"animate--out-left":       "@keyframes animate--out-left{from{opacity:1;transform:translateX(0)}to{opacity:0;transform:translateX(-24px)}}",
	"animate--out-right":      "@keyframes animate--out-right{from{opacity:1;transform:translateX(0)}to{opacity:0;transform:translateX(24px)}}",
	"animate--zoom-out-exit":  "@keyframes animate--zoom-out-exit{from{opacity:1;transform:scale(1)}to{opacity:0;transform:scale(0.88)}}",
	"animate--spin":           "@keyframes animate--spin{from{transform:rotate(0deg)}to{transform:rotate(360deg)}}",
	"animate--pulse":          "@keyframes animate--pulse{0%,100%{opacity:1}50%{opacity:0.35}}",
	"animate--bounce":         "@keyframes animate--bounce{0%,100%{transform:translateY(0);animation-timing-function:cubic-bezier(0.8,0,1,1)}50%{transform:translateY(-18px);animation-timing-function:cubic-bezier(0,0,0.2,1)}}",
	"animate--float":          "@keyframes animate--float{0%,100%{transform:translateY(0)}50%{transform:translateY(-8px)}}",
	"animate--ping":           "@keyframes animate--ping{75%,100%{transform:scale(2);opacity:0}}",
	"animate--blink":          "@keyframes animate--blink{0%,100%{opacity:1}50%{opacity:0}}",
	"animate--wave":           "@keyframes animate--wave{0%{transform:rotate(0deg)}15%{transform:rotate(14deg)}30%{transform:rotate(-8deg)}40%{transform:rotate(14deg)}50%{transform:rotate(-4deg)}60%{transform:rotate(10deg)}70%{transform:rotate(0deg)}100%{transform:rotate(0deg)}}",
}

// animateRootVars are the library defaults; the run-once utilities read them via var(), so modifiers
// (which rewrite the vars on the element) compose regardless of class order.
const animateRootVars = ":root{--animate-duration:0.5s;--animate-delay:0s;--animate-easing:cubic-bezier(0.22, 1, 0.36, 1);--animate-distance:16px}"

// animateReducedMotion honours the OS setting: near-instant, single-run — the accessible default
// (matches the library's own @media guard). Content still ends in its final frame (fill:both).
const animateReducedMotion = `@media (prefers-reduced-motion:reduce){[class*="animate-"]{animation-duration:.01ms!important;animation-iteration-count:1!important}}`

// resolveAnimate returns the CSS body for an `animate-<name>` utility, or "" if unknown.
func resolveAnimate(name string) string {
	if kf, ok := animateOnce[name]; ok {
		return "animation-name:" + kf + ";animation-duration:var(--animate-duration);" +
			"animation-timing-function:var(--animate-easing);animation-delay:var(--animate-delay);animation-fill-mode:both;"
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
		if kf, ok := animateOnce[name[:i]]; ok {
			if d, ok := animateDelay["delay-"+name[i+1:]]; ok {
				return "animation-name:" + kf + ";animation-duration:var(--animate-duration);" +
					"animation-timing-function:var(--animate-easing);animation-delay:" + d + ";animation-fill-mode:both;"
			}
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
			b.WriteString(animateFrames[kf])
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
