package richtext

import (
	"html"
	"strings"
)

// The tokenizer reads HTML the way a browser's does (WHATWG HTML, "Tokenization"), as far as the
// grammar needs it: text with its character references decoded, start and end tags with the few
// attributes the tree reads, and everything else (comments, doctypes, processing instructions, bogus
// comments, CDATA) skipped. The text of script, style, textarea, title, xmp, iframe, noembed, noframes,
// noscript and template runs to its own end tag and is skipped with it, plaintext runs to the end of
// the input, and an svg or math element is skipped with its whole subtree. Where it does not follow a
// browser exactly it skips more, never less: a mistake can lose text, and markup never gets through,
// since the tree builder writes only the grammar. Every step moves forward: the time is linear.

type tokenKind uint8

const (
	textToken tokenKind = iota + 1
	startToken
	endToken
)

// token is one thing the tree builder reads.
type token struct {
	kind tokenKind
	// name is a tag's name in lower case.
	name string
	// text is a text token's characters, character references decoded.
	text string
	// href, style, class and align are the first attributes of those names, as written: Link decodes an
	// href; a block's alignment is read from the other three (explicitAlignment), a b's weight from style.
	href, style, class, align             string
	hasHref, hasStyle, hasClass, hasAlign bool
	// fontAttribute: the tag has color, face or size, which makes a font end svg or math content.
	fontAttribute bool
	selfClosing   bool
	// raw: a start tag whose text and end tag the tokenizer has already skipped.
	raw bool
}

type tokenizer struct {
	source   string
	position int
}

// rawText are the elements whose text runs to their own end tag (script has states of its own).
var rawText = map[string]bool{
	"style": true, "textarea": true, "title": true, "xmp": true, "iframe": true,
	"noembed": true, "noframes": true, "noscript": true, "template": true,
}

// next returns the next token, false at the end of the input.
func (t *tokenizer) next() (token, bool) {
	s := t.source
	for t.position < len(s) {
		i := t.position
		if !startsMarkup(s, i) {
			end := textEnd(s, i)
			t.position = end
			return token{kind: textToken, text: html.UnescapeString(s[i:end])}, true
		}
		switch c := s[i+1]; {
		case c == '!':
			t.position = declarationEnd(s, i, false)
		case c == '?':
			t.position = bogusCommentEnd(s, i+2)
		case c == '/':
			switch {
			case s[i+2] == '>': // "</>" is nothing
				t.position = i + 3
			case !isLetter(s[i+2]): // "</1>" is a bogus comment
				t.position = bogusCommentEnd(s, i+2)
			default:
				tag, next, ok := readTag(s, i+2)
				t.position = next
				if ok {
					tag.kind = endToken
					return tag, true
				}
			}
		default:
			tag, next, ok := readTag(s, i+1)
			t.position = next
			if !ok {
				continue // the input ended inside the tag: the tag is lost
			}
			tag.kind = startToken
			if tag, ok := t.afterStart(tag); ok {
				return tag, true
			}
		}
	}
	return token{}, false
}

// afterStart skips what a start tag opens when that is not HTML to read: the text of a raw text
// element, the rest of the input after plaintext, an svg or math subtree. It returns the token the
// tree builder reads next, if any.
func (t *tokenizer) afterStart(tag token) (token, bool) {
	s := t.source
	switch {
	case tag.name == "plaintext":
		t.position = len(s)
		tag.raw = true
	case tag.name == "script":
		t.position = endTagEnd(s, scriptEnd(s, t.position))
		tag.raw = true
	case rawText[tag.name]:
		t.position = endTagEnd(s, rawTextEnd(s, t.position, tag.name))
		tag.raw = true
	case tag.name == "svg" || tag.name == "math":
		if tag.selfClosing {
			return token{}, false
		}
		return t.skipForeign(tag.name)
	}
	return tag, true
}

// startsMarkup reports whether the "<" at i opens markup rather than being text: a letter, "!", "?",
// or "/" with something after it follows.
func startsMarkup(s string, i int) bool {
	if s[i] != '<' || i+1 >= len(s) {
		return false
	}
	c := s[i+1]
	return isLetter(c) || c == '!' || c == '?' || c == '/' && i+2 < len(s)
}

// textEnd is where the text starting at i ends: at the next "<" that opens markup.
func textEnd(s string, i int) int {
	for k := i; ; k++ {
		j := strings.IndexByte(s[k:], '<')
		if j < 0 {
			return len(s)
		}
		k += j
		if startsMarkup(s, k) {
			return k
		}
	}
}

// hasMarkup reports whether the tokenizer would read anything but text in s.
func hasMarkup(s string) bool {
	for k := 0; ; k++ {
		j := strings.IndexByte(s[k:], '<')
		if j < 0 {
			return false
		}
		k += j
		if startsMarkup(s, k) {
			return true
		}
	}
}

