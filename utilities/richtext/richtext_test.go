package richtext

import (
	"strings"
	"testing"
)

// cleanCase is a source and the HTML Clean gives.
type cleanCase struct {
	source, want string
}

func checkCases(t *testing.T, cases []cleanCase) {
	t.Helper()
	for _, c := range cases {
		if got := Clean(c.source).HTML; got != c.want {
			t.Errorf("Clean(%q)\n got  %q\n want %q", c.source, got, c.want)
			continue
		}
		checkClean(t, c.source)
	}
}

// tableSources are the sources of every table below, seeds of the fuzz test too.
var tableSources = func() []string {
	var sources []string
	for _, cases := range [][]cleanCase{keptCases, mappedCases, unwrappedCases, droppedCases, skippedCases,
		attributeCases, textCases, plainCases, canonicalCases, escapedCases, alignedCases, codeCases, ruleCases} {
		for _, c := range cases {
			sources = append(sources, c.source)
		}
	}
	return sources
}()

var keptCases = []cleanCase{
	{"<p>Một đoạn.</p>", "<p>Một đoạn.</p>"},
	{"<h2>Tiêu đề</h2>", "<h2>Tiêu đề</h2>"},
	{"<h3>Mục</h3>", "<h3>Mục</h3>"},
	{"<blockquote>\n<p>một</p>\n<p>hai</p>\n</blockquote>", "<blockquote>\n<p>một</p>\n<p>hai</p>\n</blockquote>"},
	{"<ul>\n<li>một</li>\n<li>hai</li>\n</ul>", "<ul>\n<li>một</li>\n<li>hai</li>\n</ul>"},
	{"<ol>\n<li>một</li>\n<li>hai</li>\n</ol>", "<ol>\n<li>một</li>\n<li>hai</li>\n</ol>"},
	{`<p><strong>đậm</strong> <em>nghiêng</em> <a href="/x">liên kết</a>, dòng<br>mới</p>`,
		`<p><strong>đậm</strong> <em>nghiêng</em> <a href="/x">liên kết</a>, dòng<br>mới</p>`},
	{`<p><a href="https://kitwork.io/docs"><strong><em>cả ba</em></strong></a></p>`,
		`<p><a href="https://kitwork.io/docs"><strong><em>cả ba</em></strong></a></p>`},
	{`<p><a href="mailto:hi@kitwork.io">thư</a> <a href="tel:+84901234567">gọi</a></p>`,
		`<p><a href="mailto:hi@kitwork.io">thư</a> <a href="tel:+84901234567">gọi</a></p>`},
	{"<h2>A</h2>\n<p>b</p>\n<ul>\n<li>c</li>\n</ul>\n<blockquote>\n<p>d</p>\n</blockquote>",
		"<h2>A</h2>\n<p>b</p>\n<ul>\n<li>c</li>\n</ul>\n<blockquote>\n<p>d</p>\n</blockquote>"},
	{"<h4>Bốn</h4>\n<h5>Năm</h5>\n<h6>Sáu</h6>", "<h4>Bốn</h4>\n<h5>Năm</h5>\n<h6>Sáu</h6>"},
	{"<p><u>gạch chân</u> <s>gạch ngang</s> <code>mã</code></p>", "<p><u>gạch chân</u> <s>gạch ngang</s> <code>mã</code></p>"},
	{`<p><a href="/x"><strong><em><u><s><code>cả sáu</code></s></u></em></strong></a></p>`,
		`<p><a href="/x"><strong><em><u><s><code>cả sáu</code></s></u></em></strong></a></p>`},
	{"<pre><code>func main() {\n\tfmt.Println(\"xin chào\")\n}</code></pre>", "<pre><code>func main() {\n\tfmt.Println(\"xin chào\")\n}</code></pre>"},
	{"<p>trên</p>\n<hr>\n<p>dưới</p>", "<p>trên</p>\n<hr>\n<p>dưới</p>"},
	{"<p class=\"align-center\">giữa</p>\n<h2 class=\"align-right\">phải</h2>\n<blockquote>\n<p class=\"align-center\">dẫn</p>\n</blockquote>",
		"<p class=\"align-center\">giữa</p>\n<h2 class=\"align-right\">phải</h2>\n<blockquote>\n<p class=\"align-center\">dẫn</p>\n</blockquote>"},
	{"<p>cuối</p>\n<hr>", "<p>cuối</p>\n<hr>"},
}

// The grammar's own elements come out as they went in: p, h2–h6 (with an alignment class or none),
// blockquote of p, ul and ol of li, pre holding one code, hr after a block (the last one too), strong,
// em, u, s, code, a href, br.
//
// CONTROL: with blockquote taken out of the roles table (so it is unwrapped like an unknown tag), the
// quotes came out as plain paragraphs and this test failed.
func TestKeptElements(t *testing.T) {
	checkCases(t, keptCases)
}

