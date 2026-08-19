package javascript

import (
	"bytes"
	"fmt"
	stdhtml "html"
	"strconv"
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

// ScanResult is the authored KitJS use discovered in one HTML document.
// Runtime profile selection belongs to the delivery owner, not to an authored
// marker: standalone callers choose Kit or Hydrate explicitly, while Kitwork
// prepares one Hydrate graph for an opted-in generation.
type ScanResult struct {
	Components      []ComponentRef
	LocalComponents []ComponentRef
	NeedsRuntime    bool
}

// ScanHTML scans actual HTML start tags. Comments, raw-text bodies, and entire
// data-kit-ignore subtrees are opaque so examples or third-party DOM cannot
// accidentally select runtime modules.
func ScanHTML(source []byte) (ScanResult, error) {
	result := ScanResult{
		Components:      make([]ComponentRef, 0, 8),
		LocalComponents: make([]ComponentRef, 0, 4),
	}
	aliases := make(map[string]int)
	aliasComponents := make(map[string]ComponentRef)
	retains := make(map[string]int)
	serviceCalls := make([]expressionServiceCall, 0, 4)
	frames := make([]scanFrame, 0, 16)
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

		opaque := scanFramesContainIgnore(frames)
		var tag scannedTag
		var err error
		if opaque {
			tag = scanOpaqueStartTag(source, start)
		} else {
			tag, err = scanStartTag(source, start)
			if err != nil {
				return ScanResult{}, err
			}
		}
		if tag.next <= start {
			offset = start + 1
			continue
		}
		foreignChildren := scanFramesHaveForeignChildren(frames)
		if !foreignChildren {
			frames = closeImpliedHTMLFrames(frames, tag.name)
			if scanFramesContainIgnore(frames) && fosterParentsFromIgnoredTable(frames, tag.name) {
				return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-ignore cannot contain foster-parented table content", ErrUnsupportedAttribute, start)
			}
		}
		// An incoming HTML tag can implicitly close the ignored ancestor (for
		// example, div closes p and li closes li). Reparse that incoming tag with
		// full validation because it belongs to the live DOM, not the opaque
		// subtree that preceded it in source order.
		stillOpaque := scanFramesContainIgnore(frames)
		if opaque && !stillOpaque {
			tag, err = scanStartTag(source, start)
			if err != nil {
				return ScanResult{}, err
			}
		}
		tag.foreign = foreignChildren || tag.name == "svg" || tag.name == "math"
		offset = tag.next
		if stillOpaque || tag.ignore.present {
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
					foreign: tag.foreign, ignore: true,
				})
			}
			continue
		}

		if tag.needsRuntime {
			result.NeedsRuntime = true
		}
		serviceCalls = append(serviceCalls, tag.serviceCalls...)

		if tag.alias.present && !tag.component.present {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-as requires data-kit-component on the same element", ErrInvalidComponentUse, tag.alias.offset)
		}
		if tag.version.present && !tag.component.present {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-version requires data-kit-component on the same element", ErrInvalidComponentUse, tag.version.offset)
		}
		if tag.local.present && !tag.component.present {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-local requires data-kit-component on the same element", ErrInvalidComponentUse, tag.local.offset)
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
		if tag.structural {
			if tag.name != "template" {
				return ScanResult{}, fmt.Errorf("%w at byte %d: if, for, and key require a template element", ErrUnsupportedAttribute, tag.structuralOffset())
			}
			if tag.ifDirective.present && tag.forDirective.present {
				return ScanResult{}, fmt.Errorf("%w at byte %d: one template cannot combine if and for", ErrUnsupportedAttribute, tag.forDirective.offset)
			}
			if tag.keyDirective.present && !tag.forDirective.present {
				return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-key requires data-kit-for on the same template", ErrUnsupportedAttribute, tag.keyDirective.offset)
			}
		}
		if tag.name == "script" && scanFramesContainStructural(frames) {
			return ScanResult{}, fmt.Errorf("%w at byte %d: structural templates cannot contain script elements", ErrUnsupportedAttribute, start)
		}
		if tag.component.present && tag.name == "template" {
			return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-component cannot be used on a template; place the boundary inside template content", ErrInvalidComponentUse, tag.component.offset)
		}
		var scopeFields map[string]struct{}
		if tag.scope.present {
			if tag.name == "template" {
				return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-scope cannot be used on a template; place the boundary inside template content", ErrInvalidScopeUse, tag.scope.offset)
			}
			scopeFields, err = scopeInitializerFields(tag.scope.value)
			if err != nil {
				return ScanResult{}, fmt.Errorf("%w at byte %d: %v", ErrInvalidScopeUse, tag.scope.offset, err)
			}
		}
		if tag.component.present {
			result.NeedsRuntime = true
			name, inlineVersion, err := parseComponentSpec(tag.component.value)
			if err != nil {
				return ScanResult{}, fmt.Errorf("%w at byte %d: %v", ErrInvalidComponentUse, tag.component.offset, err)
			}
			if inlineVersion != "" && tag.version.present {
				return ScanResult{}, fmt.Errorf("%w at byte %d: inline component versions cannot be combined with data-kit-version", ErrInvalidComponentUse, tag.version.offset)
			}
			if tag.local.present && tag.version.present {
				return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-local components cannot use data-kit-version", ErrInvalidComponentUse, tag.version.offset)
			}
			if tag.local.present && inlineVersion != "" {
				return ScanResult{}, fmt.Errorf("%w at byte %d: data-kit-local cannot mark a versioned component", ErrInvalidComponentUse, tag.local.offset)
			}
			version := inlineVersion
			if tag.version.present {
				version, err = parseExactVersion(tag.version.value)
				if err != nil {
					return ScanResult{}, fmt.Errorf("%w at byte %d: %v", ErrInvalidComponentUse, tag.version.offset, err)
				}
			}
			client := version == ""
			if client && tag.retain.present {
				return ScanResult{}, fmt.Errorf("%w at byte %d: unversioned client components cannot use data-kit-retain", ErrInvalidComponentUse, tag.retain.offset)
			}
			alias := ""
			if tag.alias.present {
				alias = trimECMAScriptSpace(stdhtml.UnescapeString(tag.alias.value))
				if !validAlias(alias) || reservedAlias(alias) {
					return ScanResult{}, fmt.Errorf("%w at byte %d: invalid data-kit-as value %q", ErrInvalidComponentUse, tag.alias.offset, alias)
				}
				if prior, exists := aliases[alias]; exists {
					return ScanResult{}, fmt.Errorf("%w at byte %d: duplicate alias %q (first declared at byte %d)", ErrInvalidComponentUse, tag.alias.offset, alias, prior)
				}
				aliases[alias] = tag.alias.offset
			}
			if !client && name == "app" && alias == "$app" {
				for _, service := range authoredAppServices(version) {
					serviceName := service.Name
					if _, collision := scopeFields[serviceName]; collision {
						return ScanResult{}, fmt.Errorf("%w at byte %d: app scope field %q conflicts with an authored service namespace",
							ErrInvalidScopeUse, tag.scope.offset, serviceName)
					}
				}
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
			componentRef := ComponentRef{
				Name:    name,
				Version: version,
				Alias:   alias,
				Retain:  retain,
				Offset:  tag.component.offset,
			}
			if client {
				result.LocalComponents = append(result.LocalComponents, componentRef)
			} else {
				result.Components = append(result.Components, componentRef)
			}
			if alias != "" && !client {
				aliasComponents[alias] = componentRef
			}
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
	if err := validateScannedServiceCalls(serviceCalls, aliasComponents); err != nil {
		return ScanResult{}, err
	}
	return result, nil
}

func validateScannedServiceCalls(calls []expressionServiceCall, aliases map[string]ComponentRef) error {
	if len(calls) == 0 {
		return nil
	}
	for _, call := range calls {
		componentRef, exists := aliases["$app"]
		if !exists || componentRef.Name != "app" {
			return fmt.Errorf("%w at byte %d: authored service commands require data-kit-component=\"app@1.1.0\" with data-kit-as=\"$app\"",
				ErrInvalidExpressionUse, call.Position)
		}
		if call.Loader && componentRef.Version != "" && componentRef.Version != "1.1.0" {
			return fmt.Errorf("%w at byte %d: app loader bindings require exact app@1.1.0",
				ErrInvalidExpressionUse, call.Position)
		}
		if !appGrantsAuthoredService(componentRef.Version, call.Service) {
			return fmt.Errorf("%w at byte %d: app@%s does not grant authored service %s",
				ErrInvalidExpressionUse, call.Position, componentRef.Version, call.Service)
		}
	}
	return nil
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
	name         string
	next         int
	selfClosing  bool
	foreign      bool
	needsRuntime bool
	component    scannedAttribute
	version      scannedAttribute
	local        scannedAttribute
	alias        scannedAttribute
	retain       scannedAttribute
	scope        scannedAttribute
	ignore       scannedAttribute
	ifDirective  scannedAttribute
	forDirective scannedAttribute
	keyDirective scannedAttribute
	structural   bool
	serviceCalls []expressionServiceCall
}

func (tag scannedTag) structuralOffset() int {
	if tag.ifDirective.present {
		return tag.ifDirective.offset
	}
	if tag.forDirective.present {
		return tag.forDirective.offset
	}
	return tag.keyDirective.offset
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
	ignore     bool
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

func fosterParentsFromIgnoredTable(frames []scanFrame, incoming string) bool {
	for index := len(frames) - 1; index >= 0; index-- {
		frame := frames[index]
		// Caption and table cells switch the HTML parser back to ordinary body
		// rules. Content below them stays inside the ignored subtree and must not
		// be mistaken for foster-parented table content.
		if frame.name == "template" || frame.name == "caption" || frame.name == "td" || frame.name == "th" {
			return false
		}
		if frame.name != "table" && frame.name != "tbody" && frame.name != "tfoot" &&
			frame.name != "thead" && frame.name != "tr" {
			continue
		}
		if !frame.ignore {
			return false
		}
		if allowedInTableContext(frame.name, incoming) {
			return false
		}
		// Foster parenting inserts before the table. If a broader ignored
		// ancestor encloses that table, the relocated node remains opaque. It is
		// ambiguous only when the table region is the outermost ignored boundary.
		for ancestor := index - 1; ancestor >= 0; ancestor-- {
			if frames[ancestor].ignore {
				return false
			}
		}
		return true
	}
	return false
}

func allowedInTableContext(context, incoming string) bool {
	if incoming == "script" || incoming == "style" || incoming == "template" {
		return true
	}
	switch context {
	case "table":
		switch incoming {
		case "caption", "colgroup", "tbody", "tfoot", "thead", "tr", "td", "th", "col":
			return true
		}
	case "tbody", "tfoot", "thead":
		return incoming == "tr" || incoming == "td" || incoming == "th"
	case "tr":
		return incoming == "td" || incoming == "th"
	}
	return false
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

func scanFramesContainIgnore(frames []scanFrame) bool {
	for index := len(frames) - 1; index >= 0; index-- {
		if frames[index].ignore {
			return true
		}
	}
	return false
}

func scanOpaqueStartTag(source []byte, start int) scannedTag {
	tag := scannedTag{next: start + 1}
	index := start + 1
	if index >= len(source) || !asciiTagNameStart(source[index]) {
		return tag
	}
	nameStart := index
	for index < len(source) && asciiTagNamePart(source[index]) {
		index++
	}
	tag.name = strings.ToLower(string(source[nameStart:index]))
	tag.next = skipTag(source, index)
	tag.selfClosing = tagSelfClosing(source, start, tag.next)
	return tag
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
		case "data-kit-local":
			if tag.local.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-local", ErrInvalidComponentUse, attribute.offset)
			}
			if attribute.hasValue && stdhtml.UnescapeString(attribute.value) != "" {
				return scannedTag{}, fmt.Errorf("%w at byte %d: data-kit-local is an empty presence marker", ErrInvalidComponentUse, attribute.offset)
			}
			tag.local = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
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
		case "data-kit-if":
			if tag.ifDirective.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-if", ErrUnsupportedAttribute, attribute.offset)
			}
			if !attribute.hasValue {
				return scannedTag{}, fmt.Errorf("%w at byte %d: data-kit-if requires a value", ErrUnsupportedAttribute, attribute.offset)
			}
			tag.ifDirective = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
			tag.structural = true
		case "data-kit-for":
			if tag.forDirective.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-for", ErrUnsupportedAttribute, attribute.offset)
			}
			if !attribute.hasValue {
				return scannedTag{}, fmt.Errorf("%w at byte %d: data-kit-for requires a value", ErrUnsupportedAttribute, attribute.offset)
			}
			tag.forDirective = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
			tag.structural = true
		case "data-kit-key":
			if tag.keyDirective.present {
				return scannedTag{}, fmt.Errorf("%w at byte %d: duplicate data-kit-key", ErrUnsupportedAttribute, attribute.offset)
			}
			if !attribute.hasValue {
				return scannedTag{}, fmt.Errorf("%w at byte %d: data-kit-key requires a value", ErrUnsupportedAttribute, attribute.offset)
			}
			tag.keyDirective = scannedAttribute{present: true, value: attribute.value, offset: attribute.offset}
			tag.structural = true
		}
		calls, err := directiveExpressionServiceCalls(attribute)
		if err != nil {
			return scannedTag{}, fmt.Errorf("%w at byte %d in %s: %v", ErrInvalidExpressionUse, attribute.offset, attribute.name, err)
		}
		for index := range calls {
			// Scanner diagnostics consistently point at the authored attribute;
			// expression-relative byte positions remain available from the direct
			// validator without pretending decoded text has a source-file offset.
			calls[index].Position = attribute.offset
		}
		tag.serviceCalls = append(tag.serviceCalls, calls...)
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
		return validateEventModifiers(attribute, base, parts[1:])
	case "text", "show", "class", "bind", "style", "model", "scope", "component",
		"version", "local", "as", "retain", "drive", "ignore", "if", "for", "key":
		if len(parts) != 1 {
			return fmt.Errorf("%w at byte %d: %q only permits modifiers on event attributes", ErrUnsupportedAttribute, attribute.offset, name)
		}
		return nil
	default:
		return fmt.Errorf("%w at byte %d: %q is not implemented by this KitJS runtime", ErrUnsupportedAttribute, attribute.offset, name)
	}
}

