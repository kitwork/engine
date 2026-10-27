package hydrate

import "testing"

// An attribute's value is compiled as the browser reads it, its character references decoded once.
func TestCompileAuthoredAttributeUsesBrowserValue(t *testing.T) {
	node, err := Compile(authoredAttribute(`email.includes('@') &amp;&amp; count &gt;= 1`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Eval(node, map[string]any{
		"email": "hello@example.com",
		"count": 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !Truthy(result) {
		t.Fatalf("decoded expression evaluated to %v", result)
	}
}

func TestAuthoredAttributeUsesAttributeContextAndHTMLPreprocessing(t *testing.T) {
	if got := authoredAttribute("'&notit;'\r\n"); got != "'&notit;'\n" {
		t.Fatalf("authoredAttribute ambiguous entity/newline = %q", got)
	}
	if got := authoredAttribute("'&notin;'"); got != "'∉'" {
		t.Fatalf("authoredAttribute named entity = %q", got)
	}
}