var mappedCases = []cleanCase{
	{"<div>a</div><section>b</section><article>c</article><aside>d</aside><header>e</header><footer>f</footer><main>g</main><nav>h</nav>",
		"<p>a</p>\n<p>b</p>\n<p>c</p>\n<p>d</p>\n<p>e</p>\n<p>f</p>\n<p>g</p>\n<p>h</p>"},
	{"<table><tr><th>Tên</th><td>Giá</td></tr></table><dl><dt>KitDB</dt><dd>SQL nhỏ</dd></dl>",
		"<p>Tên</p>\n<p>Giá</p>\n<p>KitDB</p>\n<p>SQL nhỏ</p>"},
	{"<div>a<div>b</div>c</div>", "<p>a</p>\n<p>b</p>\n<p>c</p>"},
	{"<pre>\nline 1\n   line 2\n\nline 4</pre>", "<pre><code>line 1\n   line 2\n\nline 4</code></pre>"},
	{"<listing>a\n b</listing>", "<pre><code>a\n b</code></pre>"},
	{"<h1>A</h1><h2>B</h2><h4>C</h4><h5>D</h5><h6>E</h6>", "<h2>A</h2>\n<h2>B</h2>\n<h4>C</h4>\n<h5>D</h5>\n<h6>E</h6>"},
	{"<p><b>a</b> <i>b</i></p>", "<p><strong>a</strong> <em>b</em></p>"},
	{"<p><ins>a</ins> <del>b</del> <strike>c</strike> <kbd>d</kbd> <samp>e</samp> <tt>f</tt></p>",
		"<p><u>a</u> <s>b</s> <s>c</s> <code>d</code> <code>e</code> <code>f</code></p>"},
	{"<menu><li>a</li><li>b</li></menu>", "<ul>\n<li>a</li>\n<li>b</li>\n</ul>"},
	{"<li>a</li><li>b</li>", "<ul>\n<li>a</li>\n<li>b</li>\n</ul>"},
	{"<li>a</li>giữa<li>b</li>", "<ul>\n<li>a</li>\n</ul>\n<p>giữa</p>\n<ul>\n<li>b</li>\n</ul>"},
	{"<ul><li>a<ul><li>b</li><li>c</li></ul>d</li><li>e</li></ul>",
		"<ul>\n<li>a</li>\n<li>b</li>\n<li>c</li>\n<li>d</li>\n<li>e</li>\n</ul>"},
	{"<ol><li>a<ul><li>b<ol><li>c</li></ol></li></ul></li></ol>", "<ol>\n<li>a</li>\n<li>b</li>\n<li>c</li>\n</ol>"},
	{"<ul><li>a<ul>b</ul>c</li></ul>", "<ul>\n<li>a</li>\n<li>b</li>\n<li>c</li>\n</ul>"},
	{"<ul><li><p>a</p><p>b</p></li><li><h3>c</h3>d</li></ul>", "<ul>\n<li>a<br>b</li>\n<li>c<br>d</li>\n</ul>"},
	{"<ul><li>a<li>b</ul>", "<ul>\n<li>a</li>\n<li>b</li>\n</ul>"},
	{"<ul>x<p>y</p></ul><p><b>z</b> <b>w</b></p>", "<ul>\n<li>x<br>y</li>\n</ul>\n<p><strong>z</strong> <strong>w</strong></p>"},
	{"<blockquote>a<h2>b</h2><blockquote>c</blockquote><ul><li>d</li></ul></blockquote>",
		"<blockquote>\n<p>a</p>\n<p>b</p>\n<p>c</p>\n<p>d</p>\n</blockquote>"},
	{"<figure><figcaption>Chú thích</figcaption></figure>Văn bản", "<p>Chú thích</p>\n<p>Văn bản</p>"},
	{"a<hr>b", "<p>a</p>\n<hr>\n<p>b</p>"},
	{"<p>one<p>two<h1>three<h2>four</h2>", "<p>one</p>\n<p>two</p>\n<h2>three</h2>\n<h2>four</h2>"},
	{"<h1>Title</h4>after", "<h2>Title</h2>\n<p>after</p>"},
	{`<b style="font-weight:normal" id="docs-internal-guid-1"><p>không đậm</p></b><b style="font-weight: 700">đậm</b>`,
		"<p>không đậm</p>\n<p><strong>đậm</strong></p>"},
}

// Elements outside the grammar are read as the element of the grammar they stand for: div, section,
// article, aside, header, footer, main, nav, td, th, dd (and HTML's other blocks) are p; pre and
// listing are pre holding one code; h1 is h2; b is strong, i is em, ins is u, del and strike are s,
// kbd, samp and tt are code; menu is ul; a stray li gets a ul; a nested list is flattened into the
// outer one; a block inside a list item is a line break; a quote holds paragraphs only; hr stays.
//
// CONTROL: with a nested list no longer ending the outer item (enterList's listPlace case made
// b.boundary()), the text written straight inside a nested list ran into its outer item as a line
// break and this test failed.
func TestMappedElements(t *testing.T) {
	checkCases(t, mappedCases)
}

