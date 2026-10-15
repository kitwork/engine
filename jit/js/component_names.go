package js

import (
	"crypto/sha256"
	"regexp"
	"sort"
	"strings"
	"sync"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

// What a component's scope holds, read off its source — so the server can tell an author, at render,
// that `data-kit-click="close()"` inside a sidebar calls a method the sidebar does not have.
//
// The kernel seeds a component's scope with its blueprint's own keys (state, methods, getters) and
// adds a key whenever the component's code assigns one (`this.hideTimer = …` writes to the nearest
// scope that does not have it, which is the component's). So the names are: the keys of the object
// literal passed to kit.component, plus every property the source assigns. The second set is wider
// than the truth — `el.hidden = …` adds "hidden" — which is the safe direction: it can only silence a
// report, never invent one.
//
// Like the pass it feeds, it must never cry wolf. Anything it cannot read for certain answers
// "unknowable", and the page's check treats the component as it always did, as opaque:
//
//   - no kit.component call, or one whose blueprint is neither an object literal nor a variable
//     holding one in the same file;
//   - a key the reader cannot name: `...spread`, `[computed]`;
//   - a key assigned by computation: `this[name] = …`, Object.assign(this, …), defineProperty — but
//     not a Symbol constant of the file, `scope[TREE] = …`, which is a private slot no markup reads;
//   - JavaScript the scanner does not follow (an unclosed string, comment or bracket).

// ComponentNames is hydrate.ComponentNames for one site: its own _components, and the catalogue.
// It takes a component as either form writes it — "dialog", "dialog@v2.0.0" (a template page may
// still name a version), or a data-kitwork-component value with its alias, "theme: $theme" — and
// answers for the component, never the alias.
func ComponentNames(site Site) hydrate.ComponentNames {
	return func(component string) ([]string, bool) {
		if strings.Contains(component, ":") {
			named, _, err := hydrate.ComponentValue(component)
			if err != nil {
				return nil, false
			}
			component = named
		}
		name, version, _ := strings.Cut(strings.TrimSpace(component), "@")
		if !componentNameRe.MatchString(name) {
			return nil, false
		}
		key := name
		if version != "" {
			key = name + "@v" + strings.TrimPrefix(version, "v")
		}
		source := componentSource(key, site)
		if source == "" {
			return nil, false
		}
		return declaredNamesOf(source)
	}
}

type namesResult struct {
	names []string
	ok    bool
}

var namesCache sync.Map // sha256 of a source → namesResult

func declaredNamesOf(source string) ([]string, bool) {
	sum := sha256.Sum256([]byte(source))
	if cached, ok := namesCache.Load(sum); ok {
		r := cached.(namesResult)
		return r.names, r.ok
	}
	names, ok := scanDeclaredNames(source)
	namesCache.Store(sum, namesResult{names, ok})
	return names, ok
}

var (
	registrationRe     = regexp.MustCompile(`\bkit\.component\(\s*(?:"[^"]*"|'[^']*')\s*,\s*`)
	assignedPropertyRe = regexp.MustCompile(`\.\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:=[^=>]|\+=|-=|\*=|/=|\|\|=|&&=|\?\?=|\+\+|--)`)
	computedWriteRe    = regexp.MustCompile(`\b(?:this|self|scope)\s*\[([^\]]*)\]\s*=[^=]`)
	assignObjectRe     = regexp.MustCompile(`Object\.(?:assign|defineProperty|defineProperties)\(\s*(?:this|self|scope)\b`)
	symbolConstantRe   = regexp.MustCompile(`\b(?:var|let|const)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*Symbol\(`)
	identifierAtRe     = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*`)
)

// scanDeclaredNames is the reader itself: the keys of every blueprint the source registers, and every
// property it assigns. ok is false when any part of that is not certain.
func scanDeclaredNames(source string) ([]string, bool) {
	code, ok := codeOnly(source)
	if !ok {
		return nil, false
	}
	if computedKeyWrite(code) || assignObjectRe.MatchString(code) {
		return nil, false
	}
	registrations := registrationRe.FindAllStringIndex(code, -1)
	if len(registrations) == 0 {
		return nil, false
	}
	names := map[string]bool{}
	for _, at := range registrations {
		blueprint := at[1]
		if blueprint < len(code) && code[blueprint] != '{' {
			variable := identifierAtRe.FindString(code[blueprint:])
			if variable == "" {
				return nil, false
			}
			declaration := regexp.MustCompile(`\b(?:var|let|const)\s+` + regexp.QuoteMeta(variable) + `\s*=\s*\{`).FindStringIndex(code)
			if declaration == nil {
				return nil, false
			}
			blueprint = declaration[1] - 1
		}
		keys, ok := objectKeys(code, blueprint)
		if !ok {
			return nil, false
		}
		for _, key := range keys {
			names[key] = true
		}
	}
	for _, match := range assignedPropertyRe.FindAllStringSubmatch(code, -1) {
		names[match[1]] = true
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, true
}

// computedKeyWrite reports a write whose key is computed — `this[key] = …` — which could add any name.
// A key that is a Symbol constant of the file is not one: it is the component's private slot
// (`scope[TREE] = …`), and markup can only ever read a name, never a Symbol.
func computedKeyWrite(code string) bool {
	symbols := map[string]bool{}
	for _, match := range symbolConstantRe.FindAllStringSubmatch(code, -1) {
		symbols[match[1]] = true
	}
	for _, match := range computedWriteRe.FindAllStringSubmatch(code, -1) {
		if !symbols[strings.TrimSpace(match[1])] {
			return true
		}
	}
	return false
}

// objectKeys reads the top-level keys of the object literal whose '{' is at open: `name: …`,
// `name() {…}`, `get name() {…}`, `async name() {…}`, `"name": …`.
func objectKeys(code string, open int) ([]string, bool) {
	if open >= len(code) || code[open] != '{' {
		return nil, false
	}
	var keys []string
	i := open + 1
	for {
		i = skipSpace(code, i)
		if i >= len(code) {
			return nil, false
		}
		if code[i] == '}' {
			return keys, true
		}
		if strings.HasPrefix(code[i:], "...") || code[i] == '[' {
			return nil, false
		}
		var key string
		switch code[i] {
		case '"', '\'':
			end := strings.IndexByte(code[i+1:], code[i])
			if end < 0 {
				return nil, false
			}
			key = code[i+1 : i+1+end]
			i += end + 2
		default:
			key = identifierAtRe.FindString(code[i:])
			if key == "" {
				return nil, false
			}
			i += len(key)
			// get name() / set name() / async name(): the word before is a modifier, the next is the key.
			if key == "get" || key == "set" || key == "async" {
				j := skipSpace(code, i)
				if next := identifierAtRe.FindString(code[j:]); next != "" {
					key = next
					i = j + len(next)
				}
			}
		}
		i = skipSpace(code, i)
		if i >= len(code) || (code[i] != ':' && code[i] != '(' && code[i] != ',' && code[i] != '}') {
			return nil, false
		}
		keys = append(keys, key)
		// Skip the value (or the method's parameters and body) to the comma that ends this member.
		end, ok := skipMember(code, i)
		if !ok {
			return nil, false
		}
		i = end
		if i < len(code) && code[i] == ',' {
			i++
		}
	}
}

// skipMember moves past one member's value to the ',' or '}' that ends it at this depth.
func skipMember(code string, i int) (int, bool) {
	depth := 0
	for i < len(code) {
		switch c := code[i]; c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return i, c == '}'
			}
			depth--
		case ',':
			if depth == 0 {
				return i, true
			}
		}
		i++
	}
	return i, false
}

func skipSpace(code string, i int) int {
	for i < len(code) && (code[i] == ' ' || code[i] == '\t' || code[i] == '\n' || code[i] == '\r') {
		i++
	}
	return i
}

// codeOnly blanks comments, string and template contents and regular expressions, keeping every
// other byte where it was — so the scans above see brackets, keys and assignments only where the
// code has them. A key written as a string keeps its text: a string that is followed by ':' is left
// as it is. ok is false for anything left open.
func codeOnly(source string) (string, bool) {
	out := []byte(source)
	blank := func(from, to int) {
		for k := from; k < to; k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
	}
	previous := byte('(') // the last significant character, to tell a regex from a division
	i := 0
	for i < len(source) {
		c := source[i]
		switch {
		case c == '/' && i+1 < len(source) && source[i+1] == '/':
			end := strings.IndexByte(source[i:], '\n')
			if end < 0 {
				end = len(source) - i
			}
			blank(i, i+end)
			i += end
		case c == '/' && i+1 < len(source) && source[i+1] == '*':
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return "", false
			}
			blank(i, i+end+4)
			i += end + 4
		case c == '"' || c == '\'' || c == '`':
			end, ok := stringEnd(source, i)
			if !ok {
				return "", false
			}
			// A quoted object key ("name": …) keeps its text; any other string is blanked.
			after := skipSpace(source, end)
			if c == '`' || after >= len(source) || source[after] != ':' {
				blank(i+1, end-1)
			}
			previous = 'a'
			i = end
		case c == '/' && regexCanStart(previous, source[:i]):
			end, ok := regexEnd(source, i)
			if !ok {
				return "", false
			}
			blank(i+1, end)
			previous = 'a'
			i = end
		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				previous = c
			}
			i++
		}
	}
	return string(out), true
}

