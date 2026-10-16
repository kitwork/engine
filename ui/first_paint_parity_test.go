package ui

import (
	"encoding/json"
	"html"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
)

// The page the server paints first is the page the browser leaves once its kernel has started every
// component instance (16/10/2026, the owner's decision that a .ui expression answers alike on both
// ends): the kernel seeds each instance from its data-kitwork-scope, runs every data-kitwork-bind pair,
// and writes the text and the attributes it gets; nothing may change. This renders a page of
// instances, then runs the kernel's own code in node on what the server wrote — its parser and walker
// (kit.compile, kit.run) and its setText and writeAttribute, cut out of the kernel's source — and holds
// every text and every attribute to what the server painted.
//
// It found, before the fixes of 16/10/2026: a text or an attribute of more than one {{ }} handed to the
// kernel as (a) + (b), which added "12" to 3 and "truetrue" to 2; an input of NaN, Infinity,
// undefined, a lambda or a list with holes written null into data-kitwork-scope; an attribute whose
// object's toString is a lambda run in an empty scope on the server ("no" where the browser wrote
// "yes"); an aria attribute of null written empty where the kernel takes it away. CONTROL: the
// instance whose values are plain data agrees before and after.
func TestFirstPaintIsWhatTheKernelLeaves(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is required for this test and CI must provide it")
		}
		t.Skip("node not found")
	}
	inside := `<text>{{ String(open) }}</text><text>{{ open === null ? 'null' : 'not null' }}</text>` +
		`<text>{{ open > 1 ? 'big' : 'small' }}</text><text>{{ JSON.stringify(open) }}</text><text>{{ open }}</text>`
	page := `<content>` +
		// CONTROL: plain data, one {{ }} each
		`<box component="dropdown" open="{{ start }}"><text>{{ open ? 'on' : 'off' }}</text><box alt="{{ open ? 'x' : 'y' }}">a</box>` + inside + `</box>` +
		// more than one {{ }}: the parts' texts joined, never added
		`<box component="dropdown" open="{{ start }}"><text>{{ open ? 1 : 0 }}{{ open ? 2 : 0 }}</text><text>{{ open }}{{ open }}</text>` +
		`<text>{{ open ? 1 : 0 }}{{ open ? 2 : 0 }} items</text><box alt="{{ open ? 1 : 0 }}{{ open ? 2 : 0 }}">b</box>` +
		`<text>{{ open ? 'x' : '' }}{{ { valueOf: () => 1, toString: () => 'T' } }}</text>` +
		`<text>{{ open ? null : 1 }}{{ open ? [1, 2] : 3 }}</text>` +
		// an object whose text is a lambda, as an attribute: run in the scope that made it
		`<box alt="{{ { toString: () => open ? 'yes' : 'no' } }}">c</box><box alt="{{ [{ toString: () => open ? 'yes' : 'no' }] }}">d</box></box>` +
		// an aria attribute of null or undefined is not there; false and 0 are text
		`<box component="dropdown" open="{{ stop }}"><box alt="{{ open ? 'x' : null }}">e</box><box alt="{{ open ? 'x' : {}.y }}">f</box>` +
		`<box alt="{{ open }}">g</box><box alt="{{ open ? 1 : 0 }}">h</box></box>` +
		// inputs the browser reads back as the value the server painted with
		`<box component="dropdown" open="{{ 1 / 0 }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ -1 / 0 }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ 0 / 0 }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ -0 }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ nothing.here }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ (x) => x + 1 }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ Math }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ Math.max }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ ''.trim }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ Math.max.bind(null, 7) }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ Array(2) }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ [1].concat(Array(1), [2]) }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ { b: 0 / 0, a: [1 / 0, {}.x] } }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ JSON.rawJSON('12') }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ deep }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ [1, 2].values() }}">` + inside + `</box>` +
		`<box component="dropdown" open="{{ ratio }}">` + inside + `</box>` +
		`</content>`
	deep := map[string]any{"end": 1.0}
	for i := 0; i < 20; i++ {
		deep = map[string]any{"in": deep}
	}
	painted := render(t, page, map[string]any{"start": true, "stop": false, "deep": deep, "ratio": math.Inf(1), "nothing": map[string]any{}})

	type bind struct {
		Name   string  `json:"name"`
		Src    string  `json:"src"`
		Text   string  `json:"text"`
		Attr   *string `json:"attr"`
		Origin string  `json:"origin"`
	}
	type instance struct {
		Scope [][2]string `json:"scope"`
		Binds []bind      `json:"binds"`
	}
	hosts := regexp.MustCompile(`data-kitwork-component="dropdown" data-kitwork-scope="([^"]*)"`)
	element := regexp.MustCompile(`<(p|div)([^>]*) data-kitwork-bind="([^"]*)"([^>]*)>([^<]*)`)
	label := regexp.MustCompile(` aria-label="([^"]*)"`)
	starts := hosts.FindAllStringSubmatchIndex(painted, -1)
	if len(starts) != 20 {
		t.Fatalf("%d instances in\n%s", len(starts), painted)
	}
	var instances []instance
	for i, at := range starts {
		end := len(painted)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		var in instance
		pairs, err := hydrate.Pairs(html.UnescapeString(painted[at[2]:at[3]]))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pairs {
			in.Scope = append(in.Scope, [2]string{p.Name, p.Value})
		}
		for _, m := range element.FindAllStringSubmatch(painted[at[1]:end], -1) {
			pairs, err := hydrate.Pairs(html.UnescapeString(m[3]))
			if err != nil {
				t.Fatal(err)
			}
			var attr *string
			if l := label.FindStringSubmatch(m[2] + m[4]); l != nil {
				value := html.UnescapeString(l[1])
				attr = &value
			}
			for _, p := range pairs {
				in.Binds = append(in.Binds, bind{Name: p.Name, Src: p.Value, Text: html.UnescapeString(m[5]), Attr: attr, Origin: m[0]})
			}
		}
		instances = append(instances, in)
	}
	data, _ := json.Marshal(instances)
	kernel := hydrate.KernelFile(false)
	script := firstPaintShim + kernel + "\n" + kernelFunction(t, kernel, "setText") + "\n" + kernelFunction(t, kernel, "writeAttribute") + `
var kit = window.kit, failures = [], checked = 0;
console.error = function () {};
` + string(data) + `.forEach(function (instance) {
  // the instance as the kernel seeds it: each value run once, in the page around the host
  var scope = {};
  instance.scope.forEach(function (p) { try { scope[p[0]] = kit.run(kit.compile(p[1]), {}); } catch (e) {} });
  instance.binds.forEach(function (b) {
    var v, failed = false;
    try { v = kit.run(kit.compile(b.src), scope); } catch (e) { failed = true; } // a failing pair leaves the page
    checked++;
    if (b.name === "text") {
      var el = { textContent: b.text };
      if (!failed) try { setText(el, v); } catch (e) {} // guarded: a value with no text leaves the page
      if (el.textContent !== b.text) failures.push(b.origin + "\n    the kernel writes " + JSON.stringify(el.textContent));
      return;
    }
    var attrs = {};
    if (b.attr !== null) attrs[b.name] = b.attr;
    var fake = {
      hasAttribute: function (n) { return n in attrs; }, getAttribute: function (n) { return n in attrs ? attrs[n] : null; },
      setAttribute: function (n, x) { attrs[n] = String(x); }, removeAttribute: function (n) { delete attrs[n]; }
    };
    if (!failed) try { writeAttribute(fake, b.name, v); } catch (e) {}
    var now = b.name in attrs ? attrs[b.name] : null;
    if (now !== b.attr) failures.push(b.origin + "\n    the kernel leaves " + b.name + " = " + JSON.stringify(now));
  });
});
if (failures.length) { console.log(failures.join("\n")); process.exitCode = 1; }
else console.log(checked + " bindings, the kernel leaves the first paint as it is");
`
	path := filepath.Join(t.TempDir(), "first_paint.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("the kernel changes the first paint:\n%s", out)
	}
	t.Logf("%s", out)
	if !strings.Contains(string(out), " bindings,") {
		t.Fatalf("no binding was checked:\n%s", out)
	}
}