var unwrappedCases = []cleanCase{
	{"<p><span>a</span><font color=red>b</font><mark>c</mark> H<sub>2</sub>O x<sup>2</sup> <var>v</var> <small>s</small> <abbr>t</abbr> <big>b</big></p>",
		"<p>abc H2O x2 v s t b</p>"},
	{"<p><x-card>tuỳ chỉnh</x-card> <o:p>Word</o:p> <unknown>lạ</unknown> <video>dự phòng</video></p>",
		"<p>tuỳ chỉnh Word lạ dự phòng</p>"},
	{`<p><a href="javascript:x">a</a> <a href="#top">b</a> <a href="trang.html">c</a> <a>d</a> <a href="//evil.example">e</a></p>`,
		"<p>a b c d e</p>"},
	{`<p><a href="/x">a <span>b</span> <a href="javascript:x">c</a></a></p>`, `<p><a href="/x">a b </a>c</p>`},
}

// Elements the grammar has no place for are unwrapped, their text kept: span, font, mark, sub, sup,
// var, small, abbr, big, unknown and custom tags, and an a whose address the link rule refuses.
//
// CONTROL: with unknown tags dropped instead (start() reading an element not in the roles table as
// dropRole), their text was lost and this test failed.
func TestUnwrappedElements(t *testing.T) {
	checkCases(t, unwrappedCases)
}

var droppedCases = []cleanCase{
	{"<p>trước <script>alert(1)</script>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <style>p{color:red}</style>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <textarea><b>x</b></textarea>sau</p>", "<p>trước sau</p>"},
	{"<title>Tên trang</title><p>sau</p>", "<p>sau</p>"},
	{"<p>trước <xmp><b>x</b></xmp>sau</p>", "<p>trước</p>\n<p>sau</p>"},
	{"<p>trước <iframe><b>x</b></iframe>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <noembed><b>x</b></noembed>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <noframes><b>x</b></noframes>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <noscript><b>x</b></noscript>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <template><b>x</b></template>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <plaintext><b>x</b></plaintext>sau</p>", "<p>trước</p>"},
	{"<p>trước <svg><text>x</text><p>y</svg>sau</p>", "<p>trước</p>\n<p>ysau</p>"}, // as in a browser: the p broke out, </svg> is then nothing
	{"<p>trước <svg><g><text>x</text></g></svg>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <math><mi>x</mi><mo>=</mo></math>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <form><p>x</p><input name=a></form>sau</p>", "<p>trước</p>\n<p>sau</p>"},
	{"<p>trước <object data=x><b>x</b></object>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <select><option>x</option></select>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <button>x</button>sau</p>", "<p>trước sau</p>"},
	{"<p>trước <base href=/><meta charset=utf-8><link rel=x><embed src=x><img src=x alt=y><input value=z>sau</p>", "<p>trước sau</p>"},
}

// Some elements go with everything they hold: script, style, textarea, title, xmp, iframe, noembed,
// noframes, noscript and template (raw text to their end tag), plaintext (to the end of the input),
// svg and math (the whole foreign subtree, up to where a browser itself leaves it), form, object,
// select and button; base, meta, link, embed, img and input hold nothing and go.
//
// CONTROL: with noscript taken out of the raw text elements, its text came out as HTML ("x" bold) and
// this test failed.
func TestDroppedWithTheirContent(t *testing.T) {
	checkCases(t, droppedCases)
}

var skippedCases = []cleanCase{
	{"a<!-- chú thích -->b", "<p>ab</p>"},
	{"a<!-->b", "<p>ab</p>"},
	{"a<!--->b", "<p>ab</p>"},
	{"a<!---->b", "<p>ab</p>"},
	{"a<!--->-->b", "<p>a--&gt;b</p>"},
	{"a<!-- x -- y --->b", "<p>ab</p>"},
	{"a<!-- x --!>b", "<p>ab</p>"},
	{"a<?php echo 1 ?>b", "<p>ab</p>"},
	{"a</1>b", "<p>ab</p>"},
	{"a</>b", "<p>ab</p>"},
	{"a<![CDATA[x]]>b", "<p>ab</p>"},
	{"a<![CDATA[x>y]]>b", "<p>ay]]&gt;b</p>"},
	{"<!DOCTYPE html><p>x</p>", "<p>x</p>"},
	{"a<!-- không bao giờ đóng <b>x</b>", "<p>a</p>"},
	{"a<b title='không bao giờ đóng>x</b>", "<p>a</p>"},
}

// Comments, "<!-->" and "<!--->" (comments that end as they begin), processing instructions, end tags
// that are not tags ("</1>", "</>"), CDATA in HTML and doctypes are skipped, ending where a browser ends
// them; a comment or a tag without its end loses the rest of the input, never lets it through.
//
// CONTROL: with "<!-->" no longer ending the comment it opens (that prefix check removed from
// declarationEnd), "a<!-->b" lost its b and this test failed.
func TestSkippedMarkup(t *testing.T) {
	checkCases(t, skippedCases)
}

