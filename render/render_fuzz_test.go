package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/value"
)

func FuzzTemplateRender(f *testing.F) {
	seeds := []string{
		`<html><body><h1>{{ title }}</h1></body></html>`,
		`<div>{{ if ok }}<p>Yes</p>{{ else }}<p>No</p>{{ end }}</div>`,
		`<ul>{{ for (index, item) in items }}<li>{{ index + 1 }}: {{ item }}</li>{{ end }}</ul>`,
		`{{ let label = user.name }}{{ label ?? "Guest" }}`,
		`{{ user.active ? user.name : "Offline" }}`,
		`<script>{{ raw(meta.jsonld) }}</script>`,
		`{{ @page }}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		base := t.TempDir()
		p := filepath.Join(base, "views", "index.kitwork.html")
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		_ = os.WriteFile(p, []byte(input), 0644)

		r := New(Config{Base: base})
		data := value.New(map[string]any{
			"title": "Fuzz Test",
			"ok":    true,
			"items": []any{"a", "b", "c"},
			"user": map[string]any{
				"name":   "Kitwork",
				"active": true,
			},
			"meta": map[string]any{
				"jsonld": `{"name":"Kitwork"}`,
			},
		})
		_ = r.Bind(data).String()
	})
}
