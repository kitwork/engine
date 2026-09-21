;(function () {
"use strict";

// The split panel: two panes and a handle between them the reader can drag or
// steer with the arrow keys. The panes are authored — the first pane carries
// data-split-panel, the handle data-split-handle, the rest of the host is the
// other pane — and the component owns what markup cannot: the first pane's size
// as a percentage of the host, the pointer drag, the handle's keyboard and its
// separator semantics (aria-valuenow / min / max / orientation), and collapse.
//
//   <div data-kit-component="split" data-kit-scope="size: 30, min: 15, max: 60" class="flex">
//     <aside data-split-panel style="flex-basis: 30%">…</aside>
//     <div data-split-handle role="separator" tabindex="0" aria-label="Resize"></div>
//     <main class="flex-1">…</main>
//
// `axis` is the axis the handle moves along: "x" (panes side by side, the
// default) or "y" (panes stacked). A separator's aria-orientation is the line
// it draws, so an "x" split reports "vertical".

var INSTANCE = Symbol("kit:split");

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function number(value, fallback) {
  value = Number(value);
  return Number.isFinite(value) ? value : fallback;
}

function bounds(scope) {
  var min = Math.max(0, Math.min(100, number(scope.min, 10)));
  var max = Math.max(min, Math.min(100, number(scope.max, 90)));
  return { min: min, max: max };
}

function clamp(scope, value) {
  var range = bounds(scope);
  return Math.min(range.max, Math.max(range.min, number(value, range.min)));
}

function step(scope) {
  var value = number(scope.step, 5);
  return value > 0 ? value : 5;
}

function vertical(scope) {
  return scope.axis === "y";
}

function part(data, selector) {
  return data ? data.context.owned(selector)[0] || null : null;
}

// The first pane's flex-basis is the one DOM write; the handle's ARIA is the
// other. Both are re-applied after every render so a Morph cannot leave them
// stale.
function sync(scope, data) {
  if (data.disposed) return;
  var panel = part(data, "[data-split-panel]");
  var handle = part(data, "[data-split-handle]");
  var range = bounds(scope);
  var size = scope.collapsed ? 0 : clamp(scope, scope.size);
  if (panel) {
    panel.style.flexBasis = size + "%";
    panel.setAttribute("data-state", scope.collapsed ? "collapsed" : "open");
  }
  if (handle) {
    handle.setAttribute("aria-orientation", vertical(scope) ? "horizontal" : "vertical");
    handle.setAttribute("aria-valuemin", String(range.min));
    handle.setAttribute("aria-valuemax", String(range.max));
    handle.setAttribute("aria-valuenow", String(size));
    handle.setAttribute("data-state", data.dragging ? "dragging" : (scope.collapsed ? "collapsed" : "idle"));
  }
}

// Where the pointer is, as a percentage of the host along the split's axis.
function ratio(scope, data, event) {
  var rect = data.context.host.getBoundingClientRect();
  if (vertical(scope)) {
    return rect.height > 0 ? ((event.clientY - rect.top) / rect.height) * 100 : NaN;
  }
  return rect.width > 0 ? ((event.clientX - rect.left) / rect.width) * 100 : NaN;
}

function apply(scope, data, value) {
  value = Math.round(clamp(scope, value) * 10) / 10;
  if (scope.collapsed) scope.collapsed = false;
  if (scope.size !== value) scope.size = value;
  sync(scope, data);
  return scope.size;
}

kit.component("split", {
  size: 30,
  min: 10,
  max: 90,
  step: 5,
  axis: "x",
  collapsed: false,
  dragging: false,

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false, dragging: false, initial: clamp(scope, scope.size), pointer: null };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    scope.size = data.initial;
    sync(scope, data);

    context.listen(context.host, "pointerdown", function (event) {
      var handle = part(data, "[data-split-handle]");
      if (!handle || event.button !== 0 || !handle.contains(event.target)) return;
      data.dragging = true;
      data.pointer = event.pointerId;
      scope.dragging = true;
      try { handle.setPointerCapture(event.pointerId); } catch (_) { /* a synthetic pointer has nothing to capture */ }
      event.preventDefault();
      sync(scope, data);
    });

    context.listen(context.host, "pointermove", function (event) {
      if (!data.dragging || event.pointerId !== data.pointer) return;
      var value = ratio(scope, data, event);
      if (Number.isFinite(value)) apply(scope, data, value);
    });

    function release(event) {
      if (!data.dragging || event.pointerId !== data.pointer) return;
      data.dragging = false;
      data.pointer = null;
      scope.dragging = false;
      sync(scope, data);
    }
    context.listen(context.host, "pointerup", release);
    context.listen(context.host, "pointercancel", release);

    context.listen(context.host, "keydown", function (event) {
      var handle = part(data, "[data-split-handle]");
      if (!handle || !handle.contains(event.target)) return;
      var key = event.key;
      var shrink = vertical(scope) ? "ArrowUp" : "ArrowLeft";
      var grow = vertical(scope) ? "ArrowDown" : "ArrowRight";
      if (key === shrink) scope.shrink();
      else if (key === grow) scope.grow();
      else if (key === "Home") apply(scope, data, bounds(scope).min);
      else if (key === "End") apply(scope, data, bounds(scope).max);
      else if (key === "Enter") scope.toggle();
      else return;
      event.preventDefault();
    });

    context.listen(context.host, "dblclick", function (event) {
      var handle = part(data, "[data-split-handle]");
      if (!handle || !handle.contains(event.target)) return;
      scope.toggle();
    });

    function afterRender() {
      if (data.disposed) return;
      sync(scope, data);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () { data.disposed = true; });
  },

  set: function (value) {
    var data = instance(this);
    value = clamp(this, value);
    if (this.collapsed) this.collapsed = false;
    this.size = value;
    if (data) sync(this, data);
    return this.size;
  },
  grow: function () { return this.set(clamp(this, this.size) + step(this)); },
  shrink: function () { return this.set(clamp(this, this.size) - step(this)); },
  reset: function () {
    var data = instance(this);
    return this.set(data ? data.initial : this.size);
  },

  collapse: function () {
    this.collapsed = true;
    if (instance(this)) sync(this, instance(this));
    return true;
  },
  expand: function () {
    this.collapsed = false;
    if (instance(this)) sync(this, instance(this));
    return false;
  },
  toggle: function () { return this.collapsed ? this.expand() : this.collapse(); },
  isCollapsed: function () { return this.collapsed === true; }
});

})();
