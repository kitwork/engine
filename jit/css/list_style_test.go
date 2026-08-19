package css

import (
	"strings"
	"testing"
)

func TestTailwindListStyleUtilities(t *testing.T) {
	tests := []struct {
		className string
		want      string
	}{
		{className: "list-none", want: "list-style-type: none;"},
		{className: "list-disc", want: "list-style-type: disc;"},
		{className: "list-decimal", want: "list-style-type: decimal;"},
	}

	for _, tt := range tests {
		t.Run(tt.className, func(t *testing.T) {
			css, selector, media := ResolveCore(tt.className, nil)
			if css != tt.want {
				t.Fatalf("ResolveCore(%q) CSS = %q, want %q", tt.className, css, tt.want)
			}
			if selector != "."+tt.className {
				t.Fatalf("ResolveCore(%q) selector = %q, want %q", tt.className, selector, "."+tt.className)
			}
			if media != "" {
				t.Fatalf("ResolveCore(%q) media = %q, want no media query", tt.className, media)
			}
		})
	}
}

func TestGenerateJITEmitsListStylesUsedByMarkup(t *testing.T) {
	html := `<ol class="m-0 list-none p-0"><li>One</li></ol>` +
		`<ul class="list-disc"><li>Two</li></ul>` +
		`<ol class="list-decimal"><li>Three</li></ol>`
	generated := GenerateJIT(html, nil)

	for _, want := range []string{
		`.list-none { list-style-type: none; }`,
		`.list-disc { list-style-type: disc; }`,
		`.list-decimal { list-style-type: decimal; }`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated stylesheet missing %q\n%s", want, generated)
		}
	}
}
