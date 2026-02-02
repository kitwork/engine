package main

import (
	"fmt"
	"io/ioutil"
	"regexp"
	"strconv"
	"strings"
)

// ============================================================================
// KITWORK INDUSTRIAL SYSTEM (v14.0) - THE ARCHITECT CORE
// ============================================================================

const ExplicitUnit = "px"

var (
	Colors = map[string]string{
		"brand":   "248, 34, 68", // Industrial Red
		"white":   "255, 255, 255",
		"black":   "5, 5, 5",
		"gray":    "140, 140, 140",
		"elegant": "12, 12, 12",
		"dark":    "8, 8, 8",
		"success": "34, 197, 94",
		"gold":    "234, 179, 8",
	}

	Order       = []string{"brand", "white", "black", "gray", "elegant", "dark", "success", "gold"}
	AlphaScales = []int{2, 5, 8, 10, 20, 30, 40, 50, 60, 80}

	Queries = map[string]string{
		"tablet": "@media (max-width: 992px)",
		"mobile": "@media (max-width: 620px)",
	}

	States = map[string]string{
		"hover":       "hover",
		"group-hover": ".group:hover &",
	}

	Scale = []int{0, 1, 2, 4, 8, 12, 16, 20, 24, 32, 40, 48, 56, 64, 72, 82, 96, 110, 120, 140, 160, 200, 240, 320, 480}

	ShadowLevels = map[string]string{
		"soft":       "0 2px 10px rgba(0,0,0,0.1)",
		"industrial": "0 10px 30px -10px rgba(0,0,0,0.5)",
		"glow":       "0 0 40px rgba(248, 34, 68, 0.15)",
		"great":      "0 30px 60px -12px rgba(0,0,0,0.6)",
		"system":     "0 0 0 1px rgba(255, 255, 255, 0.05), 0 20px 40px -12px rgba(0,0,0,0.8)",
		"core":       "0 0 0 1px rgba(248, 34, 68, 0.1), 0 20px 40px -12px rgba(248, 34, 68, 0.1)",
	}
)

type ClassResolver func(m []string, neg bool) string

