package render

import "github.com/kitwork/engine/value"

// Render là bộ máy hiển thị công nghiệp của Kitwork.
func Render(tmpl string, data any) string {
	return RenderWithDir(tmpl, data, "", "")
}

func RenderWithDir(tmpl string, data any, viewDir string, globalDir string) string {
	tokens := specializeTokens(tmpl)
	node := parse(tokens)

	// Global Scope Injection: Inject '$' as Root Context
	initialScope := make(map[string]value.Value)
	valData := value.New(data)
	initialScope["$"] = valData
	initialScope["__view_dir"] = value.New(viewDir)
	initialScope["__global_view_dir"] = value.New(globalDir)

	// Flatten Data Map into Scope so 'users' is accessible in loops
	if valData.IsMap() {
		for k, v := range valData.Map() {
			initialScope[k] = v
		}
	}

	return eval(node, data, initialScope)
}