func validateEventModifiers(attribute rawScannedAttribute, event string, modifiers []string) error {
	seen := make(map[string]struct{}, len(modifiers))
	key := ""
	outside := false
	self := false
	for _, modifier := range modifiers {
		canonical := modifier
		if strings.HasPrefix(modifier, "debounce(") && strings.HasSuffix(modifier, ")") {
			canonical = "debounce"
		}
		if !supportedEventModifier(modifier) {
			return fmt.Errorf("%w at byte %d: %q uses unsupported event modifier %q", ErrUnsupportedAttribute, attribute.offset, attribute.name, modifier)
		}
		if _, duplicate := seen[canonical]; duplicate {
			return fmt.Errorf("%w at byte %d: %q uses duplicate event modifier %q", ErrUnsupportedAttribute, attribute.offset, attribute.name, canonical)
		}
		seen[canonical] = struct{}{}

		switch canonical {
		case "enter", "escape":
			if event != "keydown" && event != "keyup" {
				return fmt.Errorf("%w at byte %d: %q uses a keyboard modifier on non-key event %q", ErrUnsupportedAttribute, attribute.offset, attribute.name, event)
			}
			if key != "" {
				return fmt.Errorf("%w at byte %d: %q cannot combine enter and escape", ErrUnsupportedAttribute, attribute.offset, attribute.name)
			}
			key = canonical
		case "outside":
			outside = true
		case "self":
			self = true
		}
	}
	if outside && !outsideEvent(event) {
		return fmt.Errorf("%w at byte %d: %q uses outside on unsupported event %q", ErrUnsupportedAttribute, attribute.offset, attribute.name, event)
	}
	if outside && self {
		return fmt.Errorf("%w at byte %d: %q cannot combine outside and self", ErrUnsupportedAttribute, attribute.offset, attribute.name)
	}
	return nil
}

