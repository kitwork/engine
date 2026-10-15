package hydrate

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The web output of a Kitwork UI page (KITWORK_UI.md §11) carries its directives in EIGHT grouped
// attributes, one prefix, one way of writing them:
//
//	data-kitwork-component  "name" or "name: $alias" (a version may sit in the name: "theme@1.0.0")
//	data-kitwork-scope      "name: value; …"      starting values, run once, in the scope around the host
//	data-kitwork-bind       "text: x; show: y; aria-expanded: open; title: t"
//	data-kitwork-class      "done: value == 100; icon-sun: resolved == 'dark'"  each name is the class
//	data-kitwork-style      "width: value + '%'; --progress: value"
//	data-kitwork-event      "click.debounce(300): f(); keydown.escape: g(); click-outside: close()"
//	data-kitwork-if         one expression
//	data-kitwork-for        "x of list; key: expr" or "x, i of list; key: expr"
//
// Five of them (scope, bind, class, style, event) are lists of pairs, and ONE reader splits them all
// — this file, and its twin in kernel.js. The two are held to the same answers by
// grouped_corpus.json, which both run: a value the server accepts and the browser reads differently
// would be a directive that verifies here and does nothing there.
//
// The data-kit-* grammar stays readable beside these (hand-written template pages use it); the
// grouped form is what the engine writes.

// GroupedDirectives are the eight attributes, the one list everything that recognises the grouped
// grammar is built from. Every other data-kitwork-* attribute — generated, layout, current, delay,
// transition, shell, highlight, jit — is transport the engine hangs on a page, never a directive, and
// must not make a page look like one: most pages carry data-kitwork-generated.
var GroupedDirectives = []string{
	"data-kitwork-component",
	"data-kitwork-scope",
	"data-kitwork-bind",
	"data-kitwork-class",
	"data-kitwork-style",
	"data-kitwork-event",
	"data-kitwork-if",
	"data-kitwork-for",
}

// IsGroupedDirective reports whether an attribute name is one of the eight.
func IsGroupedDirective(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, directive := range GroupedDirectives {
		if name == directive {
			return true
		}
	}
	return false
}

// Pair is one `name: value` of a grouped attribute. Value is the expression's source, trimmed.
type Pair struct {
	Name, Value string
}

// GroupedError is a grouped value the reader cannot split. Column counts characters from 1, so the
// author can find the place in the attribute.
type GroupedError struct {
	Column int
	Reason string
}

func (e *GroupedError) Error() string {
	return fmt.Sprintf("%s (at character %d)", e.Reason, e.Column)
}

// groupedPart is one top-level ';' part, trimmed, with where it starts in the source.
type groupedPart struct {
	text string
	at   int // byte offset of text in the source
}

// pairAt is a Pair with the byte offsets of its name and value, for the server's reports.
type pairAt struct {
	Pair
	nameAt, valueAt int
}

func groupedErrorAt(src string, offset int, format string, args ...any) error {
	if offset > len(src) {
		offset = len(src)
	}
	return &GroupedError{Column: utf8.RuneCountInString(src[:offset]) + 1, Reason: fmt.Sprintf(format, args...)}
}

// jsSpace is the white space JavaScript's String.prototype.trim and a regular expression's \s take —
// the kernel's — so the twins trim the same characters. strings.TrimSpace would differ at the edges:
// it takes U+0085 and leaves U+FEFF, the other way round from the browser, and a name the server
// trimmed clean would be a name the kernel refuses.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

// trimJS is strings.TrimSpace with the kernel's idea of white space (jsSpace).
func trimJS(s string) string {
	return strings.TrimFunc(s, jsSpace)
}

// jsSpaceClass is jsSpace as a regular expression class: Go's \s is ASCII and has no \v.
const jsSpaceClass = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

// splitTopLevel cuts src at every ';' that is outside a string ('…', "…", `…`, each with backslash
// escapes) and outside ( ) [ ] { }. Empty parts are dropped, so a trailing ';' is fine, as in CSS. A
// string or bracket left open, or a closer with no opener, is an error.
func splitTopLevel(src string) ([]groupedPart, error) {
	var parts []groupedPart
	var stack []int // offsets of the open brackets
	quote, quoteAt := byte(0), 0
	start := 0
	cut := func(end int) {
		raw := src[start:end]
		trimmed := trimJS(raw)
		if trimmed != "" {
			parts = append(parts, groupedPart{text: trimmed, at: start + strings.Index(raw, trimmed)})
		}
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++ // the escaped character, whatever it is
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote, quoteAt = c, i
		case '(', '[', '{':
			stack = append(stack, i)
		case ')', ']', '}':
			if len(stack) == 0 {
				return nil, groupedErrorAt(src, i, "%q closes nothing", string(c))
			}
			if open := src[stack[len(stack)-1]]; closerOf(open) != c {
				return nil, groupedErrorAt(src, i, "%q closes the %q opened at character %d", string(c), string(open), utf8.RuneCountInString(src[:stack[len(stack)-1]])+1)
			}
			stack = stack[:len(stack)-1]
		case ';':
			if len(stack) == 0 {
				cut(i)
				start = i + 1
			}
		}
	}
	if quote != 0 {
		return nil, groupedErrorAt(src, quoteAt, "the string opened with %s is never closed", string(quote))
	}
	if len(stack) > 0 {
		at := stack[len(stack)-1]
		return nil, groupedErrorAt(src, at, "%q is never closed", string(src[at]))
	}
	cut(len(src))
	return parts, nil
}

