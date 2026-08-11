package vanilla

import (
	"fmt"
	"strings"
	"testing"

	kitcss "github.com/kitwork/engine/jit/css"
)

const exampleStylesheetName = "kitjs.examples.cb95d3a46e8563f61a36a45167e067f1a1c3e74dbfc504a175358b23802dc881.css"

func TestCheckedExampleStylesMatchJIT(t *testing.T) {
	pages := []struct {
		directory string
		name      string
	}{
		{directory: "preferences", name: "index.html"},
		{directory: "drive-progress", name: "index.html"},
		{directory: "drive-progress", name: "next.html"},
		{directory: "request-form", name: "index.html"},
		{directory: "request-form", name: "design.html"},
	}
	html := make([]string, 0, len(pages))
	for _, page := range pages {
		source := string(readVanillaFile(t, "examples", page.directory, page.name))
		if !strings.Contains(source, fmt.Sprintf(`<link rel="stylesheet" href="../%s">`, exampleStylesheetName)) {
			t.Fatalf("%s/%s does not load the checked JIT stylesheet", page.directory, page.name)
		}
		html = append(html, source)
	}

	want := kitcss.GenerateSiteCSS(nil, html...)
	got := string(readVanillaFile(t, "examples", exampleStylesheetName))
	if got != want {
		t.Fatal("checked example stylesheet is stale; rebuild it with cmd/styles")
	}
	if wantName := "kitjs.examples." + ContentHash([]byte(got)) + ".css"; exampleStylesheetName != wantName {
		t.Fatalf("checked example stylesheet name = %q, want %q", exampleStylesheetName, wantName)
	}
	for _, required := range []string{
		`.max-w-8xl`, `.bg-indigo-600`, `.animate-pulse`,
		`.pointer-events-none`, `.fixed`, `.inset-x-0`, `.top-0`, `.z-50`, `.h-1`, `.sr-only`,
		`.focus-visible\:outline:focus-visible`,
		`.focus-visible\:outline-2:focus-visible`,
		`.focus-visible\:outline-offset-2:focus-visible`,
		`.focus-visible\:outline-indigo-400:focus-visible`,
		`.text-indigo-400`, `.text-indigo-500`,
		`.text-emerald-300`, `.text-rose-300`,
		`.border-indigo-400`, `.focus-visible\:outline-rose-400:focus-visible`,
		`@media (prefers-reduced-motion:reduce)`,
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("checked example stylesheet lost %q", required)
		}
	}
}
