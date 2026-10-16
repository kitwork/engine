package render

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// shellSite writes files (paths relative to the site) into a new folder and returns the folder.
func shellSite(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A page with no shell (index.kitwork.html) above it renders no page, and Page says so apart: a
// NoShellError whose text names the shell looked for, an absolute path on the server, so a server can
// keep it from a visitor (16/10/2026: the text was the page of every such 404, in production too). Bind
// gives that text as the page, as it did. It holds for a prepared render, one that prepares when it
// renders, and a page chosen at the request. CONTROL: with a shell the page renders and Page says nothing.
func TestAPageWithNoShellIsToldApart(t *testing.T) {
	root := shellSite(t, map[string]string{
		"page.kitwork.html":       "<p>home</p>",
		"other/page.kitwork.html": "<p>other</p>",
	})
	shell := filepath.Join(root, "index.kitwork.html")
	for _, row := range []struct {
		name     string
		render   *Render
		page     string
		notfound bool
	}{
		{"prepared", New(Config{Base: root, Directory: "."}).Prepare(), "", false},
		{"not prepared", New(Config{Base: root, Directory: "."}), "", false},
		{"not-found page", New(Config{Base: root, Directory: ".", NotfoundMode: true}).Prepare(), "", true},
		{"page chosen at the request", New(Config{Base: root, Directory: "."}).Prepare(), "other", false},
	} {
		page, err := row.render.Page(row.page, row.notfound, value.New(nil))
		var noShell *NoShellError
		if !errors.As(err, &noShell) || !strings.Contains(err.Error(), shell) {
			t.Fatalf("%s: Page gave error %v, want a NoShellError naming %s", row.name, err, shell)
		}
		if page.Text() != err.Error() {
			t.Fatalf("%s: Page gave %q with the error, want its text", row.name, page.Text())
		}
		if bound := row.render.BindPage(row.page, row.notfound, value.New(nil)); bound.Text() != err.Error() {
			t.Fatalf("%s: BindPage gave %q, want the error's text as the page", row.name, bound.Text())
		}
	}

	// CONTROL
	root = shellSite(t, map[string]string{
		"index.kitwork.html":      "<main>{{ @page }}</main>",
		"page.kitwork.html":       "<p>home</p>",
		"other/page.kitwork.html": "<p>other</p>",
	})
	for page, want := range map[string]string{"": "<main><p>home</p></main>", "other": "<main><p>other</p></main>"} {
		got, err := New(Config{Base: root, Directory: "."}).Prepare().Page(page, false, value.New(nil))
		if err != nil || !strings.Contains(got.Text(), want) {
			t.Fatalf("CONTROL: Page(%q) = %q, %v; want %q and no error", page, got.Text(), err, want)
		}
	}
}

// A {{ layout "name" }} whose file cannot be read wrote "[Error: …]" in the page, read from the disk the
// os error with the absolute path looked for, and a page with neither its page nor a not-found page wrote
// <!-- 404: <absolute path> -->, in production too. With Config.ExpressionFailed set, as the engine sets it
// in production, the page carries neither and the hook hears the layout and why; while developing (nil)
// both stand in the page as before. The same holds for a template rendered from text (Render.HTML), which
// reads the layout when it renders. CONTROL: a layout that exists renders in both.
func TestAPageInProductionCarriesNoPathOfTheServer(t *testing.T) {
	root := shellSite(t, map[string]string{
		"index.kitwork.html": `<main>{{ layout "missing" }}{{ layout "here" }}{{ @page }}</main>`,
		"here.html":          "<nav>here</nav>",
	})
	type heard struct{ expression, message string }
	var log []heard
	hook := func(expression, message string) { log = append(log, heard{expression, message}) }
	production := New(Config{Base: root, Directory: ".", NotfoundMode: true, ExpressionFailed: hook})
	development := New(Config{Base: root, Directory: ".", NotfoundMode: true})

	page := production.Bind(value.New(nil)).Text()
	if strings.Contains(page, root) || strings.Contains(page, "[Error") || strings.Contains(page, "<!-- 404") {
		t.Fatalf("production page carries a path of the server or an error:\n%s", page)
	}
	if len(log) != 1 || log[0].expression != `layout "missing"` || !strings.Contains(log[0].message, filepath.Join(root, "views", "missing.html")) {
		t.Fatalf("the hook heard %q, want the layout once with the file looked for", log)
	}
	page = development.Bind(value.New(nil)).Text()
	for _, want := range []string{"[Error: ", filepath.Join(root, "views", "missing.html"), "<!-- 404: " + filepath.Join(root, "notfound.kitwork.html") + " -->"} {
		if !strings.Contains(page, want) {
			t.Fatalf("while developing the page lost %q:\n%s", want, page)
		}
	}

	log = nil
	text := `<p>{{ layout "missing" }}</p>`
	if got := production.HTML(text, nil); got != "<p></p>" || len(log) != 1 || log[0].expression != `layout "missing"` || !strings.Contains(log[0].message, root) {
		t.Fatalf("production HTML = %q, heard %q; want <p></p> and the layout heard with the file looked for", got, log)
	}
	if got := development.HTML(text, nil); !strings.Contains(got, "[Error: ") || !strings.Contains(got, root) {
		t.Fatalf("while developing HTML = %q, want the error in the page", got)
	}

	// CONTROL
	for name, r := range map[string]*Render{"production": production, "developing": development} {
		if page := r.Bind(value.New(nil)).Text(); !strings.Contains(page, "<nav>here</nav>") {
			t.Fatalf("CONTROL %s: the layout that exists is missing:\n%s", name, page)
		}
	}
}
