package richtext

import (
	"html"
	"strconv"
	"strings"
)

// The tree builder reads the tokens into the grammar's content model. Every element of the source is
// mapped to what it is in the grammar (role) and the content goes where the model lets it: text
// outside any block opens a paragraph, a list holds items and nothing else, a quote holds paragraphs,
// a code block holds text and nothing else. A stack of open elements, capped at maximumDepth, tracks
// what an end tag closes; the paragraph kind, its alignment, the place (document, quote, list), whether
// text is code and whether content is dropped are carried by each frame, and the marks by counters, so
// every token costs at most one walk down the capped stack.

// maximumDepth caps the stack of open elements; content nested deeper is dropped.
const maximumDepth = 256

// blockKind is a block of the grammar.
type blockKind uint8

const (
	paragraphBlock   blockKind = iota // p
	headingBlock                      // h2–h6, its level in block.level
	quoteBlock                        // blockquote
	listBlock                         // ul
	orderedListBlock                  // ol
	codeBlock                         // pre holding one code: text only, its white space as written
	ruleBlock                         // hr, after a block
)

// alignment is how a paragraph or a heading is aligned: left (no class: the start of the line), centre
// (align-center) or right (align-right: text-align end, which mirrors on a page written right to left).
type alignment uint8

const (
	alignLeft alignment = iota
	alignCenter
	alignRight
)

// block is one block of the document. A paragraph and a heading hold line; a quote holds its
// paragraphs in parts, a list its items; a code block holds code; a rule holds nothing.
type block struct {
	kind  blockKind
	level byte      // a heading's level, 2 to 6
	align alignment // a paragraph's or a heading's
	line  []run
	parts []part
	code  string
}

// part is a paragraph of a quote, with its alignment, or an item of a list (always left).
type part struct {
	line  []run
	align alignment
}

// marks are the marks a run carries. A link is outside them all, and they nest in this order, the first
// outermost: a > strong > em > u > s > code.
type marks uint8

const (
	strongMark marks = 1 << iota
	emphasisMark
	underlineMark
	strikeMark
	codeMark
	markCount = 5
)

// run is text with the marks it carries, or a line break.
type run struct {
	text      string
	lineBreak bool
	href      string // the address of the link around it, "" when there is none
	marks     marks
}

// role is what an element of the source is in the grammar.
type role uint8

const (
	unwrapRole       role = iota // span, font, mark, sub, sup, unknown and custom tags: the text stays, the tag goes
	paragraphRole                // p, and div, section, td… read as p
	headingRole                  // h1–h6: h2–h6, h1 is h2
	preformattedRole             // pre, listing: a code block in the document; in a list or a quote, code in the line
	quoteRole                    // blockquote
	listRole                     // ul, menu
	orderedListRole              // ol
	itemRole                     // li
	strongRole                   // strong, b
	normalWeightRole             // a strong or b whose style sets the weight back to normal
	emphasisRole                 // em, i
	underlineRole                // u, ins
	strikeRole                   // s, del, strike
	codeRole                     // code, kbd, samp, tt
	linkRole                     // a
	inertRole                    // a mark or a link inside a code block: it counts for nothing
	dropRole                     // form, object, select, button: gone with what they hold
	breakRole                    // br
	ruleRole                     // hr: kept after a block of the document; elsewhere the paragraph ends
	voidRole                     // img, input, meta, link, base, embed…: gone, they hold nothing
)

