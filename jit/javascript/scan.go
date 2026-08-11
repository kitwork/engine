package javascript

import (
	"bytes"
	"fmt"
	stdhtml "html"
	"strings"
)

// ComponentRef is one authored component host discovered in HTML. Alias comes
// only from data-kit-as; legacy inline aliases are deliberately not parsed.
// Retain is an optional, exact Morph identity from data-kit-retain.
type ComponentRef struct {
	Name    string
	Version string
	Alias   string
	Retain  string
	Offset  int
}

// ScanResult is the authored KitJS use discovered in one HTML document. App is
// an opaque compatibility identity; HasApp distinguishes a valueless positive
// marker from no marker. A positive app marker always selects Drive and Morph.
type ScanResult struct {
	Components   []ComponentRef
	NeedsRuntime bool
	Drive        bool
	HasApp       bool
	App          string
	AppOffset    int
}

// ScanHTML scans actual HTML start tags. Comments, raw-text bodies, and entire
// data-kit-ignore subtrees are opaque so examples or third-party DOM cannot
// accidentally select runtime modules.
func ScanHTML(source []byte) (ScanResult, error) {
	result := ScanResult{Components: make([]ComponentRef, 0, 8)}
	aliases := make(map[string]int)
	retains := make(map[string]int)
	frames := make([]scanFrame, 0, 16)
	var deferredStructuralError error
	for offset := 0; offset < len(source); {
		relative := bytes.IndexByte(source[offset:], '<')
		if relative < 0 {
			break
		}
		start := offset + relative

		if bytes.HasPrefix(source[start:], []byte("<!--")) {
			end := bytes.Index(source[start+4:], []byte("-->"))
			if end < 0 {
				break
			}
			offset = start + 4 + end + 3
			continue
		}
		if start+1 < len(source) && source[start+1] == '/' {
			name, next := scanClosingTag(source, start)
			frames = closeScanFrames(frames, name)
			offset = next
			continue
		}
		if start+1 >= len(source) || source[start+1] == '!' || source[start+1] == '?' {
			offset = skipTag(source, start+1)
			continue
		}

		tag, err := scanStartTag(source, start)
		if err != nil {
			return ScanResult{}, err
		}
		if tag.next <= start {
			offset = start + 1
			continue
		}
		foreignChildren := scanFramesHaveForeignChildren(frames)
		if !foreignChildren {
			frames = closeImpliedHTMLFrames(frames, tag.name)
		}
		tag.foreign = foreignChildren || tag.name == "svg" || tag.name == "math"
		offset = tag.next
		if tag.unsupportedStructural != nil && deferredStructuralError == nil {
			deferredStructuralError = tag.unsupportedStructural
		}
		if tag.ignore.present {
			offset = skipIgnoredElement(source, offset, tag)
			continue
		}

		for _, marker := range []scannedAttribute{tag.app, tag.hydrate} {
			if !marker.present || markerDisabled(marker.value) {
				continue
			}
			if result.HasApp {
				return ScanResult{}, fmt.Errorf("%w at byte %d: multiple positive app markers (first declared at byte %d)", ErrInvalidAppUse, marker.offset, result.AppOffset)
			}
			result.HasApp = true
			result.App = stdhtml.UnescapeString(marker.value)
			result.AppOffset = marker.offset
			result.NeedsRuntime = true
			result.Drive = true
		}
		if tag.needsRuntime {
			result.NeedsRuntime = true
		}

		if tag.alias.present && !tag.component.present {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-as requires data-kit-component on the same element", ErrInvalidComponentUse, tag.alias.offset)
		}
		if tag.version.present && !tag.component.present {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-version requires data-kit-component on the same element", ErrInvalidComponentUse, tag.version.offset)
		}
		if tag.retain.present && !tag.component.present {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-retain requires data-kit-component on the same element", ErrInvalidComponentUse, tag.retain.offset)
		}
		if tag.retain.present && (tag.name == "template" || tag.structural || scanFramesContainTemplate(frames) || scanFramesContainStructural(frames)) {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-retain is not allowed in structural templates", ErrInvalidComponentUse, tag.retain.offset)
		}
		if tag.retain.present && scanFramesContainRetain(frames) {
			return ScanResult{}, fmt.Errorf("%w at byte %d: retained component hosts cannot be nested", ErrInvalidComponentUse, tag.retain.offset)
		}
		if tag.component.present && tag.scope.present {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-scope cannot create a second state store on a component host", ErrInvalidComponentUse, tag.scope.offset)
		}
		if tag.component.present {
			result.NeedsRuntime = true
			name, err := parseComponentName(tag.component.value)
			if err != nil {
				return ScanResult{}, fmt.Errorf("%w at byte %d: %v", ErrInvalidComponentUse, tag.component.offset, err)
			}
			version := ""
			if tag.version.present {
				version, err = parseExactVersion(tag.version.value)
				if err != nil {
					return ScanResult{}, fmt.Errorf("%w at byte %d: %v", ErrInvalidComponentUse, tag.version.offset, err)
				}
			}
			alias := ""
			if tag.alias.present {
				alias = strings.TrimSpace(stdhtml.UnescapeString(tag.alias.value))
				if !validAlias(alias) || reservedAlias(alias) {
					return ScanResult{}, fmt.Errorf("%w at byte %d: invalid data-kit-as value %q", ErrInvalidComponentUse, tag.alias.offset, alias)
				}
				if prior, exists := aliases[alias]; exists {
					return ScanResult{}, fmt.Errorf("%w at byte %d: duplicate alias %q (first declared at byte %d)", ErrInvalidComponentUse, tag.alias.offset, alias, prior)
				}
				aliases[alias] = tag.alias.offset
			}
			retain := ""
			if tag.retain.present {
				retain, err = parseRetainKey(tag.retain.value)
				if err != nil {
					return ScanResult{}, fmt.Errorf("%w at byte %d: %v", ErrInvalidComponentUse, tag.retain.offset, err)
				}
				if prior, exists := retains[retain]; exists {
					return ScanResult{}, fmt.Errorf("%w at byte %d: duplicate retain key %q (first declared at byte %d)", ErrInvalidComponentUse, tag.retain.offset, retain, prior)
				}
				retains[retain] = tag.retain.offset
			}
			result.Components = append(result.Components, ComponentRef{
				Name:    name,
				Version: version,
				Alias:   alias,
				Retain:  retain,
				Offset:  tag.component.offset,
			})
		}

		if rawTextElement(tag.name) {
			if tag.name == "plaintext" {
				break
			}
			offset = skipRawText(source, offset, tag.name)
			continue
		}
		if !(!tag.foreign && voidElement(tag.name)) && !(tag.foreign && tag.selfClosing) {
			frames = append(frames, scanFrame{
				name: tag.name, template: tag.name == "template",
				structural: tag.structural, retain: tag.retain.present, foreign: tag.foreign,
			})
		}
	}
	if deferredStructuralError != nil {
		return ScanResult{}, deferredStructuralError
	}
	return result, nil
}

