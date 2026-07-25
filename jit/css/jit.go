package css

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/jit/hydrate"
)

// classAttrRe matches the STATIC class attribute. The leading whitespace is load-bearing: without
// it the pattern also matches the tail of any attribute ENDING in class — data-kit-class="{ open:
// x }" would be read as a class list and shredded into tokens like "{", "open:", "x". ( does not
// help: "-" is a non-word character, so it already sits on a word boundary.) HTML separates
// attributes with whitespace, so requiring it splits the two cleanly.
var classAttrRe = regexp.MustCompile(`(?:^|\s)class="([^"]+)"`)

// dynamicClassRe matches the AUTHORED dynamic class directive. Its value is an expression, not a
// class list, so it is handed to the compiler rather than split on spaces — see collectClasses.
var dynamicClassRe = regexp.MustCompile(`data-kit-class="([^"]*)"`)

// collectClasses appends every class name a page uses to classes, skipping ones already in seen.
//
// TWO sources, deliberately handled differently. A static class="a b" is a list, so it is split on
// whitespace. A dynamic data-kit-class="open ? 'on' : 'off'" is an EXPRESSION, so it is compiled
// and its string literals are read off the resulting tree — EVERY branch, not whichever one the
// page happens to take at runtime. Splitting that attribute on spaces would yield "?" and "'on'"
// and emit nothing usable.
//
// Every entry point shares this so the scans cannot drift: a class that styles a page inline must
// also reach the site-wide stylesheet, or the same markup renders differently depending on which
// path produced the CSS.
func collectClasses(html string, seen map[string]bool, classes *[]string) {
	add := func(c string) {
		if c != "" && !seen[c] {
			seen[c] = true
			*classes = append(*classes, c)
		}
	}
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		for _, c := range strings.Fields(m[1]) {
			add(c)
		}
	}
	for _, m := range dynamicClassRe.FindAllStringSubmatch(html, -1) {
		names, err := hydrate.ClassLiterals(m[1])
		if err != nil {
			// A malformed expression is already reported by the hydrate render pass, which names the
			// attribute it came from. Reporting it again here would be noise.
			continue
		}
		for _, c := range names {
			add(c)
		}
	}
}

// jitCache maps a class-set signature → generated CSS. The class SET of a page is stable
// across requests (dynamic data doesn't change it), so this skips re-resolution.
var jitCache sync.Map

// GenerateJITCached scans HTML for the Tailwind/utility classes actually used and returns
// the minimal CSS for them, deduped and cached by the (sorted, unique) class set. This is
// the server-side JIT entry point — only what the page uses is emitted.
func GenerateJITCached(html string, cfg *Config) string {
	seen := make(map[string]bool)
	var classes []string
	collectClasses(html, seen, &classes)
	if len(classes) == 0 {
		return ""
	}
	sort.Strings(classes)

	h := fnv.New64a()
	for _, c := range classes {
		_, _ = h.Write([]byte(c))
		_, _ = h.Write([]byte{' '})
	}
	if cfg != nil {
		_, _ = h.Write([]byte(fmt.Sprintf("%v", cfg.Colors)))
		_, _ = h.Write([]byte(fmt.Sprintf("%v", cfg.Animations)))
	}
	sig := h.Sum64()
	if v, ok := jitCache.Load(sig); ok {
		return v.(string)
	}

	out := buildJITCSS(classes, cfg)
	jitCache.Store(sig, out)
	return out
}