func closerOf(open byte) byte {
	switch open {
	case '(':
		return ')'
	case '[':
		return ']'
	}
	return '}'
}

// pairNameByte is what a pair's name may be made of: letters, digits, '-', '.', '_', '+' (a key
// combination: keydown.ctrl+k), '/' (a class with an alpha or a glass: background-paper/80,
// material-glass/60) and the parentheses of a modifier's argument (debounce(300)). Never ':' — the
// first one ends the name — and never a quote or a space.
func pairNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '+' || c == '/' || c == '(' || c == ')'
}

// pairNameError says why a name cannot be one, or "" when it can. A modifier's argument is one flat
// pair of parentheses — debounce(300), never nested — so a ':' inside one (debounce(3:00)) is never
// mistaken for the end of the name. (The kernel's twin: /^([\w.+\/-]|\([\w.+\/-]*\))+$/.)
func pairNameError(name string) string {
	if name == "" {
		return "a pair has no name before its ':'"
	}
	open := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !pairNameByte(c) {
			return fmt.Sprintf("%q is not a name: a name is letters, digits, '-', '.', '_', '+', '/' and a modifier's (…)", name)
		}
		switch {
		case c == '(' && open:
			return fmt.Sprintf("%q nests parentheses — a modifier's argument is one flat (…)", name)
		case c == '(':
			open = true
		case c == ')' && !open:
			return fmt.Sprintf("%q closes a parenthesis it never opened", name)
		case c == ')':
			open = false
		}
	}
	if open {
		return fmt.Sprintf("%q leaves a parenthesis open", name)
	}
	return ""
}

// cutPair cuts one part at its FIRST ':' into a name and a value, both trimmed and never empty.
func cutPair(src string, part groupedPart) (pairAt, error) {
	colon := strings.IndexByte(part.text, ':')
	if colon < 0 {
		return pairAt{}, groupedErrorAt(src, part.at, "%q has no ':' between a name and its value", part.text)
	}
	rawName, rawValue := part.text[:colon], part.text[colon+1:]
	name, value := trimJS(rawName), trimJS(rawValue)
	nameAt := part.at + strings.Index(rawName, name)
	if name == "" {
		nameAt = part.at
	}
	if problem := pairNameError(name); problem != "" {
		return pairAt{}, groupedErrorAt(src, nameAt, "%s", problem)
	}
	valueAt := part.at + colon + 1
	if value == "" {
		return pairAt{}, groupedErrorAt(src, valueAt, "%s has no value after its ':'", name)
	}
	valueAt += strings.Index(rawValue, value)
	return pairAt{Pair: Pair{Name: name, Value: value}, nameAt: nameAt, valueAt: valueAt}, nil
}

// Pairs splits the value of a pair attribute — data-kitwork-scope, -bind, -class, -style or -event —
// into its pairs, in order. "" and " ; " are no pairs. The values are expression source, not yet compiled.
func Pairs(src string) ([]Pair, error) {
	located, err := pairsAt(src)
	if err != nil {
		return nil, err
	}
	out := make([]Pair, len(located))
	for i, p := range located {
		out[i] = p.Pair
	}
	return out, nil
}