// ScanComponents preserves the component-only API for callers that do not
// need document-level runtime or Drive selection.
func ScanComponents(source []byte) ([]ComponentRef, error) {
	result, err := ScanHTML(source)
	if err != nil {
		return nil, err
	}
	return result.Components, nil
}

type scannedAttribute struct {
	present bool
	value   string
	offset  int
}

type scannedTag struct {
	name                  string
	next                  int
	selfClosing           bool
	foreign               bool
	needsRuntime          bool
	component             scannedAttribute
	version               scannedAttribute
	alias                 scannedAttribute
	retain                scannedAttribute
	scope                 scannedAttribute
	ignore                scannedAttribute
	app                   scannedAttribute
	hydrate               scannedAttribute
	structural            bool
	unsupportedStructural error
}

type rawScannedAttribute struct {
	name     string
	hasValue bool
	value    string
	offset   int
}

type scanFrame struct {
	name       string
	template   bool
	structural bool
	retain     bool
	foreign    bool
}

func scanClosingTag(source []byte, start int) (string, int) {
	index := skipHTMLSpace(source, start+2)
	nameStart := index
	for index < len(source) && asciiTagNamePart(source[index]) {
		index++
	}
	name := strings.ToLower(string(source[nameStart:index]))
	return name, skipTag(source, index)
}