// kernelFunction cuts one function out of the kernel's source, by its braces.
func kernelFunction(t *testing.T, kernel, name string) string {
	t.Helper()
	start := strings.Index(kernel, "function "+name+"(")
	if start < 0 {
		t.Fatalf("no function %s in the kernel", name)
	}
	depth := 0
	for i := strings.IndexByte(kernel[start:], '{') + start; i < len(kernel); i++ {
		switch kernel[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return kernel[start : i+1]
			}
		}
	}
	t.Fatalf("function %s does not close", name)
	return ""
}

// firstPaintShim is the little of a browser the kernel's file needs to load in node, as the hydrate
// conformance tests give it (jit/hydrate conformance_test.go).
const firstPaintShim = `
global.window = { addEventListener: function () {}, removeEventListener: function () {} };
global.document = {
  readyState: "complete",
  addEventListener: function () {}, removeEventListener: function () {},
  querySelector: function () { return null; }, querySelectorAll: function () { return []; },
  dispatchEvent: function () {},
  documentElement: { nodeType: 1, querySelectorAll: function () { return []; },
    classList: { contains: function () { return false; }, toggle: function () {} },
    style: { setProperty: function () {} } },
  body: {}
};
global.navigator = {};
global.localStorage = { getItem: function () { return null; }, setItem: function () {}, removeItem: function () {} };
global.history = { back: function () {}, forward: function () {} };
global.location = { reload: function () {} };
global.MutationObserver = function () { this.observe = function () {}; this.disconnect = function () {}; };
global.CustomEvent = function () {};
window.document = document; window.navigator = navigator; window.localStorage = localStorage;
window.history = history; window.location = location;
`
