package render

import (
	"html"
	"strings"

	"github.com/kitwork/engine/value"
)

// highlight is an engine-owned String method. It tokenizes trusted HTML source
// (a captured demo) and wraps each token in a jitcss color class, then returns a
// Raw value so `{{ src.highlight() }}` prints the result verbatim — no `.html()`
// or `raw()` needed. It is safe by construction: every text run is escaped and
// only a fixed span vocabulary is emitted, so it can never inject markup.
//
// A theme selects the palette:
//
//	{{ src.highlight() }}            engine default (behavior glows in the brand)
//	{{ src.highlight("classic") }}   named built-in palette
//	{{ src.highlight(theme) }}       a bound map { directive, tag, attr, string, punct }
//
// Colors are jitcss classes, so the same generation that styles the page also
// styles the code — no separate stylesheet.
func init() {
	value.String.Prototype("highlight", func(target value.Value, args ...value.Value) value.Value {
		result := value.New(highlightWith(target.String(), highlightPalette(args)))
		result.Raw = true
		return result
	})
}

type hlPalette struct {
	punct     string
	tag       string
	attr      string
	directive string
	str       string
}

// hlDefault keeps HTML neutral and lets the behavior layer (data-kit-*) glow in
// the site's brand color, so the code shows "keep the HTML, add the behavior".
var hlDefault = hlPalette{
	punct:     "text-zinc-600",
	tag:       "text-zinc-300",
	attr:      "text-zinc-500",
	directive: "text-brand",
	str:       "text-amber-200",
}

var hlThemes = map[string]hlPalette{
	"":        hlDefault,
	"default": hlDefault,
	"brand":   hlDefault,
	"minimal": hlDefault,
	"mono": {
		punct:     "text-zinc-500",
		tag:       "text-zinc-300",
		attr:      "text-zinc-300",
		directive: "text-zinc-300",
		str:       "text-zinc-300",
	},
	"classic": {
		punct:     "text-zinc-500",
		tag:       "text-sky-300",
		attr:      "text-violet-300",
		directive: "text-lime-300",
		str:       "text-amber-200",
	},
}

func highlightPalette(args []value.Value) hlPalette {
	if len(args) == 0 {
		return hlDefault
	}
	selector := args[0]
	if selector.K == value.String {
		if palette, ok := hlThemes[selector.String()]; ok {
			return palette
		}
		return hlDefault
	}
	if selector.K == value.Map {
		return hlPalette{
			punct:     paletteField(selector, "punct", hlDefault.punct),
			tag:       paletteField(selector, "tag", hlDefault.tag),
			attr:      paletteField(selector, "attr", hlDefault.attr),
			directive: paletteField(selector, "directive", hlDefault.directive),
			str:       paletteField(selector, "string", hlDefault.str),
		}
	}
	return hlDefault
}

func paletteField(source value.Value, key, fallback string) string {
	field := source.Get(key)
	if field.K == value.String {
		if text := field.String(); text != "" {
			return text
		}
	}
	return fallback
}

func highlightWith(src string, palette hlPalette) string {
	var builder strings.Builder
	builder.Grow(len(src) * 3)
	length := len(src)
	for index := 0; index < length; {
		if src[index] == '<' {
			index = highlightTag(&builder, src, index, palette)
			continue
		}
		next := index
		for next < length && src[next] != '<' {
			next++
		}
		builder.WriteString(html.EscapeString(src[index:next]))
		index = next
	}
	return builder.String()
}

func highlightSpan(builder *strings.Builder, class, content string) {
	builder.WriteString(`<span class="`)
	builder.WriteString(class)
	builder.WriteString(`">`)
	builder.WriteString(content)
	builder.WriteString(`</span>`)
}

func highlightIsSpace(char byte) bool {
	return char == ' ' || char == '\t' || char == '\n' || char == '\r' || char == '\f'
}

// highlightTag consumes one <...> tag beginning at open and returns the index
// just past it.
func highlightTag(builder *strings.Builder, src string, open int, palette hlPalette) int {
	length := len(src)
	index := open + 1 // past '<'
	lead := "&lt;"
	if index < length && src[index] == '/' {
		lead += "/"
		index++
	}
	highlightSpan(builder, palette.punct, lead)

	start := index
	for index < length && !highlightIsSpace(src[index]) && src[index] != '>' && src[index] != '/' {
		index++
	}
	if index > start {
		highlightSpan(builder, palette.tag, html.EscapeString(src[start:index]))
	}

	for index < length && src[index] != '>' {
		char := src[index]
		if highlightIsSpace(char) {
			builder.WriteByte(char)
			index++
			continue
		}
		if char == '/' {
			highlightSpan(builder, palette.punct, "/")
			index++
			continue
		}
		nameStart := index
		for index < length && src[index] != '=' && src[index] != '>' && src[index] != '/' && !highlightIsSpace(src[index]) {
			index++
		}
		name := src[nameStart:index]
		class := palette.attr
		if strings.HasPrefix(name, "data-kit-") {
			class = palette.directive
		}
		highlightSpan(builder, class, html.EscapeString(name))

		if index < length && src[index] == '=' {
			highlightSpan(builder, palette.punct, "=")
			index++
			if index < length && (src[index] == '"' || src[index] == '\'') {
				quote := src[index]
				valueEnd := index + 1
				for valueEnd < length && src[valueEnd] != quote {
					valueEnd++
				}
				if valueEnd < length {
					valueEnd++ // include closing quote
				}
				highlightSpan(builder, palette.str, html.EscapeString(src[index:valueEnd]))
				index = valueEnd
			}
		}
	}
	if index < length && src[index] == '>' {
		highlightSpan(builder, palette.punct, "&gt;")
		index++
	}
	return index
}
