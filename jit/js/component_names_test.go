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
// mistakes made against it go unreported. (The sidebar's list was the one that mattered: open()/close()
// left it on 23/09, and a page kept calling close(). The sidebar went with template pages on
// 26/10/2026.)
func TestEveryCatalogueComponentIsReadable(t *testing.T) {
	entries, err := jsFS.ReadDir("components")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if names, ok := ComponentNames()(entry.Name()); !ok || len(names) == 0 {
			t.Errorf("%s: its names cannot be read, so nothing written against it is checked", entry.Name())
		}
	}
	dropdown, _ := ComponentNames()("dropdown")
	if !contains(dropdown, "close") || contains(dropdown, "hide") {
		t.Errorf("dropdown names = %v", dropdown)
	}
}

// End to end, the way render calls it: a call to a method the component does not have is named at
// render, with what the component does have. (A seed's state and a site's own component went on
// 26/10/2026; a component whose source cannot be read is given to the check by namesWith.)
func TestACallToAMissingMethodIsReportedAtRender(t *testing.T) {
	page := func(inside string) string {
		return `<html><body><div data-kitwork-scope="theme: 'dark'"><header data-kitwork-component="dropdown">` + inside + `</header></div></body></html>`
	}
	check := func(html string) string {
		return capturedCheck(t, html, namesWith(map[string]string{
			"workbench": `kit.component("workbench", { init: function () { var k = "x"; this[k] = 1; } });`,
		}))
	}

	report := check(page(`<button data-kitwork-event="click: hide()">x</button>`))
	if !strings.Contains(report, `reads "hide", which component dropdown does not have`) || !strings.Contains(report, "toggle") {
		t.Fatalf("hide() inside the dropdown should be reported with the dropdown's names, got:\n%s", report)
	}
	for label, html := range map[string]string{
		"CONTROL: a method it has":           page(`<button data-kitwork-event="click: close()">x</button>`),
		"a name an outer scope declares":     page(`<b data-kitwork-bind="text: theme">x</b>`),
		"a component it cannot read":         `<html><body><div data-kitwork-component="workbench"><b data-kitwork-bind="text: anything">x</b></div></body></html>`,
		"a component nothing ships (kernel)": `<html><body><div data-kitwork-component="nonesuch"><b data-kitwork-bind="text: x">x</b></div></body></html>`,
	} {
		if report := check(html); report != "" {
			t.Errorf("%s: nothing should be reported, got:\n%s", label, report)
		}
	}
}

// From outside the host, through the alias: `$menu.hide()` in a navbar is looked up along the
// dropdown host's chain — the component, then the scopes around the host, then the page.
func TestACallThroughAnAliasIsCheckedAgainstItsHost(t *testing.T) {
	page := func(outside string) string {
		return `<html><body><div data-kitwork-scope="theme: 'dark'"><aside data-kitwork-component="dropdown: $menu"></aside></div>` +
			`<nav>` + outside + `</nav></body></html>`
	}
	check := func(html string) string {
		return capturedCheck(t, html, namesWith(map[string]string{
			"workbench": `kit.component("workbench", { init: function () { var k = "x"; this[k] = 1; } });`,
		}))
	}

	for _, call := range []string{`$menu.hide()`, `open ? $menu.hide() : 0`, `$menu . hide()`, `$menu?.hide()`} {
		report := check(page(`<button data-kitwork-event="click: ` + call + `">x</button>`))
		if !strings.Contains(report, `reads $menu.hide, which component dropdown does not have`) || !strings.Contains(report, "toggle") {
			t.Errorf("%s should be reported against the dropdown, got:\n%s", call, report)
		}
	}
	for label, html := range map[string]string{
		"CONTROL: a method it has":                page(`<button data-kitwork-event="click: $menu.close()">x</button>`),
		"state it has":                            page(`<b data-kitwork-bind="show: $menu.open">x</b>`),
		"a name a scope around the host declares": page(`<b data-kitwork-bind="text: $menu.theme">x</b>`),
		"inside a string":                         page(`<b data-kitwork-bind="text: '$menu.hide'">x</b>`),
		"an alias the page never declares":        page(`<button data-kitwork-event="click: $nobody.hide()">x</button>`),
		"an alias two hosts claim":                page(`<aside data-kitwork-component="dropdown: $menu"></aside><button data-kitwork-event="click: $menu.hide()">x</button>`),
		"the alias of a component it cannot read": `<html><body><div data-kitwork-component="workbench: $bench"></div><button data-kitwork-event="click: $bench.anything()">x</button></body></html>`,
		"a reserved handle":                       page(`<button data-kitwork-event="click: $this.blur()">x</button>`),
	} {
		if report := check(html); report != "" {
			t.Errorf("%s: nothing should be reported, got:\n%s", label, report)
		}
	}
}

// namesWith is ComponentNames, with the names of a few components given by their source — the reader
// at work on a component the catalogue does not hold (one it cannot read, a counter of a browser
// proof). A site's own _components/, which gave the check such sources, went on 26/10/2026.
func namesWith(sources map[string]string) hydrate.ComponentNames {
	catalogue := ComponentNames()
	return func(component string) ([]string, bool) {
		name := component
		if strings.Contains(component, ":") {
			named, _, err := hydrate.ComponentValue(component)
			if err != nil {
				return nil, false
			}
			name = named
		}
		if source, ok := sources[strings.TrimSpace(name)]; ok {
			return declaredNamesOf(source)
		}
		return catalogue(component)
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
