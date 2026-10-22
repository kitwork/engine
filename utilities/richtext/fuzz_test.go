package richtext

import (
	"strings"
	"testing"
)

// FuzzClean holds every invariant of Clean (checkClean) on any input: the output uses only the
// grammar's elements in the grammar's nesting (p, h2–h6, blockquote of p, ul and ol of li, pre holding
// one code of text only, hr after a block: never first, never right after an hr, and the last block
// may be one; strong, em, u, s, code, a, br), its only attributes are an href holding an allowed
// address with no raw "&" and, on a p or a heading, class="align-center" or class="align-right",
// Clean(Clean(x)) == Clean(x), and the output read again gives the same tree; a rule that ends the
// post adds nothing to the text nor the counts; Display writes that tree with rel added and nothing
// else, and neither writes more than boundFactor times the source. The seeds are the corpus, the table
// cases of the other tests, a long link left open over paragraphs, items and quote paragraphs, five
// marks with an alignment over many paragraphs, rules ending a post after each kind of block, and
// inputs the fuzzer once found (testdata/fuzz/FuzzClean).
//
// CONTROL (21/10/2026, copies of text.go through -overlay, controls.py in the scratchpad's final-rule):
// with tidyBlocks keeping a rule wherever it stands, the eleven seeds of a rule first or doubled failed
// (readGrammar: "a rule first", "a rule right after a rule"), the five seeds of a rule ending a post
// after each kind of block among them; with plainText writing a newline for a rule that ends the post,
// the thirteen seeds whose post ends in a rule failed ("ends in a rule that changes its text").
//
// CONTROL: with writeEscaped no longer escaping a quote in an attribute (its `case '"'` removed), the
// corpus seeds with a quote in their address ("an address with markup inside", "an address breaking
// out of its quotes") ended their href early and this test failed on them: readGrammar found an a with
// another attribute.
//
// CONTROL: with a link carried into every line after its own (line() no longer ending it), the three
// seeds of a long link wrote about 18,000 bytes from 524 to 592 (30 to 34 times), more than the bound
// of their source, and this test failed on them and on no other seed; with the bound taken out of
// checkClean as well, it passed.
//
// CONTROL: with a code block's text written unescaped (write's codeBlock case writing bl.code as it
// is), the seed "a pre holding escaped markup" wrote </pre><script> into the page and this test failed
// on it (readGrammar: a code block holding markup), and on the three other seeds whose code holds a "<"
// or a ">".
func FuzzClean(f *testing.F) {
	for _, c := range corpus(f) {
		f.Add(c.source)
	}
	for _, source := range tableSources {
		f.Add(source)
	}
	link := `<a href="/` + strings.Repeat("a", 256) + `">`
	for _, blocks := range []string{strings.Repeat("<p>x", 64), "<ul>" + strings.Repeat("<li>x", 64),
		"<blockquote>" + strings.Repeat("<p>x", 64)} {
		f.Add(link + blocks)
	}
	f.Add(`<div style="text-align:center"><b><i><u><s><code>` + strings.Repeat("<p>&", 64))
	f.Add("<pre>" + strings.Repeat("a<b>b</b><div>c</div><br>", 16) + "</pre><hr><p>x</p>")
	for _, block := range []string{"<p>a</p>", "<h3>a</h3>", "<blockquote><p>a</p></blockquote>", "<ol><li>a</li></ol>", "<pre>a\n</pre>"} {
		f.Add(block + "<hr><p> </p><hr>")
	}
	f.Fuzz(func(t *testing.T, source string) {
		checkClean(t, source)
	})
}
