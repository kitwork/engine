package render

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// A {{ @page }} with neither its page nor a not-found page renders nothing in its place, and Page says
// so apart: a NoPageError whose text names the page looked for, an absolute path on the server, so a
// server can log it or fail the request with it (16/10/2026: in production nothing was said, and a page
// a handler chose that does not exist answered 200 with nothing in it). The page itself carries no path in
// production; while developing a comment names it, as before. It holds for a prepared render, one that
// prepares when it renders, a not-found page and a page chosen at the request. BindPage and Bind give the
// page as they did. CONTROL: a page that is there, and a page that is not with a not-found page to stand
// in for it, say nothing.
func TestAPageWithNeitherItsPageNorANotFoundPageIsToldApart(t *testing.T) {
	root := shellSite(t, map[string]string{
		"index.kitwork.html":      "<main>{{ @page }}</main>",
		"other/page.kitwork.html": "<p>other</p>",
	})
	heard := 0
	hook := func(expression, message string) { heard++ }
	for _, row := range []struct {
		name     string
		config   Config
		prepare  bool
		page     string
		notfound bool
		want     string
	}{
		{"prepared", Config{Base: root, Directory: "."}, true, "", false, "page.kitwork.html"},
		{"not prepared", Config{Base: root, Directory: "."}, false, "", false, "page.kitwork.html"},
		{"not-found page", Config{Base: root, Directory: ".", NotfoundMode: true}, true, "", true, "notfound.kitwork.html"},
		{"page chosen at the request", Config{Base: root, Directory: "."}, true, "nope", false, filepath.Join("nope", "page.kitwork.html")},
	} {
		looked := filepath.Join(root, row.want)
		for _, production := range []bool{true, false} {
			config := row.config
			if production {
				config.ExpressionFailed = hook
			}
			r := New(config)
			if row.prepare {
				r = r.Prepare()
			}
			page, err := r.Page(row.page, row.notfound, value.New(nil))
			var noPage *NoPageError
			if !errors.As(err, &noPage) || !strings.Contains(err.Error(), looked) {
				t.Fatalf("%s (production %v): Page gave error %v, want a NoPageError naming %s", row.name, production, err, looked)
			}
			comment := "<!-- 404: " + looked + " -->"
			if production && page.Text() != "<main></main>" {
				t.Fatalf("%s: production page = %q, want <main></main>", row.name, page.Text())
			}
			if !production && page.Text() != "<main>"+comment+"</main>" {
				t.Fatalf("%s: developing page = %q, want the comment naming %s", row.name, page.Text(), looked)
			}
			if bound := r.BindPage(row.page, row.notfound, value.New(nil)); bound.Text() != page.Text() {
				t.Fatalf("%s (production %v): BindPage gave %q, want %q", row.name, production, bound.Text(), page.Text())
			}
		}
	}
	if heard != 0 {
		t.Fatalf("the expression hook heard %d records, want none: the page that is not there is Page's to say", heard)
	}

	// CONTROL
	root = shellSite(t, map[string]string{
		"index.kitwork.html":      "<main>{{ @page }}</main>",
		"page.kitwork.html":       "<p>home</p>",
		"other/page.kitwork.html": "<p>other</p>",
		"notfound.kitwork.html":   "<p>không có trang này</p>",
	})
	prepared := New(Config{Base: root, Directory: ".", ExpressionFailed: hook}).Prepare()
	for page, want := range map[string]string{
		"":      "<main><p>home</p></main>",
		"other": "<main><p>other</p></main>",
		"nope":  "<main><p>không có trang này</p></main>",
	} {
		got, err := prepared.Page(page, false, value.New(nil))
		if err != nil || got.Text() != want {
			t.Fatalf("CONTROL: Page(%q) = %q, %v; want %q and no error", page, got.Text(), err, want)
		}
	}
}
