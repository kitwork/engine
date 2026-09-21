package hydrate

import "testing"

// The system variables of ideaship-final §3 in an action: `$this` is the element that owns the
// directive (`$el` its compatibility alias), `$host` the nearest boundary element (`$root` its
// alias), `$event` the native event of the running handler. `$this` cannot be taken as a component
// alias.
func TestKernelSystemVariablesInActions(t *testing.T) {
	runNodeDOMScript(t, "system_variables.js", `
var kit = window.kit;
var host = el("section", { "data-kit-scope": "sameThis: 'unset', sameHost: 'unset', hostIsBoundary: 'unset', eventType: 'unset'" });
var button = el("button", { "data-kit-click": "sameThis = $this === $el ? 'yes' : 'no'; sameHost = $host === $root ? 'yes' : 'no'; hostIsBoundary = $host.getAttribute('data-kit-scope') ? 'yes' : 'no'; eventType = $event.type" });
var out = el("output", { "data-kit-text": "sameThis + ' ' + sameHost + ' ' + hostIsBoundary + ' ' + eventType" });
host.appendChild(button); host.appendChild(out);
document.body.appendChild(host);
kit.render();

function click(target) { document.dispatchEvent({ type: "click", target: target, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} }); kit.render(); }
click(button);
if (out.textContent !== "yes yes yes click") throw new Error("system variables in a scoped action: " + JSON.stringify(out.textContent));

kit.component("taker", { label: "x" });
var taker = el("div", { "data-kit-component": "taker", "data-kit-alias": "$this" });
document.body.appendChild(taker);
kit.render();
if (kit.scope.$this !== 0) throw new Error("$this must not be takeable as a component alias");
console.log("kernel system variables ok");
`)
}
