package hydrate

import "testing"

// The system variables of ideaship-final §3 in an action: `$this` is the element that owns the
// directive, `$host` the nearest boundary element, `$event` the native event of the running handler.
// The old spellings `$el` / `$root` resolve to nothing (B1, 22/09) — an unknown name reads as 0 —
// and `$this` cannot be taken as a component alias.
func TestKernelSystemVariablesInActions(t *testing.T) {
	runNodeDOMScript(t, "system_variables.js", `
var kitwork = window.kitwork;
var host = el("section", { "data-kitwork-scope": "isThis: 'unset'; oldEl: 'unset'; oldRoot: 'unset'; hostIsBoundary: 'unset'; eventType: 'unset'" });
// one click pair per write: ";" separates the pairs of data-kitwork-event, and each pair runs
var button = el("button", { "data-kitwork-event": "click: isThis = $this.getAttribute('data-kitwork-event') ? 'yes' : 'no'; " +
  "click: oldEl = $el === 0 ? 'gone' : 'alive'; click: oldRoot = $root === 0 ? 'gone' : 'alive'; " +
  "click: hostIsBoundary = $host.getAttribute('data-kitwork-scope') ? 'yes' : 'no'; click: eventType = $event.type" });
var out = el("output", { "data-kitwork-bind": "text: isThis + ' ' + oldEl + ' ' + oldRoot + ' ' + hostIsBoundary + ' ' + eventType" });
host.appendChild(button); host.appendChild(out);
document.body.appendChild(host);
kitwork.render();

function click(target) { document.dispatchEvent({ type: "click", target: target, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} }); kitwork.render(); }
click(button);
if (out.textContent !== "yes gone gone yes click") throw new Error("system variables in a scoped action ($el/$root must be gone): " + JSON.stringify(out.textContent));

kitwork.component("taker", { label: "x" });
var taker = el("div", { "data-kitwork-component": "taker: $this" });
document.body.appendChild(taker);
kitwork.render();
if (kitwork.scope.$this !== 0) throw new Error("$this must not be takeable as a component alias");
console.log("kernel system variables ok");
`)
}
