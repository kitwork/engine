package richtext

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"testing"
)

// boundFactor bounds what Clean and Display write: at most this many times the source's length
// (TestOutputIsBoundedByTheSource). The most a line of the grammar spends on a byte of the source is
// about 22: "<p>&" (4 bytes) under all five marks with an alignment from around it writes 87.
const boundFactor = 24

// readGrammar reads HTML written in the grammar's canonical form and fails on anything else: another
// element, another attribute or class, marks nested out of the order a > strong > em > u > s > code,
// an empty element, raw "<", ">" or "&" in text, an href holding a raw "&", a quote or a character a
// browser would read differently, blocks not one per line, a rule first or right after a rule, a code block
// holding markup or only white space, white space or line breaks a tidy line never has, two
// neighbouring runs with the same marks. It is written apart from the tree builder: what it reads is
// the tree as the output says it, to compare with the tree the builder made. With display, an a may
// carry rel="external noopener" after its href.
func readGrammar(source string, display bool) ([]block, error) {
	g := grammarReader{source: source, display: display}
	var blocks []block
	for g.at < len(g.source) {
		if len(blocks) > 0 && !g.eat("\n") {
			return nil, g.fail("blocks are not one per line")
		}
		var bl block
		var err error
		switch {
		case g.eat("<hr>"):
			bl.kind = ruleBlock
			switch {
			case len(blocks) == 0:
				return nil, g.fail("a rule first")
			case blocks[len(blocks)-1].kind == ruleBlock:
				return nil, g.fail("a rule right after a rule")
			}
		case g.eat("<pre><code>"):
			bl.kind = codeBlock
			bl.code, err = g.code()
		case g.eat("<blockquote>\n"):
			bl.kind = quoteBlock
			bl.parts, err = g.parts("p", true, "</blockquote>")
		case g.eat("<ul>\n"):
			bl.kind = listBlock
			bl.parts, err = g.parts("li", false, "</ul>")
		case g.eat("<ol>\n"):
			bl.kind = orderedListBlock
			bl.parts, err = g.parts("li", false, "</ol>")
		default:
			found := false
			for _, tag := range []string{"p", "h2", "h3", "h4", "h5", "h6"} {
				if a, ok := g.opening(tag); ok {
					found, bl.align = true, a
					if tag != "p" {
						bl.kind, bl.level = headingBlock, tag[1]-'0'
					}
					bl.line, err = g.line("</" + tag + ">")
					break
				}
			}
			if !found {
				return nil, g.fail("not a block of the grammar")
			}
		}
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, bl)
	}
	return blocks, nil
}

type grammarReader struct {
	source  string
	at      int
	display bool
}

func (g *grammarReader) eat(s string) bool {
	if strings.HasPrefix(g.source[g.at:], s) {
		g.at += len(s)
		return true
	}
	return false
}

func (g *grammarReader) fail(why string) error {
	end := g.at + 40
	if end > len(g.source) {
		end = len(g.source)
	}
	return fmt.Errorf("%s at %d: %q", why, g.at, g.source[g.at:end])
}

// opening reads the start tag of an element that may carry an alignment, written exactly so: <p>,
// <p class="align-center"> or <p class="align-right">.
func (g *grammarReader) opening(tag string) (alignment, bool) {
	for _, c := range []struct {
		align alignment
		class string
	}{{alignLeft, ""}, {alignCenter, ` class="align-center"`}, {alignRight, ` class="align-right"`}} {
		if g.eat("<" + tag + c.class + ">") {
			return c.align, true
		}
	}
	return alignLeft, false
}

// parts reads the paragraphs of a quote (aligned) or the items of a list up to end.
func (g *grammarReader) parts(tag string, aligned bool, end string) ([]part, error) {
	var parts []part
	for !g.eat(end) {
		var p part
		if aligned {
			a, ok := g.opening(tag)
			if !ok {
				return nil, g.fail("expected <" + tag + "> or " + end)
			}
			p.align = a
		} else if !g.eat("<" + tag + ">") {
			return nil, g.fail("expected <" + tag + "> or " + end)
		}
		line, err := g.line("</" + tag + ">")
		if err != nil {
			return nil, err
		}
		if !g.eat("\n") {
			return nil, g.fail("items are not one per line")
		}
		p.line = line
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return nil, g.fail("an empty " + end)
	}
	return parts, nil
}

// code reads the text of a code block up to </code></pre>: text only, escaped, not white space only,
// without a character text never keeps; its white space and newlines are as written.
func (g *grammarReader) code() (string, error) {
	k := strings.IndexByte(g.source[g.at:], '<')
	if k < 0 {
		return "", g.fail("a code block without its end")
	}
	written := g.source[g.at : g.at+k]
	g.at += k
	if !g.eat("</code></pre>") {
		return "", g.fail("a code block holding markup")
	}
	text, err := readText(written)
	switch {
	case err != nil:
		return "", g.fail(err.Error())
	case blank(text):
		return "", g.fail("a code block of white space only")
	case strings.ContainsAny(text, "\r\x00\u00a0\u200b\ufeff"):
		return "", g.fail("a character the text never keeps in a code block")
	}
	return text, nil
}

