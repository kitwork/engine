package css

import (
	"fmt"
	"strconv"
	"strings"
)

// The filter/backdrop/numeric chains exist because Tailwind lets several of these utilities stack on
// one element. A plain `filter: grayscale(100%)` would silently replace the `filter: blur(4px)` a
// `blur-sm` next to it emitted, so each utility fills its own slot and then restates the whole chain.
// The empty defaults live in Preflight; an unset slot contributes nothing.
const (
	filterChain   = "var(--tw-blur) var(--tw-brightness) var(--tw-contrast) var(--tw-grayscale) var(--tw-hue-rotate) var(--tw-invert) var(--tw-saturate) var(--tw-sepia) var(--tw-drop-shadow)"
	backdropChain = "var(--tw-backdrop-blur) var(--tw-backdrop-brightness) var(--tw-backdrop-contrast) var(--tw-backdrop-grayscale) var(--tw-backdrop-hue-rotate) var(--tw-backdrop-invert) var(--tw-backdrop-opacity) var(--tw-backdrop-saturate) var(--tw-backdrop-sepia)"
	numericChain  = "var(--tw-ordinal) var(--tw-slashed-zero) var(--tw-numeric-figure) var(--tw-numeric-spacing) var(--tw-numeric-fraction)"
)

// pctOrArb turns a Tailwind percentage scale value (150) into a CSS one (1.5); an arbitrary value
// passes through untouched.
func pctOrArb(s string) string {
	if strings.HasPrefix(s, "[") {
		return unarb(s)
	}
	return fmt.Sprintf("%g", float64(mustInt(s))/100.0)
}

// degOrArb turns a Tailwind rotation value (15) into degrees, honoring the negative prefix.
func degOrArb(s string, neg bool) string {
	if strings.HasPrefix(s, "[") {
		return unarb(s)
	}
	if neg {
		return "-" + s + "deg"
	}
	return s + "deg"
}

// parseFraction reads an alpha written as a fraction ("0.02"); an unreadable value is opaque.
func parseFraction(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 1
	}
	return f
}

