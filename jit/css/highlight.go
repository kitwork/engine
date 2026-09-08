package css

import "strings"

// Code-surface colours as first-class jitcss tokens. `terminal`, `terminal-bar`,
// `terminal-comment` and the rest resolve without a site declaring anything, so
// the whole colour machinery works for them for free: bg-terminal,
// border-terminal-line, text-terminal-comment, alpha, variants, all of it.
//
// The hex data lives in jit/highlight, next to the syntax palette it belongs to.
// To avoid jit/css depending on jit/highlight, the source is injected: that
// package calls RegisterHighlightPalette at init — the same arrangement jit/logo
// already uses for brand-<slug>. Unwired, `terminal-*` simply does not resolve.
//
// This sits BELOW the config lookup on purpose. A site that spells `terminal`
// out in router.css() wins, because twColor consults cfg.Colors first; a site
// that spells out only some rungs gets its own for those and these for the rest.

// highlightResolver maps (theme, role) → hex ("#1a1b26", true). nil until registered.
var highlightResolver func(theme, role string) (string, bool)

// RegisterHighlightPalette wires the code-surface colour source (jit/highlight)
// into the colour resolver. Called once at init; later calls replace it, which
// is what lets a test install a stub.
func RegisterHighlightPalette(fn func(theme, role string) (string, bool)) { highlightResolver = fn }

// highlightRole strips the family prefix: "terminal" → "DEFAULT",
// "terminal-keyword" → "keyword", anything else → ("", false).
func highlightRole(colorName string) (string, bool) {
	if colorName == "terminal" {
		return "DEFAULT", true
	}
	if !strings.HasPrefix(colorName, "terminal-") {
		return "", false
	}
	role := colorName[len("terminal-"):]
	if role == "" {
		return "", false
	}
	return role, true
}

// highlightColor resolves a terminal-* colour through the cascade below the
// config, most specific first:
//
//	1. cfg.Colors["terminal-keyword"]      router.css() — handled by twColor before us
//	2. cfg.HighlightPalette["keyword"]     router.highlight({ keyword: … })
//	3. the named theme's preset            router.highlight("tokyonight")
//
// Returning a Color rather than a hex string is what lets the caller emit the
// themeable var() form, the same as any configured token.
func highlightColor(colorName string, cfg *Config) (Color, bool) {
	role, ok := highlightRole(colorName)
	if !ok {
		return Color{}, false
	}
	// Only a hex belongs on the COLOUR path. An override may also name a token
	// ("brand") or spell a class ("text-pink-400"); those are resolved where the
	// class is chosen, not here. Parsing them as hex used to yield Color{0,0,0}
	// silently, so a site asking for its brand colour got black.
	if cfg != nil && cfg.HighlightPalette != nil {
		if value, ok := cfg.HighlightPalette[role]; ok && isHexColor(value) {
			return Hex(value), true
		}
	}
	if highlightResolver == nil {
		return Color{}, false
	}
	theme := ""
	if cfg != nil {
		theme = cfg.HighlightTheme
	}
	hex, ok := highlightResolver(theme, role)
	if !ok || hex == "" {
		return Color{}, false
	}
	return Hex(hex), true
}

// highlightRegistered reports whether a name resolves through this palette, so
// colorCSSValue knows to wrap it in var(--color-<name>) like a config token.
func highlightRegistered(colorName string, cfg *Config) bool {
	_, ok := highlightColor(colorName, cfg)
	return ok
}

// isHexColor reports whether a value is a literal #rgb or #rrggbb. Anything else
// is a name or a class and must not be run through Hex.
func isHexColor(value string) bool {
	if len(value) != 4 && len(value) != 7 {
		return false
	}
	if value[0] != '#' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		hexDigit := (char >= '0' && char <= '9') ||
			(char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')
		if !hexDigit {
			return false
		}
	}
	return true
}