func outsideEvent(event string) bool {
	switch event {
	case "click", "dblclick", "pointerdown", "pointerup", "focusin":
		return true
	default:
		return false
	}
}

func supportedEventModifier(modifier string) bool {
	switch modifier {
	case "self", "enter", "escape", "prevent", "stop", "once", "outside":
		return true
	}
	if !strings.HasPrefix(modifier, "debounce(") || !strings.HasSuffix(modifier, ")") {
		return false
	}
	delay := modifier[len("debounce(") : len(modifier)-1]
	if delay == "" {
		return false
	}
	for index := 0; index < len(delay); index++ {
		if delay[index] < '0' || delay[index] > '9' {
			return false
		}
	}
	milliseconds, err := strconv.ParseUint(delay, 10, 32)
	return err == nil && milliseconds >= 1 && milliseconds <= 60000
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
	case "text", "show", "class", "bind", "style", "model", "scope", "component", "local", "if", "for", "key":
		return true
	case "click", "dblclick", "submit", "input", "change", "keydown", "keyup", "pointerdown", "pointerup", "focusin", "focusout":
		return true
	default:
		return false
	}
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
	next, _ := skipRawTextClosed(source, offset, tagName)
	return next
}

func skipRawTextClosed(source []byte, offset int, tagName string) (int, bool) {
	for offset < len(source) {
		start := nextDeliveryMarkupAnyToken(source, offset, len(source))
		if start < 0 {
			return len(source), false
		}
		nameStart := start + 2
		nameEnd := nameStart + len(tagName)
		if start+2 <= len(source) && nameEnd <= len(source) && source[start+1] == '/' &&
			strings.EqualFold(string(source[nameStart:nameEnd]), tagName) &&
			(nameEnd == len(source) || htmlSpace(source[nameEnd]) || source[nameEnd] == '>') {
			return skipTag(source, nameEnd), true
		}
		offset = start + 1
	}
	return len(source), false
}

