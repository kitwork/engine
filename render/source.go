package render

import (
	"html"
	"strings"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
	"github.com/kitwork/engine/value"
)

// These String methods complete the "show, don't hide" side of the component
// recipe. highlight() colors the markup a component drives; source() and
// highlightScript() reveal the JavaScript the component *is*, and standalone()
// prints the markup in its unversioned, pure-KitJS form. Together a page can put
// the whole component on screen — the exact JS a developer would write and the
// exact HTML it drives — while the live demo above still runs on the site's
// staged delivery.
func init() {
	// source(version) returns the authored kit.component(...) JavaScript of the
	// component named by the receiver, unwrapped from its delivery IIFE so what
	// shows is what a developer writes. It is a plain (escapable) String; chain
	// .highlightScript() to color it. An unknown component yields "".
	value.String.Prototype("source", func(target value.Value, args ...value.Value) value.Value {
		version := ""
		if len(args) > 0 {
			version = args[0].String()
		}
		raw, err := kitjavascript.ComponentSource(target.String(), version)
		if err != nil {
			return value.New("")
		}
		return value.New(unwrapComponentSource(string(raw)))
	})

	// standalone() rewrites data-kit-component="name@x.y.z" to the unversioned
	// data-kit-component="name" — the form pure standalone KitJS uses, where you
	// register the component yourself and no version pin is needed. The @version
	// is a Kitwork-staging detail, so the copyable markup should not carry it.
	value.String.Prototype("standalone", func(target value.Value, args ...value.Value) value.Value {
		return value.New(stripComponentVersion(target.String()))
	})

	// highlightScript(theme?) is highlight()'s sibling for JavaScript: it tokenizes
	// trusted JS and wraps tokens in the same jitcss palette, returning Raw. The
	// kit identifier glows in the directive color to tie the JS to the behavior it
	// defines. Safe by construction: every run is escaped, only spans are emitted.
	value.String.Prototype("highlightScript", func(target value.Value, args ...value.Value) value.Value {
		result := value.New(highlightScriptWith(target.String(), highlightPalette(args)))
		result.Raw = true
		return result
	})
}

// unwrapComponentSource strips the delivery IIFE (`;(function () { "use strict";
// … })();`) so the shown source is the component itself — its helpers and the
// kit.component(...) call — the part a developer copies. The markers are fixed
// across every vendored component; if they are absent the source is returned as
// authored.
func unwrapComponentSource(raw string) string {
	body := raw
	const strict = `"use strict";`
	if i := strings.Index(body, strict); i >= 0 {
		body = body[i+len(strict):]
	}
	if j := strings.LastIndex(body, "})();"); j >= 0 {
		body = body[:j]
	}
	return strings.TrimSpace(body)
}

// stripComponentVersion removes the @version from every
// data-kit-component="name@x.y.z" occurrence, leaving data-kit-component="name".
// Only that attribute's value is touched; all other markup is copied verbatim.
func stripComponentVersion(markup string) string {
	const attr = `data-kit-component="`
	var builder strings.Builder
	builder.Grow(len(markup))
	rest := markup
	for {
		index := strings.Index(rest, attr)
		if index < 0 {
			builder.WriteString(rest)
			break
		}
		builder.WriteString(rest[:index+len(attr)])
		rest = rest[index+len(attr):]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			builder.WriteString(rest)
			break
		}
		host := rest[:end]
		if at := strings.IndexByte(host, '@'); at >= 0 {
			host = host[:at]
		}
		builder.WriteString(host)
		builder.WriteByte('"')
		rest = rest[end+1:]
	}
	return builder.String()
}

// jsKeywords is the reserved/declaration vocabulary the script highlighter paints
// in the structural (tag) color. Known globals are deliberately left out so they
// read as ordinary identifiers.
var jsKeywords = map[string]bool{
	"const": true, "let": true, "var": true, "function": true,
	"return": true, "if": true, "else": true, "for": true, "of": true,
	"in": true, "while": true, "do": true, "switch": true, "case": true,
	"break": true, "continue": true, "new": true, "typeof": true,
	"instanceof": true, "this": true, "true": true, "false": true,
	"null": true, "undefined": true, "void": true, "delete": true,
	"throw": true, "try": true, "catch": true, "finally": true,
	"class": true, "extends": true, "super": true, "import": true,
	"export": true, "default": true, "async": true, "await": true,
}

