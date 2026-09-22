package hydrate

import "testing"

// init(context) on the kernel — the same seven keys the component runtime hands out, so a
// component written for one runs on the other (B9, one kernel). Each key is checked for the thing
// that would silently break a ported body: the owned() fence, element() by name, listen/cleanup
// released when morph removes the host, afterRender running once after the paint.
func TestInitContextShapeAndOwnedFence(t *testing.T) {
	const assertions = `
var kit = window.kit;
var seen = {};
kit.component("gallery", {
  count: 0,
  init: function (context) {
    seen.keys = Object.keys(context).join(",");
    seen.host = context.host;
    seen.thisIsScope = (this.count === 0);
    this.count = 7; // this = the scope: a write here is state, visible to bindings
    seen.owned = context.owned("[data-id]").map(function (el) { return el.getAttribute("data-id"); }).join(",");
    seen.ownedIncludesHost = context.owned("[data-kit-component]").indexOf(context.host) === 0;
    seen.track = context.element("track");
    seen.slides = context.elements("slide").map(function (el) { return el.getAttribute("data-id"); }).join(",");
    seen.missing = context.element("nothing");
    seen.missingAll = context.elements("nothing").length;
    seen.badName = null;
    try { context.elements("not an identifier"); } catch (error) { seen.badName = error.message; }
  }
});
kit.component("inner", { init: function (context) { seen.innerOwned = context.owned("[data-id]").length; } });

var host = el("section", { "data-kit-component": "gallery" });
var track = el("ul", { "data-kit-element": "track" });
var own1 = el("li", { "data-id": "own-1", "data-kit-element": "slide" });
var own2 = el("li", { "data-id": "own-2", "data-kit-element": "slide" });
var nested = el("div", { "data-kit-component": "inner" });
var nestedTrack = el("ul", { "data-kit-element": "track" });
var nestedLi = el("li", { "data-id": "nested", "data-kit-element": "slide" });
var scoped = el("div", { "data-kit-scope": "open: false" });
var scopedLi = el("li", { "data-id": "scoped" });
var label = el("b", { "data-kit-text": "count" });
track.appendChild(own1); track.appendChild(own2);
nestedTrack.appendChild(nestedLi); nested.appendChild(nestedTrack);
scoped.appendChild(scopedLi);
host.appendChild(track); host.appendChild(nested); host.appendChild(scoped); host.appendChild(label);
document.body.appendChild(host);
kit.render();

if (seen.keys !== "host,owned,element,elements,listen,cleanup,afterRender") throw new Error("context keys: " + seen.keys);
if (seen.host !== host) throw new Error("context.host must be the host element");
if (!seen.thisIsScope) throw new Error("this inside init must be the component scope");
if (label.textContent !== "7") throw new Error("a write to this inside init must be state the paint reads: " + label.textContent);
if (seen.owned !== "own-1,own-2") throw new Error("owned() must fence out a nested component's and a nested scope's elements, got: " + seen.owned);
if (!seen.ownedIncludesHost) throw new Error("owned() includes the host itself when it matches");
if (seen.track !== track) throw new Error("element('track') must be the host's own track, not the nested one");
if (seen.slides !== "own-1,own-2") throw new Error("elements('slide') must list only owned slides, got: " + seen.slides);
if (seen.missing !== null || seen.missingAll !== 0) throw new Error("a missing name is null / an empty list");
if (!seen.badName) throw new Error("elements() must reject a non-identifier");
if (seen.innerOwned !== 1) throw new Error("the nested host owns its own li: " + seen.innerOwned);
console.log("init(context): 7 keys, host, this = scope, owned fence, element(s) by name");
`
	runNodeDOMScript(t, "init_context_shape.test.js", assertions)
}