// skipDeliveryTag additionally treats Kitwork template expressions as opaque.
// A comparison such as {{ value > 0 }} must not look like the end of a tag.
func skipDeliveryTag(source []byte, index int) int {
	var quote byte
	for index < len(source) {
		char := source[index]
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			index++
			continue
		}
		if index+1 < len(source) && char == '{' && source[index+1] == '{' {
			close := bytes.Index(source[index+2:], []byte("}}"))
			if close < 0 {
				return len(source)
			}
			index += 2 + close + 2
			continue
		}
		switch char {
		case '"', '\'':
			quote = char
		case '>':
			return index + 1
		}
		index++
	}
	return len(source)
}

// deliveryHeadOffset keeps authored charset/CSP metadata ahead of staged
// scripts, while placing those scripts before the first effective base href.
// Without a base it preserves the normal end-of-head injection position.
func deliveryHeadOffset(source []byte) (int, error) {
	contentStart, contentEnd, err := headContentBounds(source)
	if err != nil {
		return 0, err
	}
	if err := validateDeliveryHeadTemplateContexts(source, contentEnd); err != nil {
		return 0, err
	}
	baseOffset := -1
	templateDepth := 0
	controlDepth := 0
	for offset := contentStart; offset < contentEnd; {
		start := nextDeliveryHeadMarkup(source, offset, contentEnd)
		if start < 0 {
			if templateDepth == 0 {
				controlDepth = deliveryHeadControlDepth(source, offset, contentEnd, controlDepth)
			}
			break
		}
		if templateDepth == 0 {
			controlDepth = deliveryHeadControlDepth(source, offset, start, controlDepth)
		}
		if bytes.HasPrefix(source[start:], []byte("<!--")) {
			end := bytes.Index(source[start+4:contentEnd], []byte("-->"))
			if end < 0 {
				break
			}
			offset = start + 4 + end + 3
			continue
		}
		index := start + 1
		closing := false
		if index < contentEnd && source[index] == '/' {
			closing = true
			index++
		}
		if index >= contentEnd || !asciiTagNameStart(source[index]) {
			offset = start + 1
			continue
		}
		nameStart := index
		for index < contentEnd && asciiTagNamePart(source[index]) {
			index++
		}
		name := strings.ToLower(string(source[nameStart:index]))
		next := skipDeliveryTag(source, index)
		if next > contentEnd {
			next = contentEnd
		}
		if !closing {
			if templateDepth == 0 {
				attributes, dynamic := deliveryHeadAttributes(source, index, next)
				if name == "base" {
					if _, hasHref := attributes["href"]; hasHref || dynamic {
						if controlDepth > 0 {
							return 0, fmt.Errorf("%w: conditional base can capture staged script URLs", ErrUnsafeHeadOrder)
						}
						if baseOffset < 0 {
							baseOffset = start
						}
					}
				} else if name == "meta" {
					charset, csp := deliveryMetaSecurity(attributes)
					if controlDepth > 0 && (dynamic || charset || csp) {
						return 0, fmt.Errorf("%w: conditional security meta cannot fence staged scripts", ErrUnsafeHeadOrder)
					}
					if baseOffset >= 0 {
						if dynamic {
							return 0, fmt.Errorf("%w: dynamic meta appears after the first base href", ErrUnsafeHeadOrder)
						}
						if charset || csp {
							kind := "charset"
							if csp {
								kind = "Content-Security-Policy"
							}
							return 0, fmt.Errorf("%w: meta %s appears after the first base href", ErrUnsafeHeadOrder, kind)
						}
					}
				}
			}
			if name == "template" {
				templateDepth++
			}
			if deliveryHeadRawTextElement(name) {
				next = skipRawText(source, next, name)
				if next > contentEnd {
					next = contentEnd
				}
			}
		} else if name == "template" && templateDepth > 0 {
			templateDepth--
		}
		offset = next
	}
	if baseOffset >= 0 {
		return baseOffset, nil
	}
	if controlDepth > 0 {
		return 0, fmt.Errorf("%w: staged scripts would remain inside a template control block", ErrUnsafeHeadOrder)
	}
	return contentEnd, nil
}

func headContentBounds(source []byte) (int, int, error) {
	contentStart, err := deliveryHeadStart(source)
	if err != nil {
		return 0, 0, err
	}
	contentEnd, err := deliveryEffectiveHeadEnd(source, contentStart)
	if err != nil {
		return 0, 0, err
	}
	return contentStart, contentEnd, nil
}