func closeScanFrames(frames []scanFrame, name string) []scanFrame {
	if name == "" {
		return frames
	}
	for index := len(frames) - 1; index >= 0; index-- {
		if frames[index].name == name {
			return frames[:index]
		}
	}
	return frames
}

func scanFramesHaveForeignChildren(frames []scanFrame) bool {
	if len(frames) == 0 {
		return false
	}
	parent := frames[len(frames)-1]
	if !parent.foreign {
		return false
	}
	switch parent.name {
	case "foreignobject", "desc", "title":
		return false
	default:
		return true
	}
}

func closeImpliedHTMLFrames(frames []scanFrame, incoming string) []scanFrame {
	// In the HTML "in column group" insertion mode, every ordinary start tag
	// other than col/template first closes the open colgroup and is reprocessed.
	if incoming != "col" && incoming != "template" {
		frames = closeFrameInScope(frames, []string{"colgroup"}, []string{"table", "html", "template"})
	}
	switch incoming {
	case "li":
		frames = closeFrameInScope(frames, []string{"li"}, []string{"ul", "ol", "menu", "html", "template"})
	case "dt", "dd":
		frames = closeFrameInScope(frames, []string{"dt", "dd"}, []string{"dl", "html", "template"})
	case "rb":
		frames = closeFrameInScope(frames, []string{"rb", "rt", "rp", "rtc"}, []string{"ruby", "html", "template"})
	case "rtc":
		frames = closeFrameInScope(frames, []string{"rb", "rt", "rp", "rtc"}, []string{"ruby", "html", "template"})
	case "rt", "rp":
		frames = closeFrameInScope(frames, []string{"rb", "rt", "rp"}, []string{"ruby", "html", "template"})
	case "option":
		frames = closeFrameInScope(frames, []string{"option"}, []string{"select", "datalist", "html", "template"})
	case "optgroup":
		frames = closeFrameInScope(frames, []string{"option"}, []string{"select", "datalist", "html", "template"})
		frames = closeFrameInScope(frames, []string{"optgroup"}, []string{"select", "html", "template"})
	case "tr":
		frames = closeFrameInScope(frames, []string{"tr"}, []string{"table", "html", "template"})
	case "td", "th":
		frames = closeFrameInScope(frames, []string{"td", "th"}, []string{"tr", "table", "html", "template"})
	case "thead", "tbody", "tfoot":
		frames = closeFrameInScope(frames, []string{"thead", "tbody", "tfoot"}, []string{"table", "html", "template"})
	case "button":
		frames = closeFrameInScope(frames, []string{"button"}, []string{"html", "template"})
	case "a":
		frames = closeFrameInScope(frames, []string{"a"}, []string{"html", "template"})
	case "h1", "h2", "h3", "h4", "h5", "h6":
		frames = closeFrameInScope(frames, []string{"h1", "h2", "h3", "h4", "h5", "h6"}, []string{"html", "template"})
	}
	if closesParagraph(incoming) {
		frames = closeFrameInScope(frames, []string{"p"}, []string{"button", "table", "td", "th", "html", "template"})
	}
	return frames
}

func closeFrameInScope(frames []scanFrame, targets, boundaries []string) []scanFrame {
	for index := len(frames) - 1; index >= 0; index-- {
		name := frames[index].name
		if stringInSlice(name, targets) {
			return frames[:index]
		}
		if stringInSlice(name, boundaries) {
			return frames
		}
	}
	return frames
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func closesParagraph(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "details", "dialog", "div", "dl", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hgroup", "hr", "main", "menu", "nav", "ol", "p", "pre", "search", "section", "summary", "table", "ul":
		return true
	default:
		return false
	}
}