func pairsAt(src string) ([]pairAt, error) {
	parts, err := splitTopLevel(src)
	if err != nil {
		return nil, err
	}
	out := make([]pairAt, 0, len(parts))
	for _, part := range parts {
		p, err := cutPair(src, part)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// componentAliasRe is the alias the kernel registers (kernel.js registerAlias).
var componentAliasRe = regexp.MustCompile(`^\$[A-Za-z][A-Za-z0-9_]*$`)

// componentNameByte is what a component's name may be made of, its version included (theme@1.0.0).
func componentNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '@'
}

// ComponentValue reads data-kitwork-component: "name" or "name: $alias". A version stays in the name
// ("theme@1.0.0: $theme" → "theme@1.0.0", "$theme"); the alias is "" when there is none.
func ComponentValue(src string) (name, alias string, err error) {
	trimmed := trimJS(src)
	if trimmed == "" {
		return "", "", groupedErrorAt(src, 0, "names no component")
	}
	at := strings.Index(src, trimmed)
	name = trimmed
	if colon := strings.IndexByte(trimmed, ':'); colon >= 0 {
		name = trimJS(trimmed[:colon])
		alias = trimJS(trimmed[colon+1:])
		if alias == "" {
			return "", "", groupedErrorAt(src, at+colon+1, "names no alias after its ':' (write \"%s: $alias\", or leave the ':' out)", name)
		}
		if !componentAliasRe.MatchString(alias) {
			return "", "", groupedErrorAt(src, at+colon+1+strings.Index(trimmed[colon+1:], alias), "%q is not an alias: an alias starts with $ and is a name ($account)", alias)
		}
	}
	if name == "" {
		return "", "", groupedErrorAt(src, at, "names no component before its ':'")
	}
	for i := 0; i < len(name); i++ {
		if !componentNameByte(name[i]) {
			return "", "", groupedErrorAt(src, at+i, "%q is not a component name (letters, digits, '-', and an @version)", name)
		}
	}
	return name, alias, nil
}

// forHeadRe is the head of a for: `item of list` or `item, index of list` — the kernel's own reading
// (kernel.js FOR_HEAD), its \s spelled out as the browser's (jsSpaceClass).
// ((?s:.) is the kernel's [\s\S]: any character, a line break included.)
var forHeadRe = regexp.MustCompile(strings.ReplaceAll(
	`^\s*([$A-Za-z_][\w$]*)\s*(?:,\s*([$A-Za-z_][\w$]*)\s*)?\s+of\s+((?s:.)+)$`, `\s`, jsSpaceClass))

// ForValue reads data-kitwork-for: "item of list", "item, index of list", each optionally followed by
// "; key: expr" — the row's identity, so a moved row keeps its element. key is the only pair a for
// takes, once.
func ForValue(src string) (item, index, list, key string, err error) {
	parts, err := splitTopLevel(src)
	if err != nil {
		return "", "", "", "", err
	}
	if len(parts) == 0 {
		return "", "", "", "", groupedErrorAt(src, 0, "is empty — write \"item of list\"")
	}
	head := forHeadRe.FindStringSubmatch(parts[0].text)
	if head == nil {
		return "", "", "", "", groupedErrorAt(src, parts[0].at, "%q is not \"item of list\" or \"item, index of list\"", parts[0].text)
	}
	item, index, list = head[1], head[2], trimJS(head[3])
	for _, part := range parts[1:] {
		p, err := cutPair(src, part)
		if err != nil {
			return "", "", "", "", err
		}
		if p.Name != "key" {
			return "", "", "", "", groupedErrorAt(src, p.nameAt, "a for takes only key after the list, not %s", p.Name)
		}
		if key != "" {
			return "", "", "", "", groupedErrorAt(src, p.nameAt, "key is given twice")
		}
		key = p.Value
	}
	return item, index, list, key, nil
}

// forValueAt is ForValue with where the list and the key start, for the server's reports.
func forValueAt(src string) (item, index, list, key string, listAt, keyAt int, err error) {
	item, index, list, key, err = ForValue(src)
	if err != nil {
		return
	}
	parts, _ := splitTopLevel(src)
	head := parts[0]
	listAt = head.at + strings.LastIndex(head.text, list)
	for _, part := range parts[1:] {
		if p, perr := cutPair(src, part); perr == nil && p.Name == "key" {
			keyAt = p.valueAt
		}
	}
	return
}

// groupedClassRe finds a data-kitwork-class attribute; its value is read by Pairs.
var groupedClassRe = regexp.MustCompile(`(?:^|\s)data-kitwork-class="([^"]*)"`)

// ToggledClasses returns every class a page toggles through data-kitwork-class="<name>: condition; …"
// — the KEYS, in document order, without repeats. Each key is the class itself, written out in full,
// so every engine that emits a rule for a class it sees — CSS, icons, logos, fonts, material — emits
// one for this class too: the kernel adds it later, and it would otherwise have none. A value that
// does not split is skipped: Check names it. (A class.<name> pair of data-kitwork-bind is not read:
// that form is gone, and the kernel no longer toggles anything for it.)
func ToggledClasses(html string) []string {
	if !strings.Contains(html, "data-kitwork-class") {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range groupedClassRe.FindAllStringSubmatch(html, -1) {
		pairs, err := Pairs(authoredAttribute(m[1]))
		if err != nil {
			continue
		}
		for _, p := range pairs {
			if !seen[p.Name] {
				seen[p.Name] = true
				out = append(out, p.Name)
			}
		}
	}
	return out
}
