package work

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// $.meta.jsonld is DATA (a JSON string), never markup: it must contain no <script> tag, and a
// tenant value containing "</script>" must be escaped so it cannot break out once the head wraps it.
func TestJSONLDIsSafeJSONNotMarkup(t *testing.T) {
	node := value.New(map[string]any{
		"@type": "FAQPage",
		"name":  "</script><script>alert(1)</script>",
	})
	out := renderJSONLD([]value.Value{node})
	if strings.Contains(out, "<script") {
		t.Fatalf("$.meta.jsonld must be JSON, not markup: %s", out)
	}
	if strings.Contains(out, "</script>") {
		t.Fatalf("raw </script> leaked (not escaped): %s", out)
	}
	if strings.Contains(out, "<") {
		t.Fatalf("raw '<' present — HTML not escaped: %s", out)
	}
	if !strings.Contains(out, `"@context":"https://schema.org"`) {
		t.Fatalf("expected default @context: %s", out)
	}
}

// A caller-supplied @context is preserved, and a single node is emitted as-is (no @graph wrapper).
func TestJSONLDSingleNodeKeepsContext(t *testing.T) {
	node := value.New(map[string]any{
		"@context": "https://example.org",
		"@type":    "Thing",
	})
	out := renderJSONLD([]value.Value{node})
	if !strings.HasPrefix(out, "{") || strings.Contains(out, "@graph") {
		t.Fatalf("single node should be a bare object, not @graph: %s", out)
	}
	if !strings.Contains(out, "https://example.org") || strings.Contains(out, "schema.org") {
		t.Fatalf("caller @context must be preserved, not overridden: %s", out)
	}
}

// Several nodes combine under one @context into an @graph array — still one JSON string.
func TestJSONLDMultipleNodesUseGraph(t *testing.T) {
	a := value.New(map[string]any{"@type": "Organization"})
	b := value.New(map[string]any{"@type": "FAQPage"})
	out := renderJSONLD([]value.Value{a, b})
	if !strings.Contains(out, `"@graph"`) {
		t.Fatalf("multiple nodes should combine into @graph: %s", out)
	}
	if !strings.Contains(out, "Organization") || !strings.Contains(out, "FAQPage") {
		t.Fatalf("both nodes must be present: %s", out)
	}
	if strings.Contains(out, "<script") {
		t.Fatalf("must be JSON, not markup: %s", out)
	}
}
