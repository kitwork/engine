package richtext

import "strings"

// normalizeText takes out of text what is never kept: NUL (a browser drops it), U+200B ZERO WIDTH
// SPACE and U+FEFF (a byte order mark, or a zero width no-break space); U+00A0 NO-BREAK SPACE is a
// space, so it collapses like one.
func normalizeText(s string) string {
	if !strings.ContainsAny(s, "\x00\u00a0\u200b\ufeff") {
		return s
	}
	return strings.Map(func(c rune) rune {
		switch c {
		case 0, '\u200b', '\ufeff':
			return -1
		case '\u00a0':
			return ' '
		}
		return c
	}, s)
}

// blank reports whether s is only HTML white space (space, tab, line feed, form feed).
func blank(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			return false
		}
	}
	return true
}

// plainBlocks reads a source without tags: a blank line starts a paragraph, a single newline is a line
// break. Character references are text like any other.
func plainBlocks(source string) []block {
	var blocks []block
	var line []run
	for _, text := range strings.Split(source, "\n") {
		text = normalizeText(text)
		if blank(text) {
			if line != nil {
				blocks = append(blocks, block{kind: paragraphBlock, line: line})
				line = nil
			}
			continue
		}
		if line != nil {
			line = append(line, run{lineBreak: true})
		}
		line = append(line, run{text: text})
	}
	if line != nil {
		blocks = append(blocks, block{kind: paragraphBlock, line: line})
	}
	return blocks
}

// tidyBlocks tidies every line and leaves out what is then empty: a paragraph without text, a quote or
// a list without a paragraph or an item, a code block of white space only. A rule stands after a block
// (decided 21/10/2026: one that ends a post is kept, so what the writer sees is what is saved): one
// first (nothing kept before it) or right after another rule (two in a row are one) is left out too,
// so a post of rules only is empty.
func tidyBlocks(blocks []block) []block {
	kept := blocks[:0]
	for _, bl := range blocks {
		switch bl.kind {
		case quoteBlock, listBlock, orderedListBlock:
			parts := bl.parts[:0]
			for _, p := range bl.parts {
				if p.line = tidy(p.line); p.line != nil {
					parts = append(parts, p)
				}
			}
			if len(parts) == 0 {
				continue
			}
			bl.parts = parts
		case codeBlock:
			if blank(bl.code) {
				continue
			}
		case ruleBlock:
			if len(kept) == 0 || kept[len(kept)-1].kind == ruleBlock {
				continue
			}
		default:
			if bl.line = tidy(bl.line); bl.line == nil {
				continue
			}
		}
		kept = append(kept, bl)
	}
	return kept
}

// tidy collapses the white space of a line the way a browser lays it out: a run of white space is one
// space, none at the start or the end of the line or next to a line break. Line breaks at the start or
// the end go, and neighbouring runs with the same marks join. A line without text is nil.
func tidy(line []run) []run {
	var out []run
	var text strings.Builder // the text of the last run in out, while it may still grow
	growing := false
	flush := func() {
		if growing {
			out[len(out)-1].text = text.String()
			text.Reset()
			growing = false
		}
	}
	var piece []byte
	space := true // a space here would be dropped: at the start, after a space, after a break
	for _, r := range line {
		if r.lineBreak {
			flush()
			trimTrailingSpace(&out)
			out = append(out, r)
			space = true
			continue
		}
		piece = piece[:0]
		for i := 0; i < len(r.text); i++ {
			c := r.text[i]
			if isSpace(c) {
				if !space {
					piece = append(piece, ' ')
					space = true
				}
				continue
			}
			piece = append(piece, c)
			space = false
		}
		if len(piece) == 0 {
			continue // nothing of it is left, and it does not part its neighbours
		}
		if !growing || !sameMarks(out[len(out)-1], r) {
			flush()
			r.text = ""
			out = append(out, r)
			growing = true
		}
		text.Write(piece)
	}
	flush()
	trimTrailingSpace(&out)
	for len(out) > 0 && out[0].lineBreak {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1].lineBreak {
		out = out[:len(out)-1]
		trimTrailingSpace(&out)
	}
	for _, r := range out {
		if !r.lineBreak && r.text != "" {
			return out
		}
	}
	return nil
}

// trimTrailingSpace drops the space a line ends with before a break or at its end.
func trimTrailingSpace(out *[]run) {
	n := len(*out)
	if n == 0 || (*out)[n-1].lineBreak {
		return
	}
	last := &(*out)[n-1]
	last.text = strings.TrimSuffix(last.text, " ")
	if last.text == "" {
		*out = (*out)[:n-1]
	}
}

func sameMarks(a, b run) bool {
	return a.href == b.href && a.marks == b.marks
}

// plainText is the text of the blocks: blocks and the paragraphs of a quote apart by a blank line,
// list items and line breaks by a newline, a code block's text as written (its lines kept), and a rule
// one more blank line between the blocks around it; a rule that ends the post adds nothing (the text
// holds words, and no word follows it).
func plainText(blocks []block) string {
	var b strings.Builder
	for i, bl := range blocks {
		switch {
		case bl.kind == ruleBlock:
			if i+1 < len(blocks) {
				b.WriteByte('\n')
			}
			continue
		case i > 0:
			b.WriteString("\n\n")
		}
		switch bl.kind {
		case quoteBlock, listBlock, orderedListBlock:
			apart := "\n"
			if bl.kind == quoteBlock {
				apart = "\n\n"
			}
			for k, p := range bl.parts {
				if k > 0 {
					b.WriteString(apart)
				}
				writeLineText(&b, p.line)
			}
		case codeBlock:
			b.WriteString(bl.code)
		default:
			writeLineText(&b, bl.line)
		}
	}
	return b.String()
}

func writeLineText(b *strings.Builder, line []run) {
	for _, r := range line {
		if r.lineBreak {
			b.WriteByte('\n')
		} else {
			b.WriteString(r.text)
		}
	}
}
