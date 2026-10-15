package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// router.jitcss(config) must feed the render's JIT-CSS engine: a custom brand color from
// theme.extend.colors, and a custom dark: parent selector from darkMode.
func TestJitcssConfigApplied(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-jitcss-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("filesystem.kitwork", "")
	write("router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.jitcss({`+"\n"+
			`  darkMode: ['class', '[data-theme="dark"]'],`+"\n"+
			`  theme: { extend: { colors: { brand: { DEFAULT: '#e8173a' } } } },`+"\n"+
			`});`)
	// A page using a brand utility AND a dark: variant — both must reflect the config.
	write("index.kitwork.html", `<!doctype html><head></head><body class="jit-brand-marker bg-brand dark:text-brand">{{ @page }}</body>`)
	write("page.kitwork.html", `<main>hi</main>`)

	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	body := rec.Body.String()

	// The root router.jitcss() runs while the generation is prepared.
	if tn.SiteGeneration().Presentation().Snapshot().JITConfig == nil {
		t.Fatal("router.jitcss() did not install the generation JIT config")
	}

	// The custom brand color #e8173a (not the default #f82244) must drive .bg-brand.
	if !strings.Contains(body, "#e8173a") || strings.Contains(body, "#f82244") {
		t.Errorf("custom brand color not applied (expected #e8173a, not default #f82244):\n%.400q", body)
	}
	// The dark: variant must scope under the configured selector (minified: quotes stripped).
	if !strings.Contains(body, "[data-theme=dark]") || strings.Contains(body, ".dark .dark") {
		t.Errorf("custom dark selector [data-theme=dark] not applied:\n%.400q", body)
	}
}

// A keyframe from router.css({ theme: { extend: { keyframes } } }) arrives as maps, which Go walks in a
// different order each time. Its CSS must not: stages run from, the percentages in order, then to, and
// each stage's declarations by name, so the template page's stylesheet (and its hash) is the same on
// every boot. Control: the stages really are given out of order and in a map.
func TestKeyframeCSSIsTheSameOnEveryBoot(t *testing.T) {
	stages := map[string]value.Value{
		"to":       value.New(map[string]value.Value{"opacity": value.New("1"), "transform": value.New("none"), "filter": value.New("none")}),
		"from":     value.New(map[string]value.Value{"transform": value.New("translateY(8px)"), "opacity": value.New("0"), "filter": value.New("blur(4px)")}),
		"50%":      value.New(map[string]value.Value{"opacity": value.New("0.6"), "filter": value.New("blur(1px)")}),
		"10%, 90%": value.New(map[string]value.Value{"color": value.New("red")}),
		"bogus":    value.New("not a stage"),
	}
	want := "\tfrom {\n\t\tfilter: blur(4px);\n\t\topacity: 0;\n\t\ttransform: translateY(8px);\n\t}\n" +
		"\t10%, 90% {\n\t\tcolor: red;\n\t}\n" +
		"\t50% {\n\t\tfilter: blur(1px);\n\t\topacity: 0.6;\n\t}\n" +
		"\tto {\n\t\tfilter: none;\n\t\topacity: 1;\n\t\ttransform: none;\n\t}\n"
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		seen[buildKeyframeCSS(stages)] = true
	}
	if len(seen) != 1 {
		t.Fatalf("the same keyframe gave %d different CSS texts over 64 builds; the order must not follow the map", len(seen))
	}
	if got := buildKeyframeCSS(stages); got != want {
		t.Errorf("stages from, percentages, to; declarations by name:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