var attributeCases = []cleanCase{
	{`<p id="x" class="y" style="color:red" onclick="alert(1)" title="t" dir="ltr" lang="vi">a</p>`, "<p>a</p>"},
	{`<a href="/x" title="t" target="_blank" rel="opener" onclick="x" class="c" id="i" name="n" style="s">a</a>`,
		`<p><a href="/x">a</a></p>`},
	{`<a href="/ok" href="javascript:alert(1)">a</a>`, `<p><a href="/ok">a</a></p>`},
	{`<a href="javascript:alert(1)" HREF="/ok">a</a>`, "<p>a</p>"},
	{`<a HREF="/ok">a</a>`, `<p><a href="/ok">a</a></p>`},
	{`<p href="/x">a</p><strong href="/x">b</strong>`, "<p>a</p>\n<p><strong>b</strong></p>"},
	{`<a href=/ok>a</a> <a href='/ok'>b</a> <a href = "/ok" >c</a> <a/href="/ok">d</a> <a href="/ok"title="t">e</a>`,
		`<p><a href="/ok">a</a> <a href="/ok">b</a> <a href="/ok">c</a> <a href="/ok">d</a> <a href="/ok">e</a></p>`},
	{`<a =href="/x">a</a> <a href>b</a> <a href="">c</a>`, "<p>a b c</p>"},
	{`<a srcdoc="<script>alert(1)</script>" formaction="javascript:x" xlink:href="javascript:x">a</a>`, "<p>a</p>"},
	{`<p><img src=x onerror=alert(1)>a</p>`, "<p>a</p>"},
}

// Every attribute is dropped except the href of an a, the first one when it is written twice (as a
// browser takes it), read through the tokenizer's attribute states: unquoted, single quoted, spaces
// around "=", a "/" before it, no space after a quoted value. (A block's alignment is no attribute kept:
// the writer spells its class, TestAlignment.)
//
// CONTROL: with the last href kept instead of the first (attribute() no longer checking hasHref),
// <a href="/ok" href="javascript:…"> lost its link and this test failed.
func TestOnlyHrefSurvives(t *testing.T) {
	checkCases(t, attributeCases)
}

var textCases = []cleanCase{
	{"<p>a\xffb\xc3</p>", "<p>a\uFFFDb\uFFFD</p>"},
	{"<p>a&nbsp;&nbsp;b\u00a0\u00a0c</p>", "<p>a b c</p>"},
	{"<p>a\u200bb\ufeffc&#x200B;d</p>", "<p>abcd</p>"},
	{"<p>a\x00b&#0;c</p>", "<p>ab\uFFFDc</p>"},
	{"<p>  a \n\t b  \f</p>", "<p>a b</p>"},
	{"<p>a <b> b</b> <i> </i> c</p>", "<p>a <strong>b</strong> c</p>"},
	{"<p>a <br> b</p>", "<p>a<br>b</p>"},
	{"<p><br>a<br><br></p>", "<p>a</p>"},
	{"<p></p><p> </p><p><br></p><h2>&nbsp;</h2><ul><li> </li></ul><blockquote><p></p></blockquote><ol></ol>", ""},
	{"", ""},
	{"   \n\n  ", ""},
	{"<p></p>", ""},
}

// Text is valid UTF-8 (an invalid byte is U+FFFD), U+00A0 is a space, U+200B, U+FEFF and NUL go, white
// space collapses the way a browser lays it out, empty blocks go and an empty document is "".
//
// CONTROL: with U+00A0 no longer read as a space (normalizeText's U+00A0 case removed), the
// non-breaking spaces stayed and this test failed.
func TestText(t *testing.T) {
	checkCases(t, textCases)
	d := Clean("<p></p>")
	if !d.Empty() || d.Text != "" || d.Words != 0 || d.Characters != 0 {
		t.Errorf("an empty document: %#v", d)
	}
	if Clean("<p>a</p>").Empty() {
		t.Errorf("a document with a word is empty")
	}
}

var plainCases = []cleanCase{
	{"một\nhai\n\nba", "<p>một<br>hai</p>\n<p>ba</p>"},
	{"một\r\nhai\r\n\r\nba\rbốn", "<p>một<br>hai</p>\n<p>ba<br>bốn</p>"},
	{"  thụt lề\n \t \n  dòng   sau  ", "<p>thụt lề</p>\n<p>dòng sau</p>"},
	{"1 < 2 & 3 > 2, a <3 b", "<p>1 &lt; 2 &amp; 3 &gt; 2, a &lt;3 b</p>"},
	{"&amp; &lt;b&gt; giữ nguyên", "<p>&amp;amp; &amp;lt;b&amp;gt; giữ nguyên</p>"},
	{"cuối </", "<p>cuối &lt;/</p>"},
	{"\n\n\nchỉ một\n\n\n", "<p>chỉ một</p>"},
	{"một\nhai<!-- -->", "<p>một hai</p>"},
}

