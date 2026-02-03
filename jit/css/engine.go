package css

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// --- RESOLVER ENGINE ---

// --- RESOLVER ENGINE ---

// --- RESOLVER ENGINE ---

func resolve(full string) string {
	css, sel, mediaQ := ResolveCore(full)
	if css == "" {
		return ""
	}

	finalCSS := fmt.Sprintf("%s { %s }", sel, css)
	if mediaQ != "" {
		finalCSS = fmt.Sprintf("%s {\n\t%s\n}", mediaQ, finalCSS)
	}
	return finalCSS + "\n"
}

func ResolveCore(full string) (cssProp, selector, mediaQuery string) {
	variants, neg, core := parse(full)

	for _, reg := range Registry {
		re := regexp.MustCompile(reg.Reg)
		if m := re.FindStringSubmatch(core); len(m) > 0 {
			css := buildProp(reg.Type, m, neg)
			if css == "" {
				continue
			}

			// Escape class name for Selector
			esc := strings.NewReplacer(":", "\\:", ".", "\\.", "/", "\\/", "%", "\\%").Replace(full)
			sel := "." + esc
			if full[0] == '-' {
				sel = ".\\-" + strings.TrimPrefix(esc, "-")
			}

			// Separate media queries from pseudo-classes
			var pseudo string

			for _, v := range variants {
				if mq, ok := MediaQueries[v]; ok {
					mediaQuery = mq // Last media query wins closest context
				} else if st, ok := States[v]; ok {
					pseudo = st
				}
			}

			// Apply State to Selector
			if pseudo != "" {
				if strings.Contains(pseudo, "&") {
					sel = strings.ReplaceAll(pseudo, "&", sel)
				} else {
					sel += ":" + pseudo
				}
			}

			return css, sel, mediaQuery
		}
	}
	return "", "", ""
}

// Support stacked variants: desktop:hover:bg-red
func parse(f string) (variants []string, neg bool, core string) {
	core = f
	if strings.HasPrefix(core, "-") {
		neg = true
		core = strings.TrimPrefix(core, "-")
	}

	for {
		found := false
		// Check Media Queries
		for k := range MediaQueries {
			if strings.HasPrefix(core, k+":") {
				variants = append(variants, k)
				core = strings.TrimPrefix(core, k+":")
				found = true
				break
			}
		}
		// Check States
		if !found {
			for k := range States {
				if strings.HasPrefix(core, k+":") {
					variants = append(variants, k)
					core = strings.TrimPrefix(core, k+":")
					found = true
					break
				}
			}
		}

		if !found {
			break
		}
	}

	// Re-check negative after stripping variants (e.g. hover:-margin-top-px)
	if !neg && strings.HasPrefix(core, "-") {
		neg = true
		core = strings.TrimPrefix(core, "-")
	}

	return
}

