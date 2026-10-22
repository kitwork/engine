package richtext

import (
	"strings"
	"testing"
	"time"
)

// The stack of open elements holds at most maximumDepth (256) elements: what is nested deeper is
// dropped, and the end tags that close it are counted off before they close anything else, so the
// content after the deep part is kept. An svg nested as deep is skipped whole, and what follows is
// kept.
//
// CONTROL: with the cap no longer checked (start()'s maximumDepth test removed), the stack grew past
// 256 and the text 300 elements deep came out; this test failed.
func TestDepthIsCapped(t *testing.T) {
	deep := strings.Repeat("<em>", 300) + "sâu" + strings.Repeat("</em>", 300) + "nông"
	if got := Clean(deep).HTML; got != "<p>nông</p>" {
		t.Errorf("Clean(300 nested em) = %q, want the deep text dropped and the rest kept", got)
	}
	nested := strings.Repeat("<div>", 1000) + "x" + strings.Repeat("</div>", 1000) + "y"
	if got := Clean(nested).HTML; got != "<p>y</p>" {
		t.Errorf("Clean(1000 nested div) = %q", got)
	}
	foreign := "<svg>" + strings.Repeat("<g>", 1000) + "<text>z</text>" + strings.Repeat("</g>", 1000) + "</svg>after"
	if got := Clean(foreign).HTML; got != "<p>after</p>" {
		t.Errorf("Clean(svg 1000 deep) = %q", got)
	}
	for _, source := range []string{deep, nested, strings.Repeat("<blockquote><ul><li><b><a href=/x>", 200) + "x"} {
		tokens := tokenizer{source: source}
		var b builder
		for {
			tok, ok := tokens.next()
			if !ok {
				break
			}
			b.take(tok)
			if len(b.stack) > maximumDepth {
				t.Fatalf("the stack holds %d elements", len(b.stack))
			}
		}
		b.finish()
	}
}

// What Clean and Display write is bounded by the source: at most boundFactor (24) times its length
// (the most a line of the grammar spends on a byte of the source is about 22, "<p>&" under all five
// marks with an alignment from around it: 87 bytes from 4). A link left open over many blocks links
// the words of the line they start in and ends there, so its address is written once, not once for
// each paragraph, list item or quote paragraph after it: an address of 4 KB left open over 2,000
// paragraphs would otherwise write 8 MB, and a post of 1 MiB (the ceiling of request.form) about 64 GB.
// A line break or white space is not a word: the link starts at its first word, so a card link with a
// <br> or a space before its title still links the title.
//
// CONTROL: with a link carried into every line after its own (line() no longer ending it), the address
// was written once per block, the output was 434 to 680 times the source for the paragraphs, the
// items, the quote paragraphs and the headings, the card link linked its second block too, and this
// test failed.
//
// CONTROL: with the link's line given by its first run, a line break or a space as well (marked()
// without its word check), the card links with a <br> or a space before the title and the a left open
// over lines without words lost their link (the line it was given was dropped as empty), and this test
// failed.
//
// CONTROL: with boundFactor at 16, the bound before the grammar of 20/10/2026, the rows of five marks
// with an alignment failed (paragraphs and quote paragraphs about 21.6 times their source, headings
// about 17.5) and no other row did.
func TestOutputIsBoundedByTheSource(t *testing.T) {
	address := "/" + strings.Repeat("a", 4096)
	link := `<a href="` + address + `">`
	sources := map[string]string{
		"paragraphs":            link + strings.Repeat("<p>x", 2000),
		"items":                 link + "<ul>" + strings.Repeat("<li>x", 2000),
		"quote paragraphs":      link + "<blockquote>" + strings.Repeat("<p>x", 2000),
		"stray items":           link + strings.Repeat("<li>x", 2000),
		"headings":              "<b><i>" + link + strings.Repeat("<h2>x</h2><h3>y", 1000),
		"a web link everywhere": `<a href="https://evil.example/` + strings.Repeat("b", 4096) + `">` + strings.Repeat("<div>x", 2000),
		"short link and marks":  "<b><i><a href=/>" + strings.Repeat("<p>x", 20000),
		"links closing links":   strings.Repeat(`<a href="/x">a<a href="/y">b`, 2000),
		"items in a quote":      "<blockquote>" + link + strings.Repeat("<li>x", 2000),
		"breaks":                "<b><i>" + link + strings.Repeat("<br>", 2000),
		"lines without words":   link + strings.Repeat("<p><br> ", 2000) + "<p>x",
		"escaped text":          "<p>" + strings.Repeat("&<>", 2000),
		"plain text":            strings.Repeat("&\n", 2000),
		// the grammar of 20/10/2026: five marks, alignments, code blocks, rules
		"five marks and an alignment":   `<div style="text-align:center"><b><i><u><s><code>` + strings.Repeat("<p>&", 2000),
		"headings with five marks":      `<div class=align-right><b><i><u><s><code>` + strings.Repeat("<h6>&", 2000),
		"quote paragraphs, five marks":  `<blockquote class=align-center><b><i><u><s><code>` + strings.Repeat("<p>&", 2000),
		"code blocks":                   strings.Repeat("<pre>&", 2000),
		"rules":                         strings.Repeat("<hr>&", 2000),
		"a link over code blocks":       link + strings.Repeat("<pre>x</pre>", 2000) + "<p>x",
		"marks toggled under four more": "<i><u><s><code>" + strings.Repeat("x<b>y</b>", 2000),
	}
	for name, source := range sources {
		for _, written := range []string{Clean(source).HTML, Display(source, "https://kitwork.io"), Display(source, "")} {
			if len(written) > boundFactor*len(source) {
				t.Errorf("%s: %d bytes of source wrote %d (%d times)", name, len(source), len(written), len(written)/len(source))
				break
			}
		}
	}
	// the worst line of the grammar is what sets the bound: more than v1's 16 times, within boundFactor
	worst := sources["five marks and an alignment"]
	if n := len(Clean(worst).HTML); n <= 21*len(worst) || n > boundFactor*len(worst) {
		t.Errorf("five marks and an alignment: %d bytes of source wrote %d (%.1f times), want between 21 and %d times",
			len(worst), n, float64(n)/float64(len(worst)), boundFactor)
	}
	// the words keep their link in the line they start in, and only there
	first := `<p><a href="` + address + `">x</a></p>` + "\n<p>x</p>\n<p>x</p>"
	if got := Clean(sources["paragraphs"]).HTML; !strings.HasPrefix(got, first) || strings.Count(got, "<a ") != 1 {
		t.Errorf("an a left open over paragraphs: %.200q…, want the first paragraph linked and no other", got)
	}
	if got := Clean(`<a href="/x"><div>Tiêu đề</div><div>Mô tả</div></a>`).HTML; got != "<p><a href=\"/x\">Tiêu đề</a></p>\n<p>Mô tả</p>" {
		t.Errorf("a card link: %q", got)
	}
	if got := Clean(`<a href="/x"><br><div>Tiêu đề</div><div>Mô tả</div></a>`).HTML; got != "<p><a href=\"/x\">Tiêu đề</a></p>\n<p>Mô tả</p>" {
		t.Errorf("a card link with a line break before its title: %q", got)
	}
	if got := Clean(`<div>Xem <a href="/x"> <div>Tiêu đề</div><div>Mô tả</div></a></div>`).HTML; got != "<p>Xem</p>\n<p><a href=\"/x\">Tiêu đề</a></p>\n<p>Mô tả</p>" {
		t.Errorf("a card link with a space before its title: %q", got)
	}
	if got := Clean(sources["lines without words"]).HTML; got != `<p><a href="`+address+`">x</a></p>` {
		t.Errorf("an a left open over lines without words: %.200q…", got)
	}
	if got := Clean(`<ul><li><a href="/x">một<p>hai</p></a></li></ul>`).HTML; got != "<ul>\n<li><a href=\"/x\">một<br>hai</a></li>\n</ul>" {
		t.Errorf("a link over a line break of its own item: %q", got)
	}
	for _, source := range sources { // last: checkClean stops the test at the first source it refuses
		checkClean(t, source)
	}
}

