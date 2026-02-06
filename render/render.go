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
	initialScope := make(map[string]value.Value)
	valData := value.New(data)
	initialScope["$"] = valData

	// Flatten Data Map into Scope so 'users' is accessible in loops
	if valData.IsMap() {
		for k, v := range valData.Map() {
			initialScope[k] = v
		}
	}

	return eval(node, data, initialScope)
}