// A source with no tags at all is plain text: a blank line starts a paragraph, a single newline is a
// line break, and its characters are text as written ("&amp;" stays "&amp;"). One piece of markup and
// it is HTML: its line breaks are white space.
//
// CONTROL: with every source read as HTML (hasMarkup always true), the lines of plain text ran into
// one paragraph and this test failed.
func TestPlainText(t *testing.T) {
	checkCases(t, plainCases)
}

var canonicalCases = []cleanCase{
	{`<em><strong><a href="/x">t</a></strong></em>`, `<p><a href="/x"><strong><em>t</em></strong></a></p>`},
	{"<strong>a</strong><strong>b</strong> <b>c</b>", "<p><strong>ab</strong> <strong>c</strong></p>"},
	{"<i>a</i><em>b</em>", "<p><em>ab</em></p>"},
	{`<a href="/x">a</a><a href="/x">b</a>`, `<p><a href="/x">ab</a></p>`},
	{"<strong>a<em>b</em>c</strong>", "<p><strong>a<em>b</em>c</strong></p>"},
	{"<em>a<strong>b</strong>c</em>", "<p><em>a</em><strong><em>b</em></strong><em>c</em></p>"},
	{"<strong>a<br>b</strong>", "<p><strong>a<br>b</strong></p>"},
	{"<b>a<i>b</b>c</i>", "<p><strong>a<em>b</em></strong><em>c</em></p>"},
	{"<a href='/x'>a</a>", `<p><a href="/x">a</a></p>`},
	{"<h2>a</h2><p>b</p><ul><li>c</li><li>d</li></ul><blockquote>e</blockquote>",
		"<h2>a</h2>\n<p>b</p>\n<ul>\n<li>c</li>\n<li>d</li>\n</ul>\n<blockquote>\n<p>e</p>\n</blockquote>"},
	{`<code><s><u><em><strong><a href="/x">t</a></strong></em></u></s></code>`,
		`<p><a href="/x"><strong><em><u><s><code>t</code></s></u></em></strong></a></p>`},
	{"<u>a<code>b</code>c</u>", "<p><u>a<code>b</code>c</u></p>"},
	{"<code>a<b>b</b>c</code>", "<p><code>a</code><strong><code>b</code></strong><code>c</code></p>"},
	{"<s>a</s><del>b</del> <u>c</u><ins>d</ins> <code>e</code><kbd>f</kbd>", "<p><s>ab</s> <u>cd</u> <code>ef</code></p>"},
	{"<code>a<br>b</code>", "<p><code>a<br>b</code></p>"},
}

// The output has one form: marks nested a > strong > em > u > s > code whatever the source's order,
// neighbouring runs with the same marks joined, href and an alignment class in double quotes, every
// block on a line of its own. Clean(Clean(x)) is Clean(x) for every case of every table (checkClean).
//
// CONTROL: with neighbouring runs never joined (sameMarks always false), the tree kept the runs of
// <strong>a</strong><strong>b</strong> apart while the output wrote one strong, and this test failed:
// the output did not say the tree it was built from.
//
// CONTROL: with code written outside u and s (markTags ordered strong, em, code, u, s), the row of the
// five marks and the row of code inside u failed on their want.
func TestCanonicalForm(t *testing.T) {
	checkCases(t, canonicalCases)
}

var alignedCases = []cleanCase{
	{`<p style="text-align:center">a</p><p style="TEXT-ALIGN: Right !important">b</p><p style="text-align:end">c</p>` +
		`<p style="text-align:left">d</p><p style="text-align:justify">e</p><p style="text-align:start">f</p>`,
		"<p class=\"align-center\">a</p>\n<p class=\"align-right\">b</p>\n<p class=\"align-right\">c</p>\n<p>d</p>\n<p>e</p>\n<p>f</p>"},
	{`<p align="center">a</p><p align=right>b</p><p align="justify">c</p><center>d</center><h3 align="middle">e</h3>`,
		"<p class=\"align-center\">a</p>\n<p class=\"align-right\">b</p>\n<p>c</p>\n<p class=\"align-center\">d</p>\n<h3 class=\"align-center\">e</h3>"},
	{`<p class="align-center">a</p><h2 class="align-right">b</h2><h6 class="align-center">c</h6>`,
		"<p class=\"align-center\">a</p>\n<h2 class=\"align-right\">b</h2>\n<h6 class=\"align-center\">c</h6>"},
	// style over class over the align attribute; a value not read leaves the next one
	{`<p style="text-align:left" class="align-center" align="right">a</p><p class="align-center" align="right">b</p>` +
		`<p style="text-align:inherit" class="align-right">c</p><p style="text-align:-webkit-center" align="center">d</p>`,
		"<p>a</p>\n<p class=\"align-center\">b</p>\n<p class=\"align-right\">c</p>\n<p class=\"align-center\">d</p>"},
	// the last alignment class wins, the other classes go
	{`<p class="lead align-center align-right big">a</p>`, `<p class="align-right">a</p>`},
	// align places a table, not its words; it aligns a cell's
	{`<table align="center"><tr><td>a</td><td align="right">b</td></tr></table><section align="center">c</section>`,
		"<p>a</p>\n<p class=\"align-right\">b</p>\n<p>c</p>"},
	// inherited by the blocks inside, undone by one of them
	{`<div style="text-align:center"><p>a</p><h3>b</h3><p style="text-align:left">c</p>d</div>e`,
		"<p class=\"align-center\">a</p>\n<h3 class=\"align-center\">b</h3>\n<p>c</p>\n<p class=\"align-center\">d</p>\n<p>e</p>"},
	// a quote's paragraphs take it; a list's items, a code block and a span do not
	{`<blockquote style="text-align:right"><p>a</p>b</blockquote><ul align="center"><li>c</li></ul><pre style="text-align:center">d</pre><p><span style="text-align:center">e</span></p>`,
		"<blockquote>\n<p class=\"align-right\">a</p>\n<p class=\"align-right\">b</p>\n</blockquote>\n<ul>\n<li>c</li>\n</ul>\n<pre><code>d</code></pre>\n<p>e</p>"},
}