var roles = map[string]role{
	"p": paragraphRole, "div": paragraphRole, "section": paragraphRole, "article": paragraphRole,
	"aside": paragraphRole, "header": paragraphRole, "footer": paragraphRole, "main": paragraphRole,
	"nav": paragraphRole, "td": paragraphRole, "th": paragraphRole, "dd": paragraphRole,
	// the other blocks of HTML are read as div too, so the words of two blocks never run together
	"address": paragraphRole, "center": paragraphRole, "details": paragraphRole, "dialog": paragraphRole,
	"dl": paragraphRole, "dt": paragraphRole, "fieldset": paragraphRole, "figcaption": paragraphRole,
	"figure": paragraphRole, "hgroup": paragraphRole, "legend": paragraphRole, "summary": paragraphRole,
	"table": paragraphRole, "caption": paragraphRole, "tr": paragraphRole, "search": paragraphRole,
	"h1": headingRole, "h2": headingRole, "h3": headingRole, "h4": headingRole, "h5": headingRole, "h6": headingRole,
	"pre": preformattedRole, "listing": preformattedRole,
	"blockquote": quoteRole,
	"ul":         listRole, "menu": listRole, "ol": orderedListRole, "li": itemRole,
	"strong": strongRole, "b": strongRole, "em": emphasisRole, "i": emphasisRole,
	"u": underlineRole, "ins": underlineRole, "s": strikeRole, "del": strikeRole, "strike": strikeRole,
	"code": codeRole, "kbd": codeRole, "samp": codeRole, "tt": codeRole,
	"a":    linkRole,
	"form": dropRole, "object": dropRole, "select": dropRole, "button": dropRole,
	"br": breakRole, "hr": ruleRole,
	"img": voidRole, "image": voidRole, "input": voidRole, "meta": voidRole, "link": voidRole,
	"base": voidRole, "embed": voidRole, "area": voidRole, "col": voidRole, "param": voidRole,
	"source": voidRole, "track": voidRole, "wbr": voidRole, "keygen": voidRole, "basefont": voidRole,
	"bgsound": voidRole, "frame": voidRole,
}

// markOf is the mark an element of this role gives the text inside it, 0 for none.
func markOf(r role) marks {
	switch r {
	case strongRole:
		return strongMark
	case emphasisRole:
		return emphasisMark
	case underlineRole:
		return underlineMark
	case strikeRole:
		return strikeMark
	case codeRole:
		return codeMark
	}
	return 0
}

// pushes reports whether an element of this role goes on the stack of open elements.
func pushes(r role) bool {
	switch r {
	case unwrapRole, breakRole, ruleRole, voidRole:
		return false
	}
	return true
}

// blockRole reports whether elements of this role are blocks (HTML's "special" elements, for the
// rules that close an li or a dd).
func blockRole(r role) bool {
	switch r {
	case paragraphRole, headingRole, preformattedRole, quoteRole, listRole, orderedListRole, itemRole, dropRole:
		return true
	}
	return false
}

func headingName(name string) bool {
	return len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6'
}

// headingLevel is the level a heading of the source is in the grammar: h1 is h2 (the page's title is
// the only h1), h2 to h6 are themselves.
func headingLevel(name string) byte {
	if name == "h1" {
		return 2
	}
	return name[1] - '0'
}

func voidElement(name string) bool {
	switch roles[name] {
	case breakRole, ruleRole, voidRole:
		return true
	}
	return false
}

// place is where content goes: the document, a quote, or a list.
type place uint8

const (
	documentPlace place = iota
	quotePlace
	listPlace
)

// frame is an open element of the source and what holds inside it.
type frame struct {
	name string
	role role
	// closed: a mark whose end tag came while a block inside it was still open. It stays until the
	// blocks above it close, and counts for nothing.
	closed bool
	// container: this element made the place (the outermost quote or list, or a stray li), or the code
	// block (its pre).
	container bool
	// what holds inside the element, its own effect included
	kind         role      // the paragraph a text opens here: paragraphRole or headingRole
	level        byte      // the heading's level when kind is headingRole
	align        alignment // the alignment of a paragraph or a heading that opens here
	place        place
	preformatted bool // inside a pre in a list or a quote: its text is code, its line breaks br
	code         bool // inside a code block: text as written, an element counts for nothing but a new line
	dropped      bool
}