// declarationEnd skips "<!…" at i: a comment to "-->" or "--!>" ("<!-->" and "<!--->" end as they
// begin), a CDATA section to "]]>" inside svg or math, and anything else (a doctype, CDATA in HTML, a
// bogus comment) to the first ">". Without its end, it runs to the end of the input.
func declarationEnd(s string, i int, foreign bool) int {
	rest := s[i+2:]
	switch {
	case strings.HasPrefix(rest, "--"):
		start := i + 4
		body := s[start:]
		if strings.HasPrefix(body, ">") {
			return start + 1
		}
		if strings.HasPrefix(body, "->") {
			return start + 2
		}
		for k := 0; ; {
			j := strings.Index(body[k:], "--")
			if j < 0 {
				return len(s)
			}
			k += j + 2
			for k < len(body) && body[k] == '-' {
				k++
			}
			if k < len(body) && body[k] == '>' {
				return start + k + 1
			}
			if strings.HasPrefix(body[k:], "!>") {
				return start + k + 2
			}
		}
	case foreign && strings.HasPrefix(rest, "[CDATA["):
		if j := strings.Index(rest[7:], "]]>"); j >= 0 {
			return i + 2 + 7 + j + 3
		}
		return len(s)
	}
	return bogusCommentEnd(s, i+2)
}

// bogusCommentEnd is the position after the first ">" from i, or the end of the input.
func bogusCommentEnd(s string, i int) int {
	if j := strings.IndexByte(s[i:], '>'); j >= 0 {
		return i + j + 1
	}
	return len(s)
}

// readTag reads a tag whose name starts at i, up to its ">". false when the input ends inside it.
func readTag(s string, i int) (token, int, bool) {
	var t token
	start := i
	for i < len(s) && !isSpace(s[i]) && s[i] != '/' && s[i] != '>' {
		i++
	}
	t.name = lowerASCII(s[start:i])
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			return t, len(s), false
		}
		switch s[i] {
		case '>':
			return t, i + 1, true
		case '/':
			i++
			if i < len(s) && s[i] == '>' {
				t.selfClosing = true
				return t, i + 1, true
			}
			continue // a "/" anywhere else is nothing
		}
		// an attribute's name: its first character may be "=", the rest runs to a space, "/", ">" or "="
		nameStart := i
		i++
		for i < len(s) && !isSpace(s[i]) && s[i] != '/' && s[i] != '>' && s[i] != '=' {
			i++
		}
		name := s[nameStart:i]
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		value := ""
		if i < len(s) && s[i] == '=' {
			i++
			for i < len(s) && isSpace(s[i]) {
				i++
			}
			if i >= len(s) {
				return t, len(s), false
			}
			switch s[i] {
			case '"', '\'':
				j := strings.IndexByte(s[i+1:], s[i])
				if j < 0 {
					return t, len(s), false
				}
				value = s[i+1 : i+1+j]
				i += j + 2
			case '>': // a missing value: the tag ends here
				t.attribute(name, "")
				return t, i + 1, true
			default:
				valueStart := i
				for i < len(s) && !isSpace(s[i]) && s[i] != '>' {
					i++
				}
				value = s[valueStart:i]
			}
		}
		t.attribute(name, value)
	}
}

// attribute keeps what the tree reads of one attribute; the first of a name wins, as in a browser.
func (t *token) attribute(name, value string) {
	switch {
	case strings.EqualFold(name, "href"):
		if !t.hasHref {
			t.href, t.hasHref = value, true
		}
	case strings.EqualFold(name, "style"):
		if !t.hasStyle {
			t.style, t.hasStyle = value, true
		}
	case strings.EqualFold(name, "class"):
		if !t.hasClass {
			t.class, t.hasClass = value, true
		}
	case strings.EqualFold(name, "align"):
		if !t.hasAlign {
			t.align, t.hasAlign = value, true
		}
	case strings.EqualFold(name, "color"), strings.EqualFold(name, "face"), strings.EqualFold(name, "size"):
		t.fontAttribute = true
	}
}

// endTagEnd is the position after the end tag that starts at i ("</name …>"), or the end of the input.
func endTagEnd(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	if _, next, ok := readTag(s, i+2); ok {
		return next
	}
	return len(s)
}

// rawTextEnd is where the text of a raw text element ends: at "</name" followed by a space, "/", ">"
// or the end of the input.
func rawTextEnd(s string, i int, name string) int {
	for {
		j := strings.Index(s[i:], "</")
		if j < 0 {
			return len(s)
		}
		i += j
		if closesRawText(s, i, name) {
			return i
		}
		i += 2
	}
}

func closesRawText(s string, i int, name string) bool {
	k := i + 2 + len(name)
	return k <= len(s) && strings.HasPrefix(s[i:], "</") && strings.EqualFold(s[i+2:k], name) &&
		(k == len(s) || isSpace(s[k]) || s[k] == '/' || s[k] == '>')
}

