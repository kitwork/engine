package work

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

func testTenant(base string) *Tenant {
	return &Tenant{config: &Config{root: base, base: base}, entity: &Entity{Domain: "t.local"}}
}

// Layout slots resolve with the new @navbar token AND the legacy _navbar_ token; the partial files
// on disk keep their _x_.kitwork.html names in both cases.
func TestSlotTokens(t *testing.T) {
	prev := AllowLocal
	AllowLocal = true // local → skip minify so the output is predictable
	defer func() { AllowLocal = prev }()

	render := func(shell string) string {
		base := t.TempDir()
		mkfile(t, base, "views/index.kitwork.html", shell)
		mkfile(t, base, "views/page.kitwork.html", "PAGE {{ title }}")
		mkfile(t, base, "views/_head_.kitwork.html", "<head>HEAD</head>")
		mkfile(t, base, "views/_navbar_.kitwork.html", "<nav>NAV</nav>")
		r := NewRender(testTenant(base))
		return r.Bind(value.New(map[string]any{"title": "X"})).String()
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

// A partial can be saved with the CLEAN filename navbar.kitwork.html (matching the @navbar token),
// not just the legacy _navbar_.kitwork.html.
func TestSlotCleanFilenames(t *testing.T) {
	prev := AllowLocal
	AllowLocal = true
	defer func() { AllowLocal = prev }()

	base := t.TempDir()
	mkfile(t, base, "views/index.kitwork.html", `<html>{{ @head }}<body>{{ @navbar }}<main>{{ @page }}</main></body></html>`)
	mkfile(t, base, "views/page.kitwork.html", "PAGE")
	mkfile(t, base, "views/head.kitwork.html", "<head>CLEANHEAD</head>")
	mkfile(t, base, "views/navbar.kitwork.html", "<nav>CLEANNAV</nav>")

	out := NewRender(testTenant(base)).Bind(value.New(map[string]any{})).String()
	for _, want := range []string{"CLEANHEAD", "CLEANNAV", "PAGE"} {
		if !strings.Contains(out, want) {
			t.Errorf("clean-filename slot missing %q:\n%s", want, out)
		}
	}
}

// When both exist, the clean navbar.kitwork.html wins over legacy _navbar_.kitwork.html.
func TestSlotCleanWinsOverLegacy(t *testing.T) {
	prev := AllowLocal
	AllowLocal = true
	defer func() { AllowLocal = prev }()

	base := t.TempDir()
	mkfile(t, base, "views/index.kitwork.html", `<body>{{ @navbar }}{{ @page }}</body>`)
	mkfile(t, base, "views/page.kitwork.html", "P")
	mkfile(t, base, "views/navbar.kitwork.html", "CLEAN")
	mkfile(t, base, "views/_navbar_.kitwork.html", "LEGACY")

	out := NewRender(testTenant(base)).Bind(value.New(map[string]any{})).String()
	if !strings.Contains(out, "CLEAN") || strings.Contains(out, "LEGACY") {
		t.Errorf("clean navbar.kitwork.html should win over legacy _navbar_:\n%s", out)
	}
}

// notfound is found by walking UP from the page's folder to the nearest notfound — no declaration
// needed.
func TestNotFoundWalkUp(t *testing.T) {
	base := t.TempDir()
	mkfile(t, base, "views/notfound.kitwork.html", "ROOT404")
	mkfile(t, base, "views/docs/notfound.kitwork.html", "DOCS404")
	tenant := testTenant(base)

	suffix := func(p string) string { return filepath.ToSlash(p) }

	// A page under docs/ → the NEAREST notfound is docs/notfound.
	r := NewRender(tenant)
	r.path = "docs"
	r.page = "routing"
	if got := suffix(r.getNotFoundPath()); !strings.HasSuffix(got, "views/docs/notfound.kitwork.html") {
		t.Errorf("page under docs/ should resolve docs/notfound, got %s", got)
	}

	// A page at the root → the root notfound.
	r2 := NewRender(tenant)
	if got := suffix(r2.getNotFoundPath()); !strings.HasSuffix(got, "views/notfound.kitwork.html") {
		t.Errorf("root page should resolve root notfound, got %s", got)
	}

	// A deep page with no closer notfound → still walks up to the root notfound.
	r3 := NewRender(tenant)
	r3.path = "blog"
	r3.page = "2026/post"
	if got := suffix(r3.getNotFoundPath()); !strings.HasSuffix(got, "views/notfound.kitwork.html") {
		t.Errorf("deep page should walk up to root notfound, got %s", got)
	}
}

// .notfound("name") still overrides which filename to look for, while keeping the walk-up.
func TestNotFoundCustomName(t *testing.T) {
	base := t.TempDir()
	mkfile(t, base, "views/docs/missing.kitwork.html", "DOCSMISS")
	r := NewRender(testTenant(base))
	r.path = "docs"
	r.page = "x"
	r.notfound = "missing"
	if got := filepath.ToSlash(r.getNotFoundPath()); !strings.HasSuffix(got, "views/docs/missing.kitwork.html") {
		t.Errorf("custom notfound name should be honored with walk-up, got %s", got)
	}
}