var Registry = []struct {
	Pattern string
	Solve   ClassResolver
}{
	// 1. ARCHITECTURAL INFRASTRUCTURE
	{`^container$`, func(_ []string, _ bool) string {
		return "width: 100%; max-width: 1280px; margin-inline: auto; padding-inline: 32px;"
	}},
	{`^(background)-(gradient|grid|haze)-(brand|white|dark)$`, func(m []string, _ bool) string {
		color := m[3]
		rgb := Colors[color]
		if m[2] == "grid" {
			return fmt.Sprintf("background-image: radial-gradient(circle at 1px 1px, rgba(%s, 0.05) 1px, transparent 0); background-size: 32px 32px;", rgb)
		}
		if m[2] == "haze" {
			return fmt.Sprintf("background: radial-gradient(circle at 50%% 20%%, rgba(%s, 0.12), transparent 70%%);", rgb)
		}
		if m[2] == "gradient" && color == "brand" {
			return "background: linear-gradient(135deg, #f82244 0%, #d61b3c 100%);"
		}
		return ""
	}},
	{`^(blur)-(small|medium|large|none)$`, func(m []string, _ bool) string {
		v := map[string]string{"small": "4px", "medium": "16px", "large": "40px", "none": "0"}
		return "backdrop-filter: blur(" + v[m[2]] + "); -webkit-backdrop-filter: blur(" + v[m[2]] + ");"
	}},

	// 2. SPATIAL & SIZING (THE PRECISION ENGINE)
	{`^(margin|padding)-(x|y)-([0-9%.-]+[a-z]*|none|auto)$`, func(m []string, neg bool) string {
		p, axis, val := m[1], m[2], transformUnit(m[3])
		if neg {
			val = "-" + val
		}
		if axis == "x" {
			return fmt.Sprintf("%s-left: %s; %s-right: %s;", p, val, p, val)
		}
		return fmt.Sprintf("%s-top: %s; %s-bottom: %s;", p, val, p, val)
	}},
	{`^(gap)-(x|y)-([0-9%.-]+[a-z]*|none)$`, func(m []string, _ bool) string {
		axis, val := m[2], transformUnit(m[3])
		if axis == "x" {
			return "column-gap: " + val + ";"
		}
		return "row-gap: " + val + ";"
	}},
	{`^(margin|padding|gap|top|bottom|left|right)-([0-9%.-]+[a-z]*|none|auto)$`, func(m []string, neg bool) string {
		p, val := m[1], transformUnit(m[2])
		if m[2] == "none" {
			return p + ": none;"
		}
		if neg {
			val = "-" + val
		}
		return fmt.Sprintf("%s: %s;", p, val)
	}},
	{`^(border|outline)-(top|bottom|left|right)-([0-9%.-]+[a-z]*|none)$`, func(m []string, _ bool) string {
		p, axis, val := m[1], m[2], transformUnit(m[3])
		if m[3] == "none" {
			return fmt.Sprintf("%s-%s: none;", p, axis)
		}
		return fmt.Sprintf("%s-%s: %s solid;", p, axis, val)
	}},
	{`^(border|outline)-([0-9%.-]+[a-z]*|none)$`, func(m []string, _ bool) string {
		p, val := m[1], transformUnit(m[2])
		if m[2] == "none" {
			return p + ": none;"
		}
		return fmt.Sprintf("%s: %s solid;", p, val)
	}},
	{`^(width|height|max-width|min-width|max-height|min-height)-([0-9%.-]+[a-z]*|full|screen|auto|fit)$`, func(m []string, _ bool) string {
		p, val := m[1], transformUnit(m[2])
		if m[2] == "full" {
			val = "100%"
		} else if m[2] == "screen" {
			if p == "width" || p == "max-width" {
				val = "100vw"
			} else {
				val = "100vh"
			}
		} else if m[2] == "fit" {
			val = "fit-content"
		}
		return fmt.Sprintf("%s: %s;", p, val)
	}},
	{`^(aspect)-(video|square|auto)$`, func(m []string, _ bool) string {
		v := map[string]string{"video": "16 / 9", "square": "1 / 1", "auto": "auto"}
		return "aspect-ratio: " + v[m[2]] + ";"
	}},

	// 3. COLOR SYSTEM (THE INDUSTRIAL PALETTE)
	{`^(background|color|border|text)-([a-zA-Z]+)(?:-([0-9]+))?$`, func(m []string, _ bool) string {
		tg, color, alpha := m[1], m[2], m[3]
		if tg == "text" {
			tg = "color"
		} else if tg == "background" {
			tg = "background-color"
		} else if tg == "border" {
			tg = "border-color"
		}
		rgb, ok := Colors[color]
		if !ok {
			return ""
		}
		if alpha != "" {
			return fmt.Sprintf("%s: rgba(%s, %.2f);", tg, rgb, float64(mustInt(alpha))/100.0)
		}
		return fmt.Sprintf("%s: rgb(%s);", tg, rgb)
	}},
	{`^(border)-(top|bottom|left|right)-([a-zA-Z]+)(?:-([0-9]+))?$`, func(m []string, _ bool) string {
		axis, color, alpha := m[2], m[3], m[4]
		rgb, ok := Colors[color]
		if !ok {
			return ""
		}
		if alpha != "" {
			return fmt.Sprintf("border-%s-color: rgba(%s, %.2f);", axis, rgb, float64(mustInt(alpha))/100.0)
		}
		return fmt.Sprintf("border-%s-color: rgb(%s);", axis, rgb)
	}},
	{`^(opacity)-(\d+)$`, func(m []string, _ bool) string {
		return fmt.Sprintf("opacity: %.2f;", float64(mustInt(m[2]))/100.0)
	}},

	// 4. TYPOGRAPHY (THE HIERARCHY ENGINE)
	{`^(font)-(outfit|mono|bold|medium|light|semibold|black|900|500)$`, func(m []string, _ bool) string {
		w := map[string]string{"bold": "700", "medium": "500", "500": "500", "light": "300", "semibold": "600", "black": "900", "900": "900"}
		if weight, ok := w[m[2]]; ok {
			return "font-weight: " + weight + ";"
		}
		if m[2] == "outfit" {
			return "font-family: 'Outfit', sans-serif;"
		}
		return "font-family: 'JetBrains Mono', monospace;"
	}},
	{`^(text|font-size)-([0-9]+[a-z]*)$`, func(m []string, _ bool) string {
		return "font-size: " + transformUnit(m[2]) + ";"
	}},
	{`^(italic|uppercase|lowercase|capitalize|underline|text-clip|glow-brand)$`, func(m []string, _ bool) string {
		v := m[1]
		if v == "italic" {
			return "font-style: italic;"
		}
		if v == "underline" {
			return "text-decoration: underline;"
		}
		if v == "text-clip" {
			return "background-clip: text; -webkit-background-clip: text; -webkit-text-fill-color: transparent;"
		}
		if v == "glow-brand" {
			return "text-shadow: 0 0 30px rgba(248, 34, 68, 0.4);"
		}
		return "text-transform: " + v + ";"
	}},
	{`^(text)-(center|left|right|justify)$`, func(m []string, _ bool) string { return "text-align: " + m[2] + ";" }},
	{`^(tracking|letter-spacing)-([0-9%.-]+[a-z]*)$`, func(m []string, neg bool) string {
		val := transformUnit(m[2])
		if neg {
			val = "-" + val
		}
		return "letter-spacing: " + val + ";"
	}},
	{`^(line-height)-([0-9%.-]+[a-z]*)$`, func(m []string, _ bool) string {
		val := m[2]
		if isNumeric(val) {
			f, _ := strconv.ParseFloat(val, 64)
			if f > 10 {
				val = fmt.Sprintf("%.1f%%", f)
			} else {
				val = fmt.Sprintf("%.1f", f)
			}
		}
		return "line-height: " + val + ";"
	}},
	{`^(white-space)-(nowrap|pre|pre-wrap|pre-line)$`, func(m []string, _ bool) string { return "white-space: " + m[2] + ";" }},

	// 5. LAYOUT & INTERACTION
	{`^(display)-(flex|grid|block|inline-block|none)$`, func(m []string, _ bool) string { return "display: " + m[2] + ";" }},
	{`^(flex)-(row|column|wrap|grow|1|auto)$`, func(m []string, _ bool) string {
		if m[2] == "grow" {
			return "flex-grow: 1;"
		} else if m[2] == "1" {
			return "flex: 1 1 0%;"
		}
		return "flex-direction: " + m[2] + ";"
	}},
	{`^(justify|items)-(start|end|center|between|around|evenly)$`, func(m []string, _ bool) string {
		p, v := m[1], m[2]
		if v == "start" || v == "end" {
			v = "flex-" + v
		} else if v == "between" || v == "around" || v == "evenly" {
			v = "space-" + v
		}
		if p == "justify" {
			return "justify-content: " + v + ";"
		}
		return "align-items: " + v + ";"
	}},
	{`^(grid-columns)-(\d+)$`, func(m []string, _ bool) string {
		return fmt.Sprintf("grid-template-columns: repeat(%s, minmax(0, 1fr));", m[2])
	}},
	{`^(grid-span)-(\d+)$`, func(m []string, _ bool) string {
		return fmt.Sprintf("grid-column: span %s / span %s;", m[2], m[2])
	}},
	{`^(grid-column)-(start|end)-(\d+)$`, func(m []string, _ bool) string {
		return fmt.Sprintf("grid-column-%s: %s;", m[2], m[3])
	}},
	{`^(position)-(relative|absolute|fixed|sticky)$`, func(m []string, _ bool) string { return "position: " + m[2] + ";" }},
	{`^(z-index)-([a-z0-9-]+)$`, func(m []string, neg bool) string {
		val := m[2]
		if neg {
			val = "-" + val
		}
		return "z-index: " + val + ";"
	}},
	{`^(rounded)-(top|bottom|left|right)-([0-9%.-]+[a-z]*)$`, func(m []string, _ bool) string {
		axis, val := m[2], transformUnit(m[3])
		if axis == "top" {
			return fmt.Sprintf("border-top-left-radius: %s; border-top-right-radius: %s;", val, val)
		}
		if axis == "bottom" {
			return fmt.Sprintf("border-bottom-left-radius: %s; border-bottom-right-radius: %s;", val, val)
		}
		if axis == "left" {
			return fmt.Sprintf("border-top-left-radius: %s; border-bottom-left-radius: %s;", val, val)
		}
		return fmt.Sprintf("border-top-right-radius: %s; border-bottom-right-radius: %s;", val, val)
	}},
	{`^(rounded)-([0-9%.-]+[a-z]*)$`, func(m []string, _ bool) string { return "border-radius: " + transformUnit(m[2]) + ";" }},
	{`^(shadow)-(soft|industrial|glow|great)$`, func(m []string, _ bool) string { return "box-shadow: " + ShadowLevels[m[2]] + ";" }},
	{`^(transition)-(all|none)$`, func(m []string, _ bool) string { return "transition: all 0.4s cubic-bezier(0.2, 0.8, 0.2, 1);" }},
	{`^(translate)-(x|y)-([0-9%.-]+[a-z]*)$`, func(m []string, neg bool) string {
		axis, val := m[2], transformUnit(m[3])
		if neg {
			val = "-" + val
		}
		return fmt.Sprintf("transform: translate%s(%s);", strings.ToUpper(axis), val)
	}},
	{`^(overflow)-(hidden|auto|scroll|hidden-x|hidden-y|auto-x|auto-y)$`, func(m []string, _ bool) string {
		v := m[2]
		if strings.HasSuffix(v, "-x") {
			return "overflow-x: " + strings.TrimSuffix(v, "-x") + ";"
		}
		if strings.HasSuffix(v, "-y") {
			return "overflow-y: " + strings.TrimSuffix(v, "-y") + ";"
		}
		return "overflow: " + v + ";"
	}},
	{`^(cursor)-(pointer|default|not-allowed)$`, func(m []string, _ bool) string { return "cursor: " + m[2] + ";" }},
	{`^(pointer-events)-(none|auto)$`, func(m []string, _ bool) string { return "pointer-events: " + m[2] + ";" }},
}

