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
	css, sel, mediaQ := ResolveCore(full, nil)
	if css == "" {
		return ""
	}

	finalCSS := fmt.Sprintf("%s { %s }", sel, css)
	if mediaQ != "" {
		finalCSS = fmt.Sprintf("%s {\n\t%s\n}", mediaQ, finalCSS)
	}
	return finalCSS + "\n"
}

func ResolveCore(full string, cfg *Config) (cssProp, selector, mediaQuery string) {
	variants, neg, core := parse(full, cfg)

	mqs := MediaQueries
	sts := States
	if cfg != nil {
		mqs = cfg.MediaQueries
		sts = cfg.States
	}

	for _, reg := range Registry {
		re := regexp.MustCompile(reg.Reg)
		if m := re.FindStringSubmatch(core); len(m) > 0 {
			css := buildProp(reg.Type, m, neg, cfg)
			if css == "" {
				continue
			}

			// Escape class name for the CSS selector. Arbitrary values bring [ ] # ( ) ,
			// and decimals — all must be backslash-escaped or the selector is invalid
			// (e.g. .w-[17px] parses as .w- + [17px] attribute → rule dropped).
			esc := strings.NewReplacer(
				":", "\\:", ".", "\\.", "/", "\\/", "%", "\\%",
				"[", "\\[", "]", "\\]", "#", "\\#",
				"(", "\\(", ")", "\\)", ",", "\\,",
				"'", "\\'", "\"", "\\\"",
			).Replace(full)
			sel := "." + esc
			if full[0] == '-' {
				sel = ".\\-" + strings.TrimPrefix(esc, "-")
			}

			// space-*/divide-* target the gaps BETWEEN children, not the element itself.
			if reg.Type == "tw-space" || reg.Type == "tw-divide" || reg.Type == "tw-divide-color" {
				sel += " > :not([hidden]) ~ :not([hidden])"
			}
			// animate-on-hover pauses DESCENDANTS' animations (the :hover-runs counterpart rule is
			// appended by UsedKeyframes).
			if reg.Type == "tw-animate-onhover" {
				sel += " *"
			}

			// Apply variants. dark: scopes the selector under .dark; a media query wraps
			// it; states become pseudo-classes (multiple may stack); group-hover etc. use
			// an "&" pattern. Stacking like dark:md:hover:bg-x is supported.
			var darkMode bool
			var ampPattern string
			for _, v := range variants {
				if v == "dark" {
					darkMode = true
					continue
				}
				if mq, ok := mqs[v]; ok {
					mediaQuery = mq // last (innermost) media query wins
					continue
				}
				if st, ok := sts[v]; ok {
					if strings.Contains(st, "&") {
						ampPattern = st
					} else {
						sel += ":" + st
					}
				}
			}
			if ampPattern != "" {
				sel = strings.ReplaceAll(ampPattern, "&", sel)
			}
			if darkMode {
				dark := ".dark"
				if cfg != nil && cfg.DarkSelector != "" {
					dark = cfg.DarkSelector
				}
				sel = dark + " " + sel
			}

			return css, sel, mediaQuery
		}
	}
	return "", "", ""
}

