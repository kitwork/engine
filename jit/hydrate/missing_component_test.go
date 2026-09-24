package hydrate

import "testing"

// A host naming a component nobody registered used to wait for a CDN fetch that never came: the
// loader was opt-in through kit.cdnComponents, no site ever set it, and the host simply sat there
// looking correct. The engine ships exactly the components a page names, so an unknown name is a
// typo or a missing _components/<name>.js — and the page should say which, once.
func TestUnknownComponentIsNamedOnce(t *testing.T) {
	const assertions = `
var kit = window.kit;
var errors = [];
var realError = console.error;
console.error = function () { errors.push(Array.prototype.join.call(arguments, " ")); };

var host = el("div", { "data-kit-component": "definitely-not-registered" });
host.appendChild(el("b", { "data-kit-text": "'x'" }));
document.body.appendChild(host);
kit.render();
kit.render();
kit.render();

console.error = realError;
var joined = errors.join(" | ");
if (joined.indexOf("definitely-not-registered") < 0) {
  throw new Error("the missing component should be named, got: " + joined);
}
var named = errors.filter(function (line) { return line.indexOf("definitely-not-registered") >= 0; });
if (named.length !== 1) {
  throw new Error("three passes should produce ONE report, got " + named.length);
}

// Control: a registered component mounts, and says nothing.
errors = [];
console.error = function () { errors.push(Array.prototype.join.call(arguments, " ")); };
kit.component("actually-registered", { label: "here" });
var ok = el("div", { "data-kit-component": "actually-registered" });
var text = el("b", { "data-kit-text": "label" });
ok.appendChild(text);
document.body.appendChild(ok);
kit.render();
console.error = realError;
if (text.textContent !== "here") throw new Error("a registered component must still mount: " + text.textContent);
if (errors.length !== 0) throw new Error("a registered component must be silent, got: " + errors.join(" | "));

console.log("unknown component: named once; registered component: mounts and stays silent");
`
	runNodeDOMScript(t, "missing_component.test.js", assertions)
}
