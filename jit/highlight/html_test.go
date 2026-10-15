package highlight

import (
	"path/filepath"
	"reflect"
	"testing"
)

// markupEmbedded is what the scanner makes of region in language: the tokens a
// markup row expects where the markup hands a region to another tokenizer
// (javascript in <script> and in {{ }}). Building them from the registered
// tokenizer keeps a markup row about where the region starts and ends, whatever
// that language's own tokens are.
func markupEmbedded(language, region string) []Token {
	return tokenizeWith(registry[language], region)
}

// markupJoin concatenates token lists and merges neighbours of one kind, as the
// scanner does where a region meets the markup around it.
func markupJoin(lists ...[]Token) []Token {
	var joined []Token
	for _, list := range lists {
		for _, token := range list {
			if count := len(joined); count > 0 && joined[count-1].Kind == token.Kind {
				joined[count-1].Text += token.Text
				continue
			}
			joined = append(joined, token)
		}
	}
	return joined
}

// checkSamples spot-checks named samples of a language with checkContains, and
// fails when a named sample is missing, so a renamed file cannot pass unseen.
func checkSamples(t *testing.T, language string, wanted map[string][]Token) {
	t.Helper()
	seen := map[string]bool{}
	for _, item := range fixturesFor(t, language) {
		name := filepath.Base(item.path)
		if tokens, listed := wanted[name]; listed {
			seen[name] = true
			checkContains(t, language, []tokenCase{{item.source, tokens}})
		}
	}
	for name := range wanted {
		if !seen[name] {
			t.Errorf("testdata/%s/%s is missing; its spot checks did not run", language, name)
		}
	}
}

// sameTokenizer reports whether two tokenizers are the same function.
func sameTokenizer(first, second tokenizer) bool {
	return reflect.ValueOf(first).Pointer() == reflect.ValueOf(second).Pointer()
}

func init() {
	hostileFragments = append(hostileFragments,
		"<!-->", "<!--->", "</SCRIPT >", "<script type=", "<textarea>", "<title>", "<?xml ", "<?x?", "</>", "</ ",
		"<!doctype", "<!DOCTYPE html PUBLIC \"", "&#x", "&#", "&a", "&amp", "<a b=c", "<a\x00", "<\x00",
	)
}

