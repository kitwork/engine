package render

// Render là bộ máy hiển thị công nghiệp của Kitwork.
// Support:
// - {{ variable }}
// - {{ if variable }} ... {{ else }} ... {{ end }}
// - {{ range variable }} ... {{ end }}
// - {{ range i, v := list }} ... {{ end }}
// - {{ $variable }} (Legacy/Explicit Raw)
func Render(tmpl string, data map[string]any) string {
	tokens := specializeTokens(tmpl)
	node := parse(tokens)
	return eval(node, data)
}