// stringEnd returns the index just past the string or template that opens at i. A template's
// ${…} may hold code with its own strings; the template is blanked whole, so only its end matters.
func stringEnd(source string, i int) (int, bool) {
	quote := source[i]
	depth := 0
	for j := i + 1; j < len(source); j++ {
		switch source[j] {
		case '\\':
			j++
		case '\n':
			if quote != '`' {
				return 0, false
			}
		case '$':
			if quote == '`' && j+1 < len(source) && source[j+1] == '{' {
				depth++
				j++
			}
		case '}':
			if quote == '`' && depth > 0 {
				depth--
			}
		case quote:
			if depth == 0 {
				return j + 1, true
			}
		}
	}
	return 0, false
}

// regexCanStart: a '/' after one of these, or after a keyword that takes an expression, begins a
// regular expression; anywhere else it divides.
func regexCanStart(previous byte, before string) bool {
	if strings.IndexByte("(,=:[!&|?{};+-*%<>~^", previous) >= 0 {
		return true
	}
	trimmed := strings.TrimRight(before, " \t\r\n")
	for _, word := range []string{"return", "typeof", "case", "in", "of", "new", "delete", "void", "throw"} {
		if strings.HasSuffix(trimmed, word) {
			at := len(trimmed) - len(word)
			if at == 0 || !isIdentifierByte(trimmed[at-1]) {
				return true
			}
		}
	}
	return false
}

func regexEnd(source string, i int) (int, bool) {
	inClass := false
	for j := i + 1; j < len(source); j++ {
		switch source[j] {
		case '\\':
			j++
		case '\n':
			return 0, false
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				k := j + 1
				for k < len(source) && isIdentifierByte(source[k]) {
					k++ // flags
				}
				return k, true
			}
		}
	}
	return 0, false
}

func isIdentifierByte(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