type builder struct {
	stack    []frame
	overflow int // elements opened past maximumDepth and not closed yet
	// marking counts, for each mark (by its bit), the open elements that give it
	marking [markCount]int
	href    string // the address of the open link, "" when none is open or its address was refused
	// lines counts the lines opened (paragraphs, headings, quote paragraphs, list items); linkLine is
	// the line the open link's first word went into, 0 before it has one. A link holds phrasing: it ends
	// with that line, so one a of the source is one a of the output, and an a left open over a thousand
	// paragraphs does not write its address a thousand times.
	lines    int
	linkLine int
	blocks   []block
	// open: the last paragraph (in the document), quote paragraph or list item takes phrasing.
	open bool
	// pendingBreak: a block ended inside a list item; the item's next content starts with a br.
	pendingBreak bool
	// strayList: the last block is a list a stray li opened, and nothing came after it.
	strayList bool
	// skipNewline: the token after a pre's start tag drops one leading line feed, as in a browser.
	skipNewline bool
	// code is the text of the open code block (the last block), set on it when its pre closes;
	// codeBreak: a block ended inside it, so its next text starts on a new line.
	code      []byte
	codeBreak bool
}

// build reads HTML into the grammar's blocks (untidied).
func build(source string) []block {
	t := tokenizer{source: source}
	var b builder
	for {
		tok, ok := t.next()
		if !ok {
			break
		}
		b.take(tok)
	}
	b.finish()
	return b.blocks
}

// take reads one token.
func (b *builder) take(tok token) {
	skip := b.skipNewline
	b.skipNewline = false
	switch tok.kind {
	case textToken:
		text := tok.text
		if skip {
			text = strings.TrimPrefix(text, "\n")
		}
		b.text(text)
	case startToken:
		b.start(tok)
	case endToken:
		b.end(tok.name)
	}
}

// finish closes every element still open at the end of the input.
func (b *builder) finish() {
	for len(b.stack) > 0 {
		b.pop()
	}
}

// state is the top frame: what holds where the next token goes.
func (b *builder) state() frame {
	if len(b.stack) == 0 {
		return frame{kind: paragraphRole}
	}
	return b.stack[len(b.stack)-1]
}

// start reads a start tag: what the element is in the grammar decides where its content goes.
func (b *builder) start(tok token) {
	r := roles[tok.name]
	if b.overflow > 0 {
		if pushes(r) {
			b.overflow++
		}
		return
	}
	if b.state().dropped {
		if pushes(r) {
			b.push(tok.name, dropRole)
		}
		return
	}
	if tok.raw {
		if tok.name == "xmp" || tok.name == "plaintext" { // blocks: the paragraph before them ends
			b.closeParagraphElement()
			b.boundary()
		}
		return
	}
	if pushes(r) && len(b.stack) >= maximumDepth {
		b.overflow++
		return
	}
	switch r {
	case breakRole:
		b.lineBreak()
	case ruleRole:
		b.closeParagraphElement()
		if state := b.state(); !state.code && state.place == documentPlace {
			b.closeLine()
			b.appendBlock(block{kind: ruleBlock})
		} else {
			b.boundary() // in a list, a quote or a code block, a rule is the end of a line
		}
	case paragraphRole, preformattedRole:
		switch tok.name {
		case "td", "th":
			b.closeNearest(func(f frame) bool { return f.name == "td" || f.name == "th" },
				func(f frame) bool { return f.name == "tr" || f.name == "table" })
		case "tr":
			b.closeNearest(func(f frame) bool { return f.name == "tr" }, func(f frame) bool { return f.name == "table" })
		case "caption": // closes nothing
		case "dd", "dt":
			b.closeParagraphElement()
			b.closeNearest(func(f frame) bool { return f.name == "dd" || f.name == "dt" }, special)
		default:
			b.closeParagraphElement()
		}
		b.boundary()
		if state := b.state(); r == preformattedRole && !state.code && state.place == documentPlace {
			b.appendBlock(block{kind: codeBlock}) // push makes the pre its code block's
			b.codeBreak = false
		}
		b.push(tok.name, r)
		b.aligned(tok)
		b.skipNewline = r == preformattedRole
	case headingRole:
		b.closeParagraphElement()
		if len(b.stack) > 0 && b.state().role == headingRole {
			b.pop() // a heading does not hold a heading
		}
		b.boundary()
		b.push(tok.name, r)
		b.aligned(tok)
	case quoteRole:
		b.closeParagraphElement()
		b.enterQuote()
		b.push(tok.name, r)
		b.aligned(tok)
	case listRole, orderedListRole:
		b.closeParagraphElement()
		b.enterList(r == orderedListRole)
		b.push(tok.name, r)
		b.aligned(tok)
	case itemRole:
		b.closeNearest(func(f frame) bool { return f.name == "li" }, special)
		b.closeParagraphElement()
		b.enterItem()
		b.push(tok.name, r)
		b.aligned(tok)
	case strongRole, emphasisRole, underlineRole, strikeRole, codeRole:
		switch {
		case b.state().code: // a code block holds text only
			b.push(tok.name, inertRole)
		case r == strongRole && tok.hasStyle && normalWeight(tok.style):
			// Google Docs wraps a whole paste in <b style="font-weight:normal">: it is not bold
			b.push(tok.name, normalWeightRole)
		default:
			b.mark(r, 1)
			b.push(tok.name, r)
		}
	case linkRole:
		if b.state().code {
			b.push(tok.name, inertRole)
			return
		}
		b.closeLink() // an a closes the a before it
		b.href, b.linkLine = "", 0
		if tok.hasHref {
			if address, ok := Link(tok.href); ok {
				b.href = address
			}
		}
		b.push(tok.name, r)
	case dropRole:
		if tok.name == "form" { // a block: the paragraph around it ends
			b.closeParagraphElement()
			b.boundary()
		}
		b.push(tok.name, r)
	}
}

