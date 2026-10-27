package work

// router.jitcss(config): a folder router (usually the root) tunes the site-wide JIT-CSS engine —
// custom brand colors (theme.extend.colors), keyframes, shadows, and the dark: selector. The
// pending generation collects this declaration and freezes it at activation.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	jitcss "github.com/kitwork/engine/jit/css"
	sitepkg "github.com/kitwork/engine/site"
	"github.com/kitwork/engine/value"
)

// Jitcss builds and installs the tenant's JIT-CSS config from a Tailwind-shaped map:
//
//	router.jitcss({
//	    darkMode: ['class', '[data-theme="dark"]'],   // parent selector for dark:
//	    theme: { extend: { colors: { brand: { DEFAULT: '#f82244' } }, keyframes: {...} } },
//	})
// Css is the current name for the design-token config. The JIT prefix described
// the engine's mechanism, not the author's intent: from a site's side this is
// simply where its colours, fonts and utilities are declared.
//
//	router.css({ theme: { extend: { colors: { brand: "#f82244" } } } })
//
// Jitcss stays as a deprecated alias so existing sites keep working.
func (f *FolderRouter) Css(cfg value.Value) *FolderRouter { return f.Jitcss(cfg) }

func (f *FolderRouter) Jitcss(cfg value.Value) *FolderRouter {
	if config := buildJitcssConfig(cfg); config != nil {
		f.tenant.presentation().SetJITConfig(config)
	}
	return f
}

// buildJitcssConfig clones the default config and overlays the given theme/darkMode.
func buildJitcssConfig(cfg value.Value) *jitcss.Config {
	if !cfg.IsMap() {
		return nil
	}
	config := &jitcss.Config{
		Colors:       make(map[string]jitcss.Color),
		Order:        append([]string(nil), jitcss.DefaultConfig.Order...),
		MediaQueries: make(map[string]string),
		States:       make(map[string]string),
		ShadowLevels: make(map[string]string),
		Scale:        append([]int(nil), jitcss.DefaultConfig.Scale...),
		AlphaScales:  append([]int(nil), jitcss.DefaultConfig.AlphaScales...),
		Opacities:    append([]int(nil), jitcss.DefaultConfig.Opacities...),
		ZIndices:     append([]int(nil), jitcss.DefaultConfig.ZIndices...),
		Animations:   make(map[string]string),
		Keyframes:    make(map[string]string),
		FontFamily:   make(map[string]string),
	}
	for k, v := range jitcss.DefaultConfig.Colors {
		config.Colors[k] = v
	}
	for k, v := range jitcss.DefaultConfig.MediaQueries {
		config.MediaQueries[k] = v
	}
	for k, v := range jitcss.DefaultConfig.States {
		config.States[k] = v
	}
	for k, v := range jitcss.DefaultConfig.ShadowLevels {
		config.ShadowLevels[k] = v
	}
	for k, v := range jitcss.DefaultConfig.FontFamily {
		config.FontFamily[k] = v
	}

	m := cfg.Map()

	// darkMode: 'class' | ['class', '<selector>'] | ['selector', '<selector>'] — the custom parent
	// selector (2nd element) scopes the dark: variant. Default (empty) → ".dark". Declaring any
	// class/selector form (not 'media') says the site's dark is a saved choice → DarkByClass, and
	// the theme pre-paints it on every page.
	if dmVal, ok := m["darkMode"]; ok {
		switch {
		case dmVal.K == value.Array:
			arr := dmVal.Array()
			if len(arr) >= 2 && arr[1].Text() != "" {
				config.DarkSelector = arr[1].Text()
			}
			config.DarkByClass = len(arr) == 0 || !strings.EqualFold(arr[0].Text(), "media")
		case dmVal.IsString():
			config.DarkByClass = !strings.EqualFold(dmVal.String(), "media")
		}
	}

	// theme + theme.extend (extend overlays after base so it wins on conflicts).
	if themeVal, ok := m["theme"]; ok && themeVal.IsMap() {
		themeMap := themeVal.Map()
		parseTheme(config, themeMap)
		if extendVal, ok := themeMap["extend"]; ok && extendVal.IsMap() {
			parseTheme(config, extendVal.Map())
		}
	}
	return config
}

