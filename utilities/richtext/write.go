package richtext

import "strings"

// write is the one serialization pass: every block on a line of its own, the items of a list and the
// paragraphs of a quote too, an alignment as the one class of its p or heading, marks nested a > strong
// > em > u > s > code, a code block's text as written. For a page (display), a link to a web page on
// another host than origin's carries rel="external noopener".
func write(blocks []block, origin string, display bool) string {
	var b strings.Builder
	for i, bl := range blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch bl.kind {
		case paragraphBlock, headingBlock:
			tag := "p"
			if bl.kind == headingBlock {
				tag = string([]byte{'h', '0' + bl.level})
			}
			b.WriteString("<" + tag + alignClasses[bl.align] + ">")
			writeLine(&b, bl.line, origin, display)
			b.WriteString("</" + tag + ">")
		case quoteBlock, listBlock, orderedListBlock:
			outer, inner := "blockquote", "p"
			switch bl.kind {
			case listBlock:
				outer, inner = "ul", "li"
			case orderedListBlock:
				outer, inner = "ol", "li"
			}
			b.WriteString("<" + outer + ">\n")
			for _, p := range bl.parts {
				b.WriteString("<" + inner + alignClasses[p.align] + ">")
				writeLine(&b, p.line, origin, display)
				b.WriteString("</" + inner + ">\n")
			}
			b.WriteString("</" + outer + ">")
		case codeBlock:
			b.WriteString("<pre><code>")
			writeEscaped(&b, bl.code, false)
			b.WriteString("</code></pre>")
		case ruleBlock:
			b.WriteString("<hr>")
		}
	}
	return b.String()
}

// alignClasses is how each alignment is written on its block: left is no class.
var alignClasses = [...]string{alignLeft: "", alignCenter: ` class="` + AlignCenter + `"`, alignRight: ` class="` + AlignRight + `"`}

// writeLine writes phrasing: the runs of one link inside its a, then the marks.
func writeLine(b *strings.Builder, line []run, origin string, display bool) {
	for i := 0; i < len(line); {
		j := i + 1
		for j < len(line) && line[j].href == line[i].href {
			j++
		}
		if href := line[i].href; href != "" {
			b.WriteString(`<a href="`)
			writeEscaped(b, href, true)
			b.WriteByte('"')
			if display && External(href, origin) {
				b.WriteString(` rel="external noopener"`)
			}
			b.WriteByte('>')
			writeMarks(b, line[i:j], 0)
			b.WriteString("</a>")
		} else {
			writeMarks(b, line[i:j], 0)
		}
		i = j
	}
}

// markTags are the marks as written, in their nesting, the outermost first.
var markTags = [markCount]struct {
	mark        marks
	open, close string
}{
	{strongMark, "<strong>", "</strong>"},
	{emphasisMark, "<em>", "</em>"},
	{underlineMark, "<u>", "</u>"},
	{strikeMark, "<s>", "</s>"},
	{codeMark, "<code>", "</code>"},
}

// writeMarks writes runs inside the marks from level on: each stretch that carries the mark of level
// inside its element, what it holds inside the next level's, the text and line breaks innermost.
func writeMarks(b *strings.Builder, line []run, level int) {
	if level == len(markTags) {
		for _, r := range line {
			if r.lineBreak {
				b.WriteString("<br>")
			} else {
				writeEscaped(b, r.text, false)
			}
		}
		return
	}
	tag := markTags[level]
	for i := 0; i < len(line); {
		on := line[i].marks&tag.mark != 0
		j := i + 1
		for j < len(line) && (line[j].marks&tag.mark != 0) == on {
			j++
		}
		if on {
			b.WriteString(tag.open)
		}
		writeMarks(b, line[i:j], level+1)
		if on {
			b.WriteString(tag.close)
		}
		i = j
	}
}

// writeEscaped escapes text the way a browser serializes it (HTML, "Serializing HTML fragments"), so a
// browser that reads the value back and writes it out again gives the same bytes: & < > always, and in
// an attribute " and U+00A0 as well (text, a code block's too, holds no U+00A0: it is a space there).
func writeEscaped(b *strings.Builder, s string, attribute bool) {
	last := 0
	for i := 0; i < len(s); i++ {
		var entity string
		switch s[i] {
		case '&':
			entity = "&amp;"
		case '<':
			entity = "&lt;"
		case '>':
			entity = "&gt;"
		case '"':
			if attribute {
				entity = "&quot;"
			}
		case 0xC2: // U+00A0 is C2 A0 in UTF-8
			if attribute && i+1 < len(s) && s[i+1] == 0xA0 {
				b.WriteString(s[last:i])
				b.WriteString("&nbsp;")
				i++
				last = i + 1
			}
			continue
		}
		if entity == "" {
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(entity)
		last = i + 1
	}
	b.WriteString(s[last:])
}