func scanFramesContainTemplate(frames []scanFrame) bool {
	for index := len(frames) - 1; index >= 0; index-- {
		if frames[index].template {
			return true
		}
	}
	return false
}

func scanFramesContainRetain(frames []scanFrame) bool {
	for index := len(frames) - 1; index >= 0; index-- {
		if frames[index].retain {
			return true
		}
	}
	return false
}

func scanFramesContainStructural(frames []scanFrame) bool {
	for index := len(frames) - 1; index >= 0; index-- {
		if frames[index].structural {
			return true
		}
	}
	return false
}

func scanStartTag(source []byte, start int) (scannedTag, error) {
	tag := scannedTag{next: start + 1}
	attributes := make([]rawScannedAttribute, 0, 8)
	index := start + 1
	if index >= len(source) || !asciiTagNameStart(source[index]) {
		return tag, nil
	}
	nameStart := index
	for index < len(source) && asciiTagNamePart(source[index]) {
		index++
	}
	tag.name = strings.ToLower(string(source[nameStart:index]))

	for index < len(source) {
		index = skipHTMLSpace(source, index)
		if index >= len(source) {
			return scannedTag{}, fmt.Errorf("kitjs: unterminated <%s> tag at byte %d", tag.name, start)
		}
		if source[index] == '>' {
			tag.next = index + 1
			return finalizeScannedTag(tag, attributes)
		}
		if source[index] == '/' && index+1 < len(source) && source[index+1] == '>' {
			tag.next = index + 2
			tag.selfClosing = true
			return finalizeScannedTag(tag, attributes)
		}

		attributeOffset := index
		for index < len(source) && !attributeNameDelimiter(source[index]) {
			index++
		}
		if attributeOffset == index {
			index++
			continue
		}
		attributeName := strings.ToLower(string(source[attributeOffset:index]))
		index = skipHTMLSpace(source, index)

		hasValue := false
		value := ""
		if index < len(source) && source[index] == '=' {
			hasValue = true
			index++
			index = skipHTMLSpace(source, index)
			if index >= len(source) {
				return scannedTag{}, fmt.Errorf("kitjs: missing value for %s at byte %d", attributeName, attributeOffset)
			}
			var err error
			value, index, err = scanAttributeValue(source, index)
			if err != nil {
				return scannedTag{}, fmt.Errorf("kitjs: %w for %s at byte %d", err, attributeName, attributeOffset)
			}
		}

		attributes = append(attributes, rawScannedAttribute{
			name:     attributeName,
			hasValue: hasValue,
			value:    value,
			offset:   attributeOffset,
		})
	}
	return scannedTag{}, fmt.Errorf("kitjs: unterminated <%s> tag at byte %d", tag.name, start)
}

