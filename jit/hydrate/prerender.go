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
	var rangeBuffer [16]sourceRange
	opaque := preRenderOpaqueRanges(htmlStr, rangeBuffer[:0])
	scope := modelScopeOutside(htmlStr, opaque)
	return preRenderOutside(htmlStr, opaque, scope)
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

type sourceRange struct {
	start int
	end   int
}

type htmlFrame struct {
	name      string
	ownsRange bool
}

// preRenderOpaqueRanges returns byte ranges that PreRender must preserve exactly. A local state
// boundary cannot be evaluated from the page scope without baking the wrong first paint. Raw-text
// elements and comments are opaque too: injected CSS/JS and authored examples may legally contain
// tag-like text that the small directive regexes must never rewrite.
func preRenderOpaqueRanges(htmlStr string, ranges []sourceRange) []sourceRange {
	var frameBuffer [64]htmlFrame
	stack := frameBuffer[:0]
	activeStart := -1

	for cursor := 0; cursor < len(htmlStr); {
		rel := strings.IndexByte(htmlStr[cursor:], '<')
		if rel < 0 {
			break
		}
		tagStart := cursor + rel

		if strings.HasPrefix(htmlStr[tagStart:], "<!--") {
			closeRel := strings.Index(htmlStr[tagStart+4:], "-->")
			tagEnd := len(htmlStr)
			if closeRel >= 0 {
				tagEnd = tagStart + 4 + closeRel + 3
			}
			if activeStart < 0 {
				ranges = append(ranges, sourceRange{start: tagStart, end: tagEnd})
			}
			cursor = tagEnd
			continue
		}

		tagEnd := quotedTagEnd(htmlStr, tagStart)
		if tagEnd < 0 {
			if activeStart >= 0 {
				ranges = append(ranges, sourceRange{start: activeStart, end: len(htmlStr)})
			}
			break
		}

		name, closing := htmlTagName(htmlStr[tagStart : tagEnd+1])
		if name == "" {
			cursor = tagEnd + 1
			continue
		}

		if closing {
			match := -1
			for i := len(stack) - 1; i >= 0; i-- {
				if strings.EqualFold(stack[i].name, name) {
					match = i
					break
				}
			}
			if match >= 0 {
				endsRange := false
				for i := len(stack) - 1; i >= match; i-- {
					endsRange = endsRange || stack[i].ownsRange
				}
				stack = stack[:match]
				if endsRange {
					ranges = append(ranges, sourceRange{start: activeStart, end: tagEnd + 1})
					activeStart = -1
				}
			}
			cursor = tagEnd + 1
			continue
		}

		tag := htmlStr[tagStart : tagEnd+1]
		rawText := isHTMLRawTextElement(name)
		opaque := rawText || tagHasLocalBoundary(tag)
		ownsRange := opaque && activeStart < 0
		if ownsRange {
			activeStart = tagStart
		}

		selfClosing := tagSelfCloses(tag) || isHTMLVoidElement(name)
		if selfClosing {
			if ownsRange {
				ranges = append(ranges, sourceRange{start: activeStart, end: tagEnd + 1})
				activeStart = -1
			}
			cursor = tagEnd + 1
			continue
		}

		stack = append(stack, htmlFrame{name: name, ownsRange: ownsRange})
		cursor = tagEnd + 1
		if rawText {
			closeStart := indexRawTextClose(htmlStr, cursor, name)
			if closeStart < 0 {
				if activeStart >= 0 {
					ranges = append(ranges, sourceRange{start: activeStart, end: len(htmlStr)})
				}
				break
			}
			cursor = closeStart
		}
	}

	if activeStart >= 0 && (len(ranges) == 0 || ranges[len(ranges)-1].start != activeStart) {
		ranges = append(ranges, sourceRange{start: activeStart, end: len(htmlStr)})
	}
	return ranges
}

func isHTMLVoidElement(name string) bool {
	switch len(name) {
	case 2:
		return strings.EqualFold(name, "br") || strings.EqualFold(name, "hr")
	case 3:
		return strings.EqualFold(name, "col") || strings.EqualFold(name, "img") ||
			strings.EqualFold(name, "wbr")
	case 4:
		return strings.EqualFold(name, "area") || strings.EqualFold(name, "base") ||
			strings.EqualFold(name, "embed") || strings.EqualFold(name, "link") ||
			strings.EqualFold(name, "meta")
	case 5:
		return strings.EqualFold(name, "input") || strings.EqualFold(name, "param") ||
			strings.EqualFold(name, "track")
	case 6:
		return strings.EqualFold(name, "source")
	default:
		return false
	}
}

func isHTMLRawTextElement(name string) bool {
	switch len(name) {
	case 5:
		return strings.EqualFold(name, "style") || strings.EqualFold(name, "title")
	case 6:
		return strings.EqualFold(name, "script")
	case 8:
		return strings.EqualFold(name, "textarea")
	default:
		return false
	}
}

