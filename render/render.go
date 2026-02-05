package render

import "github.com/kitwork/engine/value"

// Render là bộ máy hiển thị công nghiệp của Kitwork.
// Support:
// - {{ variable }}
// - {{ if variable }} ... {{ else }} ... {{ end }}
// - {{ range variable }} ... {{ end }}
// - {{ range i, v := list }} ... {{ end }}
// - {{ $variable }} (Legacy/Explicit Raw)
func Render(tmpl string, data any) string {
	tokens := specializeTokens(tmpl)
	node := parse(tokens)

	// Global Scope Injection: Inject '$' as Root Context
	initialScope := map[string]value.Value{
		"$": value.New(data),
	}

	return eval(node, data, initialScope)
}