// deliveryHeadStart locates either an authored head's content or the start of
// the head the HTML parser will imply. Preamble bytes stay ahead of injection.
func deliveryHeadStart(source []byte) (int, error) {
	offset := 0
	controlStart := -1
	if len(source) >= 3 && source[0] == 0xef && source[1] == 0xbb && source[2] == 0xbf {
		offset = 3
	}
	for offset < len(source) {
		start := nextDeliveryHeadMarkup(source, offset, len(source))
		if start < 0 {
			nonSpace, firstControl, unsafeOutput := deliveryHeadGap(source, offset, len(source))
			if unsafeOutput {
				return 0, fmt.Errorf("%w: dynamic output in browser-effective head data", ErrUnsafeHeadOrder)
			}
			if controlStart < 0 && firstControl >= 0 {
				controlStart = firstControl
			}
			if nonSpace >= 0 {
				return deliveryHeadBoundary(controlStart, nonSpace), nil
			}
			return deliveryHeadBoundary(controlStart, len(source)), nil
		}
		nonSpace, firstControl, unsafeOutput := deliveryHeadGap(source, offset, start)
		if unsafeOutput {
			return 0, fmt.Errorf("%w: dynamic output in browser-effective head data", ErrUnsafeHeadOrder)
		}
		if controlStart < 0 && firstControl >= 0 {
			controlStart = firstControl
		}
		if nonSpace >= 0 {
			return deliveryHeadBoundary(controlStart, nonSpace), nil
		}
		if bytes.HasPrefix(source[start:], []byte("<!--")) {
			end := bytes.Index(source[start+4:], []byte("-->"))
			if end < 0 {
				return deliveryHeadBoundary(controlStart, start), nil
			}
			offset = start + 4 + end + 3
			continue
		}
		index := start + 1
		if index < len(source) && (source[index] == '!' || source[index] == '?') {
			next := skipDeliveryTag(source, index+1)
			if next == len(source) && (len(source) == 0 || source[len(source)-1] != '>') {
				return deliveryHeadBoundary(controlStart, start), nil
			}
			offset = next
			continue
		}
		closing := false
		if index < len(source) && source[index] == '/' {
			closing = true
			index++
		}
		if index >= len(source) || !asciiTagNameStart(source[index]) {
			return deliveryHeadBoundary(controlStart, start), nil
		}
		nameStart := index
		for index < len(source) && asciiTagNamePart(source[index]) {
			index++
		}
		name := strings.ToLower(string(source[nameStart:index]))
		next := skipDeliveryTag(source, index)
		if next == len(source) && (len(source) == 0 || source[len(source)-1] != '>') {
			return deliveryHeadBoundary(controlStart, start), nil
		}
		if closing {
			switch name {
			case "head", "body", "html", "br":
				return deliveryHeadBoundary(controlStart, start), nil
			default:
				offset = next
				continue
			}
		}
		if name == "html" {
			offset = next
			continue
		}
		if name == "head" {
			return deliveryHeadBoundary(controlStart, next), nil
		}
		return deliveryHeadBoundary(controlStart, start), nil
	}
	return deliveryHeadBoundary(controlStart, len(source)), nil
}

func deliveryHeadBoundary(controlStart, boundary int) int {
	if controlStart >= 0 {
		return controlStart
	}
	return boundary
}

// deliveryEffectiveHeadEnd follows browser-effective head termination. Body
// content cannot be allowed to hide a later base that would capture /jit URLs.
func deliveryEffectiveHeadEnd(source []byte, contentStart int) (int, error) {
	templateDepth := 0
	templateStart := -1
	for offset := contentStart; offset < len(source); {
		start := nextDeliveryHeadMarkup(source, offset, len(source))
		if start < 0 {
			if templateDepth > 0 {
				return templateStart, nil
			}
			nonSpace, _, unsafeOutput := deliveryHeadGap(source, offset, len(source))
			if unsafeOutput {
				return 0, fmt.Errorf("%w: dynamic output in browser-effective head data", ErrUnsafeHeadOrder)
			}
			if nonSpace >= 0 {
				return nonSpace, nil
			}
			return len(source), nil
		}
		if templateDepth == 0 {
			nonSpace, _, unsafeOutput := deliveryHeadGap(source, offset, start)
			if unsafeOutput {
				return 0, fmt.Errorf("%w: dynamic output in browser-effective head data", ErrUnsafeHeadOrder)
			}
			if nonSpace >= 0 {
				return nonSpace, nil
			}
		}
		if bytes.HasPrefix(source[start:], []byte("<!--")) {
			end := bytes.Index(source[start+4:], []byte("-->"))
			if end < 0 {
				if templateDepth > 0 {
					return templateStart, nil
				}
				return start, nil
			}
			offset = start + 4 + end + 3
			continue
		}
		index := start + 1
		if index < len(source) && (source[index] == '!' || source[index] == '?') {
			next := skipDeliveryTag(source, index+1)
			if next == len(source) && (len(source) == 0 || source[len(source)-1] != '>') {
				if templateDepth > 0 {
					return templateStart, nil
				}
				return start, nil
			}
			offset = next
			continue
		}
		closing := false
		if index < len(source) && source[index] == '/' {
			closing = true
			index++
		}
		if index >= len(source) || !asciiTagNameStart(source[index]) {
			if templateDepth > 0 {
				offset = start + 1
				continue
			}
			return start, nil
		}
		nameStart := index
		for index < len(source) && asciiTagNamePart(source[index]) {
			index++
		}
		name := strings.ToLower(string(source[nameStart:index]))
		next := skipDeliveryTag(source, index)
		if next == len(source) && (len(source) == 0 || source[len(source)-1] != '>') {
			if templateDepth > 0 {
				return templateStart, nil
			}
			return start, nil
		}
		if closing {
			if name == "template" && templateDepth > 0 {
				templateDepth--
				if templateDepth == 0 {
					templateStart = -1
				}
			} else if templateDepth == 0 {
				switch name {
				case "head", "body", "html", "br":
					return start, nil
				}
			}
			offset = next
			continue
		}
		if templateDepth == 0 && name != "html" && name != "head" && !deliveryHeadElement(name) {
			return start, nil
		}
		if name == "template" {
			if templateDepth == 0 {
				templateStart = start
			}
			templateDepth++
		}
		if deliveryHeadRawTextElement(name) {
			rawNext, closed := skipRawTextClosed(source, next, name)
			if !closed {
				if templateDepth > 0 {
					return templateStart, nil
				}
				return start, nil
			}
			next = rawNext
		}
		offset = next
	}
	if templateDepth > 0 {
		return templateStart, nil
	}
	return len(source), nil
}