func finalizeScannedTag(tag scannedTag, attributes []rawScannedAttribute) (scannedTag, error) {
	// data-kit-ignore makes the host and its descendants opaque. Find it before
	// validating any KitJS metadata so ignored third-party markup cannot affect
	// dependency selection or fail a generation scan.
	for _, attribute := range attributes {
		if attribute.name == "data-kit-ignore" {
			tag.ignore = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
			return tag, nil
		}
	}

	for _, attribute := range attributes {
		if err := validateReservedAttribute(attribute); err != nil {
			return scannedTag{}, err
		}
		switch attribute.name {
		case "data-kit-component":
			if tag.component.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-component", ErrInvalidComponentUse, attribute.offset)
			}
			if !attribute.hasValue {
				return scannedTag{}, fmt.Errorf("%w at byte %d: data-kit-component requires a value", ErrInvalidComponentUse, attribute.offset)
			}
			tag.component = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
		case "data-kit-version":
			if tag.version.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-version", ErrInvalidComponentUse, attribute.offset)
			}
			if !attribute.hasValue {
				return scannedTag{}, fmt.Errorf("%w at byte %d: data-kit-version requires a value", ErrInvalidComponentUse, attribute.offset)
			}
			tag.version = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
		case "data-kit-as":
			if tag.alias.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-as", ErrInvalidComponentUse, attribute.offset)
			}
			if !attribute.hasValue {
				return scannedTag{}, fmt.Errorf("%w at byte %d: data-kit-as requires a value", ErrInvalidComponentUse, attribute.offset)
			}
			tag.alias = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
		case "data-kit-retain":
			if tag.retain.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-retain", ErrInvalidComponentUse, attribute.offset)
			}
			if !attribute.hasValue {
				return scannedTag{}, fmt.Errorf("%w at byte %d: data-kit-retain requires a value", ErrInvalidComponentUse, attribute.offset)
			}
			tag.retain = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
		case "data-kit-scope":
			if tag.scope.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-scope", ErrInvalidComponentUse, attribute.offset)
			}
			tag.scope = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
		case "data-kit-app":
			if tag.app.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-app", ErrInvalidAppUse, attribute.offset)
			}
			tag.app = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
		case "data-kit-hydrate":
			if tag.hydrate.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-hydrate", ErrInvalidAppUse, attribute.offset)
			}
			tag.hydrate = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
		case "data-kit-if", "data-kit-for":
			tag.structural = true
			if tag.unsupportedStructural == nil {
				tag.unsupportedStructural = fmt.Errorf("%w at byte %d: %q is not implemented by this KitJS runtime", ErrUnsupportedAttribute, attribute.offset, attribute.name)
			}
		}
		if runtimeAttribute(attribute.name) {
			tag.needsRuntime = true
		}
	}
	return tag, nil
}

func validateReservedAttribute(attribute rawScannedAttribute) error {
	name := attribute.name
	if strings.HasPrefix(name, "data-kitwork-") {
		return fmt.Errorf("%w at byte %d: %q belongs to the engine-emitted namespace", ErrUnsupportedAttribute, attribute.offset, name)
	}
	if !strings.HasPrefix(name, "data-kit-") {
		return nil
	}

	directive := strings.TrimPrefix(name, "data-kit-")
	parts := strings.Split(directive, ":")
	base := parts[0]
	switch base {
	case "click", "dblclick", "submit", "input", "change", "keydown", "keyup", "pointerdown", "pointerup", "focusin", "focusout":
		for _, modifier := range parts[1:] {
			if !supportedEventModifier(modifier) {
				return fmt.Errorf("%w at byte %d: %q uses unsupported event modifier %q", ErrUnsupportedAttribute, attribute.offset, name, modifier)
			}
		}
		return nil
	case "text", "show", "class", "bind", "style", "model", "cloak", "scope", "component",
		"version", "as", "retain", "ref", "app", "hydrate", "drive", "ignore", "if", "for", "key":
		if len(parts) != 1 {
			return fmt.Errorf("%w at byte %d: %q only permits modifiers on event attributes", ErrUnsupportedAttribute, attribute.offset, name)
		}
		return nil
	default:
		return fmt.Errorf("%w at byte %d: %q is not implemented by this KitJS runtime", ErrUnsupportedAttribute, attribute.offset, name)
	}
}

func supportedEventModifier(modifier string) bool {
	switch modifier {
	case "self", "enter", "escape", "prevent", "stop", "once", "outside", "debounce":
		return true
	}
	if !strings.HasPrefix(modifier, "debounce(") || !strings.HasSuffix(modifier, ")") {
		return false
	}
	delay := modifier[len("debounce(") : len(modifier)-1]
	if delay == "" {
		return false
	}
	nonzero := false
	for index := 0; index < len(delay); index++ {
		if delay[index] < '0' || delay[index] > '9' {
			return false
		}
		if delay[index] != '0' {
			nonzero = true
		}
	}
	return nonzero
}

