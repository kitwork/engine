package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func mkfile(t *testing.T, base, rel, content string) {
	t.Helper()
	p := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The render engine is standalone: New(Config) + Bind(data), no *Tenant, no HTTP. Directory
// defaults to "views" (matching the files below); DefaultMinify:false keeps output predictable.
func newViews(base string) *Render { return New(Config{Base: base}) }

// Layout slots resolve with the @navbar token AND the legacy _navbar_ token; partials on disk keep
// their _x_.kitwork.html names in both cases.
func TestSlotTokens(t *testing.T) {
	render := func(shell string) string {
		base := t.TempDir()
		mkfile(t, base, "views/index.kitwork.html", shell)
		mkfile(t, base, "views/page.kitwork.html", "PAGE {{ title }}")
		mkfile(t, base, "views/_head_.kitwork.html", "<head>HEAD</head>")
		mkfile(t, base, "views/_navbar_.kitwork.html", "<nav>NAV</nav>")
		return newViews(base).Bind(value.New(map[string]any{"title": "X"})).String()
	}
	cases := map[string]string{
		"@ form": `<html>{{ @head }}<body>{{ @navbar }}<main>{{ @page }}</main></body></html>`,
		"_ form": `<html>{{ _head_ }}<body>{{ _navbar_ }}<main>{{ _page_ }}</main></body></html>`,
	}
	for name, shell := range cases {
		out := render(shell)
		for _, want := range []string{"HEAD", "NAV", "PAGE X"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output missing %q\n%s", name, want, out)
			}
		}
	}
}

// A partial can be saved with the CLEAN filename navbar.kitwork.html (matching @navbar).
func TestSlotCleanFilenames(t *testing.T) {
	base := t.TempDir()
	mkfile(t, base, "views/index.kitwork.html", `<html>{{ @head }}<body>{{ @navbar }}<main>{{ @page }}</main></body></html>`)
	mkfile(t, base, "views/page.kitwork.html", "PAGE")
	mkfile(t, base, "views/head.kitwork.html", "<head>CLEANHEAD</head>")
	mkfile(t, base, "views/navbar.kitwork.html", "<nav>CLEANNAV</nav>")
	out := newViews(base).Bind(value.New(map[string]any{})).String()
	for _, want := range []string{"CLEANHEAD", "CLEANNAV", "PAGE"} {
		if !strings.Contains(out, want) {
			t.Errorf("clean-filename slot missing %q:\n%s", want, out)
		}
	}
}

// When both exist, clean navbar.kitwork.html wins over legacy _navbar_.kitwork.html.
func TestSlotCleanWinsOverLegacy(t *testing.T) {
	base := t.TempDir()
	mkfile(t, base, "views/index.kitwork.html", `<body>{{ @navbar }}{{ @page }}</body>`)
	mkfile(t, base, "views/page.kitwork.html", "P")
	mkfile(t, base, "views/navbar.kitwork.html", "CLEAN")
	mkfile(t, base, "views/_navbar_.kitwork.html", "LEGACY")
	out := newViews(base).Bind(value.New(map[string]any{})).String()
	if !strings.Contains(out, "CLEAN") || strings.Contains(out, "LEGACY") {
		t.Errorf("clean navbar.kitwork.html should win over legacy:\n%s", out)
	}
}

// notfound is found by walking UP from the page's folder to the nearest notfound.
func TestNotFoundWalkUp(t *testing.T) {
	base := t.TempDir()
	mkfile(t, base, "views/notfound.kitwork.html", "ROOT404")
	mkfile(t, base, "views/docs/notfound.kitwork.html", "DOCS404")
	suffix := func(p string) string { return filepath.ToSlash(p) }

	r := newViews(base)
	r.path, r.page = "docs", "routing"
	if got := suffix(r.getNotFoundPath()); !strings.HasSuffix(got, "views/docs/notfound.kitwork.html") {
		t.Errorf("page under docs/ should resolve docs/notfound, got %s", got)
	}
	if got := suffix(newViews(base).getNotFoundPath()); !strings.HasSuffix(got, "views/notfound.kitwork.html") {
		t.Errorf("root page should resolve root notfound, got %s", got)
	}
	r3 := newViews(base)
	r3.path, r3.page = "blog", "2026/post"
	if got := suffix(r3.getNotFoundPath()); !strings.HasSuffix(got, "views/notfound.kitwork.html") {
		t.Errorf("deep page should walk up to root notfound, got %s", got)
	}
}

// A custom notfound name still walks up.
func TestNotFoundCustomName(t *testing.T) {
	base := t.TempDir()
	mkfile(t, base, "views/docs/missing.kitwork.html", "DOCSMISS")
	r := newViews(base)
	r.path, r.page, r.notfound = "docs", "x", "missing"
	if got := filepath.ToSlash(r.getNotFoundPath()); !strings.HasSuffix(got, "views/docs/missing.kitwork.html") {
		t.Errorf("custom notfound name should be honored with walk-up, got %s", got)
	}
}

// shouldMinify: honor the explicit flag; otherwise follow the injected DefaultMinify (the caller
// passes !AllowLocal). No dependency on any HTTP/tenant symbol.
func TestShouldMinify(t *testing.T) {
	if (&Render{}).shouldMinify() {
		t.Error("defaultMinify=false, unset → no minify")
	}
	if !(&Render{defaultMinify: true}).shouldMinify() {
		t.Error("defaultMinify=true, unset → minify")
	}
	if (&Render{defaultMinify: true, minifySet: true}).shouldMinify() {
		t.Error("explicit empty minify → OFF despite default")
	}
	if !(&Render{minifySet: true, minify: []string{"css"}}).shouldMinify() {
		t.Error("explicit minify types → ON despite default off")
	}
}