func main() {
	htmlPath := "demo/view/work.html"
	html, _ := ioutil.ReadFile(htmlPath)
	framework := GenerateFramework()
	jit := GenerateJIT(string(html))
	_ = ioutil.WriteFile("demo/public/css/framework.css", []byte(framework), 0644)
	_ = ioutil.WriteFile("demo/public/css/jit.css", []byte(jit), 0644)
	fmt.Printf("\n--- Kitwork Industrial System v14.0 ---\nFW: %d | JIT: %d\n", len(framework), len(strings.Split(jit, "}\n"))-1)
}

func GenerateFramework() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Framework v14.0 - THE ARCHITECT'S SKELETON */\n\n:root {\n")
	for _, k := range Order {
		fmt.Fprintf(&b, "\t--color-%s-rgb: %s;\n", k, Colors[k])
	}
	b.WriteString("}\n\n* { margin: 0; padding: 0; box-sizing: border-box; -webkit-font-smoothing: antialiased; }\n\n")

	gen := func(c string) { b.WriteString(resolve(c)) }
	gen("container")
	gen("transition-all")
	gen("font-outfit")
	gen("font-mono")
	gen("background-grid-brand")
	gen("background-haze-brand")
	gen("background-gradient-brand")

	for _, v := range Scale {
		s := strconv.Itoa(v) + "px"
		for _, p := range []string{"margin", "padding", "gap"} {
			gen(p + "-" + s)
			gen(p + "-x-" + s)
			gen(p + "-y-" + s)
			if v != 0 && p == "margin" {
				gen("-" + p + "-" + s)
			}
		}
		for _, p := range []string{"top", "bottom", "left", "right"} {
			gen(p + "-" + s)
			if v != 0 {
				gen("-" + p + "-" + s)
			}
		}
		gen("text-" + s)
	}

	for _, name := range Order {
		if name == "white" {
			continue
		} // white utilities at the end
		gen("text-" + name)
		gen("background-" + name)
		for _, a := range AlphaScales {
			gen(fmt.Sprintf("background-%s-%d", name, a))
		}
	}
	// white utilities at the end (match user preference)
	gen("text-white")
	gen("background-white")
	for _, a := range AlphaScales {
		gen(fmt.Sprintf("background-white-%d", a))
	}

	for _, d := range []string{"flex", "grid", "block", "none"} {
		gen("display-" + d)
	}
	gen("width-full")
	gen("height-full")
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