func TestHTML(t *testing.T) {
	checkExact(t, "html", []tokenCase{
		{"", nil},
		// The doctype: punctuation, a keyword, plain words, quoted identifiers.
		{"<!DOCTYPE html>", tokensOf(Punctuation, "<!", Keyword, "DOCTYPE", Plain, " html", Punctuation, ">")},
		{`<!doctype html PUBLIC "-//W3C//DTD HTML 4.01//EN">`, tokensOf(
			Punctuation, "<!", Keyword, "doctype", Plain, " html PUBLIC ", String, `"-//W3C//DTD HTML 4.01//EN"`, Punctuation, ">")},
		// Comments, the abruptly closed ones of the standard, and one never closed.
		{"a<!-- b -->c<!-->d<!--->e<!--- f", tokensOf(
			Plain, "a", Comment, "<!-- b -->", Plain, "c", Comment, "<!-->", Plain, "d", Comment, "<!--->", Plain, "e", Comment, "<!--- f")},
		// Attributes: double, single, unquoted and bare.
		{`<a href="/x" class='y' data-n=3 hidden>`, tokensOf(
			Punctuation, "<", Tag, "a", Plain, " ", Attribute, "href", Punctuation, "=", String, `"/x"`,
			Plain, " ", Attribute, "class", Punctuation, "=", String, "'y'",
			Plain, " ", Attribute, "data-n", Punctuation, "=", String, "3",
			Plain, " ", Attribute, "hidden", Punctuation, ">")},
		// Whitespace around '=', a self-closing tag.
		{`<img src = "a.png" />`, tokensOf(
			Punctuation, "<", Tag, "img", Plain, " ", Attribute, "src", Plain, " ", Punctuation, "=", Plain, " ",
			String, `"a.png"`, Plain, " ", Punctuation, "/>")},
		// An unquoted value runs to '>' and takes a '/' before it.
		{"<a href=/x/>t</a>", tokensOf(
			Punctuation, "<", Tag, "a", Plain, " ", Attribute, "href", Punctuation, "=", String, "/x/", Punctuation, ">",
			Plain, "t", Punctuation, "</", Tag, "a", Punctuation, ">")},
		// Character references need their semicolon; a bare & is text.
		{"Tom &amp; Jerry &#9733;&#x2615; & co &copy &nope; &#x;", tokensOf(
			Plain, "Tom ", Constant, "&amp;", Plain, " Jerry ", Constant, "&#9733;&#x2615;", Plain, " & co &copy ",
			Constant, "&nope;", Plain, " &#x;")},
		// … inside attribute values too, where they break the string.
		{`<a href="?a=1&amp;b=2">`, tokensOf(
			Punctuation, "<", Tag, "a", Plain, " ", Attribute, "href", Punctuation, "=",
			String, `"?a=1`, Constant, "&amp;", String, `b=2"`, Punctuation, ">")},
		// A '<' that starts no tag is text.
		{"if a < b && c<3 then <> <\x00", tokensOf(Plain, "if a < b && c<3 then <> <\x00")},
		// End tags; </> is dropped by a browser; </ x> is a bogus comment.
		{"</div ></>x</ y>z", tokensOf(
			Punctuation, "</", Tag, "div", Plain, " ", Punctuation, "></>", Plain, "x", Comment, "</ y>", Plain, "z")},
		// CDATA, a bogus <!…> comment, an XML declaration read like a tag.
		{`<![CDATA[ a < b ]]><!ELEMENT x><?xml version="1.0"?>`, tokensOf(
			Punctuation, "<![CDATA[", String, " a < b ", Punctuation, "]]>", Comment, "<!ELEMENT x>",
			Punctuation, "<?", Tag, "xml", Plain, " ", Attribute, "version", Punctuation, "=", String, `"1.0"`, Punctuation, "?>")},
		// <style> holds css.
		{"<style>a{color:red}</style>", tokensOf(
			Punctuation, "<", Tag, "style", Punctuation, ">",
			Tag, "a", Punctuation, "{", Property, "color", Punctuation, ":", Constant, "red",
			Punctuation, "}</", Tag, "style", Punctuation, ">")},
		// <script> holds javascript up to its own end tag, whatever a string inside says.
		{`<script>if (a < b) x = "</div>";</script>`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "script", Punctuation, ">"),
			markupEmbedded("javascript", `if (a < b) x = "</div>";`),
			tokensOf(Punctuation, "</", Tag, "script", Punctuation, ">"))},
		// The end tag is found in any case, and only as the whole name.
		{"<SCRIPT>a</scripts>b</Script >", markupJoin(
			tokensOf(Punctuation, "<", Tag, "SCRIPT", Punctuation, ">"),
			markupEmbedded("javascript", "a</scripts>b"),
			tokensOf(Punctuation, "</", Tag, "Script", Plain, " ", Punctuation, ">"))},
		// A JSON type is json; a data block is plain text, tags and all.
		{`<script type="application/ld+json">{"a":1}</script>`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "script", Plain, " ", Attribute, "type", Punctuation, "=", String, `"application/ld+json"`, Punctuation, ">"),
			markupEmbedded("json", `{"a":1}`),
			tokensOf(Punctuation, "</", Tag, "script", Punctuation, ">"))},
		{`<script type=text/x-template><b>{{ x }}</b></script>`, tokensOf(
			Punctuation, "<", Tag, "script", Plain, " ", Attribute, "type", Punctuation, "=", String, "text/x-template", Punctuation, ">",
			Plain, "<b>{{ x }}</b>", Punctuation, "</", Tag, "script", Punctuation, ">")},
		// A script never closed is javascript to the end; a self-closed one holds nothing.
		{"<script>let a = 1", markupJoin(
			tokensOf(Punctuation, "<", Tag, "script", Punctuation, ">"),
			markupEmbedded("javascript", "let a = 1"))},
		{`<script src="a.js"/>x`, tokensOf(
			Punctuation, "<", Tag, "script", Plain, " ", Attribute, "src", Punctuation, "=", String, `"a.js"`, Punctuation, "/>", Plain, "x")},
		// <textarea> and <title>: references count, tags do not.
		{"<textarea><b>&lt;</b></textarea>", tokensOf(
			Punctuation, "<", Tag, "textarea", Punctuation, ">", Plain, "<b>", Constant, "&lt;", Plain, "</b>",
			Punctuation, "</", Tag, "textarea", Punctuation, ">")},
		// A value never closed runs to the end, '>' included.
		{`<a title="x>y`, tokensOf(Punctuation, "<", Tag, "a", Plain, " ", Attribute, "title", Punctuation, "=", String, `"x>y`)},
		// A '<' inside a tag ends it: a snippet cut short still reads.
		{`<div class="a" <span>b`, tokensOf(
			Punctuation, "<", Tag, "div", Plain, " ", Attribute, "class", Punctuation, "=", String, `"a"`, Plain, " ",
			Punctuation, "<", Tag, "span", Punctuation, ">", Plain, "b")},
		// CRLF between attributes, a NUL in text.
		{"<p\r\nid=a>\x00</p>", tokensOf(
			Punctuation, "<", Tag, "p", Plain, "\r\n", Attribute, "id", Punctuation, "=", String, "a", Punctuation, ">",
			Plain, "\x00", Punctuation, "</", Tag, "p", Punctuation, ">")},
	})
}

