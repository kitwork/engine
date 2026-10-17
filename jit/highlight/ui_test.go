package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"{{ '", `{{ "}}`, "{{ `", `\{{`, "{{}}", "{{{", "}}}", "hover:", ":hover", "a:b:c", "a+b,c:", "x(300)", "(-20%",
		`<a b="{{`, `<a b="{{ x`, "<a b='{{ \"'", "<box hover:", "<x.y", "<a-b",
	)
}

func TestUI(t *testing.T) {
	javascript := func(region string) []Token { return markupEmbedded("javascript", region) }
	checkExact(t, "ui", []tokenCase{
		{"", nil},
		// {{ }} in text: the braces are punctuation, the inside is javascript.
		{`<text>{{ user?.name ?? "khách" }}</text>`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "text", Punctuation, ">{{"),
			javascript(` user?.name ?? "khách" `),
			tokensOf(Punctuation, "}}</", Tag, "text", Punctuation, ">"))},
		// … and inside an attribute value, between the parts of the string.
		{`<link value="/san-pham/{{ product.id }}">Xem</link>`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "link", Plain, " ", Attribute, "value", Punctuation, "=", String, `"/san-pham/`, Punctuation, "{{"),
			javascript(" product.id "),
			tokensOf(Punctuation, "}}", String, `"`, Punctuation, ">", Plain, "Xem", Punctuation, "</", Tag, "link", Punctuation, ">"))},
		// The closing }} is the first outside a string, as engine/ui finds it.
		{"<text>{{ \"}}\" + '}}' + `}}` }}!</text>", markupJoin(
			tokensOf(Punctuation, "<", Tag, "text", Punctuation, ">{{"),
			javascript(" \"}}\" + '}}' + `}}` "),
			tokensOf(Punctuation, "}}", Plain, "!", Punctuation, "</", Tag, "text", Punctuation, ">"))},
		// A backslash inside a string takes the quote after it.
		{`<text>{{ '\'}}' }}</text>`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "text", Punctuation, ">{{"),
			javascript(` '\'}}' `),
			tokensOf(Punctuation, "}}</", Tag, "text", Punctuation, ">"))},
		// A '<' inside an expression is javascript, not a tag.
		{"<text>{{ a < b ? `ngày ${today}` : 2 }}</text>", markupJoin(
			tokensOf(Punctuation, "<", Tag, "text", Punctuation, ">{{"),
			javascript(" a < b ? `ngày ${today}` : 2 "),
			tokensOf(Punctuation, "}}</", Tag, "text", Punctuation, ">"))},
		// An expression never closed: to the end of the source in text …
		{"<text>{{ a <b>c", markupJoin(
			tokensOf(Punctuation, "<", Tag, "text", Punctuation, ">{{"),
			javascript(" a <b>c"))},
		// … to the end of its value inside one, even with a }} further on.
		{`<box key="{{ a" b="}}">`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "box", Plain, " ", Attribute, "key", Punctuation, "=", String, `"`, Punctuation, "{{"),
			javascript(" a"),
			tokensOf(String, `"`, Plain, " ", Attribute, "b", Punctuation, "=", String, `"}}"`, Punctuation, ">"))},
		// \{{ writes {{; a lone brace is text; {{}} is two pairs of braces.
		{`<text>\{{ x }} {a} }}</text><i a="\{{ x }}"/>{{}}`, tokensOf(
			Punctuation, "<", Tag, "text", Punctuation, ">", Plain, `\{{ x }} {a} }}`, Punctuation, "</", Tag, "text", Punctuation, "><",
			Tag, "i", Plain, " ", Attribute, "a", Punctuation, "=", String, `"\{{ x }}"`, Punctuation, "/>{{}}")},
		// Three braces: {{ opens, the first }} closes, the last } is text (engine/ui reads it so).
		{"{{{ x }}}", markupJoin(tokensOf(Punctuation, "{{"), javascript("{ x "), tokensOf(Punctuation, "}}", Plain, "}"))},
		// Conditions before ':' are keywords, the attribute an attribute.
		{`<button hover:background="brand" medium+hover:color="ink" ios,android+large:columns="3" scrolled:shadow="small">`, tokensOf(
			Punctuation, "<", Tag, "button", Plain, " ",
			Keyword, "hover", Punctuation, ":", Attribute, "background", Punctuation, "=", String, `"brand"`, Plain, " ",
			Keyword, "medium", Operator, "+", Keyword, "hover", Punctuation, ":", Attribute, "color", Punctuation, "=", String, `"ink"`, Plain, " ",
			Keyword, "ios", Punctuation, ",", Keyword, "android", Operator, "+", Keyword, "large", Punctuation, ":",
			Attribute, "columns", Punctuation, "=", String, `"3"`, Plain, " ",
			Keyword, "scrolled", Punctuation, ":", Attribute, "shadow", Punctuation, "=", String, `"small"`, Punctuation, ">")},
		// An event is the attribute; what follows its ':' is a keyword. Its
		// value is an expression as a whole: the quotes are string, the inside
		// javascript.
		{`<input keydown:escape="close()" input:debounce(300)="search()" keydown:ctrl+k.prevent="open()">`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "input", Plain, " ",
				Attribute, "keydown", Punctuation, ":", Keyword, "escape", Punctuation, "=", String, `"`),
			javascript("close()"),
			tokensOf(String, `"`, Plain, " ",
				Attribute, "input", Punctuation, ":", Keyword, "debounce", Punctuation, "(", Number, "300", Punctuation, ")=",
				String, `"`),
			javascript("search()"),
			tokensOf(String, `"`, Plain, " ",
				Attribute, "keydown", Punctuation, ":", Keyword, "ctrl", Operator, "+", Keyword, "k", Punctuation, ".",
				Keyword, "prevent", Punctuation, "=", String, `"`),
			javascript("open()"),
			tokensOf(String, `"`, Punctuation, ">"))},
		// A name that is only a condition word is the attribute; a list of events; a negative percentage.
		{`<accordion open="{{ x }}" pointer-enter,focus-in="show()" intersect-enter:once.margin(-20%)="go()">`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "accordion", Plain, " ", Attribute, "open", Punctuation, "=", String, `"`, Punctuation, "{{"),
			javascript(" x "),
			tokensOf(Punctuation, "}}", String, `"`, Plain, " ",
				Attribute, "pointer-enter", Punctuation, ",", Attribute, "focus-in", Punctuation, "=", String, `"`),
			javascript("show()"),
			tokensOf(String, `"`, Plain, " ",
				Attribute, "intersect-enter", Punctuation, ":", Keyword, "once", Punctuation, ".", Keyword, "margin",
				Punctuation, "(", Number, "-20%", Punctuation, ")=", String, `"`),
			javascript("go()"),
			tokensOf(String, `"`, Punctuation, ">"))},
		// A condition before an event; a word that is not a condition is the
		// attribute, so tablet:show is text, not show.
		{`<page large:keydown:escape="x()" hover,focus:background="brand" tablet:show="true">`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "page", Plain, " ",
				Keyword, "large", Punctuation, ":", Attribute, "keydown", Punctuation, ":", Keyword, "escape", Punctuation, "=", String, `"`),
			javascript("x()"),
			tokensOf(String, `"`, Plain, " ",
				Keyword, "hover", Punctuation, ",", Keyword, "focus", Punctuation, ":", Attribute, "background", Punctuation, "=", String, `"brand"`, Plain, " ",
				Attribute, "tablet", Punctuation, ":", Keyword, "show", Punctuation, "=", String, `"true"`, Punctuation, ">"))},
		// if, show and for are expressions too, under conditions as well; a
		// value that only looks like code (value="/cart", a word that is not an
		// event) stays text.
		{`<button click="$cart.add(product, 1)" value="/cart"><text if="item.done" mobile:show="open"><list for="item of items" clicked="a()">`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "button", Plain, " ", Attribute, "click", Punctuation, "=", String, `"`),
			javascript("$cart.add(product, 1)"),
			tokensOf(String, `"`, Plain, " ", Attribute, "value", Punctuation, "=", String, `"/cart"`, Punctuation, "><",
				Tag, "text", Plain, " ", Attribute, "if", Punctuation, "=", String, `"`),
			javascript("item.done"),
			tokensOf(String, `"`, Plain, " ", Keyword, "mobile", Punctuation, ":", Attribute, "show", Punctuation, "=", String, `"`),
			javascript("open"),
			tokensOf(String, `"`, Punctuation, "><", Tag, "list", Plain, " ", Attribute, "for", Punctuation, "=", String, `"`),
			javascript("item of items"),
			tokensOf(String, `"`, Plain, " ", Attribute, "clicked", Punctuation, "=", String, `"a()"`, Punctuation, ">"))},
		// An unquoted expression runs to the blank or the '>'.
		{`<button click=go() value=a.b>`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "button", Plain, " ", Attribute, "click", Punctuation, "="),
			javascript("go()"),
			tokensOf(Plain, " ", Attribute, "value", Punctuation, "=", String, "a.b", Punctuation, ">"))},
		// .ui prints &amp; as written: no references. <!-- --> is its only declaration.
		{"<text>a &amp; b</text><!-- x -->y<!DOCTYPE html><?x?>", tokensOf(
			Punctuation, "<", Tag, "text", Punctuation, ">", Plain, "a &amp; b", Punctuation, "</", Tag, "text", Punctuation, ">",
			Comment, "<!-- x -->", Plain, "y<!DOCTYPE html><?x?>")},
		// Tag names: letters, digits, '-', '.'; what follows is an attribute.
		{`<product-card name="a" /><x.y/><Box><box_x>`, tokensOf(
			Punctuation, "<", Tag, "product-card", Plain, " ", Attribute, "name", Punctuation, "=", String, `"a"`, Plain, " ",
			Punctuation, "/><", Tag, "x.y", Punctuation, "/><", Tag, "Box", Punctuation, "><", Tag, "box", Attribute, "_x", Punctuation, ">")},
		// A comment never closed; </ and < that start no tag are text.
		{"a </ b < c<!-- d", tokensOf(Plain, "a </ b < c", Comment, "<!-- d")},
		// <code> holds .ui text: {{ }} still interpolates, no other language.
		{`<code language="javascript">const a = {{ b }};</code>`, markupJoin(
			tokensOf(Punctuation, "<", Tag, "code", Plain, " ", Attribute, "language", Punctuation, "=", String, `"javascript"`,
				Punctuation, ">", Plain, "const a = ", Punctuation, "{{"),
			javascript(" b "),
			tokensOf(Punctuation, "}}", Plain, ";", Punctuation, "</", Tag, "code", Punctuation, ">"))},
		// A value in single quotes may hold double quotes; CRLF between attributes.
		{"<text\r\nalt='{{ a ? \"x\" : \"y\" }}'>", markupJoin(
			tokensOf(Punctuation, "<", Tag, "text", Plain, "\r\n", Attribute, "alt", Punctuation, "=", String, "'", Punctuation, "{{"),
			javascript(` a ? "x" : "y" `),
			tokensOf(Punctuation, "}}", String, "'", Punctuation, ">"))},
	})
}