func resolve(full string) string {
	sc, st, neg, core := parse(full)
	for _, reg := range Registry {
		if m := regexp.MustCompile(reg.Pattern).FindStringSubmatch(core); len(m) > 0 {
			css := reg.Solve(m, neg)
			if css == "" {
				continue
			}
			esc := strings.NewReplacer(":", "\\:", ".", "\\.", "/", "\\/").Replace(full)
			sel := "." + esc
			if full[0] == '-' {
				sel = ".\\-" + strings.TrimPrefix(esc, "-")
			}
			if st != "" {
				if strings.Contains(States[st], "&") {
					sel = strings.ReplaceAll(States[st], "&", sel)
				} else {
					sel += ":" + States[st]
				}
			}
			if sc != "" {
				return fmt.Sprintf("%s {\n\t%s { %s }\n}\n", Queries[sc], sel, css)
			}
			return fmt.Sprintf("%s { %s }\n", sel, css)
		}
	}
	return ""
}

func parse(f string) (sc, st string, neg bool, core string) {
	core = f
	if strings.HasPrefix(core, "-") {
		neg = true
		core = strings.TrimPrefix(core, "-")
	}
	for {
		changed := false
		for k := range Queries {
			if strings.HasPrefix(core, k+":") {
				sc = k
				core = strings.TrimPrefix(core, k+":")
				changed = true
				break
			}
		}
		for k := range States {
			if strings.HasPrefix(core, k+":") {
				st = k
				core = strings.TrimPrefix(core, k+":")
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}
	if !neg && strings.HasPrefix(core, "-") {
		neg = true
		core = strings.TrimPrefix(core, "-")
	}
	return
}

func transformUnit(s string) string {
	if s == "none" || s == "0" {
		return "0"
	}
	if strings.HasSuffix(s, "pct") {
		return strings.TrimSuffix(s, "pct") + "%"
	}
	if strings.HasSuffix(s, "px") || strings.HasSuffix(s, "rem") || strings.HasSuffix(s, "vh") || strings.HasSuffix(s, "vw") || strings.HasSuffix(s, "em") || strings.HasSuffix(s, "%") {
		return s
	}
	if isNumeric(s) {
		return s + ExplicitUnit
	}
	return s
}
func isNumeric(s string) bool { _, err := strconv.Atoi(s); return err == nil }
func mustInt(s string) int    { i, _ := strconv.Atoi(s); return i }
