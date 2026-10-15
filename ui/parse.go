// Package ui reads Kitwork UI sources (*.ui) and renders them.
//
// A .ui file describes an interface by intent: parts, order, meaning, data and behaviour. The engine
// decides how it is drawn; HTML is the first target. See docs/KITWORK_UI.md in the root repository.
//
// The reader is hand-written on the standard library (no HTML parser dependency): .ui is its own
// markup, close to HTML but not HTML, so a browser-grade HTML parser would accept the wrong things.
package ui

import (
	"fmt"
	"strings"
)

// Node is one piece of a parsed .ui file: an element or a run of text.
type Node struct {
	Tag      string // element name; "" for text
	Attrs    []Attr
	Children []*Node
	Text     string // text content (may contain {{ }})
	Line     int
	Column   int
	Bare     bool // written self-closing (<x />) or as a void element
	File     string
	// written is the tag as written when it is not all lowercase (<Box>): tag names do not tell case
	// apart, so it is read as its lowercase and the compiler warns once (house style, decision D)
	written string
}

// Attr is one attribute as written, in source order.
type Attr struct {
	Name  string
	Value string
	// Bare: written without a value (`required`). On a tag of Kitwork a true-or-false attribute written
	// bare reads as "true" (the compiler sets Value and warns); any other one bare is an error where it
	// is read, as is every input written bare at the call of a layout or a component.
	Bare   bool
	Line   int
	Column int
}

// IsText reports whether the node is a run of text.
func (n *Node) IsText() bool { return n.Tag == "" }

// Attr returns the value of the first attribute with this exact name.
func (n *Node) Attr(name string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// Elements returns the element children, skipping text that is only whitespace.
func (n *Node) Elements() []*Node {
	out := make([]*Node, 0, len(n.Children))
	for _, c := range n.Children {
		if c.IsText() {
			continue
		}
		out = append(out, c)
	}
	return out
}

// SignificantChildren drops whitespace-only text, keeping elements and real text in order.
func (n *Node) SignificantChildren() []*Node {
	out := make([]*Node, 0, len(n.Children))
	for _, c := range n.Children {
		if c.IsText() && strings.TrimSpace(c.Text) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Error is a problem found in a .ui source, pointing at the exact place.
type Error struct {
	File    string
	Line    int
	Column  int
	Message string
}

func (e *Error) Error() string {
	if e.File == "" {
		return fmt.Sprintf("%d:%d: %s", e.Line, e.Column, e.Message)
	}
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Message)
}

// voidTags are elements that never have children. A later </input> written by habit is accepted and
// dropped.
var voidTags = map[string]bool{"input": true}

// Parse reads a .ui source into its top-level nodes.
func Parse(file string, source []byte) ([]*Node, error) {
	// a line ends in LF, CRLF (what git writes on Windows under core.autocrlf) or CR alone: each is read
	// as one line break, as a browser's HTML input stream does, so a code block or a terminal's screen
	// is the same whatever wrote the file
	text := strings.ReplaceAll(strings.ReplaceAll(string(source), "\r\n", "\n"), "\r", "\n")
	p := &parser{file: file, src: text, line: 1, column: 1}
	root := &Node{Tag: "#root"}
	stack := []*Node{root}
	for !p.eof() {
		switch {
		case p.has("<!--"):
			if err := p.skipComment(); err != nil {
				return nil, err
			}
		case p.has("</"):
			line, column := p.line, p.column
			p.advance(2)
			name := p.readName()
			p.skipSpace()
			if !p.has(">") {
				return nil, p.errorAt(line, column, "thẻ đóng </"+name+" thiếu dấu >")
			}
			p.advance(1)
			tag := strings.ToLower(name) // </Box> closes <Box>, read as box
			if voidTags[tag] && stack[len(stack)-1].Tag != tag {
				continue // </input> after a void <input>: accepted, nothing to close
			}
			top := stack[len(stack)-1]
			if top.Tag != tag {
				if top.Tag == "#root" {
					return nil, p.errorAt(line, column, "thẻ đóng </"+name+"> không có thẻ mở")
				}
				return nil, p.errorAt(line, column, fmt.Sprintf("thẻ đóng </%s> không khớp thẻ mở <%s> ở dòng %d", name, top.Tag, top.Line))
			}
			stack = stack[:len(stack)-1]
		case p.has("<") && p.peekNameStart(1):
			node, selfClosed, err := p.readOpenTag()
			if err != nil {
				return nil, err
			}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, node)
			if !selfClosed && !voidTags[node.Tag] {
				stack = append(stack, node)
			} else {
				node.Bare = true
			}
		default:
			text, line, column, err := p.readText()
			if err != nil {
				return nil, err
			}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, &Node{Text: text, Line: line, Column: column, File: file})
		}
	}
	if len(stack) > 1 {
		open := stack[len(stack)-1]
		return nil, p.errorAt(open.Line, open.Column, "thẻ <"+open.Tag+"> chưa được đóng")
	}
	return root.Children, nil
}

type parser struct {
	file   string
	src    string
	pos    int
	line   int
	column int
}

func (p *parser) eof() bool               { return p.pos >= len(p.src) }
func (p *parser) has(s string) bool       { return strings.HasPrefix(p.src[p.pos:], s) }
func (p *parser) errorf(msg string) error { return p.errorAt(p.line, p.column, msg) }

