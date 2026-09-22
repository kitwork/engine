package hydrate

import "testing"

// The system variables of ideaship-final §3 in an action: `$this` is the element that owns the
// directive, `$host` the nearest boundary element, `$event` the native event of the running handler.
// The old spellings `$el` / `$root` resolve to nothing (B1, 22/09) — an unknown name reads as 0 —
// and `$this` cannot be taken as a component alias.
func TestKernelSystemVariablesInActions(t *testing.T) {
	runNodeDOMScript(t, "system_variables.js", `
var kit = window.kit;
var host = el("section", { "data-kit-scope": "isThis: 'unset', oldEl: 'unset', oldRoot: 'unset', hostIsBoundary: 'unset', eventType: 'unset'" });
var button = el("button", { "data-kit-click": "isThis = $this.getAttribute('data-kit-click') ? 'yes' : 'no'; oldEl = $el === 0 ? 'gone' : 'alive'; oldRoot = $root === 0 ? 'gone' : 'alive'; hostIsBoundary = $host.getAttribute('data-kit-scope') ? 'yes' : 'no'; eventType = $event.type" });
var out = el("output", { "data-kit-text": "isThis + ' ' + oldEl + ' ' + oldRoot + ' ' + hostIsBoundary + ' ' + eventType" });
host.appendChild(button); host.appendChild(out);
document.body.appendChild(host);
kit.render();

function click(target) { document.dispatchEvent({ type: "click", target: target, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} }); kit.render(); }
click(button);
if (out.textContent !== "yes gone gone yes click") throw new Error("system variables in a scoped action ($el/$root must be gone): " + JSON.stringify(out.textContent));

kit.component("taker", { label: "x" });
var taker = el("div", { "data-kit-component": "taker", "data-kit-alias": "$this" });
document.body.appendChild(taker);
kit.render();
if (kit.scope.$this !== 0) throw new Error("$this must not be takeable as a component alias");
console.log("kernel system variables ok");
`)
}