// Support stacked variants: desktop:hover:bg-red
func parse(f string, cfg *Config) (variants []string, neg bool, core string) {
	core = f
	if strings.HasPrefix(core, "-") {
		neg = true
		core = strings.TrimPrefix(core, "-")
	}

	mqs := MediaQueries
	sts := States
	if cfg != nil {
		mqs = cfg.MediaQueries
		sts = cfg.States
	}

	for {
		found := false
		// Check Media Queries
		for k := range mqs {
			if strings.HasPrefix(core, k+":") {
				variants = append(variants, k)
				core = strings.TrimPrefix(core, k+":")
				found = true
				break
			}
		}
		// Check States
		if !found {
			for k := range sts {
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

func buildProp(t string, m []string, neg bool, cfg *Config) string {
	switch t {
	case "container":
		return "width: 100%; max-width: 1280px; margin-inline: auto; padding-inline: 32px;"

	// --- EFFECTS & DECOR ---
	case "special-bg":
		color := m[3]
		colors := Colors
		if cfg != nil {
			colors = cfg.Colors
		}
		rgb := colors[color]
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
		shadows := ShadowLevels
		if cfg != nil {
			shadows = cfg.ShadowLevels
		}
		return "box-shadow: " + shadows[m[2]] + ";"
	case "text-effect":
		if m[1] == "text-clip" {
			return "background-clip: text; -webkit-background-clip: text; -webkit-text-fill-color: transparent;"
		}
		return "text-shadow: 0 0 30px rgba(248, 34, 68, 0.4);"
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

	// --- 11. TAILWIND SUPPORT ---
	case "tw-side": // mt/mr/mb/ml/pt/pr/pb/pl
		prop := map[byte]string{'m': "margin", 'p': "padding"}[m[1][0]]
		dir := map[byte]string{'t': "top", 'r': "right", 'b': "bottom", 'l': "left"}[m[1][1]]
		val := twUnit(m[2])
		if neg {
			val = "-" + val
		}
		return fmt.Sprintf("%s-%s: %s;", prop, dir, val)
	case "tw-axis": // mx/my/px/py
		prop := map[byte]string{'m': "margin", 'p': "padding"}[m[1][0]]
		val := twUnit(m[2])
		if neg {
			val = "-" + val
		}
		if m[1][1] == 'x' {
			return fmt.Sprintf("%[1]s-left: %[2]s; %[1]s-right: %[2]s;", prop, val)
		}
		return fmt.Sprintf("%[1]s-top: %[2]s; %[1]s-bottom: %[2]s;", prop, val)
	case "tw-allside": // m/p
		prop := map[string]string{"m": "margin", "p": "padding"}[m[1]]
		val := twUnit(m[2])
		if neg {
			val = "-" + val
		}
		return fmt.Sprintf("%s: %s;", prop, val)
	case "tw-gap":
		return "gap: " + twUnit(m[2]) + ";"
	case "tw-gap-axis":
		if m[2] == "x" {
			return "column-gap: " + twUnit(m[3]) + ";"
		}
		return "row-gap: " + twUnit(m[3]) + ";"
	case "tw-leading":
		v := m[2]
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			return "line-height: " + v[1:len(v)-1] + ";"
		}
		named := map[string]string{"none": "1", "tight": "1.25", "snug": "1.375", "normal": "1.5", "relaxed": "1.625", "loose": "2"}
		if x, ok := named[v]; ok {
			return "line-height: " + x + ";"
		}
		return "line-height: " + twUnit(v) + ";"
	case "tw-tracking":
		named := map[string]string{"tighter": "-0.05em", "tight": "-0.025em", "normal": "0em", "wide": "0.025em", "wider": "0.05em", "widest": "0.1em"}
		v := m[2]
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			return "letter-spacing: " + v[1:len(v)-1] + ";"
		}
		return "letter-spacing: " + named[v] + ";"
	case "tw-font-family":
		fams := map[string]string{
			"sans":  "ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, sans-serif",
			"serif": "ui-serif, Georgia, Cambria, serif",
			"mono":  "ui-monospace, SFMono-Regular, Menlo, monospace",
		}
		return "font-family: " + fams[m[2]] + ";"
	case "tw-arbitrary-prop":
		body := strings.ReplaceAll(m[1], "_", " ")
		parts := strings.SplitN(body, ":", 2)
		if len(parts) != 2 {
			return ""
		}
		prop := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if prop == "" || val == "" || strings.ContainsAny(val, "{};") {
			return ""
		}
		for _, r := range prop {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-') {
				return ""
			}
		}
		return prop + ": " + val + ";"
	case "tw-shrink-grow":
		v := "1"
		if m[2] == "0" {
			v = "0"
		}
		if m[1] == "shrink" {
			return "flex-shrink: " + v + ";"
		}
		return "flex-grow: " + v + ";"
	case "tw-marker":
		switch m[1] {
		case "antialiased":
			return "-webkit-font-smoothing: antialiased; -moz-osx-font-smoothing: grayscale;"
		case "isolate":
			return "isolation: isolate;"
		case "box-border":
			return "box-sizing: border-box;"
		case "box-content":
			return "box-sizing: content-box;"
		case "sr-only":
			return "position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0,0,0,0); white-space: nowrap; border-width: 0;"
		}
		return "" // transform/group/peer: variant hooks, no CSS of their own
	case "tw-backdrop-blur":
		v := m[2]
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			v = v[1 : len(v)-1]
		} else {
			sizes := map[string]string{"sm": "4px", "md": "12px", "lg": "16px", "xl": "24px", "2xl": "40px", "3xl": "64px", "none": "0", "": "8px"}
			v = sizes[v]
		}
		return fmt.Sprintf("-webkit-backdrop-filter: blur(%s); backdrop-filter: blur(%s);", v, v)
	case "tw-translate":
		val := twUnit(m[3])
		if neg {
			val = "-" + val
		}
		if m[2] == "x" {
			return "transform: translateX(" + val + ");"
		}
		return "transform: translateY(" + val + ");"
	case "tw-sizing":
		propMap := map[string]string{"w": "width", "h": "height", "max-w": "max-width", "min-w": "min-width", "max-h": "max-height", "min-h": "min-height"}
		prop := propMap[m[1]]
		val := twUnit(m[2])
		if m[2] == "screen" && prop == "width" {
			val = "100vw"
		}
		if m[2] == "screen" && prop == "height" {
			val = "100vh"
		}
		if m[2] == "full" {
			val = "100%"
		}
		switch m[2] {
		case "dvh", "svh", "lvh":
			val = "100" + m[2]
		case "dvw", "svw", "lvw":
			val = "100" + m[2]
		}
		// arbitrary values
		if strings.HasPrefix(m[2], "[") && strings.HasSuffix(m[2], "]") {
			val = unarb(m[2])
		} else if !isNumeric(m[2]) && val == m[2] {
			// Some specific aliases like max-w-6xl
			aliases := map[string]string{"xs": "20rem", "sm": "24rem", "md": "28rem", "lg": "32rem", "xl": "36rem", "2xl": "42rem", "3xl": "48rem", "4xl": "56rem", "5xl": "64rem", "6xl": "72rem", "7xl": "80rem", "8xl": "96rem"}
			if v, ok := aliases[m[2]]; ok {
				val = v
			}
		}
		return fmt.Sprintf("%s: %s;", prop, val)
	case "tw-color-shade":
		propMap := map[string]string{"bg": "background-color", "text": "color", "border": "border-color", "ring": "box-shadow", "outline": "outline-color"}
		prop := propMap[m[1]]
		color := twColor(m[2], m[3], cfg)
		alpha := m[4]
		if color == "" {
			return ""
		}

		if color[0] == '#' {
			if alpha != "" && isNumeric(alpha) {
				return fmt.Sprintf("%s: %s%02x;", prop, color, mustInt(alpha)*255/100)
			}
			return fmt.Sprintf("%s: %s;", prop, color)
		}

		if alpha != "" {
			if strings.HasPrefix(alpha, "[") && strings.HasSuffix(alpha, "]") {
				return fmt.Sprintf("%s: rgba(%s, %s);", prop, color, alpha[1:len(alpha)-1])
			}
			return fmt.Sprintf("%s: rgba(%s, %.2f);", prop, color, float64(mustInt(alpha))/100.0)
		}
		return fmt.Sprintf("%s: rgb(%s);", prop, color)
	case "tw-color-base":
		propMap := map[string]string{"bg": "background-color", "text": "color", "border": "border-color", "ring": "box-shadow", "outline": "outline-color"}
		prop := propMap[m[1]]
		color := twColor(m[2], "", cfg)
		alpha := m[3]

		if color == "" {
			return "" // unknown color name → let other patterns try
		}
		if color == "transparent" {
			return fmt.Sprintf("%s: transparent;", prop)
		}

		if color[0] == '#' {
			if alpha != "" && isNumeric(alpha) {
				return fmt.Sprintf("%s: %s%02x;", prop, color, mustInt(alpha)*255/100)
			}
			return fmt.Sprintf("%s: %s;", prop, color)
		}

		if alpha != "" {
			if strings.HasPrefix(alpha, "[") && strings.HasSuffix(alpha, "]") {
				return fmt.Sprintf("%s: rgba(%s, %s);", prop, color, alpha[1:len(alpha)-1])
			}
			return fmt.Sprintf("%s: rgba(%s, %.2f);", prop, color, float64(mustInt(alpha))/100.0)
		}
		return fmt.Sprintf("%s: rgb(%s);", prop, color)
	case "tw-color-arbitrary":
		propMap := map[string]string{"bg": "background-color", "text": "color", "border": "border-color"}
		prop := propMap[m[1]]
		hex := m[2]
		if len(m) > 3 && m[3] != "" { // bg-[#fff]/80 → 8-digit hex with alpha
			return fmt.Sprintf("%s: %s%02x;", prop, hex, mustInt(m[3])*255/100)
		}
		return fmt.Sprintf("%s: %s;", prop, hex)
	case "tw-gradient-dir":
		dirs := map[string]string{"t": "to top", "b": "to bottom", "l": "to left", "r": "to right",
			"tl": "to top left", "tr": "to top right", "bl": "to bottom left", "br": "to bottom right"}
		return fmt.Sprintf("background-image: linear-gradient(%s, var(--tw-gradient-stops));", dirs[m[1]])
	case "tw-gradient-stop": // from/via/to-<family>-<shade>
		col := twColor(m[2], m[3], cfg)
		return gradientStop(m[1], rgbWrap(col))
	case "tw-gradient-stop-base": // from/via/to-<named color>
		col := twColor(m[2], "", cfg)
		if col == "" {
			return ""
		}
		return gradientStop(m[1], rgbWrap(col))
	case "tw-gradient-stop-arb": // from/via/to-[#hex]
		return gradientStop(m[1], m[2])
	case "tw-bg-clip":
		v := m[1]
		if v == "text" {
			return "-webkit-background-clip: text; background-clip: text;"
		}
		return "background-clip: " + v + "-box;"
	case "tw-space": // space-x/space-y → margin on subsequent children (selector suffix in ResolveCore)
		val := twUnit(m[2])
		if neg {
			val = "-" + val
		}
		if m[1] == "x" {
			return "margin-left: " + val + ";"
		}
		return "margin-top: " + val + ";"
	case "tw-divide": // divide-x/divide-y → border between children
		if m[1] == "x" {
			return "border-left-width: 1px;"
		}
		return "border-top-width: 1px;"
	case "tw-divide-color":
		col := twColor(m[1], m[2], cfg)
		if col == "" {
			return ""
		}
		return "border-color: " + rgbWrap(col) + ";"
	case "tw-outline":
		return "outline-style: solid;"
	case "tw-outline-width":
		return "outline-width: " + m[1] + "px;"
	case "tw-outline-offset":
		return "outline-offset: " + m[1] + "px;"
	case "tw-scroll":
		prop := map[byte]string{'m': "scroll-margin", 'p': "scroll-padding"}[m[1][0]]
		val := twUnit(m[2])
		if len(m[1]) == 1 { // scroll-m / scroll-p
			return prop + ": " + val + ";"
		}
		switch m[1][1] {
		case 'x':
			return fmt.Sprintf("%[1]s-left: %[2]s; %[1]s-right: %[2]s;", prop, val)
		case 'y':
			return fmt.Sprintf("%[1]s-top: %[2]s; %[1]s-bottom: %[2]s;", prop, val)
		}
		dir := map[byte]string{'t': "top", 'r': "right", 'b': "bottom", 'l': "left"}[m[1][1]]
		return fmt.Sprintf("%s-%s: %s;", prop, dir, val)
	case "tw-rounded":
		val := m[3]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			val = unarb(val)
		} else {
			sizes := map[string]string{"sm": "0.125rem", "md": "0.375rem", "lg": "0.5rem", "xl": "0.75rem", "2xl": "1rem", "3xl": "1.5rem", "full": "9999px", "none": "0px", "": "0.25rem"}
			if v, ok := sizes[val]; ok {
				val = v
			}
		}

		dir := m[2]
		if dir == "" {
			return fmt.Sprintf("border-radius: %s;", val)
		}
		if dir == "t" {
			return fmt.Sprintf("border-top-left-radius: %[1]s; border-top-right-radius: %[1]s;", val)
		}
		if dir == "b" {
			return fmt.Sprintf("border-bottom-left-radius: %[1]s; border-bottom-right-radius: %[1]s;", val)
		}
		if dir == "l" {
			return fmt.Sprintf("border-top-left-radius: %[1]s; border-bottom-left-radius: %[1]s;", val)
		}
		if dir == "r" {
			return fmt.Sprintf("border-top-right-radius: %[1]s; border-bottom-right-radius: %[1]s;", val)
		}
		if dir == "tl" {
			return fmt.Sprintf("border-top-left-radius: %s;", val)
		}
		if dir == "tr" {
			return fmt.Sprintf("border-top-right-radius: %s;", val)
		}
		if dir == "bl" {
			return fmt.Sprintf("border-bottom-left-radius: %s;", val)
		}
		if dir == "br" {
			return fmt.Sprintf("border-bottom-right-radius: %s;", val)
		}
	case "tw-text-size":
		val := m[2]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			return fmt.Sprintf("font-size: %s;", unarb(val))
		}
		sizes := map[string][]string{
			"xs": {"0.75rem", "1rem"}, "sm": {"0.875rem", "1.25rem"}, "base": {"1rem", "1.5rem"}, "lg": {"1.125rem", "1.75rem"},
			"xl": {"1.25rem", "1.75rem"}, "2xl": {"1.5rem", "2rem"}, "3xl": {"1.875rem", "2.25rem"}, "4xl": {"2.25rem", "2.5rem"},
			"5xl": {"3rem", "1"}, "6xl": {"3.75rem", "1"}, "7xl": {"4.5rem", "1"}, "8xl": {"6rem", "1"}, "9xl": {"8rem", "1"},
		}
		if s, ok := sizes[val]; ok {
			return fmt.Sprintf("font-size: %s; line-height: %s;", s[0], s[1])
		}
	case "tw-blur":
		val := m[2]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			val = unarb(val)
		} else {
			sizes := map[string]string{"sm": "4px", "md": "12px", "lg": "16px", "xl": "24px", "2xl": "40px", "3xl": "64px", "none": "0", "": "8px"}
			if v, ok := sizes[val]; ok {
				val = v
			}
		}
		return fmt.Sprintf("filter: blur(%s); -webkit-backdrop-filter: blur(%s); backdrop-filter: blur(%s);", val, val, val)
	case "tw-opacity":
		val := m[2]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			return fmt.Sprintf("opacity: %s;", unarb(val))
		}
		return fmt.Sprintf("opacity: %.2f;", float64(mustInt(val))/100.0)
	case "tw-grid-cols":
		val := m[2]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			return fmt.Sprintf("grid-template-columns: %s;", unarb(val))
		}
		if val == "none" {
			return "grid-template-columns: none;"
		}
		return fmt.Sprintf("grid-template-columns: repeat(%s, minmax(0, 1fr));", val)
	case "tw-col-span":
		if m[2] == "full" {
			return "grid-column: 1 / -1;"
		}
		return fmt.Sprintf("grid-column: span %s / span %s;", m[2], m[2])
	case "tw-flex":
		val := m[2]
		if val == "row" || val == "col" || val == "row-reverse" || val == "col-reverse" {
			if val == "col" {
				val = "column"
			}
			return "flex-direction: " + val + ";"
		}
		if val == "wrap" || val == "nowrap" || val == "wrap-reverse" {
			return "flex-wrap: " + val + ";"
		}
		if val == "1" {
			return "flex: 1 1 0%;"
		}
		if val == "auto" {
			return "flex: 1 1 auto;"
		}
		if val == "initial" {
			return "flex: 0 1 auto;"
		}
		if val == "none" {
			return "flex: none;"
		}
	case "tw-align":
		propMap := map[string]string{"justify": "justify-content", "items": "align-items", "content": "align-content", "self": "align-self"}
		prop := propMap[m[1]]
		val := m[2]
		if val == "start" || val == "end" {
			val = "flex-" + val
		}
		if val == "between" || val == "around" || val == "evenly" {
			val = "space-" + val
		}
		return fmt.Sprintf("%s: %s;", prop, val)
	case "tw-display":
		val := m[1]
		if val == "hidden" {
			return "display: none;"
		}
		return "display: " + val + ";"
	case "tw-position":
		return "position: " + m[1] + ";"
	case "tw-inset", "tw-inset-neg":
		prop := m[1]
		val := twUnit(m[2])
		if m[0][0] == '-' {
			val = "-" + val
		}
		if prop == "inset" {
			return fmt.Sprintf("inset: %s;", val)
		}
		if prop == "inset-x" {
			return fmt.Sprintf("left: %[1]s; right: %[1]s;", val)
		}
		if prop == "inset-y" {
			return fmt.Sprintf("top: %[1]s; bottom: %[1]s;", val)
		}
		return fmt.Sprintf("%s: %s;", prop, val)
	case "tw-zindex", "tw-zindex-neg":
		val := m[2]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			val = val[1 : len(val)-1]
		}
		if m[0][0] == '-' {
			val = "-" + val
		}
		return "z-index: " + val + ";"
	case "tw-shadow":
		val := m[2]
		sh := map[string]string{
			"sm":    "0 1px 2px 0 rgb(0 0 0 / 0.05)",
			"":      "0 1px 3px 0 rgb(0 0 0 / 0.1), 0 1px 2px -1px rgb(0 0 0 / 0.1)",
			"md":    "0 4px 6px -1px rgb(0 0 0 / 0.1), 0 2px 4px -2px rgb(0 0 0 / 0.1)",
			"lg":    "0 10px 15px -3px rgb(0 0 0 / 0.1), 0 4px 6px -4px rgb(0 0 0 / 0.1)",
			"xl":    "0 20px 25px -5px rgb(0 0 0 / 0.1), 0 8px 10px -6px rgb(0 0 0 / 0.1)",
			"2xl":   "0 25px 50px -12px rgb(0 0 0 / 0.25)",
			"inner": "inset 0 2px 4px 0 rgb(0 0 0 / 0.05)",
			"none":  "0 0 #0000",
		}
		if s, ok := sh[val]; ok {
			return "box-shadow: " + s + ";"
		}
		return "box-shadow: " + sh[""] + ";"
	case "tw-overflow":
		prop := m[1]
		val := m[2]
		return fmt.Sprintf("%s: %s;", prop, val)
	case "tw-cursor":
		return "cursor: " + m[2] + ";"
	case "tw-transition":
		val := m[2]
		if val == "" {
			val = "color, background-color, border-color, text-decoration-color, fill, stroke, opacity, box-shadow, transform, filter, backdrop-filter"
		}
		if val == "colors" {
			val = "color, background-color, border-color, text-decoration-color, fill, stroke"
		}
		if val == "none" {
			return "transition-property: none;"
		}
		return fmt.Sprintf("transition-property: %s; transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1); transition-duration: 150ms;", val)
	case "tw-duration":
		val := m[2]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			val = val[1 : len(val)-1]
		} else {
			val += "ms"
		}
		return "transition-duration: " + val + ";"
	case "tw-ease":
		val := m[2]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			val = val[1 : len(val)-1]
		}
		e := map[string]string{"linear": "linear", "in": "cubic-bezier(0.4, 0, 1, 1)", "out": "cubic-bezier(0, 0, 0.2, 1)", "in-out": "cubic-bezier(0.4, 0, 0.2, 1)"}
		if v, ok := e[val]; ok {
			val = v
		}
		return "transition-timing-function: " + val + ";"
	case "tw-border":
		dir := m[2]
		val := m[3]
		if val == "" {
			val = "1px"
		} else {
			if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				val = val[1 : len(val)-1]
			} else {
				val += "px"
			}
		}
		if dir == "" {
			return fmt.Sprintf("border-width: %s; border-style: solid;", val)
		}
		if dir == "x" {
			return fmt.Sprintf("border-left-width: %[1]s; border-right-width: %[1]s; border-left-style: solid; border-right-style: solid;", val)
		}
		if dir == "y" {
			return fmt.Sprintf("border-top-width: %[1]s; border-bottom-width: %[1]s; border-top-style: solid; border-bottom-style: solid;", val)
		}
		d := map[string]string{"t": "top", "b": "bottom", "l": "left", "r": "right"}[dir]
		return fmt.Sprintf("border-%[1]s-width: %[2]s; border-%[1]s-style: solid;", d, val)
	case "tw-font-weight":
		w := map[string]string{"thin": "100", "extralight": "200", "light": "300", "normal": "400", "medium": "500", "semibold": "600", "bold": "700", "extrabold": "800", "black": "900"}[m[2]]
		return "font-weight: " + w + ";"
	case "tw-text-align":
		return "text-align: " + m[2] + ";"
	case "tw-text-decor":
		val := m[1]
		if val == "italic" {
			return "font-style: italic;"
		}
		if val == "not-italic" {
			return "font-style: normal;"
		}
		if val == "uppercase" || val == "lowercase" || val == "capitalize" {
			return "text-transform: " + val + ";"
		}
		if val == "normal-case" {
			return "text-transform: none;"
		}
		if val == "underline" || val == "line-through" {
			return "text-decoration-line: " + val + ";"
		}
		if val == "no-underline" {
			return "text-decoration-line: none;"
		}
	case "tw-rotate":
		v := m[2]
		if strings.HasPrefix(v, "[") {
			v = unarb(v)
		} else {
			v = v + "deg"
		}
		if neg {
			v = "-" + v
		}
		return "transform: rotate(" + v + ");"
	case "tw-scale":
		return "transform: scale(" + scaleVal(m[2], neg) + ");"
	case "tw-scale-axis":
		ax := "X"
		if m[2] == "y" {
			ax = "Y"
		}
		return "transform: scale" + ax + "(" + scaleVal(m[3], neg) + ");"
	case "tw-skew":
		v := m[3]
		if strings.HasPrefix(v, "[") {
			v = unarb(v)
		} else {
			v = v + "deg"
		}
		if neg {
			v = "-" + v
		}
		ax := "X"
		if m[2] == "y" {
			ax = "Y"
		}
		return "transform: skew" + ax + "(" + v + ");"
	case "tw-origin":
		return "transform-origin: " + strings.ReplaceAll(m[1], "-", " ") + ";"
	case "tw-aspect":
		ratios := map[string]string{"video": "16 / 9", "square": "1 / 1", "auto": "auto"}
		return "aspect-ratio: " + ratios[m[1]] + ";"
	case "tw-aspect-arb":
		return "aspect-ratio: " + strings.ReplaceAll(m[1], "_", " ") + ";"
	case "tw-object":
		return "object-fit: " + m[1] + ";"
	case "tw-truncate":
		return "overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
	case "tw-whitespace":
		return "white-space: " + m[1] + ";"
	case "tw-line-clamp":
		if m[1] == "none" {
			return "-webkit-line-clamp: none;"
		}
		return fmt.Sprintf("display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: %s; overflow: hidden;", m[1])
	case "tw-break":
		switch m[1] {
		case "words":
			return "overflow-wrap: break-word;"
		case "all":
			return "word-break: break-all;"
		case "keep":
			return "word-break: keep-all;"
		}
		return "word-break: normal; overflow-wrap: normal;"
	case "tw-grid-line":
		base := "grid-column-"
		if m[1] == "row" {
			base = "grid-row-"
		}
		return base + m[2] + ": " + m[3] + ";"
	case "tw-row-span":
		if m[1] == "full" {
			return "grid-row: 1 / -1;"
		}
		return fmt.Sprintf("grid-row: span %s / span %s;", m[1], m[1])
	case "tw-grid-rows":
		v := m[2]
		if strings.HasPrefix(v, "[") {
			return "grid-template-rows: " + unarb(v) + ";"
		}
		if v == "none" {
			return "grid-template-rows: none;"
		}
		return fmt.Sprintf("grid-template-rows: repeat(%s, minmax(0, 1fr));", v)
	case "tw-shadow-arb":
		return "box-shadow: " + strings.ReplaceAll(m[1], "_", " ") + ";"
	case "tw-order":
		v := m[1]
		switch v {
		case "first":
			v = "-9999"
		case "last":
			v = "9999"
		case "none":
			v = "0"
		}
		if neg {
			v = "-" + v
		}
		return "order: " + v + ";"
	case "tw-pointer-events":
		return "pointer-events: " + m[1] + ";"
	case "tw-select":
		return "-webkit-user-select: " + m[1] + "; user-select: " + m[1] + ";"
	case "tw-animate":
		name := m[1]
		if name == "none" {
			return "animation: none;"
		}
		// User extend wins (theme.extend.animation), then the vendored animate catalog.
		if cfg != nil && cfg.Animations != nil {
			if r, ok := cfg.Animations[name]; ok {
				return "animation: " + r + ";"
			}
		}
		return resolveAnimate(name)
	case "tw-animate-arb":
		// Arbitrary animation: animate-[wiggle_1s_ease-in-out_infinite]. Underscores → spaces
		// (Tailwind convention). The keyframe is emitted by UsedKeyframes if the name is in the
		// catalog or theme.extend.keyframes.
		return "animation: " + strings.ReplaceAll(m[1], "_", " ") + ";"
	case "tw-animate-onhover":
		return "animation-play-state: paused;"
	}
	return ""
}
