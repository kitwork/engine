package compiler

import (
	"strings"
	"testing"
)

func TestTemplateInterpolationRejectsMalformedSource(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		wantMessage string
	}{
		{
			name:        "unterminated interpolation",
			source:      "`${{#}",
			wantMessage: "unterminated template interpolation",
		},
		{
			name:        "invalid inner expression",
			source:      "`value ${#}`",
			wantMessage: "invalid template expression",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileSource(test.source)
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("CompileSource(%q) error = %v, want %q", test.source, err, test.wantMessage)
			}
		})
	}
}

func TestCompilerRejectsIncompleteObjectAST(t *testing.T) {
	compiler := NewCompiler()
	err := compiler.Compile(&ObjectLiteral{Entries: []ObjectEntry{{}}})
	if err == nil || !strings.Contains(err.Error(), "has no key") {
		t.Fatalf("incomplete object AST error = %v", err)
	}
}
