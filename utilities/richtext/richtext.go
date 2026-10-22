// Package richtext cleans rich text: HTML written by people and editors (a paste from Google Docs or
// Word, an editor's value, plain text from a form) becomes a value in one closed grammar. The value is
// rebuilt from what was read, never filtered in place: nothing of the source reaches the output except
// text, which is escaped, link addresses, which are checked and escaped, and an alignment, which is one
// of two classes the writer spells itself.
//
// The grammar (v2, 20/10/2026):
//
//	p, h2–h6           phrasing; class="align-center" or class="align-right" (left is no class)
//	blockquote         p, one or more (each may carry the alignment)
//	ul, ol             li, one or more, one level
//	li                 phrasing
//	pre                one code holding text only: white space and newlines as written
//	hr                 after a block (never first, never right after an hr); it may end the post
//	strong, em, u, s   phrasing
//	code               phrasing (inline: text and br)
//	a href             phrasing without a
//	br
//
// Marks nest a > strong > em > u > s > code, every block stands on a line of its own, href and the two
// alignment classes are the only attributes and are written in double quotes. White space collapses
// everywhere but inside pre > code. Clean(Clean(x)) == Clean(x).
//
// A link holds the words of one line (a paragraph, a heading, a list item, a paragraph of a quote): an
// a of the source left open over several blocks links the words of the first only, so one a of the
// source is at most one a of the output, and the output is never longer than a constant times the
// source.
package richtext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The two classes of the grammar, an alignment of a paragraph or a heading: centred, and to the right
// (text-align end: the left edge on a page written right to left). Left is no class.
const (
	AlignCenter = "align-center"
	AlignRight  = "align-right"
)

// Document is cleaned rich text.
type Document struct {
	// HTML is the value in the grammar; "" when the source holds nothing to keep.
	HTML string
	// Text is the words without markup: blocks apart by a blank line, list items and line breaks by a
	// newline, a code block's lines as written, a rule one more blank line (nothing for a rule that
	// ends the post).
	Text string
	// Words counts the runs of non-space characters that hold a letter or a digit: a Vietnamese
	// syllable is a word.
	Words int
	// Characters counts the code points of Text, its line breaks not counted.
	Characters int
}

// Empty reports whether the document holds nothing.
func (d Document) Empty() bool { return d.HTML == "" }

// Clean reads source as HTML (or, when it has no tags at all, as plain text: a blank line starts a
// paragraph, a single newline is a line break, and "&amp;" is five characters) and returns it in the
// grammar. Of the source's markup it reads the tags, the href of an a, and on a block its alignment
// (text-align in style, an align-center or align-right class, the align attribute) and on b and
// strong the font-weight in style; nothing else. Time and memory are linear in the source.
func Clean(source string) Document {
	blocks := read(source)
	text := plainText(blocks)
	return Document{HTML: write(blocks, "", false), Text: text, Words: countWords(text), Characters: countCharacters(text)}
}

// Display cleans source the way Clean does and writes it for a page served from origin
// ("https://kitwork.io"): a link to a web page on another host carries rel="external noopener", added
// in the same pass that writes the grammar. Every web link is external when origin is "".
func Display(source, origin string) string {
	return write(read(source), origin, true)
}

// read is the whole reading: the source made valid UTF-8 with the line breaks a browser's input
// stream has (CR LF and CR are LF), then plain text or HTML, then tidied.
func read(source string) []block {
	source = strings.ToValidUTF8(source, "\uFFFD")
	if strings.IndexByte(source, '\r') >= 0 {
		source = strings.ReplaceAll(source, "\r\n", "\n")
		source = strings.ReplaceAll(source, "\r", "\n")
	}
	var blocks []block
	if hasMarkup(source) {
		blocks = build(source)
	} else {
		blocks = plainBlocks(source)
	}
	return tidyBlocks(blocks)
}

// countWords counts the runs of non-space characters holding a letter or a digit.
func countWords(text string) int {
	words, counted := 0, false
	for _, c := range text {
		switch {
		case unicode.IsSpace(c):
			counted = false
		case !counted && (unicode.IsLetter(c) || unicode.IsDigit(c)):
			words++
			counted = true
		}
	}
	return words
}

// countCharacters counts the code points of text without its line breaks.
func countCharacters(text string) int {
	return utf8.RuneCountInString(text) - strings.Count(text, "\n")
}