// scriptEnd is where the text of a script ends. Inside "<!--" … "-->" a "<script" opens a part in which
// "</script" does not end it (the tokenizer's "escaped" and "double escaped" script states).
func scriptEnd(s string, i int) int {
	escaped, doubled := false, false
	for {
		j := strings.IndexAny(s[i:], "<-")
		if j < 0 {
			return len(s)
		}
		i += j
		if s[i] == '-' {
			if escaped && strings.HasPrefix(s[i:], "-->") {
				escaped, doubled = false, false
				i += 3
				continue
			}
			i++
			continue
		}
		switch {
		case !escaped && strings.HasPrefix(s[i:], "<!--"):
			escaped = true
			i += 4
			for i < len(s) && s[i] == '-' {
				i++
			}
			if i < len(s) && s[i] == '>' { // "<!-->" and "<!--->" end as they begin
				escaped = false
				i++
			}
		case closesRawText(s, i, "script"):
			if !doubled {
				return i
			}
			doubled = false
			i += 8
		case escaped && !doubled && opensScript(s, i):
			doubled = true
			i += 7
		default:
			i++
		}
	}
}

func opensScript(s string, i int) bool {
	k := i + 7
	return k < len(s) && strings.EqualFold(s[i+1:k], "script") && (isSpace(s[k]) || s[k] == '/' || s[k] == '>')
}

// foreignElement is an element open in an svg or math subtree being skipped.
type foreignElement struct {
	name string
	// point: foreignObject, desc, title, mi, mo, mn, ms, mtext or annotation-xml, inside which a browser
	// reads HTML again.
	point bool
}

// integrationPoints are the svg and math elements whose content a browser reads as HTML.
var integrationPoints = map[string]bool{
	"foreignobject": true, "desc": true, "title": true,
	"mi": true, "mo": true, "mn": true, "ms": true, "mtext": true, "annotation-xml": true,
}

// breakout are the start tags that end svg or math content in a browser (outside an integration point):
// the element goes back to HTML.
var breakout = map[string]bool{
	"b": true, "big": true, "blockquote": true, "body": true, "br": true, "center": true, "code": true,
	"dd": true, "div": true, "dl": true, "dt": true, "em": true, "embed": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "head": true, "hr": true, "i": true, "img": true,
	"li": true, "listing": true, "menu": true, "meta": true, "nobr": true, "ol": true, "p": true,
	"pre": true, "ruby": true, "s": true, "small": true, "span": true, "strong": true, "strike": true,
	"sub": true, "sup": true, "table": true, "tt": true, "u": true, "ul": true, "var": true,
}

// skipForeign skips the subtree of an svg or math element whose start tag was just read. A start tag
// that ends foreign content in a browser (breakout), or an end tag of an element the subtree did not
// open, ends it too and is returned for the tree builder. Raw text elements are skipped as raw text
// here as well, and a CDATA section runs to "]]>": both can only skip more than a browser does.
func (t *tokenizer) skipForeign(root string) (token, bool) {
	s := t.source
	open := []foreignElement{{name: root}}
	points, overflow := 0, 0
	for t.position < len(s) {
		j := strings.IndexByte(s[t.position:], '<')
		if j < 0 {
			break
		}
		i := t.position + j
		if !startsMarkup(s, i) {
			t.position = i + 1
			continue
		}
		switch c := s[i+1]; {
		case c == '!':
			t.position = declarationEnd(s, i, true)
		case c == '?':
			t.position = bogusCommentEnd(s, i+2)
		case c == '/' && s[i+2] == '>':
			t.position = i + 3
		case c == '/' && !isLetter(s[i+2]):
			t.position = bogusCommentEnd(s, i+2)
		case c == '/':
			tag, next, ok := readTag(s, i+2)
			t.position = next
			if !ok {
				continue
			}
			tag.kind = endToken
			if overflow > 0 {
				overflow--
				continue
			}
			if points == 0 && (tag.name == "p" || tag.name == "br") {
				return tag, true
			}
			k := len(open) - 1
			for k >= 0 && open[k].name != tag.name {
				k--
			}
			if k < 0 {
				if points == 0 {
					return tag, true // an element around the subtree ends, and the subtree with it
				}
				continue
			}
			for _, e := range open[k:] {
				if e.point {
					points--
				}
			}
			open = open[:k]
			if len(open) == 0 {
				return token{}, false
			}
		default:
			tag, next, ok := readTag(s, i+1)
			t.position = next
			if !ok {
				continue
			}
			tag.kind = startToken
			switch {
			case tag.name == "plaintext":
				t.position = len(s)
			case tag.name == "script":
				t.position = endTagEnd(s, scriptEnd(s, t.position))
			case rawText[tag.name]:
				t.position = endTagEnd(s, rawTextEnd(s, t.position, tag.name))
			case points == 0 && (breakout[tag.name] || tag.name == "font" && tag.fontAttribute):
				return tag, true
			case tag.selfClosing, points > 0 && voidElement(tag.name):
				// nothing opens
			case len(open) >= maximumDepth:
				overflow++
			default:
				point := integrationPoints[tag.name]
				if point {
					points++
				}
				open = append(open, foreignElement{name: tag.name, point: point})
			}
		}
	}
	t.position = len(s)
	return token{}, false
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for k := i; k < len(b); k++ {
				if b[k] >= 'A' && b[k] <= 'Z' {
					b[k] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