// line reads phrasing up to end. An a opens outside every mark; a mark opens only inside the marks
// before it in the nesting (each a lower bit) and closes only as the innermost.
func (g *grammarReader) line(end string) ([]run, error) {
	var out []run
	var open run    // the link and the marks in effect
	var since []int // the runs when each open element opened, innermost last: an element holds something
	for {
		switch {
		case g.eat(end):
			if open.href != "" || open.marks != 0 {
				return nil, g.fail("an element is still open")
			}
			if len(out) == 0 {
				return nil, g.fail("an empty block")
			}
			return out, g.tidyLine(out)
		case g.eat(`<a href="`):
			if open.href != "" || open.marks != 0 {
				return nil, g.fail("an a inside a or a mark")
			}
			k := strings.IndexByte(g.source[g.at:], '"')
			if k < 0 {
				return nil, g.fail("an href without its end")
			}
			written := g.source[g.at : g.at+k]
			g.at += k + 1
			href, err := readHref(written)
			if err != nil {
				return nil, g.fail(err.Error())
			}
			if g.display {
				g.eat(` rel="external noopener"`)
			}
			if !g.eat(">") {
				return nil, g.fail("an a with another attribute")
			}
			open.href, since = href, append(since, len(out))
		case g.eat("</a>"):
			if open.href == "" || open.marks != 0 || len(out) == since[len(since)-1] {
				return nil, g.fail("an a closed out of order or empty")
			}
			open.href, since = "", since[:len(since)-1]
		case g.eat("<br>"):
			r := open
			r.text, r.lineBreak = "", true
			out = append(out, r)
		default:
			if mark, closing, ok := g.markTag(); ok {
				switch {
				case !closing && open.marks >= mark:
					return nil, g.fail("a mark inside itself or inside a mark it nests outside of")
				case !closing:
					open.marks |= mark
					since = append(since, len(out))
				case open.marks&mark == 0 || open.marks >= 2*mark || len(out) == since[len(since)-1]:
					return nil, g.fail("a mark closed out of order or empty")
				default:
					open.marks &^= mark
					since = since[:len(since)-1]
				}
				continue
			}
			k := strings.IndexByte(g.source[g.at:], '<')
			if k <= 0 {
				return nil, g.fail("not phrasing of the grammar")
			}
			written := g.source[g.at : g.at+k]
			g.at += k
			text, err := readText(written)
			if err != nil {
				return nil, g.fail(err.Error())
			}
			r := open
			r.text, r.lineBreak = text, false
			if n := len(out); n > 0 && !out[n-1].lineBreak && sameMarks(out[n-1], r) {
				return nil, g.fail("two neighbouring runs with the same marks")
			}
			out = append(out, r)
		}
	}
}

// markTag reads the start or the end tag of a mark: <strong>, <em>, <u>, <s>, <code> and their ends.
func (g *grammarReader) markTag() (mark marks, closing, ok bool) {
	for _, tag := range []struct {
		mark marks
		name string
	}{{strongMark, "strong"}, {emphasisMark, "em"}, {underlineMark, "u"}, {strikeMark, "s"}, {codeMark, "code"}} {
		switch {
		case g.eat("<" + tag.name + ">"):
			return tag.mark, false, true
		case g.eat("</" + tag.name + ">"):
			return tag.mark, true, true
		}
	}
	return 0, false, false
}

// tidyLine checks what tidy guarantees: no space at the ends of a line or next to a break, no two
// spaces, no line break at an end, some text.
func (g *grammarReader) tidyLine(line []run) error {
	if line[0].lineBreak || line[len(line)-1].lineBreak {
		return errors.New("a line break at an end of a line")
	}
	var flat strings.Builder
	for _, r := range line {
		if r.lineBreak {
			flat.WriteByte('\n')
		} else {
			flat.WriteString(r.text)
		}
	}
	text := flat.String()
	switch {
	case strings.HasPrefix(text, " ") || strings.HasSuffix(text, " "):
		return fmt.Errorf("a space at an end of the line %q", text)
	case strings.Contains(text, "  "), strings.Contains(text, " \n"), strings.Contains(text, "\n "):
		return fmt.Errorf("white space not collapsed in %q", text)
	case strings.ContainsAny(text, "\t\f\r\x00\u00a0\u200b\ufeff"):
		return fmt.Errorf("a character the text never keeps in %q", text)
	case strings.Trim(text, " \n") == "":
		return fmt.Errorf("a line without text")
	}
	return nil
}

// readText decodes text written by the serializer: only &amp; &lt; &gt; are references, and no raw
// "<" or ">" is left.
func readText(written string) (string, error) {
	if strings.ContainsAny(written, "<>") {
		return "", errors.New("a raw < or > in text")
	}
	return readReferences(written, []string{"&amp;", "&lt;", "&gt;"})
}

