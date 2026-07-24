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
		`<div>{{ #if ok }}<p>Yes</p>{{ #else }}<p>No</p>{{ /if }}</div>`,
		`<ul>{{ #for item in items }}<li>{{ item }}</li>{{ /for }}</ul>`,
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
		})
		_ = r.Bind(data).String()
	})
}