func (p *parser) errorAt(line, column int, msg string) error {
	return &Error{File: p.file, Line: line, Column: column, Message: msg}
}

func (p *parser) advance(n int) {
	for i := 0; i < n && p.pos < len(p.src); i++ {
		if p.src[p.pos] == '\n' {
			p.line++
			p.column = 1
		} else if p.src[p.pos]&0xC0 != 0x80 { // count runes, not bytes, for columns
			p.column++
		}
		p.pos++
	}
}

func (p *parser) peekNameStart(offset int) bool {
	i := p.pos + offset
	// an uppercase start is read as a tag too, so the compiler can name the mistake (<Box>)
	return i < len(p.src) && (p.src[i] >= 'a' && p.src[i] <= 'z' || p.src[i] >= 'A' && p.src[i] <= 'Z')
}

func (p *parser) skipSpace() {
	for !p.eof() {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.advance(1)
		default:
			return
		}
	}
}

func (p *parser) skipComment() error {
	line, column := p.line, p.column
	end := strings.Index(p.src[p.pos+4:], "-->")
	if end < 0 {
		return p.errorAt(line, column, "chú thích <!-- chưa được đóng bằng -->")
	}
	p.advance(4 + end + 3)
	return nil
}

// readName reads a tag name: lowercase letters, digits, '-' and '.'.
func (p *parser) readName() string {
	start := p.pos
	for !p.eof() {
		c := p.src[p.pos]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
			p.advance(1)
			continue
		}
		if c >= 'A' && c <= 'Z' {
			p.advance(1) // kept so the compiler can name the mistake; tags are lowercase
			continue
		}
		break
	}
	return p.src[start:p.pos]
}

func attributeNameByte(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '=', '>', '/', '"', '\'':
		return false
	}
	return true
}

func (p *parser) readOpenTag() (*Node, bool, error) {
	line, column := p.line, p.column
	p.advance(1) // <
	name := p.readName()
	node := &Node{Tag: name, Line: line, Column: column, File: p.file}
	if lower := strings.ToLower(name); lower != name {
		// tag names do not tell case apart: <Box> is box, and the compiler warns (compile.go lint)
		node.Tag, node.written = lower, name
	}
	for {
		p.skipSpace()
		if p.eof() {
			return nil, false, p.errorAt(line, column, "thẻ <"+name+"> chưa được đóng bằng >")
		}
		if p.has("/>") {
			p.advance(2)
			return node, true, nil
		}
		if p.has(">") {
			p.advance(1)
			return node, false, nil
		}
		aline, acolumn := p.line, p.column
		start := p.pos
		for !p.eof() && attributeNameByte(p.src[p.pos]) {
			// keep {{ }} and parentheses of modifiers inside the name: input:debounce(300)
			p.advance(1)
		}
		attrName := p.src[start:p.pos]
		if attrName == "" {
			return nil, false, p.errorAt(aline, acolumn, fmt.Sprintf("ký tự không hợp lệ %q trong thẻ <%s>", p.src[p.pos], name))
		}
		p.skipSpace()
		if !p.has("=") {
			// what a bare attribute means is the tag's to say: `required` on an input is required="true"
			// (compile.go lint), `open` at the call of a layout is an input without a value
			node.Attrs = append(node.Attrs, Attr{Name: attrName, Bare: true, Line: aline, Column: acolumn})
			continue
		}
		p.advance(1)
		p.skipSpace()
		if p.eof() || (p.src[p.pos] != '"' && p.src[p.pos] != '\'') {
			return nil, false, p.errorAt(p.line, p.column, "giá trị của "+attrName+" phải đặt trong dấu nháy")
		}
		quote := p.src[p.pos]
		p.advance(1)
		vstart := p.pos
		for !p.eof() && p.src[p.pos] != quote {
			p.advance(1)
		}
		if p.eof() {
			return nil, false, p.errorAt(aline, acolumn, "giá trị của "+attrName+" thiếu dấu nháy đóng")
		}
		value := p.src[vstart:p.pos]
		p.advance(1)
		node.Attrs = append(node.Attrs, Attr{Name: attrName, Value: value, Line: aline, Column: acolumn})
	}
}

// readText reads until the next tag, treating {{ … }} as opaque so a `<` inside an expression
// (`{{ a < b }}`) is not taken for a tag.
func (p *parser) readText() (string, int, int, error) {
	line, column := p.line, p.column
	var b strings.Builder
	for !p.eof() {
		if p.has("{{") {
			end := closingBraces(p.src[p.pos+2:])
			if end < 0 {
				return "", 0, 0, p.errorf("biểu thức {{ chưa được đóng bằng }}")
			}
			b.WriteString(p.src[p.pos : p.pos+2+end+2])
			p.advance(2 + end + 2)
			continue
		}
		if p.has("<!--") || p.has("</") || (p.has("<") && p.peekNameStart(1)) {
			break
		}
		b.WriteByte(p.src[p.pos])
		p.advance(1)
	}
	return b.String(), line, column, nil
}

// closingBraces returns the offset of the "}}" that closes an expression, skipping quoted strings.
func closingBraces(s string) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '}':
			if i+1 < len(s) && s[i+1] == '}' {
				return i
			}
		}
	}
	return -1
}