func deliveryHeadElement(name string) bool {
	switch name {
	case "base", "basefont", "bgsound", "link", "meta", "noframes", "noscript", "script", "style", "template", "title":
		return true
	default:
		return false
	}
}

func deliveryHeadRawTextElement(name string) bool {
	switch name {
	case "noframes", "noscript", "script", "style", "title":
		return true
	default:
		return false
	}
}

type deliveryHeadControlKind uint8

const (
	deliveryHeadControlNeutral deliveryHeadControlKind = iota
	deliveryHeadControlOpen
	deliveryHeadControlBranch
	deliveryHeadControlEnd
)

func deliveryHeadControlToken(source []byte, index, end int) (int, deliveryHeadControlKind, bool) {
	if index+1 >= end || source[index] != '{' || source[index+1] != '{' {
		return index, deliveryHeadControlNeutral, false
	}
	close := bytes.Index(source[index+2:end], []byte("}}"))
	if close < 0 {
		return index, deliveryHeadControlNeutral, false
	}
	next := index + 2 + close + 2
	fields := strings.Fields(string(source[index+2 : next-2]))
	if len(fields) == 0 {
		return next, deliveryHeadControlNeutral, true
	}
	switch fields[0] {
	case "if", "for":
		return next, deliveryHeadControlOpen, true
	case "else", "elseif":
		return next, deliveryHeadControlBranch, true
	case "end":
		return next, deliveryHeadControlEnd, true
	case "let":
		return next, deliveryHeadControlNeutral, true
	default:
		return index, deliveryHeadControlNeutral, false
	}
}

// deliveryHeadGap treats only template control statements as structural
// whitespace. Arbitrary output at top-level head data is unsafe: it may render
// empty and leave a later CSP/base active, or render text and close the head.
func deliveryHeadGap(source []byte, start, end int) (nonSpace, firstControl int, unsafeOutput bool) {
	firstControl = -1
	for index := start; index < end; {
		if htmlSpace(source[index]) {
			index++
			continue
		}
		if next, _, ok := deliveryHeadControlToken(source, index, end); ok {
			if firstControl < 0 {
				firstControl = index
			}
			index = next
			continue
		}
		if index+1 < end && source[index] == '{' && source[index+1] == '{' {
			return index, firstControl, true
		}
		return index, firstControl, false
	}
	return -1, firstControl, false
}

func nextDeliveryHeadMarkup(source []byte, start, end int) int {
	for index := start; index < end; {
		if source[index] == '<' {
			return index
		}
		if next, _, ok := deliveryHeadControlToken(source, index, end); ok {
			index = next
			continue
		}
		index++
	}
	return -1
}

func deliveryHeadControlDepth(source []byte, start, end, depth int) int {
	for index := start; index < end; {
		if next, kind, ok := deliveryHeadControlToken(source, index, end); ok {
			switch kind {
			case deliveryHeadControlOpen:
				depth++
			case deliveryHeadControlEnd:
				if depth > 0 {
					depth--
				}
			}
			index = next
			continue
		}
		index++
	}
	return depth
}

func deliveryTemplateTokenEnd(source []byte, index, end int) (int, bool) {
	if index+1 >= end || source[index] != '{' || source[index+1] != '{' {
		return index, false
	}
	close := bytes.Index(source[index+2:end], []byte("}}"))
	if close < 0 {
		return index, false
	}
	return index + 2 + close + 2, true
}

func nextDeliveryMarkupAnyToken(source []byte, start, end int) int {
	for index := start; index < end; {
		if source[index] == '<' {
			return index
		}
		if next, ok := deliveryTemplateTokenEnd(source, index, end); ok {
			index = next
			continue
		}
		index++
	}
	return -1
}

func validateDeliveryHeadTemplateContexts(source []byte, end int) error {
	return validateDeliveryTemplateStructure(source, 0, end, false)
}