func buildJITCSS(classes []string, cfg *Config) string {
	groups := make(map[string][]string)
	for _, c := range classes {
		css, sel, mediaQ := ResolveCore(c, cfg)
		if css == "" {
			continue
		}
		rule := fmt.Sprintf("%s { %s }\n", sel, css)
		groups[mediaQ] = append(groups[mediaQ], rule)
	}

	var b strings.Builder
	// The brand token, emitted ONCE and actually defined. Everything brand-coloured reads it —
	// including the kernel's navigation progress bar, which sets an inline style and so needs a
	// value it can resolve without a stylesheet rule of its own. Two forms because both are needed:
	// the hex for a plain colour, the "r, g, b" triplet for alpha, as in rgba(var(--…-rgb), .12).
	brand := BrandColor(cfg)
	b.WriteString(":root { --kitwork-brand: " + brand.HexString() + "; --kitwork-brand-rgb: " + brand.String() + "; }\n")
	b.WriteString("*, ::before, ::after { box-sizing: border-box; border-width: 0; border-style: solid; border-color: currentColor; }\n")
	b.WriteString("html { line-height: 1.5; -webkit-text-size-adjust: 100%; tab-size: 4; }\n")
	b.WriteString("body { margin: 0; line-height: inherit; }\n")
	b.WriteString("a { color: inherit; text-decoration: inherit; }\n")
	b.WriteString("button, input, optgroup, select, textarea { font: inherit; color: inherit; margin: 0; padding: 0; }\n")
	b.WriteString("img, svg, video, canvas, audio, iframe, embed, object { display: block; vertical-align: middle; }\n")
	// The `hidden` attribute must always win — otherwise any element with an explicit display (e.g. an
	// icon's display:inline-block, or flex/grid utilities) overrides the UA `[hidden]{display:none}`
	// and stays visible. data-kit-show toggles `el.hidden`, so without this it silently fails on such
	// elements (both branches of a show/hide pair render at once).
	b.WriteString("[hidden] { display: none !important; }\n")

	// 1. Base styles (no media query)
	if baseRules, ok := groups[""]; ok {
		for _, r := range baseRules {
			b.WriteString(r)
		}
	}

	// Ordered list of known media queries for correct CSS cascading
	orderedMQs := []string{
		"@media (max-width: 1535.98px)",
		"@media (max-width: 1279.98px)",
		"@media (max-width: 1023.98px)",
		"@media (max-width: 767.98px)",
		"@media (max-width: 639.98px)",
		"@media (min-width: 640px)",
		"@media (min-width: 768px)",
		"@media (min-width: 1024px)",
		"@media (min-width: 1280px)",
		"@media (min-width: 1536px)",
	}

	seenMQ := make(map[string]bool)
	for _, mq := range orderedMQs {
		if rules, ok := groups[mq]; ok {
			b.WriteString(mq + " {\n")
			for _, r := range rules {
				b.WriteString("\t" + r)
			}
			b.WriteString("}\n")
			seenMQ[mq] = true
		}
	}

	// Custom/remaining media queries (if any)
	var remainingMQs []string
	for mq := range groups {
		if mq != "" && !seenMQ[mq] {
			remainingMQs = append(remainingMQs, mq)
		}
	}
	sort.Strings(remainingMQs)
	for _, mq := range remainingMQs {
		b.WriteString(mq + " {\n")
		for _, r := range groups[mq] {
			b.WriteString("\t" + r)
		}
		b.WriteString("}\n")
	}

	return b.String()
}

// GenerateSiteCSS scans many HTML sources (a whole tenant's templates) for utility classes
// and returns ONE combined stylesheet — the site-wide JIT served at a single path like
// /jitcss, so the browser caches it once for every page instead of inlining per render.
func GenerateSiteCSS(cfg *Config, htmls ...string) string {
	seen := make(map[string]bool)
	var classes []string
	for _, h := range htmls {
		collectClasses(h, seen, &classes)
	}
	if len(classes) == 0 {
		return ""
	}
	sort.Strings(classes)
	css := buildJITCSS(classes, cfg)
	// Emit @keyframes + :root vars + reduced-motion for ONLY the animations used site-wide.
	if kf := UsedKeyframes(css, cfg); kf != "" {
		css = kf + "\n" + css
	}
	return css
}

// ============================================================================
// KITWORK INDUSTRIAL SYSTEM (v15.2) - JIT ENGINE MAIN COMPONENT
// ============================================================================

