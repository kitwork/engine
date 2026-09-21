package hydrate

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// data-kit-seed on the server (chốt 21/09): the client reads a seed once at boot, DOM → state; the
// server reads the very same attributes here so the page-scope it bakes text/show/bind from is the
// state the client will compute. Same three forms — data-kit-seed="key" (the element's text, or the
// JSON of a <script type="application/json">), data-kit-seed:<name>="key" (property/attribute by the
// bind groups in reverse), a dotted path, list[] in document order — and the same restraint: page
// scope only; a seed inside a local boundary is the client's, and PreRender never bakes a value it
// might get wrong. Attribute values stay strings (a numeric input is a number, a boolean property a
// boolean), and a seed overrides a model's value for the same key, as it does on the client.

// seedTagRe matches an OPEN TAG carrying data-kit-seed or data-kit-seed:<name>.
var seedTagRe = regexp.MustCompile(`(?i)<([a-z][a-z0-9-]*)\b[^>]*\bdata-kit-seed(?::[a-z][a-z0-9-]*)?="[^"]*"[^>]*>`)

// seedAttrRe finds every seed attribute inside one tag.
var seedAttrRe = regexp.MustCompile(`(?i)\bdata-kit-seed(?::([a-z][a-z0-9-]*))?="([^"]*)"`)

// seedTargetRe is the state target: key, dotted path, list[].
var seedTargetRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)((?:\.[A-Za-z_][A-Za-z0-9_]*)*)(\[\])?$`)

var seedReflectedBoolean = map[string]bool{"disabled": true, "required": true, "readonly": true, "multiple": true, "hidden": true, "open": true}
var seedLiveBoolean = map[string]bool{"checked": true, "selected": true, "indeterminate": true}

// seedScopeOutside fills scope from the seeds that sit outside every local boundary.
func seedScopeOutside(htmlStr string, opaque []sourceRange, scope map[string]any) {
	lists := map[string]bool{}
	searchOffset := 0
	for searchOffset < len(htmlStr) {
		match := seedTagRe.FindStringSubmatchIndex(htmlStr[searchOffset:])
		if match == nil {
			break
		}
		for i := range match {
			if match[i] >= 0 {
				match[i] += searchOffset
			}
		}
		tagStart, tagEnd := match[0], match[1]
		searchOffset = tagEnd
		tag := htmlStr[tagStart:tagEnd]
		name := strings.ToLower(htmlStr[match[2]:match[3]])
		// Inside a boundary the seed belongs to the client. A <script> owns an opaque range of its
		// own (raw text), which is the one opaque range a seed may sit at the start of.
		if r := rangeContaining(tagStart, opaque); r != nil && !(r.start == tagStart && name == "script") {
			continue
		}
		for _, am := range seedAttrRe.FindAllStringSubmatch(tag, -1) {
			source := strings.ToLower(am[1])
			target := strings.TrimSpace(authoredAttribute(am[2]))
			tm := seedTargetRe.FindStringSubmatch(target)
			if tm == nil {
				continue
			}
			path := strings.Split(tm[1]+tm[2], ".")
			if blockedSeedPath(path) {
				continue
			}
			value, ok := seedValue(htmlStr, tag, tagEnd, name, source)
			if !ok {
				continue
			}
			if tm[3] != "" {
				key := strings.Join(path, ".")
				if !lists[key] {
					lists[key] = true
					seedAssign(scope, path, []any{})
				}
				current, _ := seedRead(scope, path).([]any)
				seedAssign(scope, path, append(current, value))
				continue
			}
			seedAssign(scope, path, value)
		}
	}
}

// seedTargetError says why a data-kit-seed value is not a state target: the same rule the kernel
// and the component runtime's scanner apply — a key, a dotted path, or list[], no blocked names.
func seedTargetError(target string) error {
	tm := seedTargetRe.FindStringSubmatch(target)
	if tm == nil {
		return fmt.Errorf("data-kit-seed target %q must be a key, a dotted path, or list[]", target)
	}
	if blockedSeedPath(strings.Split(tm[1]+tm[2], ".")) {
		return fmt.Errorf("data-kit-seed target %q uses a blocked name", target)
	}
	return nil
}

func rangeContaining(pos int, ranges []sourceRange) *sourceRange {
	for i := range ranges {
		if ranges[i].start <= pos && pos < ranges[i].end {
			return &ranges[i]
		}
	}
	return nil
}

func blockedSeedPath(path []string) bool {
	for _, segment := range path {
		if blockedKey(segment) {
			return true
		}
	}
	return false
}

// seedValue reads what the client's readSeed would read for this tag: its text (or JSON) when the
// source is empty, else the named property/attribute. ok is false when the server cannot know the
// value — a non-leaf element's text, malformed JSON — and the seed is left to the client.
func seedValue(htmlStr, tag string, tagEnd int, name, source string) (any, bool) {
	if source == "" {
		if name == "script" {
			if !strings.EqualFold(tagAttribute(tag, "type"), "application/json") {
				return nil, false
			}
			closeStart := indexRawTextClose(htmlStr, tagEnd, "script")
			if closeStart < 0 {
				return nil, false
			}
			var value any
			if err := json.Unmarshal([]byte(htmlStr[tagEnd:closeStart]), &value); err != nil || blockedSeedData(value) {
				return nil, false
			}
			return value, true
		}
		rest := htmlStr[tagEnd:]
		lt := strings.IndexByte(rest, '<')
		if lt < 0 || !strings.HasPrefix(rest[lt:], "</") {
			return nil, false // not a leaf: the client reads the composed text
		}
		return strings.TrimSpace(html.UnescapeString(rest[:lt])), true
	}
	if strings.HasPrefix(source, "on") || strings.HasPrefix(source, "data-kit") || source == "style" || source == "srcdoc" || source == "innerhtml" || source == "outerhtml" {
		return nil, false
	}
	if seedReflectedBoolean[source] || seedLiveBoolean[source] {
		return tagHasAttribute(tag, source), true
	}
	if source == "value" {
		value := tagAttribute(tag, "value")
		kind := strings.ToLower(tagAttribute(tag, "type"))
		if name == "input" && (kind == "number" || kind == "range") {
			if strings.TrimSpace(value) == "" {
				return nil, true
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return nil, true
			}
			return f, true
		}
		return value, true
	}
	if !tagHasAttribute(tag, source) {
		return nil, true
	}
	return tagAttribute(tag, source), true
}

func blockedSeedData(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, inner := range v {
			if blockedKey(key) || blockedSeedData(inner) {
				return true
			}
		}
	case []any:
		for _, inner := range v {
			if blockedSeedData(inner) {
				return true
			}
		}
	}
	return false
}

func tagHasAttribute(tag, name string) bool {
	re := regexp.MustCompile(`(?i)[\s"']` + regexp.QuoteMeta(name) + `(?:\s*=|[\s/>])`)
	return re.MatchString(tag)
}

func tagAttribute(tag, name string) string {
	re := regexp.MustCompile(`(?i)[\s"']` + regexp.QuoteMeta(name) + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	for _, group := range m[1:] {
		if group != "" {
			return authoredAttribute(group)
		}
	}
	return ""
}

func seedAssign(scope map[string]any, path []string, value any) {
	holder := scope
	for _, segment := range path[:len(path)-1] {
		next, ok := holder[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			holder[segment] = next
		}
		holder = next
	}
	holder[path[len(path)-1]] = value
}

func seedRead(scope map[string]any, path []string) any {
	var current any = scope
	for _, segment := range path {
		holder, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = holder[segment]
	}
	return current
}
