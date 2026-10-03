package js

import (
	"io"
	"os"
	"strings"
	"testing"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

// The reader: what a component's scope holds, from its source — or "unknowable".
func TestComponentNamesAreReadOffTheSource(t *testing.T) {
	cases := []struct {
		name, source string
		want         []string // nil with ok=false
		missing      []string
	}{
		{"an inline blueprint", `kit.component("a", { open: false, items: [], toggle: function () { this.open = !this.open; } });`,
			[]string{"items", "open", "toggle"}, nil},
		{"a blueprint held in a variable", `var aDef = { count: 0, add() { this.count++; } };
window.kit.component("a", aDef); window.kit.component("a@v1.0.0", aDef);`,
			[]string{"add", "count"}, nil},
		{"getters, async methods and quoted keys", `kit.component("a", { get total() { return 1; }, async load() {}, "label": "x" });`,
			[]string{"label", "load", "total"}, nil},
		{"values that hold brackets, strings, regexes and comments", `kit.component("a", {
  // a comment with { and ,
  pattern: /[}]+/g, // read as a division, its "}" would close the blueprint early
  text: "}, not: a key",
  nested: { inner: 1, deeper: [1, { x: 2 }] },
  run: function (x) { return x / 2 > 1 ? "a" : "b"; }
});`, []string{"nested", "pattern", "run", "text"}, []string{"inner", "deeper", "x", "not"}},
		{"a key the code assigns later", `kit.component("a", { init: function () { this.hideTimer = 0; } });`,
			[]string{"hideTimer", "init"}, nil},
		{"a Symbol slot is not a name", `var SLOT = Symbol("a"); kit.component("a", { init: function (context) { this[SLOT] = context; } });`,
			[]string{"init"}, nil},
	}
	for _, c := range cases {
		names, ok := scanDeclaredNames(c.source)
		if !ok {
			t.Errorf("%s: unreadable, want %v", c.name, c.want)
			continue
		}
		got := strings.Join(names, ",")
		for _, want := range c.want {
			if !contains(names, want) {
				t.Errorf("%s: %q missing from %s", c.name, want, got)
			}
		}
		for _, absent := range c.missing {
			if contains(names, absent) {
				t.Errorf("%s: %q is not a key of the blueprint, got %s", c.name, absent, got)
			}
		}
	}

	// Unknowable: the check must then stay silent about the whole component.
	for name, source := range map[string]string{
		"no registration":         `var x = { a: 1 };`,
		"a spread key":            `kit.component("a", { ...base, b: 1 });`,
		"a computed key":          `kit.component("a", { [name]: 1 });`,
		"a key written by name":   `kit.component("a", { init: function () { var k = "x"; this[k] = 1; } });`,
		"Object.assign onto this": `kit.component("a", { init: function () { Object.assign(this, defaults); } });`,
		"a blueprint from a call": `kit.component("a", build());`,
		"an unclosed string":      `kit.component("a", { a: "open });`,
	} {
		if names, ok := scanDeclaredNames(source); ok {
			t.Errorf("%s: should be unknowable, read %v", name, names)
		}
	}
}

// The catalogue is the component set the engine owns: every one must be readable, or the markup
// mistakes made against it go unreported. The sidebar's list is the one that mattered: open()/close()
// left it on 23/09, and a page kept calling close().
func TestEveryCatalogueComponentIsReadable(t *testing.T) {
	entries, err := jsFS.ReadDir("components")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if names, ok := ComponentNames(nil, nil)(entry.Name()); !ok || len(names) == 0 {
			t.Errorf("%s: its names cannot be read, so nothing written against it is checked", entry.Name())
		}
	}
	sidebar, _ := ComponentNames(nil, nil)("sidebar")
	if !contains(sidebar, "closeDrawer") || contains(sidebar, "close") {
		t.Errorf("sidebar names = %v", sidebar)
	}
}

// End to end, the way render calls it: a call to a method the component does not have is named at
// render, with what the component does have.
func TestACallToAMissingMethodIsReportedAtRender(t *testing.T) {
	page := func(inside string) string {
		return `<html><body><div data-kit-scope="theme: 'dark'"><header data-kit-component="sidebar">` + inside + `</header></div></body></html>`
	}
	site := fakeSite{
		"workbench": `kit.component("workbench", { init: function () { var k = "x"; this[k] = 1; } });`,
		"stepper":   `kit.component("stepper", { amount: 0, add: function () { this.amount++; } });`,
	}
	check := func(html string) string {
		return capturedCheck(t, html, ComponentNames(site, map[string]string{"dialog": "2.0.0"}))
	}

	report := check(page(`<button data-kit-click="close()">x</button>`))
	if !strings.Contains(report, `reads "close", which component sidebar does not have`) || !strings.Contains(report, "closeDrawer") {
		t.Fatalf("close() inside the sidebar should be reported with the sidebar's names, got:\n%s", report)
	}
	for label, html := range map[string]string{
		"CONTROL: a method it has":           page(`<button data-kit-click="closeDrawer()">x</button>`),
		"a name an outer scope declares":     page(`<b data-kit-text="theme">x</b>`),
		"state a seed writes in the host":    page(`<h1 data-kit-seed="title">t</h1><b data-kit-text="title"></b>`),
		"a component it cannot read":         `<html><body><div data-kit-component="workbench"><b data-kit-text="anything">x</b></div></body></html>`,
		"a site component's own method":      `<html><body><div data-kit-component="stepper"><button data-kit-click="add()">+</button></div></body></html>`,
		"a component nothing ships (kernel)": `<html><body><div data-kit-component="nonesuch"><b data-kit-text="x">x</b></div></body></html>`,
	} {
		if report := check(html); report != "" {
			t.Errorf("%s: nothing should be reported, got:\n%s", label, report)
		}
	}
	if report := check(`<html><body><div data-kit-component="stepper"><button data-kit-click="ad()">+</button></div></body></html>`); !strings.Contains(report, "component stepper does not have") {
		t.Errorf("a typo against a site's own component should be reported, got:\n%s", report)
	}
}

func capturedCheck(t *testing.T, html string, components hydrate.ComponentNames) string {
	t.Helper()
	previous := hydrate.Diagnostics
	hydrate.Diagnostics = true
	defer func() { hydrate.Diagnostics = previous }()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = write
	hydrate.Check(html, components)
	os.Stdout = stdout
	_ = write.Close()
	printed, _ := io.ReadAll(read)
	return strings.TrimSpace(string(printed))
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