// end reads an end tag: the nearest open element of its name closes, with what was opened inside it.
func (b *builder) end(name string) {
	r := roles[name]
	if b.overflow > 0 {
		if pushes(r) {
			b.overflow--
		}
		return
	}
	switch {
	case r == breakRole: // "</br>" is read as <br>, as browsers do
		b.lineBreak()
	case markOf(r) != 0 || r == linkRole:
		b.closeMark(name)
	case name == "p":
		// without an open p, a browser makes an empty one: the paragraph ends all the same
		if !b.closeParagraphElement() && !b.state().dropped {
			b.boundary()
		}
	case r == headingRole: // any h1–h6 end tag closes the open heading
		for k := len(b.stack) - 1; k >= 0; k-- {
			if f := b.stack[k]; !f.closed && headingName(f.name) {
				b.popTo(k)
				return
			}
		}
	case pushes(r):
		if k := b.nearest(name); k >= 0 {
			b.popTo(k)
		}
	}
}

// push opens an element, its frame carrying what holds inside it. Inside a code block an element
// changes nothing but what a dropped one drops: the block holds text only.
func (b *builder) push(name string, r role) {
	f := b.state()
	f.name, f.role, f.closed, f.container = name, r, false, false
	switch {
	case r == dropRole:
		f.dropped = true
	case f.code:
	case r == paragraphRole:
		f.kind = paragraphRole
	case r == headingRole:
		f.kind, f.level = headingRole, headingLevel(name)
	case r == preformattedRole:
		if f.place == documentPlace {
			f.code, f.container = true, true
		} else {
			f.kind, f.preformatted = paragraphRole, true
		}
	case r == quoteRole:
		if f.place == documentPlace {
			f.place, f.container = quotePlace, true
		}
	case r == listRole, r == orderedListRole, r == itemRole:
		if f.place == documentPlace {
			f.place, f.container = listPlace, true
		}
	}
	b.stack = append(b.stack, f)
}

// aligned gives the block element just opened the alignment its source writes, when it writes one:
// a paragraph or a heading opened inside it is aligned so, as CSS inherits text-align.
func (b *builder) aligned(tok token) {
	if a, ok := explicitAlignment(tok); ok {
		b.stack[len(b.stack)-1].align = a
	}
}

// pop closes the top element, then drops the closed marks it leaves on top.
func (b *builder) pop() {
	f := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	if !f.closed {
		b.closed(f)
	}
	for len(b.stack) > 0 && b.stack[len(b.stack)-1].closed {
		b.stack = b.stack[:len(b.stack)-1]
	}
}

