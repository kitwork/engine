package hydrate

import (
	"strings"
	"testing"
)

// A style pair names one CSS property — data-kitwork-style="width: w" — so a component with a continuous
// value says it in markup instead of writing element.style from JavaScript, which is what makes it
// visible to the server and to a reader. The property is held to the kernel's rule at render. (The
// template pages' data-kit-style:<property> was tested here until 27/10/2026.)
func TestStylePropertyRuleAtRender(t *testing.T) {
	check := func(property string) error { return stylePropertyErrorIn("data-kitwork-style "+property, property) }
	for _, ok := range []string{"width", "opacity", "grid-template-columns", "-webkit-line-clamp", "--brand-shift", "--x_1"} {
		if err := check(ok); err != nil {
			t.Errorf("%s = %v, want nil", ok, err)
		}
	}
	for property, reason := range map[string]string{
		"Width":         "not a CSS property name",
		"css-text":      "escape the declaration",
		"behavior":      "escape the declaration",
		"-moz-binding":  "escape the declaration",
		"--kit-brand":   "engine's own namespace",
		"--kitwork-bar": "engine's own namespace",
		"--9lives":      "not a custom property name",
	} {
		err := check(property)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s = %v, want an error naming %q", property, err, reason)
		}
	}
	// a page whose only directive is a style still needs the kernel
	in := `<head></head><body>` + marker + `<span data-kitwork-style="width: value"></span></section></body>`
	if !UsesGrammar(in) {
		t.Error("a page whose only directive is a style still needs the runtime")
	}
}

// The value semantics: numbers and strings are written, and a nullish, false or empty result puts
// back exactly what the author wrote in the style attribute — a binding that stops applying must
// not leave a value behind.
func TestStyleWritesAndRestoresTheAuthoredBaseline(t *testing.T) {
	const assertions = `
var kit = window.kit;
// The reset path is about the EXPRESSION's result, so bind width to a value that can itself
// become nullish — "value + '%'" would only ever produce the string "null%".
var host = el("section", { "data-kitwork-scope": "width: '40%'; tone: 'red'; gone: false" });
var bar = el("div", { "data-kitwork-style": "width: width; color: tone; opacity: gone" });
bar.setAttribute("style", "color: blue");
bar.style.setProperty("color", "blue", "");
host.appendChild(bar);
document.body.appendChild(host);
kit.render();

if (bar.style.getPropertyValue("width") !== "40%") throw new Error("width not written: " + bar.style.getPropertyValue("width"));
if (bar.style.getPropertyValue("color") !== "red") throw new Error("color not written: " + bar.style.getPropertyValue("color"));
if (bar.style.getPropertyValue("opacity") !== "") throw new Error("a false result must write nothing: " + bar.style.getPropertyValue("opacity"));

// a value that becomes nullish restores the author's own declaration, not an empty one
kit.scopeFor(host).tone = null;
kit.render();
if (bar.style.getPropertyValue("color") !== "blue") throw new Error("a nullish result must restore the authored value, got: " + bar.style.getPropertyValue("color"));

// a property the author never wrote is removed instead
kit.scopeFor(host).width = null;
kit.render();
if (bar.style.getPropertyValue("width") !== "") throw new Error("a property with no baseline must be removed, got: " + bar.style.getPropertyValue("width"));

// and it comes back when the value does
kit.scopeFor(host).width = "75%";
kit.render();
if (bar.style.getPropertyValue("width") !== "75%") throw new Error("width did not come back: " + bar.style.getPropertyValue("width"));
console.log("data-kitwork-style: writes, restores the authored baseline, removes what it invented");
`
	runNodeDOMScript(t, "style_write.test.js", assertions)
}

// A value may not carry a second declaration, a comment, !important, or a function that fetches or
// executes. var() IS allowed: a Kitwork site's colours are custom properties, so refusing it would
// make the directive useless here. A refused value is reported like any failing directive.
func TestStyleRefusesAValueThatEscapesTheDeclaration(t *testing.T) {
	const assertions = `
var kit = window.kit;
var failed = "";
var originalError = console.error;
console.error = function (error, directive) { failed = directive; };
var boundary = el("div", { "data-kitwork-scope": "bad: ''; good: ''" });
var probe = el("p", { "data-kitwork-style": "color: bad; background: good" });
boundary.appendChild(probe);
document.body.appendChild(boundary);
kit.render();

var scope = kit.scopeFor(boundary);
[
  ["red; position: fixed", "a second declaration"],
  ["red /* comment */", "a comment"],
  ["red !important", "!important"],
  ["url(https://example.com/x.png)", "url()"],
  ["image-set('a.png' 1x)", "image-set()"],
  ["attr(data-x)", "attr()"],
  ["javascript:alert(1)", "a javascript: value"]
].forEach(function (pair) {
  failed = "";
  scope.bad = pair[0];
  kit.render();
  if (failed !== "data-kitwork-style color") throw new Error(pair[1] + " must be reported, naming the attribute and the pair, got " + JSON.stringify(failed));
  if (probe.style.getPropertyValue("color") !== "") throw new Error(pair[1] + " must write nothing: " + probe.style.getPropertyValue("color"));
});

// the site's own tokens go through
scope.bad = "";
scope.good = "var(--color-brand)";
kit.render();
console.error = originalError;
if (probe.style.getPropertyValue("background") !== "var(--color-brand)") throw new Error("var() must be allowed: " + probe.style.getPropertyValue("background"));
console.log("data-kitwork-style: refuses an escaping value, allows var()");
`
	runNodeDOMScript(t, "style_unsafe.test.js", assertions)
}

// A property name the engine refuses is reported at render; the kernel then simply does not write
// it — no half-applied style, no silent nothing.
func TestStyleSkipsARefusedProperty(t *testing.T) {
	const assertions = `
var kit = window.kit;
var host = el("section", { "data-kitwork-scope": "value: 'red'" });
var probe = el("p", { "data-kitwork-style": "--kit-brand: value; color: value" });
host.appendChild(probe);
document.body.appendChild(host);
kit.render();
if (probe.style.getPropertyValue("--kit-brand") !== "") throw new Error("the engine namespace must not be written");
if (probe.style.getPropertyValue("color") !== "red") throw new Error("the property beside it must still be written");
console.log("data-kitwork-style: a refused property writes nothing, its neighbour still applies");
`
	runNodeDOMScript(t, "style_refused.test.js", assertions)
}
