package hydrate

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// PreRender is the SERVER half of first paint: it runs the same expressions the client would
// evaluate at boot and bakes the results into the HTML — so the page arrives already showing the
// right values (no flash of "0"), reads correctly with JS disabled (progressive enhancement), and
// is fully indexable. It uses the SAME compiler + Go walker (Eval) as ctx.validate, so what the
// server paints and what the client re-renders are computed identically.
//
// The initial scope is derived from the page itself — the value= of each data-kit-model input —
// exactly as the client seeds it, so both ends start from the same data. A missing variable reads
// as 0/undefined on both ends.
//
// Scope of v1, stated honestly:
//   - Page scope only. Bindings inside a data-kit-scope are left for the client (they may flash);
//     PreRender never bakes a value it might get wrong.
//   - Leaf text bindings only: an element whose content is plain text (the overwhelming norm for
//     data-kit-text). An element wrapping other tags is left untouched.
//   - text and show. Everything else (click/model/live/validate) is inert markup at rest anyway.
//
// PreRender runs after Render in the pipeline. Like Render it is gated by the data-kitwork-hydrate
// root marker, so static pages and example-showing docs are never touched.
func PreRender(htmlStr string) string {
	if !strings.Contains(htmlStr, rootMarker) && !strings.Contains(htmlStr, rootMarkerShort) {
		return htmlStr
	}
	scope := modelScope(htmlStr)
	out := preRenderText(htmlStr, scope)
	out = preRenderShow(out, scope)
	return out
}

// PreRender evaluates authored SOURCE only, so all three regexes match the data-kit-* prefix
// exclusively — data-kitwork-text/show on the wire is engine-emitted IR (JSON), not an expression,
// and must not be Eval'd as source. model has no IR form but follows the same authored canon.
var (
	// a data-kit-model input — captures the scope key; value/type are read from the tag body.
	modelTagRe  = regexp.MustCompile(`<[a-zA-Z][^>]*\bdata-kit-model="([^"]*)"[^>]*>`)
	attrValueRe = regexp.MustCompile(`\bvalue="([^"]*)"`)
	attrTypeRe  = regexp.MustCompile(`\btype="([^"]*)"`)
	// leaf text binding: (open tag)(plain-text content)(closing "</"). Content has no nested tag.
	textLeafRe = regexp.MustCompile(`(<[a-zA-Z][^>]*\bdata-kit-text="([^"]*)"[^>]*>)([^<]*)(</)`)
	// an element carrying data-kit-show — the whole opening tag, plus its expression.
	showTagRe = regexp.MustCompile(`<[a-zA-Z][^>]*\bdata-kit-show="([^"]*)"[^>]*>`)
)

// modelScope seeds the initial scope from the value= of each model input, matching the client's
// modelValue(): a number-typed input coerces to a float, everything else stays a string.
func modelScope(htmlStr string) map[string]any {
	scope := map[string]any{}
	for _, tag := range modelTagRe.FindAllStringSubmatch(htmlStr, -1) {
		key := tag[1]
		if key == "" {
			continue
		}
		if _, seen := scope[key]; seen {
			continue
		}
		value := ""
		if m := attrValueRe.FindStringSubmatch(tag[0]); m != nil {
			value = m[1]
		}
		if m := attrTypeRe.FindStringSubmatch(tag[0]); m != nil && m[1] == "number" {
			f, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
			scope[key] = f
		} else {
			scope[key] = value
		}
	}
	return scope
}

func preRenderText(htmlStr string, scope map[string]any) string {
	return textLeafRe.ReplaceAllStringFunc(htmlStr, func(m string) string {
		sub := textLeafRe.FindStringSubmatch(m)
		open, expr, closing := sub[1], sub[2], sub[4]
		node, err := Compile(expr)
		if err != nil {
			return m // malformed → leave the authored content, the client (and its logger) handle it
		}
		v, err := Eval(node, scope)
		if err != nil {
			return m
		}
		return open + html.EscapeString(display(v)) + closing
	})
}

func preRenderShow(htmlStr string, scope map[string]any) string {
	return showTagRe.ReplaceAllStringFunc(htmlStr, func(m string) string {
		sub := showTagRe.FindStringSubmatch(m)
		node, err := Compile(sub[1])
		if err != nil {
			return m
		}
		v, err := Eval(node, scope)
		if err != nil {
			return m
		}
		hasHidden := strings.Contains(m, " hidden>") || strings.Contains(m, " hidden ") || strings.Contains(m, " hidden=")
		if truthy(v) {
			return m // shown: leave as-is (author should not pre-hide a shown region)
		}
		if hasHidden {
			return m
		}
		return m[:len(m)-1] + " hidden>" // falsy → hide it, matching the client's el.hidden = true
	})
}

// display renders a value the way the client's textContent assignment would (v == null ? "" : v).
func display(v any) string {
	if v == nil {
		return ""
	}
	return toStr(v)
}
