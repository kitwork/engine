package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Validates the meta model: $.meta is a default binding field fed by (1) router.meta()/.title()
// inherited root→leaf, and (2) a handler's deferred ctx.bind({...}).title(...). The <title> in the
// inherited head must reflect the right source per route.
func TestTreeMetaInheritanceAndBuilder(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-treemeta-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("filesystem.kitwork", "")
	// Root declares site-wide default meta once — inherited by every page.
	write("router.kitwork.js", `import { router } from "kitwork";`+"\n"+`router.meta({ title: "Site Default", type: "website" });`)
	// The inherited head reads $.meta — the always-present default binding field.
	write("index.kitwork.html", `<!doctype html><head><title>{{ $.meta.title }}</title><meta data-type="{{ $.meta.type }}"></head><body>{{ @page }}</body>`)
	write("page.kitwork.html", `<main>home</main>`) // no override → inherits "Site Default"

	// A static page sets its own title in ONE line, no handler.
	write("about/router.kitwork.js", `import { router } from "kitwork";`+"\n"+`router.title("Trang về tôi");`)
	write("about/page.kitwork.html", `<main>about</main>`)

	// A dynamic page sets meta from data via the deferred builder.
	write("notes/{slug}/page.kitwork.html", `<main>{{ note.body }}</main>`)
	write("notes/{slug}/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`const notes = { 'alpha': { title: 'Ghi chú Alpha', body: 'thân bài alpha' } };`+"\n"+
			`router.get((ctx) => {`+"\n"+
			`  const note = notes[ctx.params('slug')];`+"\n"+
			`  return note ? ctx.bind({ note }).title(note.title).type("article") : ctx.notfound();`+"\n"+
			`});`)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	get := func(p string) string {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+p, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec.Body.String()
	}

	cases := []struct{ path, wantTitle, wantBody string }{
		{"/", "Site Default", "home"},                       // inherited default
		{"/about", "Trang về tôi", "about"},                 // folder-level override, no handler
		{"/notes/alpha", "Ghi chú Alpha", "thân bài alpha"}, // dynamic, builder
	}
	for _, c := range cases {
		body := get(c.path)
		if !strings.Contains(body, "<title>"+c.wantTitle+"</title>") {
			t.Errorf("%s: <title> not %q — body=%.200q", c.path, c.wantTitle, body)
		}
		if !strings.Contains(body, c.wantBody) {
			t.Errorf("%s: body missing %q", c.path, c.wantBody)
		}
		wantType := "website"
		if c.path == "/notes/alpha" {
			wantType = "article"
		}
		if !strings.Contains(body, `data-type="`+wantType+`"`) && !strings.Contains(body, `data-type=`+wantType) {
			t.Errorf("%s: meta type not %q — body=%.300q", c.path, wantType, body)
		}
	}
}
