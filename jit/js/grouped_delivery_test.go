package js

import (
	"strings"
	"testing"
)

// A Kitwork UI page is written in the grouped data-kitwork-* form (KITWORK_UI.md §11), the only form
// delivery reads since template pages' data-kit-* went (26/10/2026): the kernel for a page that uses
// it, a file per component it names — whatever alias the name carries, never a version — and the
// observer for an intersect event.

func TestGroupedHostGetsItsComponentFile(t *testing.T) {
	page := func(body string) string { return `<html><head></head><body>` + body + `</body></html>` }
	cases := []struct {
		name, body, want string
	}{
		{"a bare name", `<nav data-kitwork-component="dropdown"></nav>`, "/_kitwork/components/dropdown."},
		{"a name and an alias", `<nav data-kitwork-component="dropdown: $nav"></nav>`, "/_kitwork/components/dropdown."},
		{"spaces around the parts", `<b data-kitwork-component=" toast :  $toast "></b>`, "/_kitwork/components/toast."},
	}
	for _, c := range cases {
		roles, paths := deliveredFiles(t, Deliver(page(c.body), Options{}))
		if strings.Join(roles, ",") != "runtime,component" || !strings.HasPrefix(paths[1], c.want) {
			t.Errorf("%s: %s\n  want the kernel and %s…, got %v %v", c.name, c.body, c.want, roles, paths)
		}
	}
	// control: a name nothing ships (a site's own _components/ went on 26/10/2026), a name that carries
	// a version (answered for template pages until then), a value the reader refuses, and a host printed
	// in an example bring no component file — all but the last still use the grammar, so they get the
	// kernel alone
	for _, body := range []string{
		`<b data-kitwork-component="nonesuch"></b>`,
		`<div data-kitwork-component="bid-stepper: $bid"></div>`,
		`<b data-kitwork-component="toast@1.1.0: $toast"></b>`,
		`<b data-kitwork-component="toast@v1.1.0"></b>`,
		`<nav data-kitwork-component="dropdown: nav"></nav>`,
	} {
		if roles, _ := deliveredFiles(t, Deliver(page(body), Options{})); strings.Join(roles, ",") != "runtime" {
			t.Errorf("%s: want the kernel alone, got %v", body, roles)
		}
	}
	example := page(`<pre><code>&lt;nav data-kitwork-component="dropdown"&gt;</code></pre>`)
	if out := Deliver(example, Options{}); out != example {
		t.Errorf("a host printed in an example brings nothing:\n%s", out)
	}
}

// A page whose only directives are grouped gets the kernel; a page with only the engine's transport
// markers gets nothing — they sit on nearly every page.
func TestGroupedGrammarBringsTheKernel(t *testing.T) {
	for _, body := range []string{
		`<b data-kitwork-bind="text: total">0</b>`,
		`<button data-kitwork-event="click: open = !open">x</button>`,
		`<div data-kitwork-scope="open: false"></div>`,
		`<p data-kitwork-if="open">x</p>`,
		`<li data-kitwork-for="row of rows">x</li>`,
		`<div data-kitwork-style="width: w + '%'"></div>`,
		`<i data-kitwork-class="icon-sun: dark"></i>`,
	} {
		page := `<html><head></head><body>` + body + `</body></html>`
		if roles, _ := deliveredFiles(t, Deliver(page, Options{})); strings.Join(roles, ",") != "runtime" {
			t.Errorf("%s: want the kernel, got %v", body, roles)
		}
	}
	transport := `<html><head><style data-kitwork-generated="css"></style></head><body data-kitwork-layout="view.ui">` +
		`<a href="/" data-kitwork-current="">x</a><div data-kitwork-delay="0" data-kitwork-transition="fade"></div></body></html>`
	if out := Deliver(transport, Options{}); out != transport {
		t.Errorf("transport markers are not grammar: the page must come back untouched, got\n%s", out)
	}
}

// intersect-enter / intersect-leave in a data-kitwork-event ask for the observer (as data-kit-intersect
// did until 26/10/2026); another event, or the word elsewhere on the page, does not.
func TestGroupedIntersectBringsTheObserver(t *testing.T) {
	for _, event := range []string{"intersect-enter.once: seen()", "click: x(); intersect-leave: gone()", "intersect-enter.margin(10px): seen()"} {
		page := `<html><head></head><body><section data-kitwork-event="` + event + `"></section></body></html>`
		roles, paths := deliveredFiles(t, Deliver(page, Options{}))
		if strings.Join(roles, ",") != "runtime,directive" || !strings.HasPrefix(paths[1], "/_kitwork/directives/intersect.") {
			t.Errorf("%s: want the kernel and the observer, got %v %v", event, roles, paths)
		}
	}
	for _, page := range []string{
		`<html><head></head><body><section data-kitwork-event="click: x()" class="intersect-grid"></section></body></html>`,
		`<html><head></head><body><section data-kitwork-event="intersect-enter: f(" ></section></body></html>`, // does not split
		`<html><head></head><body><section data-kitwork-bind="title: 'intersect-enter: x'"></section></body></html>`,
	} {
		if roles, _ := deliveredFiles(t, Deliver(page, Options{})); strings.Join(roles, ",") != "runtime" {
			t.Errorf("CONTROL %s: want the kernel alone, got %v", page, roles)
		}
	}
}

// The server's component check reads a grouped host by its name, whatever alias follows it.
func TestComponentNamesTakeAGroupedValue(t *testing.T) {
	names := ComponentNames()
	for _, value := range []string{"dropdown", "dropdown: $menu", " dropdown :  $menu "} {
		got, ok := names(value)
		if !ok || strings.Join(got, ",") != "close,open,toggle" {
			t.Errorf("%q: want close,open,toggle, got %v %v", value, got, ok)
		}
	}
	// control: a value the reader refuses is not guessed at
	if got, ok := names("dropdown: menu"); ok {
		t.Errorf("an alias without $ is refused, not read as the component: got %v", got)
	}

	page := func(inside string) string {
		return `<html><body><div data-kitwork-component="dropdown: $menu">` + inside + `</div><button data-kitwork-event="click: $menu.tggle()">x</button></body></html>`
	}
	report := capturedCheck(t, page(`<button data-kitwork-event="click: tggle()">+</button>`), names)
	if !strings.Contains(report, `data-kitwork-event click reads "tggle", which component dropdown does not have`) ||
		!strings.Contains(report, `reads $menu.tggle, which component dropdown does not have`) {
		t.Errorf("a call the component does not have is named, inside the host and through its alias, got:\n%s", report)
	}
	// control: the page calling what the component has says nothing
	fine := `<html><body><div data-kitwork-component="dropdown: $menu"><button data-kitwork-event="click: toggle()">+</button></div><button data-kitwork-event="click: $menu.toggle()">x</button></body></html>`
	if report := capturedCheck(t, fine, names); report != "" {
		t.Errorf("nothing to report, got:\n%s", report)
	}
}