// What sits between {{ and }} is javascript. Pinned by function so the choice
// holds while the javascript tokenizer itself changes.
func TestUIExpressionsAreJavaScript(t *testing.T) {
	if uiDialect.expression != "javascript" {
		t.Fatalf("ui expressions are read as %q, want javascript", uiDialect.expression)
	}
	if tokenize := lookUpTokenizer(uiDialect.expression); tokenize == nil || !sameTokenizer(tokenize, tokenizeJavaScript) {
		t.Errorf("ui expressions are not handed to tokenizeJavaScript")
	}
}

// The condition words are exactly KITWORK_UI §8's: targets, widths, states.
func TestUIConditionWords(t *testing.T) {
	for _, word := range []string{
		"website", "desktop", "mobile", "ios", "android", "windows", "macos", "linux",
		"small", "medium", "large", "wide", "huge",
		"open", "active", "selected", "scrolled", "hover", "focus", "pressed", "disabled",
	} {
		if !uiConditionWords.has(word) {
			t.Errorf("%q is a condition of KITWORK_UI §8 but not one here", word)
		}
	}
	if len(uiConditionWords) != 21 {
		t.Errorf("%d condition words, want the 21 of KITWORK_UI §8", len(uiConditionWords))
	}
	for _, group := range []string{"", "+", "hover+", ",hover", "hover,,focus", "tablet", "click", "md"} {
		if uiIsConditionGroup(group) {
			t.Errorf("%q read as a condition group", group)
		}
	}
}