// A paragraph, a heading and a quote's paragraph keep an alignment, written class="align-center" or
// class="align-right" (left is no class), read from the block of the source the way CSS ranks it: the
// last text-align of its style (center; right or end; left, start or justify for none), else the last
// align-center or align-right among its classes, else its align attribute on p, div, h1–h6, tr, td or
// th (center or middle, right, left or justify; on a table it places the table, not its words), else
// <center>; a value not read leaves the next one, and the blocks inside inherit it. A list's item, a
// code block and an inline element take none.
//
// CONTROL: with explicitAlignment no longer reading the class (its hasClass branch disabled), this test
// failed at its first row, whose source writes no class: Clean of Clean's own output lost the alignment
// (Clean(Clean(x)) != Clean(x), checkClean), since the alignment Clean writes is a class.
func TestAlignment(t *testing.T) {
	checkCases(t, alignedCases)
}

var codeCases = []cleanCase{
	// white space and newlines as written; the one line feed right after <pre> goes, as in a browser
	{"<pre>\n\n  a  \tb\n\n</pre>", "<pre><code>\n  a  \tb\n\n</code></pre>"},
	// CR LF and CR are LF; NUL, U+200B and U+FEFF go; U+00A0 is a space
	{"<pre>a\r\nb\rc\x00d\xe2\x80\x8be\xef\xbb\xbff&nbsp;g\xc2\xa0h</pre>", "<pre><code>a\nb\ncdef g h</code></pre>"},
	// a CR written as a reference is LF too, over the whole text: a CR and an LF in two tokens are one
	{"<pre>a&#13;b&#13;&#10;c</pre>", "<pre><code>a\nb\nc</code></pre>"},
	{"<pre>a&#13;<b>&#10;b</b></pre>", "<pre><code>a\nb</code></pre>"},
	{"<pre>&#13;&#10;x&#x0D;y&#13z</pre><pre><code>x&#13;&#13;y</code></pre>", "<pre><code>\nx\ny\nz</code></pre>\n<pre><code>x\n\ny</code></pre>"},
	// a br is a newline, a block inside starts a new line, marks and links are text
	{"<pre>a<br>b<div>c</div>d<p>e<p>f</pre>", "<pre><code>a\nb\nc\nd\ne\nf</code></pre>"},
	{`<pre><code class="language-go"><b>x</b> <a href="/y">y</a> <em><u>z</u></em></code></pre>`, "<pre><code>x y z</code></pre>"},
	// escaped on output, read back as text
	{"<pre>&lt;b&gt; &amp;&amp; 1 < 2 > 0</pre>", "<pre><code>&lt;b&gt; &amp;&amp; 1 &lt; 2 &gt; 0</code></pre>"},
	// white space only, or nothing: no block
	{"<pre>  \n\t </pre><pre></pre><pre><code></code></pre>", ""},
	// two pre are two blocks; a pre ends the heading it stands in; inline code collapses its white space
	{"<pre>a</pre><pre>b</pre><h2>c<pre>d</pre>e</h2><p><code>  f   g  </code></p>",
		"<pre><code>a</code></pre>\n<pre><code>b</code></pre>\n<h2>c</h2>\n<pre><code>d</code></pre>\n<h2>e</h2>\n<p><code>f g</code></p>"},
	// in a list or a quote, where no block goes, a pre is code in the line and its line breaks br
	{"<ul><li>chạy <pre>npm  install\nnpm test</pre></li></ul><blockquote><pre>x  y</pre></blockquote>",
		"<ul>\n<li>chạy<code><br>npm install<br>npm test</code></li>\n</ul>\n<blockquote>\n<p><code>x y</code></p>\n</blockquote>"},
}