func GenerateFramework() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Framework v15.2 - COMPLETE TABLE */\n")
	b.WriteString(":root {\n")
	for _, k := range Order {
		if _, ok := Colors[k]; ok {
			b.WriteString(fmt.Sprintf("\t--color-%s-rgb: %s;\n", k, Colors[k]))
		}
	}
	b.WriteString("}\n")
	b.WriteString("* { margin: 0; padding: 0; box-sizing: border-box; -webkit-font-smoothing: antialiased; }\n")
	b.WriteString("html { scroll-behavior: smooth; }\n\n")

	// Grouping Buffers: "" -> Root, others -> Media Query
	buffers := make(map[string]*strings.Builder)
	// Initialize strict order for output consistency
	mqOrder := []string{"", "mobile", "tablet", "laptop", "desktop"}

	for _, k := range mqOrder {
		var key string
		if k != "" {
			key = MediaQueries[k]
		}
		buffers[key] = &strings.Builder{}
	}

	// Internal Gen Function using ResolveCore
	gen := func(c string) {
		css, sel, mq := ResolveCore(c, nil)
		if css != "" {
			if buf, ok := buffers[mq]; ok {
				buf.WriteString(fmt.Sprintf("%s { %s }\n", sel, css))
			} else {
				// Fallback if mq not found in pre-init maps (shouldn't happen with strict keys)
				if buffers[mq] == nil {
					buffers[mq] = &strings.Builder{}
				}
				buffers[mq].WriteString(fmt.Sprintf("%s { %s }\n", sel, css))
			}
		}
	}

	prefixes := []string{"", "mobile:", "tablet:", "laptop:", "desktop:"}
	// States to generate for interaction heavy utilities
	states := []string{"", "hover:", "focus:", "active:", "group-hover:"}

	for _, pre := range prefixes {
		// 1. CORE LAYOUT (Base only usually, but some want hover:block)
		// Let's keep Layout simple for now, mostly responsive.
		gen(pre + "container")
		gen(pre + "width-full")
		gen(pre + "height-full")
		gen(pre + "width-screen")
		gen(pre + "height-screen")

		for _, p := range []string{"block", "inline-block", "flex", "grid", "none", "hidden"} {
			gen(pre + "display-" + p)
			gen(pre + "hover:display-" + p)       // Useful for hover effects
			gen(pre + "group-hover:display-" + p) // Useful for mega-menus
		}
		for _, p := range []string{"relative", "absolute", "fixed", "sticky", "static"} {
			gen(pre + "position-" + p)
		}
		for _, p := range []string{"hidden", "auto", "scroll", "visible"} {
			gen(pre + "overflow-" + p)
		}
		for _, p := range []string{"pointer", "default", "text", "move", "not-allowed"} {
			gen(pre + "cursor-" + p)
		}

		// 2. FLEXBOX & GRID
		gen(pre + "flex-row")
		gen(pre + "flex-column")
		gen(pre + "flex-wrap")
		gen(pre + "flex-nowrap")
		gen(pre + "flex-grow")
		gen(pre + "flex-1")
		for _, a := range []string{"start", "end", "center", "between", "around", "evenly", "stretch", "baseline"} {
			gen(pre + "justify-" + a)
			gen(pre + "items-" + a)
			gen(pre + "content-" + a)
			gen(pre + "self-" + a)
		}
		for i := 1; i <= 12; i++ {
			gen(pre + "grid-columns-" + strconv.Itoa(i))
			gen(pre + "grid-span-" + strconv.Itoa(i))
		}
		gen(pre + "grid-span-full")

		// 3. TYPOGRAPHY (States useful for color/decoration)
		gen(pre + "font-outfit")
		gen(pre + "font-mono")
		for _, w := range []string{"bold", "medium", "light", "semibold", "black", "900", "500"} {
			gen(pre + "font-" + w)
		}
		for _, a := range []string{"center", "left", "right", "justify"} {
			gen(pre + "text-" + a)
		}
		for _, st := range states {
			for _, t := range []string{"uppercase", "lowercase", "capitalize", "italic", "underline", "line-through", "no-underline"} {
				gen(pre + st + t)
			}
		}
		gen(pre + "break-words")
		gen(pre + "break-all")
		gen(pre + "white-space-nowrap")

		// 4. COLORS & SHADOWS (Highly Interactive)
		for _, st := range states {
			for _, k := range Order {
				gen(pre + st + "text-" + k)
				gen(pre + st + "background-" + k)
				gen(pre + st + "border-" + k)
				if k == "white" || k == "black" || k == "brand" {
					for _, o := range []int{5, 10, 20, 30, 40, 50, 60, 80, 90} {
						gen(fmt.Sprintf("%s%stext-%s-%d", pre, st, k, o))
						gen(fmt.Sprintf("%s%sbackground-%s-%d", pre, st, k, o))
						gen(fmt.Sprintf("%s%sborder-%s-%d", pre, st, k, o))
					}
				}
			}
			for k := range ShadowLevels {
				gen(pre + st + "shadow-" + k)
			}
			// Opacity & Blur
			for i := 0; i <= 100; i += 10 {
				gen(pre + st + "opacity-" + strconv.Itoa(i))
			}
			gen(pre + st + "blur-small")
			gen(pre + st + "blur-medium")
			gen(pre + st + "blur-large")
			gen(pre + st + "blur-none")
		}

		// 5. ANIMATION & TRANSITION
		gen(pre + "transition-all")
		gen(pre + "transition-none")
		gen(pre + "transition-colors")
		gen(pre + "transition-opacity")
		gen(pre + "duration-150")
		gen(pre + "duration-300")
		gen(pre + "duration-500")
		gen(pre + "animate-pulse")
		gen(pre + "animate-spin")
		gen(pre + "animate-bounce")

		// 6. SCALES (Dimensions & Spacing)
		for _, v := range Scale {
			s := strconv.Itoa(v) + "px"
			for _, p := range []string{"margin", "padding"} {
				gen(pre + p + "-" + s)
				gen(pre + p + "-x-" + s)
				gen(pre + p + "-y-" + s)
				gen(pre + p + "-top-" + s)
				gen(pre + p + "-bottom-" + s)
				gen(pre + p + "-left-" + s)
				gen(pre + p + "-right-" + s)
				if v != 0 && p == "margin" {
					gen(pre + "-" + p + "-" + s)
					gen(pre + "-" + p + "-top-" + s)
				}
			}
			gen(pre + "gap-" + s)
			gen(pre + "gap-x-" + s)
			gen(pre + "gap-y-" + s)

			// Responsive Width/Height usually doesn't need hover, but let's allow it for "width-full" etc
			gen(pre + "width-" + s)
			gen(pre + "height-" + s)
			gen(pre + "text-" + s)
			gen(pre + "rounded-" + s)
			gen(pre + "border-" + s)
		}

		// Special Sizing
		for _, v := range []string{"100pct", "50pct", "33pct", "auto", "screen", "full"} {
			gen(pre + "width-" + v)
			gen(pre + "height-" + v)
		}
		gen(pre + "rounded-full")
		gen(pre + "rounded-none")

		// 7. MISC VISIBILITY & Z-INDEX
		for _, z := range []string{"0", "10", "20", "30", "40", "50", "100", "9999"} {
			gen(pre + "z-index-" + z)
		}
		gen(pre + "-z-index-1")
		gen(pre + "-z-index-2")
	}

	// OUTPUT FLUSHING PHASE
	// 1. Root
	b.WriteString(buffers[""].String())

	// 2. Responsive Blocks (in order)
	for _, k := range mqOrder {
		if k == "" {
			continue // Already written
		}
		mqStr := MediaQueries[k]
		content := buffers[mqStr].String()
		if len(content) > 0 {
			b.WriteString(fmt.Sprintf("\n%s {\n", mqStr))
			// Indent content for beauty
			lines := strings.Split(content, "\n")
			for _, l := range lines {
				if strings.TrimSpace(l) != "" {
					b.WriteString("\t" + l + "\n")
				}
			}
			b.WriteString("}\n")
		}
	}

	return b.String()
}

func GenerateJIT(html string, cfg *Config) string {
	seen := make(map[string]bool)
	var classes []string
	collectClasses(html, seen, &classes)
	if len(classes) == 0 {
		return ""
	}
	sort.Strings(classes)
	return buildJITCSS(classes, cfg)
}
