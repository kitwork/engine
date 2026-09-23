package hydrate

import (
	"strings"
	"testing"
)

// data-kit-style:<property>="expr" — one property per attribute, the same shape as bind and seed.
// A component with a continuous value says it in markup instead of writing element.style from
// JavaScript, which is what makes it visible to the server and to a reader.
func TestStylePropertyRuleAtRender(t *testing.T) {
	for _, ok := range []string{"width", "opacity", "grid-template-columns", "-webkit-line-clamp", "--brand-shift", "--x_1"} {
		if err := stylePropertyError(ok); err != nil {
			t.Errorf("stylePropertyError(%q) = %v, want nil", ok, err)
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
		err := stylePropertyError(property)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("stylePropertyError(%q) = %v, want an error naming %q", property, err, reason)
		}
	}

	// The expression itself is verified like any other directive, and the presence of one brings
	// the runtime — a page whose only directive is a style still needs the kernel.
	const authored = `data-kit-style:width="value + '%'"`
	if !directiveRe.MatchString(authored) {
		t.Error("a style expression must be verified at render")
	}
	if !presenceRe.MatchString(authored) {
		t.Error("a style directive must bring the runtime")
	}
	in := `<head></head><body>` + marker + `<span data-kit-style:width="value + '%'"></span></section></body>`
	if out := Render(in); strings.Count(out, injectTag) != 1 {
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
var host = el("section", { "data-kit-scope": "width: '40%', tone: 'red', gone: false" });
var bar = el("div", { "data-kit-style:width": "width", "data-kit-style:color": "tone", "data-kit-style:opacity": "gone" });
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
console.log("data-kit-style: writes, restores the authored baseline, removes what it invented");
`
	runNodeDOMScript(t, "style_write.test.js", assertions)
}

// A value may not carry a second declaration, a comment, !important, or a function that fetches or
// executes. var() IS allowed: a Kitwork site's colours are custom properties, so refusing it would
// make the directive useless here. A refused value reaches the error boundary like any directive.
func TestStyleRefusesAValueThatEscapesTheDeclaration(t *testing.T) {
	const assertions = `
var kit = window.kit;
var boundary = el("div", { "data-kit-scope": "failed: '', bad: '', good: ''", "data-kit-error": "failed = $error.directive" });
var probe = el("p", { "data-kit-style:color": "bad", "data-kit-style:background": "good" });
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
  scope.failed = "";
  scope.bad = pair[0];
  kit.render();
  if (scope.failed !== "data-kit-style:color") throw new Error(pair[1] + " must reach the error boundary, got " + JSON.stringify(scope.failed));
  if (probe.style.getPropertyValue("color") !== "") throw new Error(pair[1] + " must write nothing: " + probe.style.getPropertyValue("color"));
});

// the site's own tokens go through
scope.bad = "";
scope.good = "var(--color-brand)";
kit.render();
if (probe.style.getPropertyValue("background") !== "var(--color-brand)") throw new Error("var() must be allowed: " + probe.style.getPropertyValue("background"));
console.log("data-kit-style: refuses an escaping value, allows var()");
`
	runNodeDOMScript(t, "style_unsafe.test.js", assertions)
}

// A property name the engine refuses is reported at render; the kernel then simply does not write
// it — no half-applied style, no silent nothing.
func TestStyleSkipsARefusedProperty(t *testing.T) {
	const assertions = `
var kit = window.kit;
var host = el("section", { "data-kit-scope": "value: 'red'" });
var probe = el("p", { "data-kit-style:--kit-brand": "value", "data-kit-style:color": "value" });
host.appendChild(probe);
document.body.appendChild(host);
kit.render();
if (probe.style.getPropertyValue("--kit-brand") !== "") throw new Error("the engine namespace must not be written");
if (probe.style.getPropertyValue("color") !== "red") throw new Error("the property beside it must still be written");
console.log("data-kit-style: a refused property writes nothing, its neighbour still applies");
`
	runNodeDOMScript(t, "style_refused.test.js", assertions)
}
