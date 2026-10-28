package hydrate

import "testing"

// init(context) on the kernel — six keys: host, owned, listen, cleanup, afterRender and emit (B9, one
// kernel). Each key is checked for the thing that would silently break a ported body: the owned()
// fence, listen/cleanup released when morph removes the host, afterRender running once after the
// paint. (element, elements and require read data-kit-element, which only template pages wrote: they
// went with them on 26/10/2026.)
func TestInitContextShapeAndOwnedFence(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
var seen = {};
kitwork.component("gallery", {
  count: 0,
  init: function (context) {
    seen.keys = Object.keys(context).join(",");
    seen.host = context.host;
    seen.thisIsScope = (this.count === 0);
    this.count = 7; // this = the scope: a write here is state, visible to bindings
    seen.owned = context.owned("[data-id]").map(function (el) { return el.getAttribute("data-id"); }).join(",");
    seen.ownedIncludesHost = context.owned("[data-kitwork-component]").indexOf(context.host) === 0;
  }
});
kitwork.component("inner", { init: function (context) { seen.innerOwned = context.owned("[data-id]").length; } });

var host = el("section", { "data-kitwork-component": "gallery" });
var track = el("ul", {});
var own1 = el("li", { "data-id": "own-1" });
var own2 = el("li", { "data-id": "own-2" });
var nested = el("div", { "data-kitwork-component": "inner" });
var nestedTrack = el("ul", {});
var nestedLi = el("li", { "data-id": "nested" });
var scoped = el("div", { "data-kitwork-scope": "open: false" });
var scopedLi = el("li", { "data-id": "scoped" });
var label = el("b", { "data-kitwork-bind": "text: count" });
track.appendChild(own1); track.appendChild(own2);
nestedTrack.appendChild(nestedLi); nested.appendChild(nestedTrack);
scoped.appendChild(scopedLi);
host.appendChild(track); host.appendChild(nested); host.appendChild(scoped); host.appendChild(label);
document.body.appendChild(host);
kitwork.render();

if (seen.keys !== "host,owned,listen,cleanup,afterRender,emit") throw new Error("context keys: " + seen.keys);
if (seen.host !== host) throw new Error("context.host must be the host element");
if (!seen.thisIsScope) throw new Error("this inside init must be the component scope");
if (label.textContent !== "7") throw new Error("a write to this inside init must be state the paint reads: " + label.textContent);
if (seen.owned !== "own-1,own-2") throw new Error("owned() must fence out a nested component's and a nested scope's elements, got: " + seen.owned);
if (!seen.ownedIncludesHost) throw new Error("owned() includes the host itself when it matches");
if (seen.innerOwned !== 1) throw new Error("the nested host owns its own li: " + seen.innerOwned);
console.log("init(context): 6 keys, host, this = scope, owned fence");
`
	runNodeDOMScript(t, "init_context_shape.test.js", assertions)
}

func TestInitContextListenAndCleanupReleaseWithTheHost(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
var log = [];
kitwork.component("widget", {
  init: function (context) {
    var self = this;
    context.listen(context.host, "ping", function () { log.push("ping"); });
    var cancel = context.cleanup(function () { log.push("cleanup:" + (this === self || this === kitwork.scope ? "scope" : "other")); });
    context.cleanup(function () { log.push("second"); });
    self.cancelFirst = cancel;
  }
});

var parent = el("div", {});
var host = el("section", { "data-kitwork-component": "widget" }); // no directive inside: mounts anyway
parent.appendChild(host);
document.body.appendChild(parent);
kitwork.render();

host.dispatchEvent({ type: "ping" });
if (log.join("|") !== "ping") throw new Error("listen: handler should fire while mounted, got " + log.join("|"));

// the returned cancel runs the cleanup NOW, once
var scope = kitwork.scopeFor(host);
scope.cancelFirst();
scope.cancelFirst();
if (log.join("|") !== "ping|cleanup:scope") throw new Error("cancel must run the cleanup once, with this = the scope: " + log.join("|"));

// morph drops the host → remaining cleanups run, listener released
kitwork.morph(parent, el("div", {}));
if (log.join("|") !== "ping|cleanup:scope|second") throw new Error("host removal must run the remaining cleanups: " + log.join("|"));
host.dispatchEvent({ type: "ping" });
if (log.join("|") !== "ping|cleanup:scope|second") throw new Error("listen: the listener must be released with the host, got " + log.join("|"));
console.log("init(context): listen + cleanup released with the host; cancel runs once");
`
	runNodeDOMScript(t, "init_context_cleanup.test.js", assertions)
}

