package highlight

import "strings"

// tokenizeHTML reads HTML the way a browser splits it, near enough for colour:
//
//   - <, </, >, /> and = are punctuation; element names are tags; attribute names
//     are attributes; attribute values are strings, quotes included.
//   - <!-- … --> and the bogus comments a browser makes of <!…>, <?…> and </…>
//     are comments; <!DOCTYPE …> is punctuation, a keyword and plain words.
//   - Character references (&amp; &#9733; &#x2615;, the semicolon required) are
//     constants, in text and inside attribute values alike.
//   - <script> holds javascript (json when its type is a JSON one, plain text for
//     a data block such as text/x-template), <style> holds css, and <textarea>
//     and <title> hold text and references but no tags. Each ends at its own end
//     tag, whatever the language inside says: "</script>" in a string ends it.
//
// It forgives what a browser forgives: an unterminated comment, value or tag runs
// to the end, and a '<' inside a tag ends that tag (<div class="a" <span>), so a
// snippet cut short still reads.
func tokenizeHTML(scan *scanner) { htmlDialect.read(scan) }

// markupDialect is one dialect of angle-bracket markup. html and ui share the
// reader below; a dialect says only where they differ.
type markupDialect struct {
	tagNameByte  func(byte) bool // continues an element name after its first ASCII letter
	textStops    string          // bytes that may start something other than plain text
	valueStops   string          // bytes that may start something other than value text
	references   bool            // &amp; &#9733; &#x2615; are character references
	declarations bool            // <!DOCTYPE>, <![CDATA[ ]]>, <?xml ?>, bogus comments
	abruptClose  bool            // <!--> and <!---> are whole comments (the HTML standard)

	// rawContent says what the content of an element is when it is not markup:
	// raw is false for an ordinary element; tokenize is nil for plain text.
	rawContent func(element, typeValue string) (tokenize tokenizer, raw bool)
	// expression names the language between {{ and }}; "" when {{ is text.
	expression string
	// attributeName emits one attribute name ending at end; nil emits it whole
	// as an attribute.
	attributeName func(scan *scanner, end int)
	// codeValue reports whether the value of the attribute named name is an
	// expression as a whole, handed to the expression language rather than
	// read as text; nil when no value is.
	codeValue func(name string) bool
}

var htmlDialect = &markupDialect{
	tagNameByte:  htmlTagNameByte,
	textStops:    "<&",
	valueStops:   "&",
	references:   true,
	declarations: true,
	abruptClose:  true,
	rawContent:   htmlRawContent,
}

// htmlTagNameByte is what the HTML standard lets continue a tag name, less '<'
// (which starts the next tag in a forgiving read).
func htmlTagNameByte(character byte) bool {
	return !isWhitespace(character) && character != '/' && character != '>' && character != '<'
}

// htmlInstructionNameByte is an attribute or target name byte inside <?xml … ?>,
// where '?' starts the closer.
func htmlInstructionNameByte(character byte) bool {
	return markupAttributeNameByte(character) && character != '?'
}

// markupAttributeNameByte is what may sit in an attribute name: anything but
// whitespace and the bytes that end a name or start a value. .ui keeps '(' ')'
// ':' '+' ',' '.' in its names (input:debounce(300), medium+hover:color), and so
// does this.
func markupAttributeNameByte(character byte) bool {
	switch character {
	case '/', '>', '=', '<', '"', '\'':
		return false
	}
	return !isWhitespace(character)
}

// htmlRawContent: <script> by its type, <style> as css, <textarea> and <title>
// as text with references. Languages are looked up in the registry when used,
// so no tokenizer refers to another at initialization.
func htmlRawContent(element, typeValue string) (tokenizer, bool) {
	switch {
	case strings.EqualFold(element, "script"):
		return lookUpTokenizer(htmlScriptLanguage(typeValue)), true
	case strings.EqualFold(element, "style"):
		return lookUpTokenizer("css"), true
	case strings.EqualFold(element, "textarea"), strings.EqualFold(element, "title"):
		return htmlCharacterData, true
	}
	return nil, false
}