func scanAttributeValue(source []byte, index int) (string, int, error) {
	if source[index] == '"' || source[index] == '\'' {
		quote := source[index]
		index++
		start := index
		for index < len(source) && source[index] != quote {
			index++
		}
		if index >= len(source) {
			return "", index, fmt.Errorf("unterminated quoted attribute")
		}
		return string(source[start:index]), index + 1, nil
	}

	start := index
	for index < len(source) && !htmlSpace(source[index]) && source[index] != '>' {
		if source[index] == '<' || source[index] == '"' || source[index] == '\'' || source[index] == '`' || source[index] == '=' {
			return "", index, fmt.Errorf("invalid unquoted attribute value")
		}
		index++
	}
	if start == index {
		return "", index, fmt.Errorf("empty unquoted attribute value")
	}
	return string(source[start:index]), index, nil
}

func markerDisabled(value string) bool {
	return stdhtml.UnescapeString(value) == "false"
}

func runtimeAttribute(name string) bool {
	const prefix = "data-kit-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	directive := strings.TrimPrefix(name, prefix)
	if colon := strings.IndexByte(directive, ':'); colon >= 0 {
		directive = directive[:colon]
	}
	switch directive {
	case "text", "show", "class", "bind", "style", "model", "cloak", "scope", "component":
		return true
	case "click", "dblclick", "submit", "input", "change", "keydown", "keyup", "pointerdown", "pointerup", "focusin", "focusout":
		return true
	default:
		return false
	}
}

func skipIgnoredElement(source []byte, offset int, root scannedTag) int {
	if root.foreign && root.selfClosing || !root.foreign && voidElement(root.name) {
		return offset
	}
	if root.name == "plaintext" {
		return len(source)
	}
	if rawTextElement(root.name) {
		return skipRawText(source, offset, root.name)
	}

	depth := 1
	for offset < len(source) {
		relative := bytes.IndexByte(source[offset:], '<')
		if relative < 0 {
			return len(source)
		}
		start := offset + relative
		if bytes.HasPrefix(source[start:], []byte("<!--")) {
			end := bytes.Index(source[start+4:], []byte("-->"))
			if end < 0 {
				return len(source)
			}
			offset = start + 4 + end + 3
			continue
		}

		index := start + 1
		closing := false
		if index < len(source) && source[index] == '/' {
			closing = true
			index++
		}
		if index >= len(source) || !asciiTagNameStart(source[index]) {
			offset = start + 1
			continue
		}
		nameStart := index
		for index < len(source) && asciiTagNamePart(source[index]) {
			index++
		}
		name := strings.ToLower(string(source[nameStart:index]))
		next := skipTag(source, index)
		if strings.EqualFold(name, root.name) {
			if closing {
				depth--
				if depth == 0 {
					return next
				}
			} else if !(root.foreign && tagSelfClosing(source, start, next)) && !(!root.foreign && voidElement(name)) {
				depth++
			}
		}
		if !closing && rawTextElement(name) {
			if name == "plaintext" {
				return len(source)
			}
			next = skipRawText(source, next, name)
		}
		offset = next
	}
	return len(source)
}

func tagSelfClosing(source []byte, start, next int) bool {
	if next <= start || next > len(source) {
		return false
	}
	index := next - 2
	for index > start && htmlSpace(source[index]) {
		index--
	}
	return index > start && source[index] == '/'
}