func TestInitContextAfterRenderRunsOnceAfterThePaintAndItsWritesRepaint(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
var runs = 0, thisWasScope = false;
kitwork.component("measure", {
  width: 0,
  init: function (context) {
    context.afterRender(function () {
      runs++;
      thisWasScope = (this.width === 0);
      this.width = 320; // a write AFTER the paint: must schedule its own repaint (no lost update)
    });
  }
});
var host = el("section", { "data-kitwork-component": "measure" });
var label = el("b", { "data-kitwork-bind": "text: width" });
host.appendChild(label);
document.body.appendChild(host);
kitwork.render();
if (runs !== 1) throw new Error("afterRender should run once after the first paint, ran " + runs);
if (!thisWasScope) throw new Error("afterRender: this must be the scope");
if (label.textContent !== "0") throw new Error("afterRender runs AFTER the paint, so this paint still shows 0, got " + label.textContent);
kitwork.render();
if (runs !== 1) throw new Error("afterRender is one-shot, ran " + runs);
setTimeout(function () {
  if (label.textContent !== "320") throw new Error("a write inside afterRender must repaint on its own: " + label.textContent);
  console.log("init(context): afterRender once, after the paint, writes repaint");
}, 10);
`
	runNodeDOMScript(t, "init_context_after_render.test.js", assertions)
}

func TestInitContextAfterRenderSkipsARemovedHost(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
var ran = 0, cancelled = 0, inits = 0;
kitwork.component("late", {
  init: function (context) {
    inits++;
    var cancel = context.afterRender(function () { cancelled++; });
    cancel();
    context.afterRender(function () { ran++; });
  }
});
var parent = el("div", {});
var host = el("section", { "data-kitwork-component": "late" });
parent.appendChild(host);
document.body.appendChild(parent);
// mount without painting: seed + init through scopeFor, then remove the host before any render
kitwork.scopeFor(host);
kitwork.morph(parent, el("div", {}));
kitwork.render();
if (inits !== 1) throw new Error("control: the host must have mounted (init ran " + inits + " times), or nothing below is tested");
if (cancelled !== 0) throw new Error("a cancelled afterRender must not run");
if (ran !== 0) throw new Error("afterRender must not run for a host morph removed before the paint");
console.log("init(context): afterRender cancel + removed host skipped");
`
	runNodeDOMScript(t, "init_context_after_render_removed.test.js", assertions)
}

// A throwing init is reported — to the console, naming the directive — and the component stays
// mounted with its seeded state. (It used to reach data-kit-error, removed 02/10.)
func TestInitThatThrowsIsReported(t *testing.T) {
	const assertions = `
var kitwork = window.kitwork;
var reported = [];
var originalError = console.error;
console.error = function (error, directive) { reported.push(String(error && error.message || error) + "@" + directive); };
kitwork.component("broken", {
  ready: "seeded",
  init: function () { throw new Error("no camera"); }
});
var host = el("section", { "data-kitwork-component": "broken" });
var label = el("b", { "data-kitwork-bind": "text: ready" });
host.appendChild(label);
document.body.appendChild(host);
kitwork.render();
console.error = originalError;
if (reported.join("|") !== "no camera@init") throw new Error("the init error must be reported once, naming init: " + reported.join("|"));
if (label.textContent !== "seeded") throw new Error("a failing init leaves the component mounted with its seeded state: " + label.textContent);
console.log("init(context): a throwing init is reported as directive init");
`
	runNodeDOMScript(t, "init_context_error.test.js", assertions)
}