// Which attribute values are expressions as a whole: if, show, for and events,
// read past conditions the way uiAttributeName reads the name.
func TestUICodeValues(t *testing.T) {
	for _, name := range []string{
		"if", "show", "for", "click", "keydown:escape", "input:debounce(300)", "pointer-enter,focus-in",
		"large:keydown:escape", "mobile:show", "hover+medium:if", "ready",
	} {
		if !uiCodeValue(name) {
			t.Errorf("%q: the value is an expression, read as text", name)
		}
	}
	for _, name := range []string{
		"", "value", "tablet:show", "clicked", "click,", ",click", "click,value", "hover:color", "large:", "open",
	} {
		if uiCodeValue(name) {
			t.Errorf("%q: the value is text, read as an expression", name)
		}
	}
}

// The samples, spot-checked: a few tokens that only a right reading gives.
func TestUISamples(t *testing.T) {
	checkSamples(t, "ui", map[string][]Token{
		"order.ui": tokensOf(
			Comment, "<!-- Trang quản lý đơn: dữ liệu từ router.view, trạng thái var trong orders.work -->",
			Tag, "page",
			Attribute, "keydown", Punctuation, ":", Keyword, "ctrl", Operator, "+", Keyword, "k",
			Keyword, "scrolled", Punctuation, ":", Attribute, "material",
			Keyword, "medium", Operator, "+", Keyword, "hover", Punctuation, ":", Attribute, "background",
			Attribute, "input", Punctuation, ":", Keyword, "debounce", Punctuation, "(", Number, "300",
			Tag, "order-row",
			Attribute, "click-outside",
			Keyword, "ios", Punctuation, ",", Keyword, "android", Operator, "+", Keyword, "large",
			Plain, "Tổng: ",
			Tag, "code",
		),
		"menu.ui": tokensOf(
			Tag, "product-card",
			String, `"card padding-x-4 max-width-640px"`,
			Plain, "% &amp; phí — ",
		),
	})
}