// parseTheme overlays colors / boxShadow / animation / keyframes from a theme map onto config.
func parseTheme(config *jitcss.Config, m map[string]value.Value) {
	if colorsVal, ok := m["colors"]; ok && colorsVal.IsMap() {
		for colorName, colorVal := range colorsVal.Map() {
			if colorVal.IsString() {
				config.Colors[colorName] = jitcss.Hex(colorVal.String())
				if !contains(config.Order, colorName) {
					config.Order = append(config.Order, colorName)
				}
			} else if colorVal.IsMap() {
				for shadeName, shadeVal := range colorVal.Map() {
					if !shadeVal.IsString() {
						continue
					}
					flatName := colorName + "-" + shadeName
					if shadeName == "DEFAULT" {
						flatName = colorName
					}
					config.Colors[flatName] = jitcss.Hex(shadeVal.String())
					if !contains(config.Order, flatName) {
						config.Order = append(config.Order, flatName)
					}
				}
			}
		}
	}
	if ffVal, ok := m["fontFamily"]; ok && ffVal.IsMap() {
		for name, v := range ffVal.Map() {
			var parts []string
			switch {
			case v.K == value.Array:
				for _, e := range v.Array() {
					if e.IsString() {
						parts = append(parts, quoteFamily(e.String()))
					}
				}
			case v.IsString():
				parts = append(parts, quoteFamily(v.String()))
			}
			if len(parts) > 0 {
				config.FontFamily[name] = strings.Join(parts, ", ")
			}
		}
	}
	if shadowVal, ok := m["boxShadow"]; ok && shadowVal.IsMap() {
		for name, v := range shadowVal.Map() {
			if v.IsString() {
				config.ShadowLevels[name] = v.String()
			}
		}
	}
	if animVal, ok := m["animation"]; ok && animVal.IsMap() {
		for name, v := range animVal.Map() {
			if v.IsString() {
				config.Animations[name] = v.String()
			}
		}
	}
	if kfVal, ok := m["keyframes"]; ok && kfVal.IsMap() {
		for name, v := range kfVal.Map() {
			if v.IsMap() {
				config.Keyframes[name] = buildKeyframeCSS(v.Map())
			}
		}
	}
}

func contains(arr []string, val string) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// buildKeyframeCSS writes the stages of one @keyframes in the order they run (from, then each
// percentage, then to) and each stage's declarations by name. The config arrives as maps, so this
// order is what makes the CSS, and the stylesheet hash and ETag built from it, the same on every boot.
func buildKeyframeCSS(stagesMap map[string]value.Value) string {
	stages := make([]string, 0, len(stagesMap))
	for stage, props := range stagesMap {
		if props.IsMap() {
			stages = append(stages, stage)
		}
	}
	sort.Slice(stages, func(i, j int) bool {
		a, b := keyframeOffset(stages[i]), keyframeOffset(stages[j])
		if a != b {
			return a < b
		}
		return stages[i] < stages[j]
	})
	var sb strings.Builder
	for _, stage := range stages {
		props := stagesMap[stage].Map()
		names := make([]string, 0, len(props))
		for prop := range props {
			names = append(names, prop)
		}
		sort.Strings(names)
		sb.WriteString("\t" + stage + " {\n")
		for _, prop := range names {
			sb.WriteString(fmt.Sprintf("\t\t%s: %s;\n", prop, props[prop].String()))
		}
		sb.WriteString("\t}\n")
	}
	return sb.String()
}

// keyframeOffset is where a stage selector starts in the animation, in percent: from is 0, to is 100,
// "50%" is 50, and "0%, 100%" counts from its first offset. A selector it cannot read sorts last.
func keyframeOffset(stage string) float64 {
	first := strings.TrimSpace(strings.SplitN(stage, ",", 2)[0])
	switch strings.ToLower(first) {
	case "from":
		return 0
	case "to":
		return 100
	}
	if number, ok := strings.CutSuffix(first, "%"); ok {
		if offset, err := strconv.ParseFloat(number, 64); err == nil {
			return offset
		}
	}
	return 1000
}

// publishSitePalette hands the site root's colours and typefaces to the presentation as the site's CSS
// configuration. It is rebuilt whole from the defaults on every call, so router.colors and router.fonts
// may come in either order and as many times as the file likes. A branch keeps its own on the folder
// (routeCSS). router.css() is a template site's whole configuration: on one site the later wins.
func (f *FolderRouter) publishSitePalette() {
	if f == nil || f.tenant == nil || f.node == nil || f.node.parent != nil {
		return
	}
	config := buildJitcssConfig(value.Value{K: value.Map, V: map[string]value.Value{}})
	if config == nil {
		return
	}
	if applyPalette(config, f.colors, f.fonts) {
		f.tenant.presentation().SetThemes([]string{"dark"})
	}
	f.tenant.presentation().SetJITConfig(config)
}