// popTo closes the element at k and everything opened inside it.
func (b *builder) popTo(k int) {
	for len(b.stack) > k {
		b.pop()
	}
}

// carriageReturns makes CR LF and a lone CR one LF, as a browser's input stream does.
var carriageReturns = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// closed undoes what an element did when it opened.
func (b *builder) closed(f frame) {
	if f.code {
		switch {
		case f.container: // the code block's pre: what it held is the block's text
			code := string(b.code)
			if strings.IndexByte(code, '\r') >= 0 {
				// a carriage return written as a reference (&#13;) is decoded after read() made the
				// source's own CR LF and CR into LF; a browser reading the value again makes it LF, so
				// it is LF here, over the whole text: a CR and an LF in two tokens are one newline
				code = carriageReturns.Replace(code)
			}
			b.blocks[len(b.blocks)-1].code = code
			b.code, b.codeBreak = b.code[:0], false
		case f.role != dropRole && blockRole(f.role) || f.name == "form":
			b.boundary() // a block inside a code block: what follows starts on a new line
		}
		return
	}
	switch f.role {
	case strongRole, emphasisRole, underlineRole, strikeRole, codeRole:
		b.mark(f.role, -1)
	case linkRole:
		b.href, b.linkLine = "", 0
	case quoteRole:
		if f.container {
			b.closeLine()
		} else {
			b.boundary()
		}
	case listRole, orderedListRole, itemRole:
		// a list or an item ends (after a nested list, what follows in the outer item is a new item);
		// inside a quote they were read as paragraphs
		if f.place == listPlace {
			b.closeLine()
		} else {
			b.boundary()
		}
	case paragraphRole, preformattedRole, headingRole:
		b.boundary()
	case dropRole:
		if f.name == "form" && !b.state().dropped {
			b.boundary()
		}
	}
}

// mark counts an element of a mark's role opening (1) or closing (-1).
func (b *builder) mark(r role, n int) {
	m := markOf(r)
	for i := range b.marking {
		if m == 1<<i {
			b.marking[i] += n
		}
	}
}

// marks are the marks in effect: those of the open elements, code inside a pre in a list or a quote.
func (b *builder) marks() marks {
	var m marks
	for i, n := range b.marking {
		if n > 0 {
			m |= 1 << i
		}
	}
	if b.state().preformatted {
		m |= codeMark
	}
	return m
}

// closeMark reads the end tag of a mark or an a: the nearest open element of that name closes. When
// blocks it holds are still open, it closes alone and they stay (a browser's adoption agency).
func (b *builder) closeMark(name string) {
	k := b.nearest(name)
	switch {
	case k < 0:
	case k == len(b.stack)-1:
		b.pop()
	default:
		b.stack[k].closed = true
		b.closed(b.stack[k])
	}
}

// closeLink closes an open a before another a opens.
func (b *builder) closeLink() {
	for k := len(b.stack) - 1; k >= 0; k-- {
		if f := b.stack[k]; !f.closed && f.role == linkRole {
			if k == len(b.stack)-1 {
				b.pop()
			} else {
				b.stack[k].closed = true
				b.closed(f)
			}
			return
		}
	}
}

// nearest is the index of the nearest open element of a name, -1 when none is open.
func (b *builder) nearest(name string) int {
	for k := len(b.stack) - 1; k >= 0; k-- {
		if f := b.stack[k]; !f.closed && f.name == name {
			return k
		}
	}
	return -1
}

// closeNearest closes the nearest open element that matches, unless an element that stops the search
// comes first.
func (b *builder) closeNearest(match, stop func(frame) bool) {
	for k := len(b.stack) - 1; k >= 0; k-- {
		f := b.stack[k]
		if f.closed {
			continue
		}
		if match(f) {
			b.popTo(k)
			return
		}
		if stop(f) {
			return
		}
	}
}

// special stops the search an li, a dd or a dt makes for the one it closes: a block other than p,
// div and address.
func special(f frame) bool {
	return blockRole(f.role) && f.name != "p" && f.name != "div" && f.name != "address"
}