// htmlScriptLanguage reads a <script type>: no type, a module or a JavaScript
// media type is javascript; JSON media types, importmap and speculationrules are
// json; anything else (text/x-template, text/plain) is a data block: "".
func htmlScriptLanguage(typeValue string) string {
	media := strings.TrimSpace(typeValue)
	if semicolon := strings.IndexByte(media, ';'); semicolon >= 0 {
		media = strings.TrimSpace(media[:semicolon])
	}
	media = strings.ToLower(media)
	switch {
	case media == "", media == "module", strings.Contains(media, "javascript"), strings.Contains(media, "ecmascript"):
		return "javascript"
	case strings.Contains(media, "typescript"):
		return "typescript"
	case media == "importmap", media == "speculationrules", strings.HasSuffix(media, "json"):
		return "json"
	}
	return ""
}

// lookUpTokenizer is the registered tokenizer of a language, nil for "" or a
// name nobody registered.
func lookUpTokenizer(language string) tokenizer {
	if language == "" {
		return nil
	}
	return registry[language]
}

// htmlCharacterData is the content of <textarea> and <title>: text whose
// character references still count, with no tags.
func htmlCharacterData(scan *scanner) {
	for loop := scan.loop(); loop.next(); {
		source, at := scan.source, scan.position
		if source[at] == '&' {
			emitCharacterReference(scan, len(source), Plain)
			continue
		}
		end := len(source)
		if found := strings.IndexByte(source[at:], '&'); found >= 0 {
			end = at + found
		}
		scan.emit(Plain, end)
	}
}

// read is the whole reader: runs of text, and whatever a stop byte starts.
func (dialect *markupDialect) read(scan *scanner) {
	for loop := scan.loop(); loop.next(); {
		source, at := scan.source, scan.position
		switch character := source[at]; {
		case character == '<':
			dialect.angle(scan)
		case character == '&' && dialect.references:
			emitCharacterReference(scan, len(source), Plain)
		case character == '{' && dialect.expression != "":
			dialect.braces(scan, len(source), Plain)
		default:
			end := len(source)
			if found := strings.IndexAny(source[at+1:], dialect.textStops); found >= 0 {
				end = at + 1 + found
			}
			scan.emit(Plain, end)
		}
	}
}

// angle reads what a '<' in text starts: a comment, a start or end tag, a
// declaration, or nothing (a '<' that is text).
func (dialect *markupDialect) angle(scan *scanner) {
	source, at := scan.source, scan.position
	next := byteAt(source, at+1)
	switch {
	case strings.HasPrefix(source[at:], "<!--"):
		scan.emit(Comment, dialect.commentEnd(source, at))
	case isASCIILetter(next):
		dialect.startTag(scan)
	case next == '/' && isASCIILetter(byteAt(source, at+2)):
		dialect.endTag(scan)
	case !dialect.declarations:
		scan.emitLength(Plain, 1)
	case next == '!':
		htmlDeclaration(scan)
	case next == '?' && isASCIILetter(byteAt(source, at+2)):
		// <?xml version="1.0"?>: read like a tag that closes with ?>.
		scan.emitLength(Punctuation, 2)
		scan.emit(Tag, scanWhile(source, scan.position, htmlInstructionNameByte))
		dialect.attributes(scan, true)
	case next == '/' && byteAt(source, at+2) == '>':
		scan.emitLength(Punctuation, 3) // </> : a browser drops it
	case next == '?' || next == '/':
		scan.emit(Comment, scanUntil(source, at+2, ">")) // a bogus comment
	default:
		scan.emitLength(Plain, 1)
	}
}

// commentEnd is where the comment opening at start ends: after "-->", or the end
// of the source when it never closes.
func (dialect *markupDialect) commentEnd(source string, start int) int {
	body := start + 4
	if dialect.abruptClose {
		if byteAt(source, body) == '>' {
			return body + 1
		}
		if strings.HasPrefix(source[body:], "->") {
			return body + 2
		}
	}
	return scanUntil(source, body, "-->")
}

