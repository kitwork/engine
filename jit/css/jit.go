package css

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

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
		css, sel, mq := ResolveCore(c)
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

func GenerateJIT(html string) string {
	var b strings.Builder
	seen := make(map[string]bool)
	re := regexp.MustCompile(`class="([^"]+)"`)
	for _, m := range re.FindAllStringSubmatch(html, -1) {
		for _, class := range strings.Fields(m[1]) {
			if !seen[class] {
				if css := resolve(class); css != "" {
					b.WriteString(css)
					seen[class] = true
				}
			}
		}
	}
	return b.String()
}