// closeParagraphElement closes an open p a block start tag ends (a browser's "close a p element", in
// button scope). It reports whether there was one.
func (b *builder) closeParagraphElement() bool {
	for k := len(b.stack) - 1; k >= 0; k-- {
		f := b.stack[k]
		if f.closed {
			continue
		}
		switch f.name {
		case "p":
			b.popTo(k)
			return true
		case "td", "th", "table", "caption", "button", "object":
			return false
		}
	}
	return false
}

// boundary ends the paragraph content is going into. Inside a list item the item goes on: its next
// content starts on a new line; inside a code block, its next text does.
func (b *builder) boundary() {
	switch state := b.state(); {
	case state.code:
		b.codeBreak = true
	case state.place == listPlace:
		if b.open {
			b.pendingBreak = true
		}
	default:
		b.closeLine()
	}
}

// closeLine ends the paragraph, quote paragraph or list item content is going into: what comes next
// opens a new one.
func (b *builder) closeLine() {
	b.open, b.pendingBreak = false, false
}

func (b *builder) enterQuote() {
	switch state := b.state(); {
	case state.code: // a code block holds text only
		b.boundary()
	case state.place == documentPlace:
		b.closeLine()
		b.appendBlock(block{kind: quoteBlock})
	default: // a quote in a quote or a list is read as its paragraphs
		b.boundary()
	}
}

func (b *builder) enterList(ordered bool) {
	switch state := b.state(); {
	case state.code:
		b.boundary()
	case state.place == documentPlace:
		b.closeLine()
		kind := listBlock
		if ordered {
			kind = orderedListBlock
		}
		b.appendBlock(block{kind: kind})
	case state.place == listPlace: // a nested list: its items join the outer list
		b.closeLine()
	default:
		b.boundary()
	}
}

func (b *builder) enterItem() {
	switch state := b.state(); {
	case state.code:
		b.boundary()
	case state.place == documentPlace: // a stray li: a list is made for it, or for the stray items before it
		b.closeLine()
		if !b.strayList {
			b.appendBlock(block{kind: listBlock})
		}
		b.strayList = true
	case state.place == listPlace:
		b.closeLine()
	default:
		b.boundary()
	}
}

func (b *builder) appendBlock(bl block) {
	b.blocks = append(b.blocks, bl)
	b.strayList = false
}

// line is the phrasing content goes into now, opened when none is: a paragraph or a heading with the
// alignment in effect, a quote paragraph with it too, a list item. A line opened after the open link's
// first word ends the link: its words stay, without it.
func (b *builder) line() *[]run {
	if !b.open {
		b.lines++
		if b.linkLine != 0 {
			b.href, b.linkLine = "", 0
		}
	}
	state := b.state()
	if state.place == documentPlace {
		if !b.open {
			bl := block{kind: paragraphBlock, align: state.align}
			if state.kind == headingRole {
				bl.kind, bl.level = headingBlock, state.level
			}
			b.appendBlock(bl)
			b.open = true
		}
		return &b.blocks[len(b.blocks)-1].line
	}
	container := &b.blocks[len(b.blocks)-1]
	if !b.open {
		p := part{}
		if container.kind == quoteBlock {
			p.align = state.align
		}
		container.parts = append(container.parts, p)
		b.open, b.pendingBreak = true, false
	}
	line := &container.parts[len(container.parts)-1].line
	if b.pendingBreak {
		*line = append(*line, b.marked(run{lineBreak: true}))
		b.pendingBreak = false
	}
	return line
}

// marked gives a run the marks in effect; the open link's first word gives it its line. A line break or
// white space is not a word: a line holding only those is dropped (tidy), and the link would go with it.
func (b *builder) marked(r run) run {
	if b.href != "" && b.linkLine == 0 && !r.lineBreak && !blank(r.text) {
		b.linkLine = b.lines
	}
	r.href, r.marks = b.href, b.marks()
	return r
}

