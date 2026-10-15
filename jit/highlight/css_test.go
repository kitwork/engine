package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"url(", "url( ", `url("`, "url(a", "@media", "@keyframes x{", "{from{", "!important", "! ", "#", "#f",
		"U+", "u+?", "U+0-", "--", "-", "-.", "+.5", ".a\\", "\\", "[a=", "[a^=", "::", ":not(", "a{b:c",
		// The look-ahead skips an unquoted url( to its end, so this reads one long
		// statement per '{'; only the memo keeps it from looking ahead again at each.
		"url({",
	)
}

func TestCSS(t *testing.T) {
	checkExact(t, "css", []tokenCase{
		{"", nil},
		// Comments anywhere, one never closed.
		{"/* a */ b {} /* c", tokensOf(
			Comment, "/* a */", Plain, " ", Tag, "b", Plain, " ", Punctuation, "{}", Plain, " ", Comment, "/* c")},
		// Selectors: tags, classes and ids as attributes, pseudo-classes and
		// pseudo-elements as keywords, combinators as operators, * as a tag.
		{`ul > li.item#main:first-child::before, a[href$=".pdf" i] ~ * {}`, tokensOf(
			Tag, "ul", Plain, " ", Operator, ">", Plain, " ", Tag, "li", Attribute, ".item#main",
			Keyword, ":first-child::before", Punctuation, ",", Plain, " ", Tag, "a",
			Punctuation, "[", Attribute, "href", Operator, "$=", String, `".pdf"`, Plain, " ", Keyword, "i", Punctuation, "]",
			Plain, " ", Operator, "~", Plain, " ", Tag, "*", Plain, " ", Punctuation, "{}")},
		// An unquoted attribute value is a string.
		{"input[type=email]{}", tokensOf(
			Tag, "input", Punctuation, "[", Attribute, "type", Operator, "=", String, "email", Punctuation, "]{}")},
		// Declarations: property, punctuation, value words as constants, a sign on a number.
		{"p { color: red; margin: 0 auto -2px; }", tokensOf(
			Tag, "p", Plain, " ", Punctuation, "{", Plain, " ", Property, "color", Punctuation, ":", Plain, " ", Constant, "red",
			Punctuation, ";", Plain, " ", Property, "margin", Punctuation, ":", Plain, " ", Number, "0", Plain, " ",
			Constant, "auto", Plain, " ", Number, "-2px", Punctuation, ";", Plain, " ", Punctuation, "}")},
		// Numbers keep their unit; '-' between spaces is an operator, '+' and '-' before a digit a sign.
		{"a{width:calc(100% - 2.5rem);line-height:1.5;z-index:-1;top:+.5em;transition:opacity 150ms}", tokensOf(
			Tag, "a", Punctuation, "{", Property, "width", Punctuation, ":", Function, "calc", Punctuation, "(",
			Number, "100%", Plain, " ", Operator, "-", Plain, " ", Number, "2.5rem", Punctuation, ");",
			Property, "line-height", Punctuation, ":", Number, "1.5", Punctuation, ";",
			Property, "z-index", Punctuation, ":", Number, "-1", Punctuation, ";",
			Property, "top", Punctuation, ":", Number, "+.5em", Punctuation, ";",
			Property, "transition", Punctuation, ":", Constant, "opacity", Plain, " ", Number, "150ms", Punctuation, "}")},
		// A hexadecimal colour is a number; a hash that is not hexadecimal is a constant.
		{"a{color:#B45309;background:#0003;border-color:#zz}", tokensOf(
			Tag, "a", Punctuation, "{", Property, "color", Punctuation, ":", Number, "#B45309", Punctuation, ";",
			Property, "background", Punctuation, ":", Number, "#0003", Punctuation, ";",
			Property, "border-color", Punctuation, ":", Constant, "#zz", Punctuation, "}")},
		// Custom properties are variables where they are set and where they are read.
		{":root{--brand:#b45309;color:var(--brand, red)}", tokensOf(
			Keyword, ":root", Punctuation, "{", Variable, "--brand", Punctuation, ":", Number, "#b45309", Punctuation, ";",
			Property, "color", Punctuation, ":", Function, "var", Punctuation, "(", Variable, "--brand", Punctuation, ",",
			Plain, " ", Constant, "red", Punctuation, ")}")},
		// An unquoted url(…) is one string, ';' and ',' included; a quoted one is a string in parentheses.
		{`a{background:url(data:image/svg+xml;base64,AA==) , url( "b.png" )}`, tokensOf(
			Tag, "a", Punctuation, "{", Property, "background", Punctuation, ":", Function, "url", Punctuation, "(",
			String, "data:image/svg+xml;base64,AA==", Punctuation, ")", Plain, " ", Punctuation, ",", Plain, " ",
			Function, "url", Punctuation, "(", Plain, " ", String, `"b.png"`, Plain, " ", Punctuation, ")}")},
		// !important is a keyword, written together or apart; another '!' is an operator.
		{"a{color:red!important;margin:0 ! important;x:1 !ie}", tokensOf(
			Tag, "a", Punctuation, "{", Property, "color", Punctuation, ":", Constant, "red", Keyword, "!important", Punctuation, ";",
			Property, "margin", Punctuation, ":", Number, "0", Plain, " ", Keyword, "!", Plain, " ", Keyword, "important", Punctuation, ";",
			Property, "x", Punctuation, ":", Number, "1", Plain, " ", Operator, "!", Constant, "ie", Punctuation, "}")},
		// At-rules: the @name is a keyword; in the prelude, logic words are keywords and features properties.
		{"@media screen and (min-width: 640px) {}", tokensOf(
			Keyword, "@media", Plain, " ", Constant, "screen", Plain, " ", Keyword, "and", Plain, " ", Punctuation, "(",
			Property, "min-width", Punctuation, ":", Plain, " ", Number, "640px", Punctuation, ")", Plain, " ", Punctuation, "{}")},
		{"@media (640px <= width < 1024px){}@supports not (display: grid){}", tokensOf(
			Keyword, "@media", Plain, " ", Punctuation, "(", Number, "640px", Plain, " ", Operator, "<=", Plain, " ",
			Property, "width", Plain, " ", Operator, "<", Plain, " ", Number, "1024px", Punctuation, "){}",
			Keyword, "@supports", Plain, " ", Keyword, "not", Plain, " ", Punctuation, "(", Property, "display",
			Punctuation, ":", Plain, " ", Constant, "grid", Punctuation, "){}")},
		{`@import url(a.css) layer(base);@charset "utf-8";`, tokensOf(
			Keyword, "@import", Plain, " ", Function, "url", Punctuation, "(", String, "a.css", Punctuation, ")", Plain, " ",
			Function, "layer", Punctuation, "(", Constant, "base", Punctuation, ");", Keyword, "@charset", Plain, " ",
			String, `"utf-8"`, Punctuation, ";")},
		// Inside @keyframes, from and to are keywords; after it, names are tags again.
		{"@keyframes spin{from{opacity:0}50%{opacity:.5}to{opacity:1}}a{}", tokensOf(
			Keyword, "@keyframes", Plain, " ", Constant, "spin", Punctuation, "{",
			Keyword, "from", Punctuation, "{", Property, "opacity", Punctuation, ":", Number, "0", Punctuation, "}",
			Number, "50%", Punctuation, "{", Property, "opacity", Punctuation, ":", Number, ".5", Punctuation, "}",
			Keyword, "to", Punctuation, "{", Property, "opacity", Punctuation, ":", Number, "1", Punctuation, "}}",
			Tag, "a", Punctuation, "{}")},
		// Nesting: a rule inside a rule is a selector, its neighbours declarations.
		{".card{color:red;&:hover{color:blue}.title{margin:0}}", tokensOf(
			Attribute, ".card", Punctuation, "{", Property, "color", Punctuation, ":", Constant, "red", Punctuation, ";",
			Keyword, "&:hover", Punctuation, "{", Property, "color", Punctuation, ":", Constant, "blue", Punctuation, "}",
			Attribute, ".title", Punctuation, "{", Property, "margin", Punctuation, ":", Number, "0", Punctuation, "}}")},
		// A declaration pasted on its own reads as one.
		{"color: red;\npadding: 4px", tokensOf(
			Property, "color", Punctuation, ":", Plain, " ", Constant, "red", Punctuation, ";", Plain, "\n",
			Property, "padding", Punctuation, ":", Plain, " ", Number, "4px")},
		// unicode-range values are numbers.
		{"@font-face{unicode-range:U+0000-00FF,u+4??}", tokensOf(
			Keyword, "@font-face", Punctuation, "{", Property, "unicode-range", Punctuation, ":",
			Number, "U+0000-00FF", Punctuation, ",", Number, "u+4??", Punctuation, "}")},
		// Escapes stay inside a class name.
		{`.hover\:underline:hover,.w-1\/2{}`, tokensOf(
			Attribute, `.hover\:underline`, Keyword, ":hover", Punctuation, ",", Attribute, `.w-1\/2`, Punctuation, "{}")},
		// :nth-child's argument: numbers, an operator, of; odd and even are constants.
		{"li:nth-child(2n + 1 of .x):not(:last-child){}tr:nth-child(odd){}", tokensOf(
			Tag, "li", Keyword, ":nth-child", Punctuation, "(", Number, "2n", Plain, " ", Operator, "+", Plain, " ",
			Number, "1", Plain, " ", Keyword, "of", Plain, " ", Attribute, ".x", Punctuation, ")",
			Keyword, ":not", Punctuation, "(", Keyword, ":last-child", Punctuation, "){}",
			Tag, "tr", Keyword, ":nth-child", Punctuation, "(", Constant, "odd", Punctuation, "){}")},
		// Strings take escapes; one left open stops at the line break and the next rule reads.
		{"a{content:\"\\201C\\\"\";font-family:'Open Sans'}b{content:\"oops\n}i{}", tokensOf(
			Tag, "a", Punctuation, "{", Property, "content", Punctuation, ":", String, "\"\\201C\\\"\"", Punctuation, ";",
			Property, "font-family", Punctuation, ":", String, "'Open Sans'", Punctuation, "}",
			Tag, "b", Punctuation, "{", Property, "content", Punctuation, ":", String, "\"oops", Plain, "\n",
			Punctuation, "}", Tag, "i", Punctuation, "{}")},
		// A '{' inside a string, a comment or an unquoted url( does not make a
		// declaration a selector; the rule after the url( is a selector again.
		{"a{content:\"{\";color:/* { */red}", tokensOf(
			Tag, "a", Punctuation, "{", Property, "content", Punctuation, ":", String, `"{"`, Punctuation, ";",
			Property, "color", Punctuation, ":", Comment, "/* { */", Constant, "red", Punctuation, "}")},
		{"a{b:url(x'y);c{}}", tokensOf(
			Tag, "a", Punctuation, "{", Property, "b", Punctuation, ":", Function, "url", Punctuation, "(", String, "x'y",
			Punctuation, ");", Tag, "c", Punctuation, "{}}")},
		// A ';' inside parentheses does not end the declaration.
		{"a{b:f(x;y);c:d}", tokensOf(
			Tag, "a", Punctuation, "{", Property, "b", Punctuation, ":", Function, "f", Punctuation, "(", Constant, "x",
			Punctuation, ";", Constant, "y", Punctuation, ");", Property, "c", Punctuation, ":", Constant, "d", Punctuation, "}")},
		// '-' right after '%' or ')' is an operator, not a sign.
		{"a{b:calc(50%-2px)}", tokensOf(
			Tag, "a", Punctuation, "{", Property, "b", Punctuation, ":", Function, "calc", Punctuation, "(",
			Number, "50%", Operator, "-", Number, "2px", Punctuation, ")}")},
		// Stray braces.
		{"}}{{a", tokensOf(Punctuation, "}}{{", Property, "a")},
	})
}

// The samples, spot-checked: a few tokens that only a right reading gives.
func TestCSSSamples(t *testing.T) {
	checkSamples(t, "css", map[string][]Token{
		"dashboard.css": tokensOf(
			Keyword, "@layer",
			Variable, "--surface",
			Function, "color-mix",
			Attribute, `.md\:grid-cols-3`,
			Keyword, "@container",
			Property, "min-width",
			Keyword, "@keyframes",
			Keyword, "from",
			Number, "62.5%",
			Keyword, "to",
			Tag, "table",
			Keyword, "!important",
			String, "data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg'/>",
			Number, "U+0102-0103",
		),
		"site.css": tokensOf(
			Attribute, ".card",
			Keyword, "::before",
			String, "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=",
		),
	})
}