func voidElement(name string) bool {
	switch name {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func skipTag(source []byte, index int) int {
	var quote byte
	for index < len(source) {
		char := source[index]
		if quote != 0 {
			if char == quote {
				quote = 0
			}
		} else {
			switch char {
			case '"', '\'':
				quote = char
			case '>':
				return index + 1
			}
		}
		index++
	}
	return len(source)
}

func skipRawText(source []byte, offset int, tagName string) int {
	for offset < len(source) {
		relative := bytes.IndexByte(source[offset:], '<')
		if relative < 0 {
			return len(source)
		}
		start := offset + relative
		nameStart := start + 2
		nameEnd := nameStart + len(tagName)
		if start+2 <= len(source) && nameEnd <= len(source) && source[start+1] == '/' &&
			strings.EqualFold(string(source[nameStart:nameEnd]), tagName) &&
			(nameEnd == len(source) || htmlSpace(source[nameEnd]) || source[nameEnd] == '>') {
			return skipTag(source, nameEnd)
		}
		offset = start + 1
	}
	return len(source)
}

// headCloseOffset finds a real closing head tag while treating comments and
// raw-text element bodies as opaque. It is used only for engine tag injection.
func headCloseOffset(source []byte) int {
	for offset := 0; offset < len(source); {
		relative := bytes.IndexByte(source[offset:], '<')
		if relative < 0 {
			return -1
		}
		start := offset + relative
		if bytes.HasPrefix(source[start:], []byte("<!--")) {
			end := bytes.Index(source[start+4:], []byte("-->"))
			if end < 0 {
				return -1
			}
			offset = start + 4 + end + 3
			continue
		}
		index := start + 1
		closing := false
		if index < len(source) && source[index] == '/' {
			closing = true
			index++
		}
		if index >= len(source) || !asciiTagNameStart(source[index]) {
			offset = start + 1
			continue
		}
		nameStart := index
		for index < len(source) && asciiTagNamePart(source[index]) {
			index++
		}
		name := strings.ToLower(string(source[nameStart:index]))
		next := skipTag(source, index)
		if closing && name == "head" {
			return start
		}
		if !closing && rawTextElement(name) {
			if name == "plaintext" {
				return -1
			}
			next = skipRawText(source, next, name)
		}
		offset = next
	}
	return -1
}

// hasRuntimeMarkerAttribute performs a lenient semantic start-tag scan for the
// reserved engine marker. It ignores comments, raw-text bodies, encoded text,
// and Kitwork {{ ... }} tokens inside a start tag; unrelated template syntax
// must not be interpreted as malformed HTML by the injection guard.
func hasRuntimeMarkerAttribute(source []byte) bool {
	for offset := 0; offset < len(source); {
		relative := bytes.IndexByte(source[offset:], '<')
		if relative < 0 {
			return false
		}
		start := offset + relative
		if bytes.HasPrefix(source[start:], []byte("<!--")) {
			end := bytes.Index(source[start+4:], []byte("-->"))
			if end < 0 {
				return false
			}
			offset = start + 4 + end + 3
			continue
		}
		index := start + 1
		if index >= len(source) || source[index] == '/' || !asciiTagNameStart(source[index]) {
			offset = skipTag(source, index)
			continue
		}
		nameStart := index
		for index < len(source) && asciiTagNamePart(source[index]) {
			index++
		}
		name := strings.ToLower(string(source[nameStart:index]))
		next := skipTag(source, index)
		end := next
		if end > 0 && end <= len(source) && source[end-1] == '>' {
			end--
		}
		for index < end {
			index = skipHTMLSpace(source, index)
			if index >= end || source[index] == '/' {
				break
			}
			if index+1 < end && source[index] == '{' && source[index+1] == '{' {
				close := bytes.Index(source[index+2:end], []byte("}}"))
				if close < 0 {
					break
				}
				index += 2 + close + 2
				continue
			}
			attributeStart := index
			for index < end && !attributeNameDelimiter(source[index]) {
				index++
			}
			if strings.EqualFold(string(source[attributeStart:index]), "data-kitwork-runtime") {
				return true
			}
			index = skipHTMLSpace(source, index)
			if index >= end || source[index] != '=' {
				continue
			}
			index++
			index = skipHTMLSpace(source, index)
			if index >= end {
				break
			}
			if source[index] == '"' || source[index] == '\'' {
				quote := source[index]
				index++
				for index < end && source[index] != quote {
					index++
				}
				if index < end {
					index++
				}
				continue
			}
			for index < end && !htmlSpace(source[index]) && source[index] != '>' {
				index++
			}
		}
		if rawTextElement(name) {
			if name == "plaintext" {
				return false
			}
			next = skipRawText(source, next, name)
		}
		offset = next
	}
	return false
}

func rawTextElement(name string) bool {
	switch name {
	case "script", "style", "textarea", "title", "xmp", "iframe", "noembed", "noframes", "plaintext":
		return true
	default:
		return false
	}
}

func parseComponentName(raw string) (string, error) {
	name := strings.TrimSpace(stdhtml.UnescapeString(raw))
	if name == "" {
		return "", fmt.Errorf("empty data-kit-component value")
	}
	if strings.Contains(name, "@") {
		return "", fmt.Errorf("inline component versions are not supported; use data-kit-version")
	}
	if !validModuleName(name) {
		return "", fmt.Errorf("invalid component name %q", name)
	}
	return name, nil
}

func parseExactVersion(raw string) (string, error) {
	version := strings.TrimSpace(stdhtml.UnescapeString(raw))
	if !validExactSemVer(version) {
		return "", fmt.Errorf("requires an exact SemVer, got %q", strings.TrimSpace(stdhtml.UnescapeString(raw)))
	}
	return version, nil
}

func parseRetainKey(raw string) (string, error) {
	key := stdhtml.UnescapeString(raw)
	if !validRetainKey(key) {
		return "", fmt.Errorf("invalid data-kit-retain value %q", key)
	}
	return key, nil
}

func validRetainKey(key string) bool {
	if len(key) < 1 || len(key) > 128 || !asciiLetter(key[0]) {
		return false
	}
	for index := 1; index < len(key); index++ {
		char := key[index]
		if asciiLetter(char) || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '.', '_', ':', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func validAlias(alias string) bool {
	if len(alias) < 2 || alias[0] != '$' || !asciiLetter(alias[1]) {
		return false
	}
	for index := 2; index < len(alias); index++ {
		if !asciiLetter(alias[index]) && alias[index] != '_' && (alias[index] < '0' || alias[index] > '9') {
			return false
		}
	}
	return true
}

func reservedAlias(alias string) bool {
	switch alias {
	case "$element", "$host", "$event", "$refs", "$component", "$parent", "$error", "$alias", "$invalidate":
		return true
	default:
		return false
	}
}

func validExactSemVer(version string) bool {
	if version == "" {
		return false
	}
	mainAndPre := version
	if plus := strings.IndexByte(version, '+'); plus >= 0 {
		if strings.IndexByte(version[plus+1:], '+') >= 0 || !validSemVerIdentifiers(version[plus+1:], false) {
			return false
		}
		mainAndPre = version[:plus]
	}
	main := mainAndPre
	if hyphen := strings.IndexByte(mainAndPre, '-'); hyphen >= 0 {
		if !validSemVerIdentifiers(mainAndPre[hyphen+1:], true) {
			return false
		}
		main = mainAndPre[:hyphen]
	}
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !validSemVerNumber(part) {
			return false
		}
	}
	return true
}

// ValidExactSemVer reports whether version is an exact SemVer 2.0.0 value.
// It deliberately rejects a leading "v", ranges, surrounding whitespace, and
// every other release-channel shorthand so callers can safely embed the value
// in generated artifact metadata.
func ValidExactSemVer(version string) bool {
	return validExactSemVer(version)
}

func validSemVerNumber(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func validSemVerIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for index := range len(identifier) {
			char := identifier[index]
			if (char < '0' || char > '9') && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && char != '-' {
				return false
			}
			if char < '0' || char > '9' {
				numeric = false
			}
		}
		if rejectNumericLeadingZero && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func asciiTagNameStart(char byte) bool {
	return (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')
}

func asciiTagNamePart(char byte) bool {
	return asciiTagNameStart(char) || (char >= '0' && char <= '9') || char == ':' || char == '-'
}

func attributeNameDelimiter(char byte) bool {
	return htmlSpace(char) || char == '=' || char == '>' || char == '/'
}

func skipHTMLSpace(source []byte, index int) int {
	for index < len(source) && htmlSpace(source[index]) {
		index++
	}
	return index
}

func htmlSpace(char byte) bool {
	return char == ' ' || char == '\t' || char == '\n' || char == '\r' || char == '\f'
}