// validateDeliveryTemplateStructure keeps server-side control blocks from
// crossing an HTML-parser boundary. A locally balanced control inside title,
// an attribute, or template content is harmless; one that consumes a closing
// quote/tag can trap all staged scripts in an inert or conditional region.
func validateDeliveryTemplateStructure(source []byte, start, end int, opaqueWhole bool) error {
	if opaqueWhole {
		if err := validateDeliveryOpaqueTokens(source, start, end); err != nil {
			return err
		}
	}
	for offset := start; offset < end; {
		markup := nextDeliveryMarkupAnyToken(source, offset, end)
		if markup < 0 {
			return nil
		}
		if bytes.HasPrefix(source[markup:end], []byte("<!--")) {
			close := bytes.Index(source[markup+4:end], []byte("-->"))
			if close < 0 {
				return fmt.Errorf("%w: unterminated comment in browser-effective head", ErrUnsafeHeadOrder)
			}
			next := markup + 4 + close + 3
			if err := validateDeliveryOpaqueTokens(source, markup, next); err != nil {
				return err
			}
			offset = next
			continue
		}
		index := markup + 1
		if index < end && (source[index] == '!' || source[index] == '?') {
			next := skipDeliveryTag(source, index+1)
			if next > end {
				next = end
			}
			if err := validateDeliveryOpaqueTokens(source, markup, next); err != nil {
				return err
			}
			offset = next
			continue
		}
		closing := false
		if index < end && source[index] == '/' {
			closing = true
			index++
		}
		if index >= end || !asciiTagNameStart(source[index]) {
			offset = markup + 1
			continue
		}
		nameStart := index
		for index < end && asciiTagNamePart(source[index]) {
			index++
		}
		name := strings.ToLower(string(source[nameStart:index]))
		next := skipDeliveryTag(source, index)
		if next > end {
			next = end
		}
		if err := validateDeliveryOpaqueTokens(source, markup, next); err != nil {
			return err
		}
		if closing {
			offset = next
			continue
		}
		if name == "template" {
			closeStart, closeEnd, ok := deliveryTemplateClose(source, next, end)
			if !ok {
				return fmt.Errorf("%w: unterminated template in browser-effective head", ErrUnsafeHeadOrder)
			}
			if err := validateDeliveryTemplateStructure(source, next, closeStart, true); err != nil {
				return err
			}
			if err := validateDeliveryOpaqueTokens(source, closeStart, closeEnd); err != nil {
				return err
			}
			offset = closeEnd
			continue
		}
		if deliveryHeadRawTextElement(name) {
			closeStart, closeEnd, ok := deliveryRawTextClose(source, next, end, name)
			if !ok {
				return fmt.Errorf("%w: unterminated %s in browser-effective head", ErrUnsafeHeadOrder, name)
			}
			if err := validateDeliveryOpaqueTokens(source, next, closeStart); err != nil {
				return err
			}
			if err := validateDeliveryOpaqueTokens(source, closeStart, closeEnd); err != nil {
				return err
			}
			offset = closeEnd
			continue
		}
		offset = next
	}
	return nil
}

func deliveryTemplateClose(source []byte, offset, end int) (int, int, bool) {
	depth := 1
	for offset < end {
		start := nextDeliveryMarkupAnyToken(source, offset, end)
		if start < 0 {
			return 0, 0, false
		}
		if bytes.HasPrefix(source[start:end], []byte("<!--")) {
			close := bytes.Index(source[start+4:end], []byte("-->"))
			if close < 0 {
				return 0, 0, false
			}
			offset = start + 4 + close + 3
			continue
		}
		index := start + 1
		closing := false
		if index < end && source[index] == '/' {
			closing = true
			index++
		}
		if index >= end || !asciiTagNameStart(source[index]) {
			offset = start + 1
			continue
		}
		nameStart := index
		for index < end && asciiTagNamePart(source[index]) {
			index++
		}
		name := strings.ToLower(string(source[nameStart:index]))
		next := skipDeliveryTag(source, index)
		if next > end {
			return 0, 0, false
		}
		if name == "template" {
			if closing {
				depth--
				if depth == 0 {
					return start, next, true
				}
			} else {
				depth++
			}
		} else if !closing && deliveryTemplateRawTextElement(name) {
			if name == "plaintext" {
				return 0, 0, false
			}
			_, rawEnd, ok := deliveryRawTextClose(source, next, end, name)
			if !ok {
				return 0, 0, false
			}
			next = rawEnd
		}
		offset = next
	}
	return 0, 0, false
}

func deliveryTemplateRawTextElement(name string) bool {
	return name == "noscript" || rawTextElement(name)
}

func deliveryRawTextClose(source []byte, offset, end int, tagName string) (int, int, bool) {
	for offset < end {
		start := nextDeliveryMarkupAnyToken(source, offset, end)
		if start < 0 {
			return 0, 0, false
		}
		nameStart := start + 2
		nameEnd := nameStart + len(tagName)
		if start+2 <= end && nameEnd <= end && source[start+1] == '/' &&
			strings.EqualFold(string(source[nameStart:nameEnd]), tagName) &&
			(nameEnd == end || htmlSpace(source[nameEnd]) || source[nameEnd] == '>') {
			next := skipDeliveryTag(source, nameEnd)
			if next <= end {
				return start, next, true
			}
			return 0, 0, false
		}
		offset = start + 1
	}
	return 0, 0, false
}

func validateDeliveryOpaqueTokens(source []byte, start, end int) error {
	depth := 0
	for offset := start; offset < end; {
		relative := bytes.Index(source[offset:end], []byte("{{"))
		if relative < 0 {
			break
		}
		index := offset + relative
		next, ok := deliveryTemplateTokenEnd(source, index, end)
		if !ok {
			return fmt.Errorf("%w: unterminated template token in an opaque head region", ErrUnsafeHeadOrder)
		}
		if controlNext, kind, control := deliveryHeadControlToken(source, index, end); control {
			next = controlNext
			switch kind {
			case deliveryHeadControlOpen:
				depth++
			case deliveryHeadControlBranch:
				if depth == 0 {
					return fmt.Errorf("%w: template control crosses an opaque head boundary", ErrUnsafeHeadOrder)
				}
			case deliveryHeadControlEnd:
				if depth == 0 {
					return fmt.Errorf("%w: template control crosses an opaque head boundary", ErrUnsafeHeadOrder)
				}
				depth--
			}
		} else if deliveryHeadFragmentToken(source[index+2 : next-2]) {
			return fmt.Errorf("%w: raw template fragment in an opaque head region", ErrUnsafeHeadOrder)
		}
		offset = next
	}
	if depth != 0 {
		return fmt.Errorf("%w: template control crosses an opaque head boundary", ErrUnsafeHeadOrder)
	}
	return nil
}