// A pre of the document is a pre holding one code with text only: its white space and newlines as
// written (the line feed right after <pre> aside, CR LF and CR read as LF, NUL, U+200B and U+FEFF gone,
// U+00A0 a space, as everywhere), a br inside it a newline, a block inside it a new line, a mark or a
// link inside it its text; a code block of white space only goes. In a list or a quote, where the
// grammar has no block for it, it is code in the line, its line breaks br.
//
// CONTROL: with text() handing a code block its text before normalizeText (U+00A0, NUL, U+200B and
// U+FEFF kept), the row of those characters failed, and so did the browser round trip on it
// (TestBrowserCleanHoldsInABrowser: the browser writes U+00A0 back as &nbsp;).
//
// CONTROL: with a code block's text no longer made LF when its pre closes (closed() setting b.code
// as it is, carriageReturns not applied), the three rows of &#13; kept a raw CR ("a\rb\r\nc") and
// failed on their want; the corpus case "a carriage return written as a reference in a pre" failed
// on its want and out of the grammar (a character the text never keeps in a code block), FuzzClean
// failed on the four seeds, and the browser wrote all four back with LF in place of the CR
// (TestBrowserCleanHoldsInABrowser).
func TestCodeBlocks(t *testing.T) {
	checkCases(t, codeCases)
}

var ruleCases = []cleanCase{
	{"<p>a</p><hr><p>b</p>", "<p>a</p>\n<hr>\n<p>b</p>"},
	{"a<hr>b<hr/>c", "<p>a</p>\n<hr>\n<p>b</p>\n<hr>\n<p>c</p>"},
	{"<p>a<hr>b</p>", "<p>a</p>\n<hr>\n<p>b</p>"},
	{"<h2>a</h2><hr><pre>b</pre><hr><ul><li>c</li></ul><hr><h3>d<hr>e</h3>",
		"<h2>a</h2>\n<hr>\n<pre><code>b</code></pre>\n<hr>\n<ul>\n<li>c</li>\n</ul>\n<hr>\n<h3>d</h3>\n<hr>\n<h3>e</h3>"},
	// after a block only: never first, never twice in a row (two around a block that is dropped are
	// two in a row); one that ends the post stays (21/10/2026)
	{"<hr><p>a</p><hr><hr><p> </p><hr><p>b</p><hr>", "<p>a</p>\n<hr>\n<p>b</p>\n<hr>"},
	{"<p>a</p><hr>", "<p>a</p>\n<hr>"},
	{"<p>a</p><hr><hr/><hr>", "<p>a</p>\n<hr>"},
	{"<ul><li>a</li></ul><hr>", "<ul>\n<li>a</li>\n</ul>\n<hr>"},
	{"<pre>a</pre><hr><p> </p>", "<pre><code>a</code></pre>\n<hr>"},
	// the editor's area after rule() on the last block: the empty paragraph for the caret goes, the
	// rule stays
	{"<p>a</p><hr><p><br></p>", "<p>a</p>\n<hr>"},
	// a post of rules only is empty: the first rule has nothing before it
	{"<hr><hr>", ""},
	{"<hr>", ""},
	{"<p> </p><hr><p><br></p>", ""},
	// in a list, a quote or a code block a rule ends a line and is not kept
	{"<ul><li>a<hr>b</li></ul><blockquote><p>c</p><hr><p>d</p></blockquote><pre>e<hr>f</pre>",
		"<ul>\n<li>a<br>b</li>\n</ul>\n<blockquote>\n<p>c</p>\n<p>d</p>\n</blockquote>\n<pre><code>e\nf</code></pre>"},
	{"<ul><li>a<hr></li></ul>", "<ul>\n<li>a</li>\n</ul>"},
}

// An hr of the document stays, written <hr>, after a block: one first (nothing kept before it) or
// right after another (two in a row, or two around a block that is dropped because it is empty) goes,
// and one that ends the post stays (decided 21/10/2026: what the writer sees is what is saved), so a
// post of rules only is empty. Inside a list, a quote or a code block it ends the line it is in and is
// not kept: the grammar has no rule there.
//
// CONTROL (run again 21/10/2026, a copy of text.go through -overlay, controls.py in the scratchpad's
// final-rule): with tidyBlocks keeping a rule wherever it stands (its ruleBlock case left empty), the
// five rows of rules first and doubled failed on their want (and FuzzClean, TestCorpus).
//
// CONTROL (21/10/2026, the same way): with tidyBlocks dropping a rule at the end again (the check of the
// last block put back), the six rows of a rule that ends the post failed on their want, and with them
// the corpus case "rules first, doubled and last" (TestCorpus) and the row of a final rule of
// TestKeptElements.
func TestRules(t *testing.T) {
	checkCases(t, ruleCases)
}