func indexRawTextClose(htmlStr string, start int, name string) int {
	for cursor := start; cursor < len(htmlStr); {
		rel := strings.IndexByte(htmlStr[cursor:], '<')
		if rel < 0 {
			return -1
		}
		pos := cursor + rel
		nameStart := pos + 2
		nameEnd := nameStart + len(name)
		if nameEnd <= len(htmlStr) && htmlStr[pos+1] == '/' &&
			strings.EqualFold(htmlStr[nameStart:nameEnd], name) {
			return pos
		}
		cursor = pos + 1
	}
	return -1
}

func quotedTagEnd(htmlStr string, start int) int {
	var quote byte
	for i := start + 1; i < len(htmlStr); i++ {
		c := htmlStr[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			return i
		}
	}
	return -1
}

func htmlTagName(tag string) (string, bool) {
	if len(tag) < 3 || tag[0] != '<' {
		return "", false
	}
	i := 1
	for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\r' || tag[i] == '\n') {
		i++
	}
	closing := i < len(tag) && tag[i] == '/'
	if closing {
		i++
	}
	start := i
	for i < len(tag) && isHTMLNameByte(tag[i]) {
		i++
	}
	if i == start {
		return "", closing
	}
	return tag[start:i], closing
}

func isHTMLNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '-' || c == ':' || c == '_'
}

func tagSelfCloses(tag string) bool {
	for i := len(tag) - 2; i >= 0; i-- {
		switch tag[i] {
		case ' ', '\t', '\r', '\n':
			continue
		case '/':
			return true
		default:
			return false
		}
	}
	return false
}

func tagHasLocalBoundary(tag string) bool {
	i := 1
	for i < len(tag) && tag[i] != '>' {
		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\r' || tag[i] == '\n') {
			i++
		}
		start := i
		for i < len(tag) && isHTMLNameByte(tag[i]) {
			i++
		}
		if i == start {
			i++
			continue
		}
		name := tag[start:i]
		if isLocalBoundaryAttribute(name) {
			return true
		}

		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\r' || tag[i] == '\n') {
			i++
		}
		if i >= len(tag) || tag[i] != '=' {
			continue
		}
		i++
		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\r' || tag[i] == '\n') {
			i++
		}
		if i < len(tag) && (tag[i] == '"' || tag[i] == '\'') {
			quote := tag[i]
			i++
			for i < len(tag) && tag[i] != quote {
				i++
			}
			if i < len(tag) {
				i++
			}
		} else {
			for i < len(tag) && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\r' &&
				tag[i] != '\n' && tag[i] != '>' {
				i++
			}
		}
	}
	return false
}

func isLocalBoundaryAttribute(name string) bool {
	switch len(name) {
	case len("data-kit-api"):
		return strings.EqualFold(name, "data-kit-api") ||
			strings.EqualFold(name, "data-kit-for")
	case len("data-kit-item"):
		return strings.EqualFold(name, "data-kit-item")
	case len("data-kit-scope"):
		return strings.EqualFold(name, "data-kit-scope")
	case len("data-kit-component"):
		return strings.EqualFold(name, "data-kit-component") ||
			strings.EqualFold(name, "data-kitwork-scope")
	case len("data-kitwork-api"):
		return strings.EqualFold(name, "data-kitwork-api") ||
			strings.EqualFold(name, "data-kitwork-for")
	case len("data-kitwork-item"):
		return strings.EqualFold(name, "data-kitwork-item")
	case len("data-kitwork-component"):
		return strings.EqualFold(name, "data-kitwork-component")
	default:
		return false
	}
}

func modelScopeOutside(htmlStr string, opaque []sourceRange) map[string]any {
	scope := map[string]any{}
	seedModelScopeOutside(htmlStr, opaque, scope)
	return scope
}

// seedModelScope applies the client's first-model-wins rule to authored page scope.
func seedModelScope(htmlStr string, scope map[string]any) {
	seedModelScopeOutside(htmlStr, nil, scope)
}

func seedModelScopeOutside(htmlStr string, opaque []sourceRange, scope map[string]any) {
	searchOffset := 0
	rangeIndex := 0
	for searchOffset < len(htmlStr) {
		match := modelTagRe.FindStringSubmatchIndex(htmlStr[searchOffset:])
		if match == nil {
			break
		}
		for i := range match {
			if match[i] >= 0 {
				match[i] += searchOffset
			}
		}
		searchOffset = match[1]
		if overlapsOpaqueRange(match[0], match[1], opaque, &rangeIndex) {
			continue
		}

		key := authoredAttribute(htmlStr[match[2]:match[3]])
		if key == "" {
			continue
		}
		if _, seen := scope[key]; seen {
			continue
		}

		tag := htmlStr[match[0]:match[1]]
		value := ""
		if valueMatch := attrValueRe.FindStringSubmatchIndex(tag); valueMatch != nil {
			value = authoredAttribute(tag[valueMatch[2]:valueMatch[3]])
		}
		typeName := ""
		if typeMatch := attrTypeRe.FindStringSubmatchIndex(tag); typeMatch != nil {
			typeName = tag[typeMatch[2]:typeMatch[3]]
		}
		if typeName == "number" || typeName == "range" {
			f, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
			scope[key] = f
		} else {
			scope[key] = value
		}
	}
}