// htmlDeclaration reads at "<!" that is not a comment: CDATA, a doctype, or a
// bogus comment through the next '>'.
func htmlDeclaration(scan *scanner) {
	source, at := scan.source, scan.position
	switch {
	case strings.HasPrefix(source[at:], "<![CDATA["):
		scan.emitLength(Punctuation, len("<![CDATA["))
		closer := strings.Index(source[scan.position:], "]]>")
		if closer < 0 {
			scan.emitRest(String)
			return
		}
		scan.emitLength(String, closer)
		scan.emitLength(Punctuation, len("]]>"))
	case len(source) >= at+9 && strings.EqualFold(source[at+2:at+9], "doctype"):
		scan.emitLength(Punctuation, 2)
		scan.emitLength(Keyword, 7)
		htmlDoctype(scan)
	default:
		scan.emit(Comment, scanUntil(source, at+2, ">"))
	}
}

// htmlDoctype reads the rest of a doctype through its '>': plain words
// (html, PUBLIC) and quoted identifiers as strings. A quote left open stops at
// the '>', as a browser stops it.
func htmlDoctype(scan *scanner) {
	for loop := scan.loop(); loop.next(); {
		source, at := scan.source, scan.position
		switch character := source[at]; {
		case character == '>':
			scan.emitLength(Punctuation, 1)
			return
		case isWhitespace(character):
			scan.emit(Plain, scanWhitespace(source, at))
		case character == '"' || character == '\'':
			end := at + 1
			for end < len(source) && source[end] != character && source[end] != '>' {
				end++
			}
			if byteAt(source, end) == character && end < len(source) {
				end++
			}
			scan.emit(String, end)
		default:
			scan.emit(Plain, scanWhile(source, at, func(character byte) bool {
				return !isWhitespace(character) && character != '>' && character != '"' && character != '\''
			}))
		}
	}
}

// startTag reads <name attributes…> and, for an element whose content is not
// markup (<script>, <style>), that content up to its end tag.
func (dialect *markupDialect) startTag(scan *scanner) {
	scan.emitLength(Punctuation, 1)
	nameStart := scan.position
	scan.emit(Tag, scanWhile(scan.source, nameStart+1, dialect.tagNameByte))
	name := scan.source[nameStart:scan.position]
	closer, typeValue := dialect.attributes(scan, false)
	if closer != ">" || dialect.rawContent == nil {
		return
	}
	tokenize, raw := dialect.rawContent(name, typeValue)
	if !raw {
		return
	}
	end := htmlEndTag(scan.source, scan.position, name)
	if tokenize == nil {
		scan.emit(Plain, end)
		return
	}
	scan.embed(tokenize, end)
}

// endTag reads </name …>. Attributes in an end tag mean nothing but are read
// like any others.
func (dialect *markupDialect) endTag(scan *scanner) {
	scan.emitLength(Punctuation, 2)
	scan.emit(Tag, scanWhile(scan.source, scan.position+1, dialect.tagNameByte))
	dialect.attributes(scan, false)
}

// htmlEndTag is where the content of a raw element named name ends: at the '<'
// of the first "</name" (any case) followed by whitespace, '/', '>' or the end,
// or at the end of the source. Each "</" is looked at once.
func htmlEndTag(source string, from int, name string) int {
	for index := from; index < len(source); {
		found := strings.Index(source[index:], "</")
		if found < 0 {
			break
		}
		at := index + found
		nameEnd := at + 2 + len(name)
		if nameEnd <= len(source) && strings.EqualFold(source[at+2:nameEnd], name) {
			if after := byteAt(source, nameEnd); nameEnd == len(source) || isWhitespace(after) || after == '/' || after == '>' {
				return at
			}
		}
		index = at + 2
	}
	return len(source)
}