// A word is a run of non-space characters with a letter or a digit in it (a Vietnamese syllable is a
// word, a dash alone is not); characters are code points, line breaks not counted. The text keeps a
// code block's lines as written and counts its words; a rule is one more blank line, and a rule that
// ends the post adds nothing.
//
// CONTROL (21/10/2026, a copy of text.go through -overlay, controls.py in the scratchpad's final-rule):
// with plainText writing a newline for every rule (the last one too), the two rows of a rule that ends
// the post failed on their text ("a\n", "x\n\n"), their counts unchanged.
//
// CONTROL: with every run of non-space characters counted (the letter or digit condition removed from
// countWords), the dash counted as a word and this test failed.
//
// CONTROL: with a code block's text left out of plainText (its codeBlock case removed), the row of a
// code block failed on its text and its counts.
func TestCounts(t *testing.T) {
	for _, c := range []struct {
		source, text      string
		words, characters int
	}{
		{"Tiếng Việt có dấu", "Tiếng Việt có dấu", 4, 17},
		{"3.14 — xin chào!", "3.14 — xin chào!", 3, 16},
		{"<p>a<br>b</p>", "a\nb", 2, 2},
		{"<h2>a</h2><p>b</p><ul><li>c</li><li>d</li></ul><blockquote><p>e</p><p>f</p></blockquote>",
			"a\n\nb\n\nc\nd\n\ne\n\nf", 6, 6},
		{"\U0001F44D ok", "\U0001F44D ok", 1, 4},
		{"e-mail, co-op", "e-mail, co-op", 2, 13},
		{"Việt", "Việt", 1, 6},
		{"--- ... !!!", "--- ... !!!", 0, 11},
		{"", "", 0, 0},
		// a code block's lines as written, a rule one more blank line, the words of code counted
		{"<p>a</p><hr><pre>x = 1\n  y</pre>", "a\n\n\nx = 1\n  y", 4, 9},
		// a rule that ends the post adds nothing; a code block's last newline is its own
		{"<p>a</p><hr>", "a", 1, 1},
		{"<pre>x\n</pre><hr>", "x\n", 1, 1},
		{"<p><code>go test</code> <u>u</u> <s>s</s></p><h5>h</h5>", "go test u s\n\nh", 5, 12},
	} {
		d := Clean(c.source)
		if d.Text != c.text || d.Words != c.words || d.Characters != c.characters {
			t.Errorf("Clean(%q): text %q, %d words, %d characters; want %q, %d, %d",
				c.source, d.Text, d.Words, d.Characters, c.text, c.words, c.characters)
		}
	}
}

var escapedCases = []cleanCase{
	{`<a href="/a?x=1&amp;y=2">a</a>`, `<p><a href="/a?x=1&amp;y=2">a</a></p>`},
	{`<a href="/a?x=1&y=2&region=vn">a</a>`, `<p><a href="/a?x=1&amp;y=2&amp;region=vn">a</a></p>`},
	{`<a href="https://a.example/&quot;x&quot;">a</a>`, `<p><a href="https://a.example/&quot;x&quot;">a</a></p>`},
	{`<a href="https://a.example/?q=&lt;b&gt;">a</a>`, `<p><a href="https://a.example/?q=&lt;b&gt;">a</a></p>`},
	{`<a href="/a&nbsp;b">a</a>`, `<p><a href="/a&nbsp;b">a</a></p>`},
	{`<p>"a" & 'b' &lt;c&gt;</p>`, `<p>"a" &amp; 'b' &lt;c&gt;</p>`},
}

// An address is written escaped, so a browser reads exactly the string the link rule checked: & " < >
// and U+00A0 are references in an href, & < > in text, and no href of any table or corpus case holds a
// raw "&".
//
// CONTROL: with "&" no longer escaped (writeEscaped's "&" case removed), href="/a?x=1&y=2" came out raw
// and this test failed.
func TestAddressIsEscaped(t *testing.T) {
	checkCases(t, escapedCases)
	sources := append([]string(nil), tableSources...)
	for _, c := range corpus(t) {
		sources = append(sources, c.source)
	}
	for _, source := range sources {
		out := Clean(source).HTML
		for rest := out; ; {
			i := strings.Index(rest, `href="`)
			if i < 0 {
				break
			}
			rest = rest[i+6:]
			href := rest[:strings.IndexByte(rest, '"')]
			for k := strings.IndexByte(href, '&'); k >= 0; {
				tail := href[k:]
				if !strings.HasPrefix(tail, "&amp;") && !strings.HasPrefix(tail, "&quot;") && !strings.HasPrefix(tail, "&lt;") &&
					!strings.HasPrefix(tail, "&gt;") && !strings.HasPrefix(tail, "&nbsp;") {
					t.Errorf("a raw & in href %q of Clean(%q)", href, source)
					break
				}
				next := strings.IndexByte(href[k+1:], '&')
				if next < 0 {
					break
				}
				k += 1 + next
			}
		}
	}
}