func deliveryHeadFragmentToken(content []byte) bool {
	value := strings.TrimSpace(string(content))
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return false
	}
	command := fields[0]
	if command == "include" || command == "layout" || strings.HasPrefix(command, "@") {
		return true
	}
	if !strings.HasPrefix(value, "raw") {
		return false
	}
	rest := value[len("raw"):]
	if rest != "" && (rest[0] == '_' || rest[0] >= 'a' && rest[0] <= 'z' || rest[0] >= 'A' && rest[0] <= 'Z' || rest[0] >= '0' && rest[0] <= '9') {
		return false
	}
	rest = strings.TrimLeftFunc(rest, func(char rune) bool {
		return char == ' ' || char == '\t' || char == '\r' || char == '\n' || char == '\f'
	})
	return strings.HasPrefix(rest, "(")
}

type deliveryHeadAttribute struct {
	value    string
	hasValue bool
}

func deliveryHeadAttributes(source []byte, index, next int) (map[string]deliveryHeadAttribute, bool) {
	end := next
	if end > 0 && end <= len(source) && source[end-1] == '>' {
		end--
	}
	dynamic := bytes.Contains(source[index:end], []byte("{{"))
	attributes := make(map[string]deliveryHeadAttribute, 4)
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
		if attributeStart == index {
			index++
			continue
		}
		name := strings.ToLower(string(source[attributeStart:index]))
		attribute := deliveryHeadAttribute{}
		index = skipHTMLSpace(source, index)
		if index < end && source[index] == '=' {
			attribute.hasValue = true
			index++
			index = skipHTMLSpace(source, index)
			if index < end && (source[index] == '"' || source[index] == '\'') {
				quote := source[index]
				index++
				valueStart := index
				for index < end && source[index] != quote {
					index++
				}
				attribute.value = string(source[valueStart:index])
				if index < end {
					index++
				}
			} else {
				valueStart := index
				for index < end && !htmlSpace(source[index]) && source[index] != '>' {
					index++
				}
				attribute.value = string(source[valueStart:index])
			}
		}
		if _, exists := attributes[name]; !exists {
			attributes[name] = attribute
		}
	}
	return attributes, dynamic
}

func deliveryMetaSecurity(attributes map[string]deliveryHeadAttribute) (bool, bool) {
	_, charset := attributes["charset"]
	httpEquiv := strings.ToLower(strings.TrimSpace(stdhtml.UnescapeString(attributes["http-equiv"].value)))
	if httpEquiv == "content-security-policy" {
		return charset, true
	}
	if httpEquiv == "content-type" {
		content := strings.ToLower(stdhtml.UnescapeString(attributes["content"].value))
		charset = charset || strings.Contains(content, "charset=")
	}
	return charset, false
}

// hasRuntimeMarkerAttribute performs a lenient semantic start-tag scan for
// reserved engine delivery markers. It ignores comments, raw-text bodies,
// encoded text, and Kitwork {{ ... }} tokens inside a start tag; unrelated
// template syntax must not be interpreted as malformed HTML by the injection
// guard.
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
			attributeName := strings.ToLower(string(source[attributeStart:index]))
			index = skipHTMLSpace(source, index)
			if index >= end || source[index] != '=' {
				if reservedDeliveryMarker(name, attributeName, "", false) {
					return true
				}
				continue
			}
			index++
			index = skipHTMLSpace(source, index)
			if index >= end {
				if reservedDeliveryMarker(name, attributeName, "", true) {
					return true
				}
				break
			}
			value := ""
			if source[index] == '"' || source[index] == '\'' {
				quote := source[index]
				index++
				valueStart := index
				for index < end && source[index] != quote {
					index++
				}
				value = string(source[valueStart:index])
				if index < end {
					index++
				}
				if reservedDeliveryMarker(name, attributeName, value, true) {
					return true
				}
				continue
			}
			valueStart := index
			for index < end && !htmlSpace(source[index]) && source[index] != '>' {
				index++
			}
			value = string(source[valueStart:index])
			if reservedDeliveryMarker(name, attributeName, value, true) {
				return true
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

func reservedDeliveryMarker(tagName, attributeName, value string, hasValue bool) bool {
	switch attributeName {
	case "data-kitwork-runtime", "data-kitwork-hash":
		return true
	case "data-kitwork-jit":
		if tagName != "script" || !hasValue {
			return false
		}
		role := strings.ToLower(strings.TrimSpace(stdhtml.UnescapeString(value)))
		switch JITRole(role) {
		case JITRoleRuntime, JITRoleHydrate, JITRoleGraph, JITRoleService, JITRoleComponent, JITRoleComponents:
			return true
		}
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

func parseComponentSpec(raw string) (string, string, error) {
	decoded := stdhtml.UnescapeString(raw)
	spec := trimECMAScriptSpace(decoded)
	if spec == "" {
		return "", "", fmt.Errorf("empty data-kit-component value")
	}
	name := spec
	version := ""
	if separator := strings.IndexByte(spec, '@'); separator >= 0 {
		inlineSpec := trimLeftECMAScriptSpace(decoded)
		separator = strings.IndexByte(inlineSpec, '@')
		name = inlineSpec[:separator]
		version = inlineSpec[separator+1:]
		if !validExactSemVer(version) {
			return "", "", fmt.Errorf("invalid inline component version: requires an exact SemVer, got %q", version)
		}
	}
	if !componentNamePattern.MatchString(name) || blockedComponentNames[name] {
		return "", "", fmt.Errorf("invalid component name %q", name)
	}
	return name, version, nil
}

func parseExactVersion(raw string) (string, error) {
	version := trimECMAScriptSpace(stdhtml.UnescapeString(raw))
	if !validExactSemVer(version) {
		return "", fmt.Errorf("requires an exact SemVer, got %q", trimECMAScriptSpace(stdhtml.UnescapeString(raw)))
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

func asciiLetter(character byte) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
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