// Clean takes time linear in its input: each of these inputs of about 2 MB, built to make a careless
// tokenizer or tree builder search the input again and again, is cleaned within a few seconds (in
// tens of milliseconds, unloaded; a quadratic step would take minutes).
//
// CONTROL: with the end of a comment searched for "-->" and "--!>" apart, each to the end of the input
// (declarationEnd's single scan replaced by two strings.Index calls), the input of many comments did not
// finish in time and this test failed.
func TestLinearTime(t *testing.T) {
	const size = 2 << 20
	repeat := func(s string) string { return strings.Repeat(s, size/len(s)) }
	inputs := map[string]string{
		"comments":              repeat("<!---->x"),
		"near end tags in text": "<style>" + repeat("</styl"),
		"script escapes":        "<script>" + repeat("<!--<script>-"),
		"links closing links":   repeat("<a href=/x>a"),
		"unclosed marks":        repeat("<b>"),
		"end tags deep down":    strings.Repeat("<div>", 255) + repeat("</b>x"),
		"blocks deep down":      strings.Repeat("<span><b>", 120) + repeat("<div>"),
		"paragraphs":            repeat("<p>a"),
		"items":                 repeat("<li>a"),
		"cells":                 repeat("<td>a"),
		"references in an href": `<a href="/` + repeat("&region") + `">x</a>`,
		"long reference names":  `<a href="/&` + repeat("a") + `">x</a>`,
		"many text runs":        repeat("x<!---->y "),
		"foreign content":       repeat("<svg><g>"),
		"foreign end tags":      "<svg>" + strings.Repeat("<g>", 255) + repeat("</x>"),
		"attributes":            "<p " + repeat("a=b ") + ">x",
		"plain text lines":      repeat("một dòng\n"),
		"white space":           "<p>" + repeat(" \t\n") + "x</p>",
		// the grammar of 20/10/2026
		"code text in pieces":     "<pre>" + repeat("a<b>b</b>"),
		"blocks inside code":      "<pre>" + repeat("<div>x"),
		"aligned paragraphs":      repeat(`<p style="text-align:center">x`),
		"a long class list":       `<p class="` + repeat("x ") + `align-center">x`,
		"a long style":            `<p style="` + repeat("a:b;") + `text-align:right">x`,
		"rules between words":     repeat("<hr>x"),
		"marks toggled in a line": "<i><u><s><code>" + repeat("x<b>y</b>"),
	}
	for name, source := range inputs {
		done := make(chan struct{})
		start := time.Now()
		go func() {
			Clean(source)
			Display(source, "https://kitwork.io")
			close(done)
		}()
		select {
		case <-done:
			t.Logf("%s: %v", name, time.Since(start))
		case <-time.After(20 * time.Second):
			t.Fatalf("%s: Clean of %d bytes took more than 20s", name, len(source))
		}
	}
}