// buildPropV3 handles the plain Tailwind v3 utilities that the original registry never covered —
// visibility, background sizing/position/repeat, resize, scroll-snap, filters, table, float, and the
// rest. They were found by resolving every class token the repo's sites actually use against the
// engine and listing the ones that produced no CSS at all: the class was written, looked right, and
// did nothing. buildProp falls through to here so the original switch stays readable.
func buildPropV3(t string, m []string, cfg *Config) string {
	switch t {
	case "tw-visibility":
		if m[1] == "invisible" {
			return "visibility: hidden;"
		}
		return "visibility: " + m[1] + ";"
	case "tw-vertical-align":
		return "vertical-align: " + m[1] + ";"
	case "tw-appearance":
		return "-webkit-appearance: " + m[1] + "; appearance: " + m[1] + ";"
	case "tw-bg-size":
		return "background-size: " + m[1] + ";"
	case "tw-bg-position":
		return "background-position: " + strings.ReplaceAll(m[1], "-", " ") + ";"
	case "tw-bg-repeat":
		if m[1] == "repeat-round" || m[1] == "repeat-space" {
			return "background-repeat: " + strings.TrimPrefix(m[1], "repeat-") + ";"
		}
		return "background-repeat: " + m[1] + ";"
	case "tw-bg-attachment":
		return "background-attachment: " + m[1] + ";"
	case "tw-bg-origin":
		return "background-origin: " + m[1] + "-box;"
	case "tw-bg-none":
		return "background-image: none;"
	case "tw-object-position":
		return "object-position: " + strings.ReplaceAll(m[1], "-", " ") + ";"
	case "tw-resize":
		switch m[1] {
		case "":
			return "resize: both;"
		case "x":
			return "resize: horizontal;"
		case "y":
			return "resize: vertical;"
		}
		return "resize: none;"
	case "tw-scroll-behavior":
		return "scroll-behavior: " + m[1] + ";"
	case "tw-snap-type":
		if m[1] == "none" {
			return "scroll-snap-type: none;"
		}
		return fmt.Sprintf("scroll-snap-type: %s var(--tw-scroll-snap-strictness);", m[1])
	case "tw-snap-strictness":
		return "--tw-scroll-snap-strictness: " + m[1] + ";"
	case "tw-snap-align":
		if m[1] == "align-none" {
			return "scroll-snap-align: none;"
		}
		return "scroll-snap-align: " + m[1] + ";"
	case "tw-snap-stop":
		return "scroll-snap-stop: " + m[1] + ";"
	case "tw-numeric":
		if m[1] == "normal-nums" {
			return "font-variant-numeric: normal;"
		}
		slot := map[string]string{
			"ordinal": "--tw-ordinal", "slashed-zero": "--tw-slashed-zero",
			"lining-nums": "--tw-numeric-figure", "oldstyle-nums": "--tw-numeric-figure",
			"proportional-nums": "--tw-numeric-spacing", "tabular-nums": "--tw-numeric-spacing",
			"diagonal-fractions": "--tw-numeric-fraction", "stacked-fractions": "--tw-numeric-fraction",
		}[m[1]]
		return fmt.Sprintf("%s: %s; font-variant-numeric: %s;", slot, m[1], numericChain)
	case "tw-float":
		return "float: " + m[1] + ";"
	case "tw-clear":
		return "clear: " + m[1] + ";"
	case "tw-table-layout":
		return "table-layout: " + m[1] + ";"
	case "tw-border-collapse":
		return "border-collapse: " + m[1] + ";"
	case "tw-border-spacing":
		v := twUnit(m[1])
		return fmt.Sprintf("--tw-border-spacing-x: %[1]s; --tw-border-spacing-y: %[1]s; border-spacing: %[1]s %[1]s;", v)
	case "tw-border-spacing-axis":
		v := twUnit(m[2])
		if m[1] == "x" {
			return fmt.Sprintf("--tw-border-spacing-x: %s; border-spacing: %s var(--tw-border-spacing-y);", v, v)
		}
		return fmt.Sprintf("--tw-border-spacing-y: %s; border-spacing: var(--tw-border-spacing-x) %s;", v, v)
	case "tw-caption":
		return "caption-side: " + m[1] + ";"
	case "tw-text-overflow":
		if m[1] == "ellipsis" {
			return "overflow: hidden; text-overflow: ellipsis;"
		}
		return "text-overflow: clip;"
	case "tw-text-wrap":
		return "text-wrap: " + m[1] + ";"
	case "tw-list-position":
		return "list-style-position: " + m[1] + ";"
	case "tw-list-image":
		return "list-style-image: none;"
	case "tw-decoration-thickness":
		if m[1] == "auto" || m[1] == "from-font" {
			return "text-decoration-thickness: " + m[1] + ";"
		}
		return "text-decoration-thickness: " + m[1] + "px;"
	case "tw-decoration-style":
		return "text-decoration-style: " + m[1] + ";"
	case "tw-outline-style":
		return "outline-style: " + m[1] + ";"
	case "tw-justify-self":
		v := m[1]
		if v == "start" || v == "end" {
			v = "flex-" + v
		}
		return "justify-self: " + v + ";"
	case "tw-justify-items":
		return "justify-items: " + m[1] + ";"
	case "tw-basis":
		return "flex-basis: " + twUnit(m[1]) + ";"
	case "tw-auto-track":
		vals := map[string]string{"auto": "auto", "min": "min-content", "max": "max-content", "fr": "minmax(0, 1fr)"}
		prop := "grid-auto-columns"
		if m[1] == "auto-rows" {
			prop = "grid-auto-rows"
		}
		return prop + ": " + vals[m[2]] + ";"
	case "tw-grid-flow":
		vals := map[string]string{"row": "row", "col": "column", "dense": "dense", "row-dense": "row dense", "col-dense": "column dense"}
		return "grid-auto-flow: " + vals[m[1]] + ";"
	case "tw-overscroll":
		prop := "overscroll-behavior"
		switch m[1] {
		case "overscroll-x":
			prop = "overscroll-behavior-x"
		case "overscroll-y":
			prop = "overscroll-behavior-y"
		}
		return prop + ": " + m[2] + ";"
	case "tw-touch":
		return "touch-action: " + m[1] + ";"
	case "tw-will-change":
		return "will-change: " + m[1] + ";"
	case "tw-indent":
		v := twUnit(m[1])
		if m[0][0] == '-' {
			v = "-" + v
		}
		return "text-indent: " + v + ";"
	case "tw-columns":
		return "columns: " + unarb(m[1]) + ";"
	case "tw-mix-blend":
		return "mix-blend-mode: " + m[1] + ";"
	case "tw-bg-blend":
		return "background-blend-mode: " + m[1] + ";"
	case "tw-break-side":
		return m[1] + ": " + m[2] + ";"
	case "tw-break-inside":
		return "break-inside: " + m[1] + ";"
	case "tw-box-decoration":
		return "-webkit-box-decoration-break: " + m[1] + "; box-decoration-break: " + m[1] + ";"
	case "tw-hyphens":
		return "-webkit-hyphens: " + m[1] + "; hyphens: " + m[1] + ";"
	case "tw-misc":
		switch m[1] {
		case "not-sr-only":
			return "position: static; width: auto; height: auto; padding: 0; margin: 0; overflow: visible; clip: auto; white-space: normal;"
		case "subpixel-antialiased":
			return "-webkit-font-smoothing: auto; -moz-osx-font-smoothing: auto;"
		}
		return "isolation: auto;"
	case "tw-divide-arb":
		if m[2] != "" {
			if strings.HasPrefix(m[2], "[") {
				return fmt.Sprintf("border-color: %s%02x;", m[1], int(parseFraction(unarb(m[2]))*255+0.5))
			}
			return fmt.Sprintf("border-color: %s%02x;", m[1], mustInt(m[2])*255/100)
		}
		return "border-color: " + m[1] + ";"
	case "tw-divide-style":
		return "border-style: " + m[1] + ";"
	case "tw-content-arb":
		return "--tw-content: " + unarb(m[1]) + "; content: var(--tw-content);"
	case "tw-content-none":
		return "--tw-content: none; content: none;"
	case "tw-reverse":
		return fmt.Sprintf("--tw-%s-%s-reverse: 1;", m[1], m[2])
	case "tw-caret-shade", "tw-caret-base":
		shade, alpha := "", ""
		if t == "tw-caret-shade" {
			shade, alpha = m[2], m[3]
		}
		if c := colorCSSValue(m[1], shade, alpha, cfg); c != "" {
			return "caret-color: " + c + ";"
		}
	case "tw-placeholder-shade", "tw-placeholder-base":
		shade, alpha := "", ""
		if t == "tw-placeholder-shade" {
			shade, alpha = m[2], m[3]
		} else {
			alpha = m[2]
		}
		if c := colorCSSValue(m[1], shade, alpha, cfg); c != "" {
			return "color: " + c + ";"
		}
	case "tw-filter":
		return "filter: " + filterChain + ";"
	case "tw-filter-none":
		return "filter: none;"
	case "tw-filter-toggle":
		v := m[1] + "(100%)"
		if m[2] == "0" {
			v = m[1] + "(0)"
		}
		return fmt.Sprintf("--tw-%s: %s; filter: %s;", m[1], v, filterChain)
	case "tw-filter-pct":
		return fmt.Sprintf("--tw-%s: %s(%s); filter: %s;", m[1], m[1], pctOrArb(m[2]), filterChain)
	case "tw-filter-hue":
		return fmt.Sprintf("--tw-hue-rotate: hue-rotate(%s); filter: %s;", degOrArb(m[2], m[0][0] == '-'), filterChain)
	case "tw-drop-shadow":
		sizes := map[string]string{
			"":     "drop-shadow(0 1px 2px rgb(0 0 0 / 0.1)) drop-shadow(0 1px 1px rgb(0 0 0 / 0.06))",
			"sm":   "drop-shadow(0 1px 1px rgb(0 0 0 / 0.05))",
			"md":   "drop-shadow(0 4px 3px rgb(0 0 0 / 0.07)) drop-shadow(0 2px 2px rgb(0 0 0 / 0.06))",
			"lg":   "drop-shadow(0 10px 8px rgb(0 0 0 / 0.04)) drop-shadow(0 4px 3px rgb(0 0 0 / 0.1))",
			"xl":   "drop-shadow(0 20px 13px rgb(0 0 0 / 0.03)) drop-shadow(0 8px 5px rgb(0 0 0 / 0.08))",
			"2xl":  "drop-shadow(0 25px 25px rgb(0 0 0 / 0.15))",
			"none": "drop-shadow(0 0 #0000)",
		}
		v, ok := sizes[m[1]]
		if !ok {
			v = "drop-shadow(" + unarb(m[1]) + ")"
		}
		return fmt.Sprintf("--tw-drop-shadow: %s; filter: %s;", v, filterChain)
	case "tw-backdrop-toggle":
		v := m[1] + "(100%)"
		if m[2] == "0" {
			v = m[1] + "(0)"
		}
		return fmt.Sprintf("--tw-backdrop-%s: %s; -webkit-backdrop-filter: %[3]s; backdrop-filter: %[3]s;", m[1], v, backdropChain)
	case "tw-backdrop-pct":
		return fmt.Sprintf("--tw-backdrop-%s: %s(%s); -webkit-backdrop-filter: %[4]s; backdrop-filter: %[4]s;", m[1], m[1], pctOrArb(m[2]), backdropChain)
	case "tw-backdrop-hue":
		return fmt.Sprintf("--tw-backdrop-hue-rotate: hue-rotate(%s); -webkit-backdrop-filter: %[2]s; backdrop-filter: %[2]s;", degOrArb(m[2], m[0][0] == '-'), backdropChain)
	case "tw-backdrop-filter":
		return "-webkit-backdrop-filter: " + backdropChain + "; backdrop-filter: " + backdropChain + ";"
	case "tw-backdrop-filter-none":
		return "-webkit-backdrop-filter: none; backdrop-filter: none;"
	}
	return ""
}
