package work

// router.jitcss(config): a folder router (usually the root) tunes the site-wide JIT-CSS engine —
// custom brand colors (theme.extend.colors), keyframes, shadows, and the dark: selector. It writes
// tenant.jitcssConfig, which the render engine feeds to GenerateJITCached for every page. Declare
// once at the root; it applies to the whole tenant.

import (
	"fmt"
	"strings"

	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/value"
)

// Jitcss builds and installs the tenant's JIT-CSS config from a Tailwind-shaped map:
//
//	router.jitcss({
//	    darkMode: ['class', '[data-theme="dark"]'],   // parent selector for dark:
//	    theme: { extend: { colors: { brand: { DEFAULT: '#f82244' } }, keyframes: {...} } },
//	})
func (f *FolderRouter) Jitcss(cfg value.Value) *FolderRouter {
	if config := buildJitcssConfig(cfg); config != nil {
		f.tenant.jitcssConfig = config
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

	m := cfg.Map()

	// darkMode: 'class' | ['class', '<selector>'] | ['selector', '<selector>'] — the custom parent
	// selector (2nd element) scopes the dark: variant. Default (empty) → ".dark".
	if dmVal, ok := m["darkMode"]; ok && dmVal.K == value.Array {
		if arr := dmVal.Array(); len(arr) >= 2 && arr[1].Text() != "" {
			config.DarkSelector = arr[1].Text()
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

func buildKeyframeCSS(stagesMap map[string]value.Value) string {
	var sb strings.Builder
	for stage, props := range stagesMap {
		if !props.IsMap() {
			continue
		}
		sb.WriteString("\t" + stage + " {\n")
		for prop, val := range props.Map() {
			sb.WriteString(fmt.Sprintf("\t\t%s: %s;\n", prop, val.String()))
		}
		sb.WriteString("\t}\n")
	}
	return sb.String()
}