func preRenderOutside(htmlStr string, opaque []sourceRange, scope map[string]any) string {
	var adjustedBuffer [16]sourceRange
	withShow, adjusted := preRenderShowOutside(
		htmlStr,
		opaque,
		scope,
		adjustedBuffer[:0],
	)
	return preRenderTextOutside(withShow, adjusted, scope)
}

func preRenderText(htmlStr string, scope map[string]any) string {
	return preRenderTextOutside(htmlStr, nil, scope)
}

func preRenderTextOutside(htmlStr string, opaque []sourceRange, scope map[string]any) string {
	searchOffset := 0
	copyOffset := 0
	rangeIndex := 0
	changed := false
	var out strings.Builder

	for searchOffset < len(htmlStr) {
		match := textLeafRe.FindStringSubmatchIndex(htmlStr[searchOffset:])
		if match == nil {
			break
		}
		for i := range match {
			if match[i] >= 0 {
				match[i] += searchOffset
			}
		}
		searchOffset = match[1]
		if overlapsOpaqueRange(match[0], match[1], opaque, &rangeIndex) {
			continue
		}

		node, err := compileAuthoredAttribute(htmlStr[match[4]:match[5]])
		if err != nil {
			continue // malformed → leave authored content; the client logger handles it
		}
		v, err := Eval(node, scope)
		if err != nil {
			continue
		}
		replacement := html.EscapeString(display(v))
		if replacement == htmlStr[match[6]:match[7]] {
			continue
		}

		if !changed {
			out.Grow(len(htmlStr))
			changed = true
		}
		out.WriteString(htmlStr[copyOffset:match[6]])
		out.WriteString(replacement)
		copyOffset = match[7]
	}

	if !changed {
		return htmlStr
	}
	out.WriteString(htmlStr[copyOffset:])
	return out.String()
}

func preRenderShow(htmlStr string, scope map[string]any) string {
	var adjustedBuffer [16]sourceRange
	rendered, _ := preRenderShowOutside(htmlStr, nil, scope, adjustedBuffer[:0])
	return rendered
}

func preRenderShowOutside(
	htmlStr string,
	opaque []sourceRange,
	scope map[string]any,
	adjustedBuffer []sourceRange,
) (string, []sourceRange) {
	searchOffset := 0
	copyOffset := 0
	rangeIndex := 0
	changed := false
	rangesCopied := false
	adjusted := opaque
	var out strings.Builder

	for searchOffset < len(htmlStr) {
		match := showTagRe.FindStringSubmatchIndex(htmlStr[searchOffset:])
		if match == nil {
			break
		}
		for i := range match {
			if match[i] >= 0 {
				match[i] += searchOffset
			}
		}
		searchOffset = match[1]
		if overlapsOpaqueRange(match[0], match[1], opaque, &rangeIndex) {
			continue
		}

		node, err := compileAuthoredAttribute(htmlStr[match[2]:match[3]])
		if err != nil {
			continue
		}
		v, err := Eval(node, scope)
		if err != nil {
			continue
		}
		tag := htmlStr[match[0]:match[1]]
		hasHidden := strings.Contains(tag, " hidden>") ||
			strings.Contains(tag, " hidden ") ||
			strings.Contains(tag, " hidden=")
		if truthy(v) {
			continue // shown: leave as-is (author should not pre-hide a shown region)
		}
		if hasHidden {
			continue
		}

		if !changed {
			out.Grow(len(htmlStr) + len(" hidden"))
			changed = true
		}
		out.WriteString(htmlStr[copyOffset : match[1]-1])
		out.WriteString(" hidden>")
		copyOffset = match[1]

		for i, r := range opaque {
			if match[1] > r.start {
				continue
			}
			if !rangesCopied {
				if cap(adjustedBuffer) < len(opaque) {
					adjustedBuffer = make([]sourceRange, len(opaque))
				} else {
					adjustedBuffer = adjustedBuffer[:len(opaque)]
				}
				copy(adjustedBuffer, opaque)
				adjusted = adjustedBuffer
				rangesCopied = true
			}
			adjusted[i].start += len(" hidden")
			adjusted[i].end += len(" hidden")
		}
	}

	if !changed {
		return htmlStr, opaque
	}
	out.WriteString(htmlStr[copyOffset:])
	return out.String(), adjusted
}

func overlapsOpaqueRange(start, end int, ranges []sourceRange, rangeIndex *int) bool {
	for *rangeIndex < len(ranges) && ranges[*rangeIndex].end <= start {
		(*rangeIndex)++
	}
	return *rangeIndex < len(ranges) &&
		ranges[*rangeIndex].start < end &&
		ranges[*rangeIndex].end > start
}

// display renders a value the way the client's textContent assignment would (v == null ? "" : v).
func display(v any) string {
	if v == nil {
		return ""
	}
	return toStr(v)
}