// attributes reads the inside of a tag up to and including its closer: ">",
// "/>" (or "?>" for <?xml ?>). It returns that closer, "" when the tag runs to
// the end or into a new '<', and the value of the type attribute (for <script>).
func (dialect *markupDialect) attributes(scan *scanner, question bool) (closer, typeValue string) {
	for loop := scan.loop(); loop.next(); {
		source, at := scan.source, scan.position
		character := source[at]
		switch {
		case isWhitespace(character):
			scan.emit(Plain, scanWhitespace(source, at))
		case character == '>':
			scan.emitLength(Punctuation, 1)
			return ">", typeValue
		case character == '/' && byteAt(source, at+1) == '>':
			scan.emitLength(Punctuation, 2)
			return "/>", typeValue
		case question && character == '?' && byteAt(source, at+1) == '>':
			scan.emitLength(Punctuation, 2)
			return "?>", typeValue
		case character == '<':
			return "", typeValue
		case character == '/' || character == '=' || (question && character == '?'):
			scan.emitLength(Punctuation, 1)
		case character == '"' || character == '\'':
			dialect.value(scan, nil) // a quoted value with no name before it
		default:
			nameByte := markupAttributeNameByte
			if question {
				nameByte = htmlInstructionNameByte
			}
			nameEnd := scanWhile(source, at, nameByte)
			if dialect.attributeName != nil {
				dialect.attributeName(scan, nameEnd)
			} else {
				scan.emit(Attribute, nameEnd)
			}
			equals := scanWhitespace(source, nameEnd)
			if byteAt(source, equals) != '=' {
				continue
			}
			scan.emit(Plain, equals)
			scan.emitLength(Punctuation, 1)
			scan.emit(Plain, scanWhitespace(source, scan.position))
			valueStart := scan.position
			var code tokenizer
			if dialect.codeValue != nil && dialect.codeValue(source[at:nameEnd]) {
				code = lookUpTokenizer(dialect.expression)
			}
			dialect.value(scan, code)
			if strings.EqualFold(source[at:nameEnd], "type") {
				typeValue = strings.Trim(source[valueStart:scan.position], `"'`)
			}
		}
	}
	return "", typeValue
}

// value reads an attribute value: quoted (through its closing quote, across
// lines, with no escapes, as markup has none) or unquoted (up to whitespace or
// '>'). When code is not nil the value is an expression: what sits inside the
// quotes is handed to code, and only the quotes are string.
func (dialect *markupDialect) value(scan *scanner, code tokenizer) {
	source, at := scan.source, scan.position
	if at >= len(source) {
		return
	}
	quote := source[at]
	if quote != '"' && quote != '\'' {
		end := scanWhile(source, at, func(character byte) bool {
			return !isWhitespace(character) && character != '>'
		})
		if code != nil {
			scan.embed(code, end)
			return
		}
		dialect.valueBody(scan, end)
		return
	}
	closing := len(source)
	if found := strings.IndexByte(source[at+1:], quote); found >= 0 {
		closing = at + 1 + found
	}
	scan.emitLength(String, 1)
	if code != nil {
		scan.embed(code, closing)
	} else {
		dialect.valueBody(scan, closing)
	}
	scan.emit(String, closing+1)
}

// valueBody reads a value's text up to end: a string, broken by character
// references (html) or {{ expressions }} (ui).
func (dialect *markupDialect) valueBody(scan *scanner, end int) {
	for loop := scan.loop(); loop.next() && scan.position < end; {
		source, at := scan.source, scan.position
		stop := end
		if found := strings.IndexAny(source[at:end], dialect.valueStops); found >= 0 {
			stop = at + found
		}
		if stop > at {
			scan.emit(String, stop)
			continue
		}
		switch {
		case source[at] == '&' && dialect.references:
			emitCharacterReference(scan, end, String)
		case source[at] == '{' && dialect.expression != "":
			dialect.braces(scan, end, String)
		default:
			scan.emitLength(String, 1)
		}
	}
}

// emitCharacterReference emits the reference at the position as a constant, or
// the '&' alone as outside when none starts there. A reference ends by limit.
func emitCharacterReference(scan *scanner, limit int, outside Kind) {
	if end := scanCharacterReference(scan.source[:limit], scan.position); end > scan.position {
		scan.emit(Constant, end)
		return
	}
	scan.emitLength(outside, 1)
}

// scanCharacterReference reads &name;, &#digits; or &#xhex; at start, or
// returns start. The semicolon is required: a bare & is text.
func scanCharacterReference(source string, start int) int {
	index := start + 1
	if byteAt(source, index) == '#' {
		index++
		digit := isDecimalDigit
		if marker := byteAt(source, index); marker == 'x' || marker == 'X' {
			digit = isHexadecimalDigit
			index++
		}
		end := scanWhile(source, index, digit)
		if end == index || byteAt(source, end) != ';' {
			return start
		}
		return end + 1
	}
	if !isASCIILetter(byteAt(source, index)) {
		return start
	}
	end := scanWhile(source, index, func(character byte) bool { return isASCIILetter(character) || isDecimalDigit(character) })
	if byteAt(source, end) != ';' {
		return start
	}
	return end + 1
}
