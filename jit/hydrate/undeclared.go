package hydrate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A name nothing declares is the most expensive mistake an authored page can make.
//
// The kernel reads it as 0 and carries on — correct at runtime, since a half-built page must not
// explode — so `data-kit-show="dark"` against a boundary holding `darkMode` simply never shows.
// No error, no clue, and the markup looks right. Measured across this repo, the boundary that owns
// a name sits a median of 87 lines away from the expression that reads it, and at the 90th
// percentile a thousand lines away: nobody can hold that in their head while editing.
//
// The server can say it, because it has both the expression and the tree. This pass reports a read
// of a name no boundary above it declares — at render, next to the markup, by name.
//
// IT MUST NEVER CRY WOLF. A false warning would teach authors to ignore the channel that carries
// the real ones, so every uncertainty ends the check instead of guessing:
//
//   - a component boundary anywhere in the chain → skip. Its state lives in JavaScript the server
//     cannot read, so any name might be legitimate.
//   - a capability boundary (data-kit-api / -live / -remember) → skip, for the same reason: those
//     write names into the boundary at runtime.
//   - a page that writes its own page scope from a script (kit.set, kit.scope.x = …) → skip the
//     page scope entirely, since a name can then arrive from somewhere the markup does not show.
//     Every other page HAS a readable page scope: a data-kit-model or data-kit-seed written outside
//     any boundary declares a name there, and a nested boundary reads it by falling through, which
//     is exactly what the kernel does.
//   - markup the scanner cannot read confidently → stop the whole pass.
//
// What remains is the case that is actually knowable: a plain data-kit-scope boundary, whose names
// are all written down in the markup itself.

// reservedReadNames are always available inside an expression, so they are never undeclared.
// It holds no $-names: the scanner skips those before it looks here (a $alias belongs to the page,
// and the scanner cannot know a page's aliases), so a $-name in this map would be unreachable.
var reservedReadNames = map[string]bool{
	"kit": true,
	// the expression globals (globals.go) — present on every page, declared by no scope
	"Math": true, "Date": true, "JSON": true, "Number": true, "String": true, "Boolean": true,
	"Array": true, "Object": true, "parseInt": true, "parseFloat": true,
	"encodeURIComponent": true, "decodeURIComponent": true, "isNaN": true, "isFinite": true,
	// the row overlay data-kit-for adds beside the author's own item/index names
	"count": true, "first": true, "last": true, "even": true, "odd": true,
}

var (
	// scopeLiteralKeyRe reads the top-level keys of a scope literal: `count: 0, open: true`.
	scopeLiteralKeyRe = regexp.MustCompile(`(?:^|[,{])\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*:`)
	// forNamesRe reads the names data-kit-for binds: `item, index of items` or `item of items`.
	forNamesRe = regexp.MustCompile(`^\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:,\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*)?\bof\b`)
	// expressionAttrRe finds the attributes whose value reads scope names.
	expressionAttrRe = regexp.MustCompile(`^data-kit-(?:text|show|if|class|error|bind:[a-z][a-z0-9-]*|style:[-a-zA-Z0-9_]+|click|dblclick|submit|input|change|keydown|keyup|pointerdown|pointerup|focusin|focusout)(?::[a-z0-9+]+(?:\([0-9]+\))?)*$`)
)

// boundary is one data-kit-scope element while the scan is inside it.
type boundary struct {
	tag      string
	depth    int
	declared map[string]bool
	opaque   bool      // a component or capability boundary: its names are not knowable here
	parent   *boundary // reads fall through to it, exactly as the kernel's scope chain does
}

// undeclaredRead is one reported read, kept until the whole document is scanned because a name may
// be declared by a data-kit-model or data-kit-seed further down inside the same boundary.
type undeclaredRead struct {
	name      string
	attribute string
	tag       string
	boundary  *boundary
}

// wholeDocument says whether a piece of HTML can be judged at all. A page is assembled from a shell
// and its parts, and a fragment — the body of a page slotted into a shell, a header, a Drive
// response — is missing everything above it: every boundary, and every data-kit-element declared
// in a sibling slot. Nearly every name in it would look undeclared. Render receives the assembled
// page, so this costs nothing there and keeps every check honest everywhere else.
func wholeDocument(html string) bool {
	return strings.Contains(html, "<body") || strings.Contains(html, "<html")
}

