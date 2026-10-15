package js

import (
	"strings"
	"testing"
)

// A Kitwork UI page is written in the grouped data-kitwork-* form (KITWORK_UI.md §11). Delivery reads
// it as it reads data-kit-*: the kernel for a page that uses it, a file per component it names —
// whatever alias or version the name carries — and the observer for an intersect event.

func TestGroupedHostGetsItsComponentFile(t *testing.T) {
	page := func(body string) string { return `<html><head></head><body>` + body + `</body></html>` }
	// A version written in the host picks the source; the file's name is the component's either way.
	v1, v2 := componentAsset("clipboard@v1.0.0", nil), componentAsset("clipboard@v2.0.0", nil)
	cases := []struct {
		name, body, want string
	}{
		{"a bare name", `<nav data-kitwork-component="menu"></nav>`, "/_kitwork/components/menu."},
		{"a name and an alias", `<nav data-kitwork-component="menu: $nav"></nav>`, "/_kitwork/components/menu."},
		{"a version written without its v", `<b data-kitwork-component="clipboard@1.0.0: $copy"></b>`, v1.Path},
		{"a version written with its v", `<b data-kitwork-component="clipboard@v2.0.0"></b>`, v2.Path},
		{"spaces around the parts", `<b data-kitwork-component=" toast :  $toast "></b>`, "/_kitwork/components/toast."},
	}
	for _, c := range cases {
		roles, paths := deliveredFiles(t, Deliver(page(c.body), nil, Options{}))
		if strings.Join(roles, ",") != "runtime,component" || !strings.HasPrefix(paths[1], c.want) {
			t.Errorf("%s: %s\n  want the kernel and %s…, got %v %v", c.name, c.body, c.want, roles, paths)
		}
	}
	// a site's own component, by the grouped host
	site := fakeSite{"bid-stepper": `window.kit.component("bid-stepper", { amount: 0 });`}
	if _, paths := deliveredFiles(t, Deliver(page(`<div data-kitwork-component="bid-stepper: $bid"></div>`), site, Options{})); len(paths) != 2 || !strings.HasPrefix(paths[1], "/_kitwork/components/bid-stepper.") {
		t.Errorf("a site component named by a grouped host ships its file, got %v", paths)
	}
	// control: a name nothing ships, a value the reader refuses, and a host printed in an example bring
	// no component file — the first two still use the grammar, so they get the kernel alone
	for _, body := range []string{
		`<b data-kitwork-component="nonesuch"></b>`,
		`<nav data-kitwork-component="menu: nav"></nav>`,
	} {
		if roles, _ := deliveredFiles(t, Deliver(page(body), nil, Options{})); strings.Join(roles, ",") != "runtime" {
			t.Errorf("%s: want the kernel alone, got %v", body, roles)
		}
	}
	example := page(`<pre><code>&lt;nav data-kitwork-component="menu"&gt;</code></pre>`)
	if out := Deliver(example, nil, Options{}); out != example {
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
		if roles, _ := deliveredFiles(t, Deliver(page, nil, Options{})); strings.Join(roles, ",") != "runtime" {
			t.Errorf("%s: want the kernel, got %v", body, roles)
		}
	}
	transport := `<html><head><style data-kitwork-generated="css"></style></head><body data-kitwork-layout="page.ui">` +
		`<a href="/" data-kitwork-current="">x</a><div data-kitwork-delay="0" data-kitwork-transition="fade"></div></body></html>`
	if out := Deliver(transport, nil, Options{}); out != transport {
		t.Errorf("transport markers are not grammar: the page must come back untouched, got\n%s", out)
	}
}

// intersect-enter / intersect-leave in a data-kitwork-event ask for the observer, as data-kit-intersect
// does; another event, or the word elsewhere on the page, does not.
func TestGroupedIntersectBringsTheObserver(t *testing.T) {
	for _, event := range []string{"intersect-enter.once: seen()", "click: x(); intersect-leave: gone()", "intersect-enter.margin(10px): seen()"} {
		page := `<html><head></head><body><section data-kitwork-event="` + event + `"></section></body></html>`
		roles, paths := deliveredFiles(t, Deliver(page, nil, Options{}))
		if strings.Join(roles, ",") != "runtime,directive" || !strings.HasPrefix(paths[1], "/_kitwork/directives/intersect.") {
			t.Errorf("%s: want the kernel and the observer, got %v %v", event, roles, paths)
		}
	}
	for _, page := range []string{
		`<html><head></head><body><section data-kitwork-event="click: x()" class="intersect-grid"></section></body></html>`,
		`<html><head></head><body><section data-kitwork-event="intersect-enter: f(" ></section></body></html>`, // does not split
		`<html><head></head><body><section data-kitwork-bind="title: 'intersect-enter: x'"></section></body></html>`,
	} {
		if roles, _ := deliveredFiles(t, Deliver(page, nil, Options{})); strings.Join(roles, ",") != "runtime" {
			t.Errorf("CONTROL %s: want the kernel alone, got %v", page, roles)
		}
	}
}

// The server's component check reads a grouped host by its name, whatever alias follows it.
func TestComponentNamesTakeAGroupedValue(t *testing.T) {
	site := fakeSite{"stepper": `kit.component("stepper", { amount: 0, add: function () { this.amount++; } });`}
	names := ComponentNames(site)
	for _, value := range []string{"stepper", "stepper: $step", " stepper :  $step "} {
		got, ok := names(value)
		if !ok || strings.Join(got, ",") != "add,amount" {
			t.Errorf("%q: want add,amount, got %v %v", value, got, ok)
		}
	}
	// control: a value the reader refuses is not guessed at
	if got, ok := names("stepper: step"); ok {
		t.Errorf("an alias without $ is refused, not read as the component: got %v", got)
	}

	page := func(inside string) string {
		return `<html><body><div data-kitwork-component="stepper: $step">` + inside + `</div><button data-kitwork-event="click: $step.ad()">x</button></body></html>`
	}
	report := capturedCheck(t, page(`<button data-kitwork-event="click: ad()">+</button>`), names)
	if !strings.Contains(report, `data-kitwork-event click reads "ad", which component stepper does not have`) ||
		!strings.Contains(report, `reads $step.ad, which component stepper does not have`) {
		t.Errorf("a call the component does not have is named, inside the host and through its alias, got:\n%s", report)
	}
	// control: the page calling what the component has says nothing
	fine := `<html><body><div data-kitwork-component="stepper: $step"><button data-kitwork-event="click: add()">+</button></div><button data-kitwork-event="click: $step.add()">x</button></body></html>`
	if report := capturedCheck(t, fine, names); report != "" {
		t.Errorf("nothing to report, got:\n%s", report)
	}
}