// The language of a <script> comes from its type; <style> is css; <textarea>
// and <title> are text with references; other elements are markup. Pinned by
// function so the choice holds while the embedded tokenizers change.
func TestHTMLRawContentLanguages(t *testing.T) {
	for _, row := range []struct{ typeValue, language string }{
		{"", "javascript"},
		{"module", "javascript"},
		{" text/javascript; charset=utf-8 ", "javascript"},
		{"application/ecmascript", "javascript"},
		{"application/json", "json"},
		{"application/ld+json", "json"},
		{"importmap", "json"},
		{"speculationrules", "json"},
		{"text/typescript", "typescript"},
		{"text/x-template", ""},
		{"text/plain", ""},
	} {
		if got := htmlScriptLanguage(row.typeValue); got != row.language {
			t.Errorf("<script type=%q> is %q, want %q", row.typeValue, got, row.language)
		}
	}
	for _, row := range []struct {
		element, typeValue string
		want               tokenizer
		raw                bool
	}{
		{"script", "", registry["javascript"], true},
		{"Script", "application/json", registry["json"], true},
		{"script", "text/x-template", nil, true},
		{"STYLE", "", registry["css"], true},
		{"textarea", "", htmlCharacterData, true},
		{"title", "", htmlCharacterData, true},
		{"div", "", nil, false},
	} {
		got, raw := htmlRawContent(row.element, row.typeValue)
		if raw != row.raw || (got == nil) != (row.want == nil) || (got != nil && !sameTokenizer(got, row.want)) {
			t.Errorf("<%s type=%q>: raw %v, want %v; tokenizer does not match", row.element, row.typeValue, raw, row.raw)
		}
	}
}

// The samples, spot-checked: a few tokens that only a right reading gives.
func TestHTMLSamples(t *testing.T) {
	checkSamples(t, "html", map[string][]Token{
		"storefront.html": tokensOf(
			Punctuation, "<!", Keyword, "DOCTYPE",
			Constant, "&amp;",
			Variable, "--brand",
			Keyword, "@keyframes",
			Attribute, "data-kit-click",
			String, `"/gio-hang?ma=DH-1024`, Constant, "&amp;", String, `buoc=2"`,
			Comment, "<!-- Món nổi bật: server dựng sẵn -->",
			Constant, "&#8363;",
			Plain, "<b>Giao trước 9 giờ</b> ",
			Punctuation, "</", Tag, "textarea",
		),
		"page.html": tokensOf(
			String, "active",
			Constant, "&#x2615;",
			Attribute, "disabled",
		),
	})
}