// undeclaredWarnings is the pass itself, kept apart from printing so a test can read exactly what
// an author would be told.
func undeclaredWarnings(html string) []string {
	reads, ok := scanUndeclaredNames(html)
	if !ok {
		return nil
	}
	var lines []string
	for _, read := range reads {
		if declared, knowable := chainDeclares(read.boundary, read.name); declared || !knowable {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s reads %q, which no scope above it declares — a name nothing declares reads as 0, so this expression quietly does nothing. In %s",
			read.attribute, read.name, read.tag))
	}
	return lines
}

// chainDeclares answers the question the kernel answers at runtime: walking out from this boundary,
// does anything declare the name? knowable is false as soon as an opaque boundary is met, because
// beyond it the answer lives in JavaScript.
func chainDeclares(from *boundary, name string) (declared bool, knowable bool) {
	for b := from; b != nil; b = b.parent {
		if b.opaque {
			return false, false
		}
		if b.declared[name] {
			return true, true
		}
	}
	return false, true
}

// scanUndeclaredNames walks the markup and returns the reads worth reporting. ok is false when the
// scanner met something it could not read confidently, in which case the caller reports nothing.
func scanUndeclaredNames(html string) (reads []undeclaredRead, ok bool) {
	if !wholeDocument(html) {
		return nil, false
	}
	// The page scope is a boundary too — the outermost one. It is knowable from the markup unless
	// the page writes it from a script, which nothing in the tree would show.
	page := &boundary{tag: "page", depth: -1, declared: map[string]bool{}, opaque: pageScopeIsWritten(html)}
	stack := []*boundary{page}
	depth := 0
	index := 0
	for index < len(html) {
		next := strings.IndexByte(html[index:], '<')
		if next < 0 {
			break
		}
		index += next
		if strings.HasPrefix(html[index:], "<!--") {
			end := strings.Index(html[index:], "-->")
			if end < 0 {
				return nil, false
			}
			index += end + 3
			continue
		}
		if strings.HasPrefix(html[index:], "<!") || strings.HasPrefix(html[index:], "<?") {
			end := strings.IndexByte(html[index:], '>')
			if end < 0 {
				return nil, false
			}
			index += end + 1
			continue
		}
		if strings.HasPrefix(html[index:], "</") {
			end := strings.IndexByte(html[index:], '>')
			if end < 0 {
				return nil, false
			}
			name := strings.ToLower(strings.TrimSpace(html[index+2 : index+end]))
			index += end + 1
			if name == "" {
				continue
			}
			depth--
			for len(stack) > 1 && stack[len(stack)-1].depth > depth {
				stack = stack[:len(stack)-1]
			}
			continue
		}

		tagEnd := tagEndIndex(html, index)
		if tagEnd < 0 {
			return nil, false
		}
		tag := html[index : tagEnd+1]
		name := tagName(tag)
		if name == "" {
			return nil, false
		}
		selfClosing := strings.HasSuffix(strings.TrimSpace(tag[:len(tag)-1]), "/") || voidElements[name]

		current := innermost(stack)
		opened := readBoundary(tag, depth)
		if opened != nil {
			opened.parent = current
			current = opened
		}
		collectTagNames(tag, current, &reads)

		if opened != nil && !selfClosing {
			stack = append(stack, opened)
		}
		if !selfClosing {
			depth++
		}
		index = tagEnd + 1

		// Raw-text elements hold no markup: skip to the close so their content cannot confuse the
		// tag scan.
		if rawTextElements[name] && !selfClosing {
			closing := "</" + name
			at := strings.Index(strings.ToLower(html[index:]), closing)
			if at < 0 {
				return nil, false
			}
			index += at
		}
	}
	return reads, true
}

// innermost is the boundary an expression resolves against; the page scope sits at the bottom of
// the stack, so there is always one.
func innermost(stack []*boundary) *boundary {
	return stack[len(stack)-1]
}

// pageScopeIsWritten reports whether the page pokes its own page scope from a script, which makes
// every page-scope name unknowable from the markup alone.
func pageScopeIsWritten(html string) bool {
	return strings.Contains(html, "kit.set(") || strings.Contains(html, "kit.scope.") ||
		strings.Contains(html, "kitwork.set(") || strings.Contains(html, "kitwork.scope.")
}

// readBoundary returns the boundary a tag opens, or nil when it opens none. A component or
// capability host opens an OPAQUE boundary: the scan keeps walking so nesting stays right, but
// nothing inside it is ever reported.
func readBoundary(tag string, depth int) *boundary {
	scope, hasScope := attributeValue(tag, "data-kit-scope")
	opaque := false
	for _, marker := range []string{"data-kit-component", "data-kit-api", "data-kit-live", "data-kit-remember"} {
		if _, has := attributeValue(tag, marker); has {
			opaque = true
		}
	}
	if !hasScope && !opaque {
		return nil
	}
	declared := map[string]bool{}
	if hasScope {
		for _, match := range scopeLiteralKeyRe.FindAllStringSubmatch("{"+authoredAttribute(scope)+"}", -1) {
			declared[match[1]] = true
		}
	}
	return &boundary{tag: tagName(tag), depth: depth, declared: declared, opaque: opaque}
}