// highlightScriptWith tokenizes trusted JavaScript and wraps each meaningful token
// in a jitcss color span, sharing the palette with the HTML highlighter. Plain
// identifiers and numbers are left unwrapped and inherit the code block's base
// text color, so the accents (keywords, strings, calls, kit.*) stand out.
func highlightScriptWith(src string, palette hlPalette) string {
	var builder strings.Builder
	builder.Grow(len(src) * 3)
	length := len(src)
	for index := 0; index < length; {
		char := src[index]

		if highlightIsSpace(char) {
			builder.WriteByte(char)
			index++
			continue
		}

		// Line comment.
		if char == '/' && index+1 < length && src[index+1] == '/' {
			end := index
			for end < length && src[end] != '\n' {
				end++
			}
			highlightSpan(&builder, palette.punct, html.EscapeString(src[index:end]))
			index = end
			continue
		}

		// Block comment.
		if char == '/' && index+1 < length && src[index+1] == '*' {
			end := index + 2
			for end+1 < length && !(src[end] == '*' && src[end+1] == '/') {
				end++
			}
			if end+1 < length {
				end += 2
			} else {
				end = length
			}
			highlightSpan(&builder, palette.punct, html.EscapeString(src[index:end]))
			index = end
			continue
		}

		// String or template literal.
		if char == '"' || char == '\'' || char == '`' {
			end := index + 1
			for end < length && src[end] != char {
				if src[end] == '\\' && end+1 < length {
					end++
				}
				end++
			}
			if end < length {
				end++ // closing quote
			}
			highlightSpan(&builder, palette.str, html.EscapeString(src[index:end]))
			index = end
			continue
		}

		// Number: left unwrapped (base color), just consumed as one run.
		if char >= '0' && char <= '9' {
			end := index
			for end < length && isJSNumberPart(src[end]) {
				end++
			}
			builder.WriteString(src[index:end])
			index = end
			continue
		}

		// Identifier or keyword.
		if isJSIdentStart(char) {
			end := index
			for end < length && isJSIdentPart(src[end]) {
				end++
			}
			word := src[index:end]
			class := ""
			switch {
			case word == "kit":
				class = palette.directive
			case jsKeywords[word]:
				class = palette.tag
			case precededByDot(src, index) || followedByCall(src, end):
				class = palette.attr
			}
			if class != "" {
				highlightSpan(&builder, class, html.EscapeString(word))
			} else {
				builder.WriteString(html.EscapeString(word))
			}
			index = end
			continue
		}

		// Structural punctuation.
		highlightSpan(&builder, palette.punct, html.EscapeString(src[index:index+1]))
		index++
	}
	return builder.String()
}

func isJSIdentStart(char byte) bool {
	return char == '_' || char == '$' ||
		(char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

func isJSIdentPart(char byte) bool {
	return isJSIdentStart(char) || (char >= '0' && char <= '9')
}

func isJSNumberPart(char byte) bool {
	return (char >= '0' && char <= '9') || char == '.' ||
		char == 'e' || char == 'E' || char == 'x' || char == 'X' ||
		(char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')
}

// precededByDot reports whether the token starting at index is a property access
// (the previous non-space byte is a single '.', not part of a '...' spread).
func precededByDot(src string, index int) bool {
	cursor := index - 1
	for cursor >= 0 && highlightIsSpace(src[cursor]) {
		cursor--
	}
	if cursor < 0 || src[cursor] != '.' {
		return false
	}
	return cursor == 0 || src[cursor-1] != '.'
}

// followedByCall reports whether the token ending at index is immediately called
// (the next non-space byte is '(').
func followedByCall(src string, index int) bool {
	cursor := index
	for cursor < len(src) && highlightIsSpace(src[cursor]) {
		cursor++
	}
	return cursor < len(src) && src[cursor] == '('
}
