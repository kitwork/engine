package hydrate

import (
	"strings"
	"testing"
)

func TestCompileAuthoredAttributeUsesBrowserValue(t *testing.T) {
	node, err := compileAuthoredAttribute(`email.includes('@') &amp;&amp; count &gt;= 1`)
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

func TestPreRenderDecodesExpressionAndModelEntitiesOnce(t *testing.T) {
	input := marker +
		`<input data-kit-model="name" value="Tom &amp; Jerry">` +
		`<input type="number" data-kit-model="count" value="1">` +
		`<b data-kit-text="count &gt; 0 ? name : 'empty'">pending</b>`

	output := PreRender(input)
	if !strings.Contains(output, `>Tom &amp; Jerry</b>`) {
		t.Fatalf("attribute entities were not decoded exactly once:\n%s", output)
	}
	if strings.Contains(output, `&amp;amp;`) {
		t.Fatalf("model value was double escaped:\n%s", output)
	}
}

func TestPreRenderBindDecodesExpressionEntities(t *testing.T) {
	tag := `<button data-kit-component="counter" data-kit-bind="{ disabled: count &lt; 1 }">`
	output := PreRenderBind(tag, map[string]map[string]any{
		"counter": {"count": 0.0},
	})
	if !strings.Contains(output, `disabled=""`) {
		t.Fatalf("encoded bind comparison was not evaluated:\n%s", output)
	}
}