// collectTagNames records what this tag declares into its boundary, and what it reads for checking.
func collectTagNames(tag string, current *boundary, reads *[]undeclaredRead) {
	if current == nil {
		return
	}
	// data-kit-model and data-kit-seed WRITE a name into the nearest boundary, so they declare it
	// for the whole boundary — including expressions written above them.
	if value, has := attributeValue(tag, "data-kit-model"); has {
		if name := leadingName(authoredAttribute(value)); name != "" {
			current.declared[name] = true
		}
	}
	// data-kit-for binds its own names for the subtree. Attributing them to the boundary is wider
	// than the truth, which is the safe direction: it can only silence a warning, never invent one.
	if value, has := attributeValue(tag, "data-kit-for"); has {
		if match := forNamesRe.FindStringSubmatch(authoredAttribute(value)); match != nil {
			current.declared[match[1]] = true
			if match[2] != "" {
				current.declared[match[2]] = true
			}
		}
	}
	for _, attribute := range attributeNames(tag) {
		if strings.HasPrefix(attribute, "data-kit-seed") {
			if value, has := attributeValue(tag, attribute); has {
				if name := leadingName(authoredAttribute(value)); name != "" {
					current.declared[name] = true
				}
			}
			continue
		}
		if !expressionAttrRe.MatchString(attribute) {
			continue
		}
		value, has := attributeValue(tag, attribute)
		if !has || strings.TrimSpace(value) == "" {
			continue
		}
		names, err := ReadNamesAttribute(value)
		if err != nil {
			continue // a malformed expression is already reported by the verify pass
		}
		for _, name := range names {
			if reservedReadNames[name] || strings.HasPrefix(name, "$") {
				continue
			}
			*reads = append(*reads, undeclaredRead{name: name, attribute: attribute, tag: shortTag(tag), boundary: current})
		}
	}
}

// leadingName is the first identifier of a state target: `user.email` → user, `rows[]` → rows.
func leadingName(target string) string {
	target = strings.TrimSpace(target)
	end := 0
	for end < len(target) {
		c := target[end]
		if c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (end > 0 && c >= '0' && c <= '9') {
			end++
			continue
		}
		break
	}
	return target[:end]
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true,
}

var rawTextElements = map[string]bool{"script": true, "style": true, "textarea": true}

// tagEndIndex is the index of the '>' that closes the tag starting at start, respecting quoted
// attribute values. -1 when the tag never closes.
func tagEndIndex(html string, start int) int {
	quote := byte(0)
	for i := start; i < len(html); i++ {
		c := html[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i
		case '<':
			if i != start {
				return -1 // an unclosed tag: stop rather than guess
			}
		}
	}
	return -1
}

func tagName(tag string) string {
	end := 1
	for end < len(tag) {
		c := tag[end]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '>' || c == '/' {
			break
		}
		end++
	}
	return strings.ToLower(tag[1:end])
}

var attributeNameRe = regexp.MustCompile(`(?:^|\s)([A-Za-z_:][-A-Za-z0-9_:.+]*)\s*=\s*"`)

func attributeNames(tag string) []string {
	var out []string
	for _, match := range attributeNameRe.FindAllStringSubmatch(tag, -1) {
		out = append(out, strings.ToLower(match[1]))
	}
	sort.Strings(out)
	return out
}

// attributeValue reads one double-quoted attribute from a tag.
func attributeValue(tag, name string) (string, bool) {
	lower := strings.ToLower(tag)
	search := 0
	for {
		at := strings.Index(lower[search:], name+"=\"")
		if at < 0 {
			return "", false
		}
		at += search
		if at > 0 {
			previous := lower[at-1]
			if previous != ' ' && previous != '\t' && previous != '\n' && previous != '\r' {
				search = at + len(name)
				continue
			}
		}
		start := at + len(name) + 2
		end := strings.IndexByte(tag[start:], '"')
		if end < 0 {
			return "", false
		}
		return tag[start : start+end], true
	}
}

// shortTag is the opening tag trimmed to something a person can read in a log line.
func shortTag(tag string) string {
	tag = strings.Join(strings.Fields(tag), " ")
	if len(tag) > 120 {
		return tag[:117] + "…"
	}
	return tag
}