// applyPalette writes one folder's router.colors() and router.fonts() into a CSS configuration: each
// colour, its dark rung (<token>-dark, which re-values the token under .dark) when it has one, and the
// typefaces. It reports whether any dark value was declared. Tokens are written in name order, so the
// CSS is the same on every boot.
//
// An accent (brand, success, warning… every colour but the surfaces and the text) is a family: from
// its one hue the engine grows deep, soft, wash and on (jitcss.Palette) unless the palette writes the
// rung itself. A grown rung is a token like any other, so a badge's soft fill and deep text get a dark
// value of their own instead of staying a light pastel on a dark page.
func applyPalette(config *jitcss.Config, colors, fonts map[string]value.Value) bool {
	dark := false
	declared := map[string]bool{}
	for name := range colors {
		declared[strings.ReplaceAll(name, ".", "-")] = true
	}
	for _, name := range sortedNames(colors) {
		v := colors[name]
		light, night := "", ""
		switch {
		case v.IsString():
			light = v.String()
		case v.IsMap():
			if c, ok := v.Map()["light"]; ok && c.IsString() {
				light = c.String()
			}
			if c, ok := v.Map()["dark"]; ok && c.IsString() {
				night = c.String()
			}
		}
		token := strings.ReplaceAll(name, ".", "-")
		if light != "" {
			setColor(config, token, light)
		}
		if night != "" {
			dark = true
			setColor(config, token+"-dark", night)
		}
		if light == "" || !accentToken(token) {
			continue
		}
		grown := jitcss.Palette(light)
		for _, rung := range sortedRungs(grown) {
			if rung == "DEFAULT" || declared[token+"-"+rung] {
				continue // the hue itself is the token; a rung the palette writes is its own
			}
			setColor(config, token+"-"+rung, grown[rung])
		}
	}
	for _, role := range sortedNames(fonts) {
		v := fonts[role]
		if !v.IsString() || strings.TrimSpace(v.String()) == "" {
			continue
		}
		fallback := jitcss.DefaultConfig.FontFamily["sans"]
		if role == "code" {
			fallback = jitcss.DefaultConfig.FontFamily["mono"]
		}
		config.FontFamily[role] = quoteFamily(strings.TrimSpace(v.String())) + ", " + fallback
	}
	return dark
}

func setColor(config *jitcss.Config, token, hex string) {
	config.Colors[token] = jitcss.Hex(hex)
	if !contains(config.Order, token) {
		config.Order = append(config.Order, token)
	}
}

func sortedRungs(m map[string]string) []string {
	rungs := make([]string, 0, len(m))
	for rung := range m {
		rungs = append(rungs, rung)
	}
	sort.Strings(rungs)
	return rungs
}

// accentToken reports whether a colour is an accent that grows a family: every colour except the
// surfaces (canvas, panel, paper, line) and the text (ink and its rungs).
func accentToken(token string) bool {
	switch token {
	case "canvas", "panel", "paper", "line", "ink":
		return false
	}
	return !strings.HasPrefix(token, "ink-")
}

// routeCSS is one route's CSS configuration, and whether its pages have a dark look (the render plan
// then pre-paints every one of them). It is the site's, with the router.colors() and router.fonts() of
// every folder between the site root and the route merged over it, the nearest last, and the dark look
// derived again from the merged tokens: a branch that changes brand gets a dark brand of its own, and
// a dark pair written only in a branch gives that branch a dark look. A route whose appearance is
// "dark" (mode) gets one too, derived from the tokens, when nothing declared one. A route that changes
// nothing shares the site's configuration as it is.
func routeCSS(site *jitcss.Config, node *RouteNode, mode string) (*jitcss.Config, bool) {
	var chain []*FolderRouter
	for n := node; n != nil && n.parent != nil; n = n.parent {
		if n.folder != nil && (len(n.folder.colors) > 0 || len(n.folder.fonts) > 0) {
			chain = append(chain, n.folder)
		}
	}
	siteDark := site != nil && (site.Themes["dark"] != nil || site.DarkByClass)
	deriveDark := mode == "dark" && !siteDark
	if len(chain) == 0 && !deriveDark {
		return site, siteDark
	}
	var merged *jitcss.Config
	if site != nil {
		merged = sitepkg.CloneJITConfig(site)
	} else {
		merged = buildJitcssConfig(value.Value{K: value.Map, V: map[string]value.Value{}})
	}
	var modes []string
	for m := range merged.Themes {
		modes = append(modes, m)
	}
	merged.Themes = nil
	dark := deriveDark
	for i := len(chain) - 1; i >= 0; i-- {
		if applyPalette(merged, chain[i].colors, chain[i].fonts) {
			dark = true
		}
	}
	if dark && !contains(modes, "dark") {
		modes = append(modes, "dark")
	}
	sort.Strings(modes)
	jitcss.DeriveThemes(merged, modes...)
	return merged, contains(modes, "dark") || merged.DarkByClass
}