func TestInitContextListenAndCleanupReleaseWithTheHost(t *testing.T) {
	const assertions = `
var kit = window.kit;
var log = [];
kit.component("widget", {
  init: function (context) {
    var self = this;
    context.listen(context.host, "ping", function () { log.push("ping"); });
    var cancel = context.cleanup(function () { log.push("cleanup:" + (this === self || this === kit.scope ? "scope" : "other")); });
    context.cleanup(function () { log.push("second"); });
    self.cancelFirst = cancel;
  }
});

var parent = el("div", {});
var host = el("section", { "data-kit-component": "widget" }); // no directive inside: mounts anyway
parent.appendChild(host);
document.body.appendChild(parent);
kit.render();

host.dispatchEvent({ type: "ping" });
if (log.join("|") !== "ping") throw new Error("listen: handler should fire while mounted, got " + log.join("|"));

// the returned cancel runs the cleanup NOW, once
var scope = kit.scopeFor(host);
scope.cancelFirst();
scope.cancelFirst();
if (log.join("|") !== "ping|cleanup:scope") throw new Error("cancel must run the cleanup once, with this = the scope: " + log.join("|"));

// morph drops the host → remaining cleanups run, listener released
kit.morph(parent, el("div", {}));
if (log.join("|") !== "ping|cleanup:scope|second") throw new Error("host removal must run the remaining cleanups: " + log.join("|"));
host.dispatchEvent({ type: "ping" });
if (log.join("|") !== "ping|cleanup:scope|second") throw new Error("listen: the listener must be released with the host, got " + log.join("|"));
console.log("init(context): listen + cleanup released with the host; cancel runs once");
`
	runNodeDOMScript(t, "init_context_cleanup.test.js", assertions)
}

func TestInitContextAfterRenderRunsOnceAfterThePaintAndItsWritesRepaint(t *testing.T) {
	const assertions = `
var kit = window.kit;
var runs = 0, thisWasScope = false;
kit.component("measure", {
  width: 0,
  init: function (context) {
    context.afterRender(function () {
      runs++;
      thisWasScope = (this.width === 0);
      this.width = 320; // a write AFTER the paint: must schedule its own repaint (no lost update)
    });
  }
});
var host = el("section", { "data-kit-component": "measure" });
var label = el("b", { "data-kit-text": "width" });
host.appendChild(label);
document.body.appendChild(host);
kit.render();
if (runs !== 1) throw new Error("afterRender should run once after the first paint, ran " + runs);
if (!thisWasScope) throw new Error("afterRender: this must be the scope");
if (label.textContent !== "0") throw new Error("afterRender runs AFTER the paint, so this paint still shows 0, got " + label.textContent);
kit.render();
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
var kit = window.kit;
var ran = 0, cancelled = 0;
kit.component("late", {
  init: function (context) {
    var cancel = context.afterRender(function () { cancelled++; });
    cancel();
    context.afterRender(function () { ran++; });
  }
});
var parent = el("div", {});
var host = el("section", { "data-kit-component": "late" });
parent.appendChild(host);
document.body.appendChild(parent);
// mount without painting: seed + init through scopeFor, then remove the host before any render
kit.scopeFor(host);
kit.morph(parent, el("div", {}));
kit.render();
if (cancelled !== 0) throw new Error("a cancelled afterRender must not run");
if (ran !== 0) throw new Error("afterRender must not run for a host morph removed before the paint");
console.log("init(context): afterRender cancel + removed host skipped");
`
	runNodeDOMScript(t, "init_context_after_render_removed.test.js", assertions)
}

func TestInitThatThrowsReachesTheErrorBoundary(t *testing.T) {
	const assertions = `
var kit = window.kit;
kit.component("broken", {
  ready: "seeded",
  init: function () { throw new Error("no camera"); }
});
var boundary = el("div", { "data-kit-scope": "failed: '', where: ''", "data-kit-error": "failed = $error.message; where = $error.directive" });
var host = el("section", { "data-kit-component": "broken" });
var label = el("b", { "data-kit-text": "ready" });
var failed = el("i", { "data-kit-text": "failed + '@' + where" });
host.appendChild(label);
boundary.appendChild(host);
boundary.appendChild(failed);
document.body.appendChild(boundary);
kit.render();
if (failed.textContent !== "no camera@init") throw new Error("the boundary must see the init error with directive 'init': " + failed.textContent);
if (label.textContent !== "seeded") throw new Error("a failing init leaves the component mounted with its seeded state: " + label.textContent);
console.log("init(context): a throwing init reaches data-kit-error as directive init");
`
	runNodeDOMScript(t, "init_context_error.test.js", assertions)
}