func buildProp(t string, m []string, neg bool) string {
	switch t {
	case "container":
		return "width: 100%; max-width: 1280px; margin-inline: auto; padding-inline: 32px;"

	// --- EFFECTS & DECOR ---
	case "special-bg":
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
	case "blur":
		v := map[string]string{"small": "4px", "medium": "16px", "large": "40px", "none": "0"}
		return "backdrop-filter: blur(" + v[m[2]] + "); -webkit-backdrop-filter: blur(" + v[m[2]] + ");"
	case "shadow":
		return "box-shadow: " + ShadowLevels[m[2]] + ";"
	case "text-effect":
		if m[1] == "text-clip" {
			return "background-clip: text; -webkit-background-clip: text; -webkit-text-fill-color: transparent;"
		}
		return "text-shadow: 0 0 30px rgba(248, 34, 68, 0.4);"
	case "animate":
		if m[2] == "pulse" {
			return "animation: pulse 2s cubic-bezier(0.4, 0, 0.6, 1) infinite; @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: .5; } }"
		}
		if m[2] == "spin" {
			return "animation: spin 1s linear infinite; @keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }"
		}
		return "animation: " + m[2] + " 1s infinite;"

	// --- BOX MODEL ---
	case "spacing-axis":
		p, axis, val := m[1], m[2], transformUnit(m[3])
		if neg && p == "margin" {
			val = "-" + val
		}
		if p == "gap" {
			if axis == "x" {
				return "column-gap: " + val + ";"
			}
			return "row-gap: " + val + ";"
		}
		if axis == "x" {
			return fmt.Sprintf("%[1]s-left: %[2]s; %[1]s-right: %[2]s;", p, val)
		}
		return fmt.Sprintf("%[1]s-top: %[2]s; %[1]s-bottom: %[2]s;", p, val)
	case "spacing-dir":
		p, d, val := m[1], m[2], transformUnit(m[3])
		if neg && p == "margin" {
			val = "-" + val
		}
		return fmt.Sprintf("%s-%s: %s;", p, d, val)
	case "spacing-single":
		p, val := m[1], transformUnit(m[2])
		if m[2] == "none" {
			return p + ": 0;"
		}
		if neg && (p == "margin" || p == "top" || p == "bottom" || p == "left" || p == "right") {
			val = "-" + val
		}
		return fmt.Sprintf("%s: %s;", p, val)
	case "sizing":
		p, val := m[1], transformUnit(m[2])
		if m[2] == "full" {
			val = "100%"
		}
		if m[2] == "screen" {
			if strings.Contains(p, "width") {
				val = "100vw"
			} else {
				val = "100vh"
			}
		}
		if m[2] == "fit" {
			val = "fit-content"
		}
		return fmt.Sprintf("%s: %s;", p, val)
	case "aspect":
		v := map[string]string{"video": "16 / 9", "square": "1 / 1", "auto": "auto"}
		return "aspect-ratio: " + v[m[2]] + ";"

	// --- BORDERS ---
	case "border-side":
		p, axis, val := m[1], m[2], transformUnit(m[3])
		if m[3] == "none" {
			return fmt.Sprintf("%s-%s: none;", p, axis)
		}
		return fmt.Sprintf("%s-%s: %s solid;", p, axis, val)
	case "border-all":
		p, val := m[1], transformUnit(m[2])
		if m[2] == "none" {
			return p + ": none;"
		}
		return fmt.Sprintf("%s: %s solid;", p, val)
	case "border-style":
		return "border-style: " + m[2] + ";"
	case "rounded-side":
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
	case "rounded-all":
		val := transformUnit(m[2])
		if m[2] == "full" {
			val = "9999px"
		}
		return "border-radius: " + val + ";"

	// --- COLORS ---
	case "color-plain":
		tg, color, alpha := m[1], m[2], m[3]
		if tg == "bg" {
			tg = "background"
		}
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
	case "border-color-side":
		axis, color, alpha := m[2], m[3], m[4]
		rgb, ok := Colors[color]
		if !ok {
			return ""
		}
		if alpha != "" {
			return fmt.Sprintf("border-%s-color: rgba(%s, %.2f);", axis, rgb, float64(mustInt(alpha))/100.0)
		}
		return fmt.Sprintf("border-%s-color: rgb(%s);", axis, rgb)
	case "border-color":
		color, alpha := m[2], m[3]
		rgb, ok := Colors[color]
		if !ok {
			return ""
		}
		if alpha != "" {
			return fmt.Sprintf("border-color: rgba(%s, %.2f);", rgb, float64(mustInt(alpha))/100.0)
		}
		return fmt.Sprintf("border-color: rgb(%s);", rgb)

	// --- TYPOGRAPHY ---
	case "font-family-weight":
		w := map[string]string{"bold": "700", "medium": "500", "500": "500", "light": "300", "semibold": "600", "black": "900", "900": "900"}
		if weight, ok := w[m[2]]; ok {
			return "font-weight: " + weight + ";"
		}
		if m[2] == "outfit" {
			return "font-family: 'Outfit', sans-serif;"
		}
		return "font-family: 'JetBrains Mono', monospace;"
	case "font-size":
		return "font-size: " + transformUnit(m[2]) + ";"
	case "text-transform":
		v := m[1]
		if v == "italic" {
			return "font-style: italic;"
		}
		if v == "underline" {
			return "text-decoration: underline;"
		}
		if v == "line-through" {
			return "text-decoration: line-through;"
		}
		if v == "no-underline" {
			return "text-decoration: none;"
		}
		return "text-transform: " + v + ";"
	case "text-align":
		return "text-align: " + m[2] + ";"
	case "letter-spacing":
		val := transformUnit(m[2])
		if neg {
			val = "-" + val
		}
		return "letter-spacing: " + val + ";"
	case "line-height":
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
	case "white-space":
		return "white-space: " + m[2] + ";"
	case "word-break":
		if m[2] == "all" {
			return "word-break: break-all;"
		}
		return "word-wrap: break-word;"

	// --- LAYOUT ---
	case "display":
		if m[2] == "hidden" {
			return "display: none;"
		}
		return "display: " + m[2] + ";"
	case "flex-prop":
		if m[2] == "grow" {
			return "flex-grow: 1;"
		}
		if m[2] == "1" {
			return "flex: 1 1 0%;"
		}
		if m[2] == "wrap" {
			return "flex-wrap: wrap;"
		}
		if m[2] == "nowrap" {
			return "flex-wrap: nowrap;"
		}
		if m[2] == "none" {
			return "flex: none;"
		}
		return "flex-direction: " + m[2] + ";"
	case "flex-align":
		p, v := m[1], m[2]
		if v == "start" || v == "end" {
			v = "flex-" + v
		} else if v == "between" || v == "around" || v == "evenly" {
			v = "space-" + v
		}
		if p == "justify" {
			return "justify-content: " + v + ";"
		}
		if p == "content" {
			return "align-content: " + v + ";"
		}
		return "align-items: " + v + ";"
	case "self-align":
		v := m[2]
		if v == "start" || v == "end" {
			v = "flex-" + v
		}
		return "align-self: " + v + ";"
	case "order":
		v := m[2]
		if v == "first" {
			v = "-9999"
		} else if v == "last" {
			v = "9999"
		} else if v == "none" {
			v = "0"
		}
		return "order: " + v + ";"
	case "grid-cols":
		if m[2] == "none" {
			return "grid-template-columns: none;"
		}
		return fmt.Sprintf("grid-template-columns: repeat(%s, minmax(0, 1fr));", m[2])
	case "grid-rows":
		if m[2] == "none" {
			return "grid-template-rows: none;"
		}
		return fmt.Sprintf("grid-template-rows: repeat(%s, minmax(0, 1fr));", m[2])
	case "grid-span":
		if m[2] == "full" {
			return "grid-column: 1 / -1;"
		}
		return fmt.Sprintf("grid-column: span %s / span %s;", m[2], m[2])
	case "grid-pos":
		return fmt.Sprintf("grid-column-%s: %s;", m[2], m[3])

	// --- INTERACTION ---
	case "position":
		return "position: " + m[2] + ";"
	case "z-index":
		return "z-index: " + m[2] + ";"
	case "opacity":
		return fmt.Sprintf("opacity: %.2f;", float64(mustInt(m[2]))/100.0)
	case "cursor":
		return "cursor: " + m[2] + ";"
	case "pointer-events":
		return "pointer-events: " + m[2] + ";"
	case "user-select":
		return "user-select: " + m[2] + "; -webkit-user-select: " + m[2] + ";"
	case "appearance":
		return "appearance: " + m[2] + "; -webkit-appearance: " + m[2] + ";"
	case "resize":
		return "resize: " + m[2] + ";"

	// --- TRANSITIONS & ANIMATIONS ---
	case "transition":
		if m[2] == "none" {
			return "transition: none;"
		}
		p := "all"
		if m[2] != "all" {
			p = m[2]
		}
		return "transition-property: " + p + "; transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1); transition-duration: 150ms;"
	case "duration":
		return "transition-duration: " + m[2] + "ms;"
	case "delay":
		return "transition-delay: " + m[2] + "ms;"
	case "ease":
		return "transition-timing-function: " + m[2] + ";"

	// --- TRANSFORMS (Modern) ---
	case "translate":
		axis, val := m[2], transformUnit(m[3])
		if neg {
			val = "-" + val
		}
		return fmt.Sprintf("transform: translate%s(%s);", strings.ToUpper(axis), val)
	case "scale":
		return "transform: scale(" + m[2] + ");"
	case "scale-axis":
		return fmt.Sprintf("transform: scale%s(%s);", strings.ToUpper(m[2]), m[3])
	case "rotate":
		val := m[2]
		if neg {
			val = "-" + val
		}
		return "transform: rotate(" + val + "deg);"
	case "origin":
		return "transform-origin: " + m[2] + ";"

	// --- MISC ---
	case "overflow":
		v := m[2]
		if strings.HasSuffix(v, "-x") {
			return "overflow-x: " + strings.TrimSuffix(v, "-x") + ";"
		}
		if strings.HasSuffix(v, "-y") {
			return "overflow-y: " + strings.TrimSuffix(v, "-y") + ";"
		}
		return "overflow: " + v + ";"
	case "object-fit":
		return "object-fit: " + m[2] + ";"
	}
	return ""
}