func (b *builder) text(s string) {
	if b.overflow > 0 {
		return
	}
	state := b.state()
	if state.dropped {
		return
	}
	s = normalizeText(s)
	if state.code {
		b.codeText(s)
		return
	}
	if !state.preformatted {
		b.appendText(s)
		return
	}
	for {
		j := strings.IndexByte(s, '\n')
		if j < 0 {
			b.appendText(s)
			return
		}
		b.appendText(s[:j])
		b.lineBreak()
		s = s[j+1:]
	}
}

func (b *builder) appendText(s string) {
	if s == "" || (!b.open || b.pendingBreak) && blank(s) {
		return // white space opens nothing
	}
	line := b.line()
	*line = append(*line, b.marked(run{text: s}))
}

// codeText adds text to the open code block as it is written, on a new line when a block ended inside
// it since its last text.
func (b *builder) codeText(s string) {
	if s == "" {
		return
	}
	if b.codeBreak && len(b.code) > 0 && b.code[len(b.code)-1] != '\n' {
		b.code = append(b.code, '\n')
	}
	b.codeBreak = false
	b.code = append(b.code, s...)
}

func (b *builder) lineBreak() {
	if b.overflow > 0 || b.state().dropped {
		return
	}
	if b.state().code {
		b.codeText("\n") // a br in a code block is a newline
		return
	}
	line := b.line()
	*line = append(*line, b.marked(run{lineBreak: true}))
}

// styleValue is the value of the last declaration of property in a style attribute (its references
// decoded), trimmed, without !important, in lower case; false when none is written. Clean reads two
// properties of style and nothing else: font-weight on b and strong (normalWeight), text-align on a
// block (explicitAlignment).
func styleValue(style, property string) (string, bool) {
	value, found := "", false
	for _, declaration := range strings.Split(html.UnescapeString(style), ";") {
		name, v, ok := strings.Cut(declaration, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), property) {
			value, found = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "!important"))), true
		}
	}
	return value, found
}

// normalWeight reports whether a style attribute sets font-weight to normal, lighter or a weight
// under 600 (the last declaration wins, as in CSS).
func normalWeight(style string) bool {
	weight, _ := styleValue(style, "font-weight")
	switch weight {
	case "normal", "lighter":
		return true
	}
	n, err := strconv.Atoi(weight)
	return err == nil && n < 600
}

// alignAttribute are the elements whose align attribute a browser reads as text-align (HTML,
// "Rendering"): on a table it places the table, not its words.
var alignAttribute = map[string]bool{
	"p": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"tr": true, "td": true, "th": true,
}

// explicitAlignment is the alignment a block element of the source writes for itself, and whether it
// writes one, read the way CSS ranks them: its style's text-align, else the last align-center or
// align-right among its classes, else its align attribute (on the elements of alignAttribute), else
// centre for <center>. Read are: center (and middle in align) as centre; right (and end in style) as
// right; left, start and justify (justify is no alignment of the grammar) as left, which undoes an
// alignment around. Any other value is not read, so the alignment around holds, as it would for a value
// CSS drops or inherits.
func explicitAlignment(tok token) (alignment, bool) {
	if tok.hasStyle {
		if v, ok := styleValue(tok.style, "text-align"); ok {
			switch v {
			case "center":
				return alignCenter, true
			case "right", "end":
				return alignRight, true
			case "left", "start", "justify":
				return alignLeft, true
			}
		}
	}
	if tok.hasClass {
		a, found := alignLeft, false
		for _, name := range strings.FieldsFunc(decodeAttribute(tok.class), func(c rune) bool { return c < 0x80 && isSpace(byte(c)) }) {
			switch name {
			case AlignCenter:
				a, found = alignCenter, true
			case AlignRight:
				a, found = alignRight, true
			}
		}
		if found {
			return a, true
		}
	}
	if tok.hasAlign && alignAttribute[tok.name] {
		switch lowerASCII(decodeAttribute(tok.align)) {
		case "center", "middle":
			return alignCenter, true
		case "right":
			return alignRight, true
		case "left", "justify":
			return alignLeft, true
		}
	}
	if tok.name == "center" {
		return alignCenter, true
	}
	return alignLeft, false
}