// readHref decodes an href written by the serializer and checks it is an address Link keeps as it is.
func readHref(written string) (string, error) {
	if strings.ContainsAny(written, "<>\"\\\t\n\r\f\x00") {
		return "", fmt.Errorf("a character an href is never written with in %q", written)
	}
	href, err := readReferences(written, []string{"&amp;", "&quot;", "&lt;", "&gt;", "&nbsp;"})
	if err != nil {
		return "", err
	}
	if href == "" {
		return "", errors.New("an empty href")
	}
	if address, ok := Link(written); !ok || address != href {
		return "", fmt.Errorf("an href Link does not keep as it is: %q", href)
	}
	if !allowedLink(href) {
		return "", fmt.Errorf("an href to a place a link may not go: %q", href)
	}
	return href, nil
}

func readReferences(written string, allowed []string) (string, error) {
	for i := strings.IndexByte(written, '&'); i >= 0; {
		ok := false
		for _, reference := range allowed {
			if strings.HasPrefix(written[i:], reference) {
				ok = true
			}
		}
		if !ok {
			return "", fmt.Errorf("a raw & in %q", written)
		}
		next := strings.IndexByte(written[i+1:], '&')
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return html.UnescapeString(written), nil
}

// describe writes blocks out plainly, to compare two trees and to show them.
func describe(blocks []block) string {
	var b strings.Builder
	names := [markCount]string{" strong", " em", " u", " s", " code"}
	line := func(runs []run) {
		for _, r := range runs {
			if r.lineBreak {
				b.WriteString("[br")
			} else {
				fmt.Fprintf(&b, "[%q", r.text)
			}
			if r.href != "" {
				fmt.Fprintf(&b, " a=%q", r.href)
			}
			for i, name := range names {
				if r.marks&(1<<i) != 0 {
					b.WriteString(name)
				}
			}
			b.WriteString("]")
		}
	}
	for _, bl := range blocks {
		fmt.Fprintf(&b, "%d", bl.kind)
		if bl.kind == headingBlock {
			fmt.Fprintf(&b, " h%d", bl.level)
		}
		if bl.align != alignLeft {
			fmt.Fprintf(&b, " align=%d", bl.align)
		}
		b.WriteString("{")
		line(bl.line)
		for _, p := range bl.parts {
			b.WriteString("{")
			if p.align != alignLeft {
				fmt.Fprintf(&b, "align=%d ", p.align)
			}
			line(p.line)
			b.WriteString("}")
		}
		if bl.kind == codeBlock {
			fmt.Fprintf(&b, "%q", bl.code)
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// checkClean checks every invariant of Clean on one source: the output is in the grammar (readGrammar:
// only its elements, href and the two alignment classes the only attributes), it says exactly the tree
// the builder made, Clean is idempotent, the output read again gives the same tree, Display writes the
// same tree with rel added only, and neither writes more than boundFactor times the source
// (TestOutputIsBoundedByTheSource).
func checkClean(t testing.TB, source string) {
	t.Helper()
	d := Clean(source)
	if len(d.HTML) > boundFactor*len(source) {
		t.Fatalf("Clean(%.120q…) wrote %d bytes from %d: more than %d times its source", source, len(d.HTML), len(source), boundFactor)
	}
	built := describe(read(source))
	blocks, err := readGrammar(d.HTML, false)
	if err != nil {
		t.Fatalf("Clean(%q) is not in the grammar: %v\n%s", source, err, d.HTML)
	}
	if said := describe(blocks); said != built {
		t.Fatalf("Clean(%q) does not say the tree it built:\n wrote %s\n built %s", source, said, built)
	}
	if again := Clean(d.HTML); again != d {
		t.Fatalf("Clean is not idempotent on %q:\n once  %#v\n twice %#v", source, d, again)
	}
	if reread := describe(read(d.HTML)); reread != built {
		t.Fatalf("the output of %q read again is another tree:\n first %s\n again %s", source, built, reread)
	}
	if d.Empty() != (len(blocks) == 0) || d.Empty() != (d.Text == "") {
		t.Fatalf("Empty() %v for %#v", d.Empty(), d)
	}
	if without, ok := strings.CutSuffix(d.HTML, "\n<hr>"); ok {
		// a rule that ends the post adds nothing to the text nor the counts
		if other := Clean(without); other.HTML != without || other.Text != d.Text || other.Words != d.Words || other.Characters != d.Characters {
			t.Fatalf("Clean(%q) ends in a rule that changes its text or counts:\n with    %#v\n without %#v", source, d, other)
		}
	}
	shown := Display(source, "https://kitwork.io")
	if len(shown) > boundFactor*len(source) {
		t.Fatalf("Display(%.120q…) wrote %d bytes from %d: more than %d times its source", source, len(shown), len(source), boundFactor)
	}
	displayed, err := readGrammar(shown, true)
	if err != nil {
		t.Fatalf("Display(%q) is not in the grammar: %v\n%s", source, err, shown)
	}
	if said := describe(displayed); said != built {
		t.Fatalf("Display(%q) writes another tree:\n %s\n %s", source, said, built)
	}
	if strings.ReplaceAll(shown, ` rel="external noopener"`, "") != d.HTML {
		t.Fatalf("Display(%q) differs from Clean by more than rel:\n %s\n %s", source, shown, d.HTML)
	}
}
