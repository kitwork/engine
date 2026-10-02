/* split component @v1.0.0 — a draggable, keyboard-operable divider between two panes.
 *
 * Usage — the markup draws the panes and binds their size; the component moves the divider:
 *
 *   <div data-kit-component="split" class="flex">
 *     <aside data-kit-style:flex-basis="collapsed ? '0%' : size + '%'">Files</aside>
 *     <div role="separator" data-kit-element="handle" tabindex="0" aria-label="Resize the file list"
 *          aria-orientation="vertical" aria-valuemin="20" aria-valuemax="60" aria-valuenow="34"
 *          data-kit-bind:aria-valuenow="collapsed ? 0 : size"
 *          data-kit-class="dragging ? 'bg-brand' : 'bg-line'"></div>
 *     <main>…</main>
 *   </div>
 *
 * THE ARIA IS THE CONFIGURATION. A separator must tell a screen reader its range and where it
 * starts — aria-valuemin, aria-valuemax, aria-valuenow, aria-orientation — so those are what the
 * component reads: the range to clamp to, the size to start at and return to on reset(), and the
 * axis (a vertical separator divides left from right). data-step on the handle sets how far one key
 * press moves it, 5 by default. Nothing is said twice.
 *
 * WHY THIS IS A COMPONENT. The pane's width is a style binding and the handle's state is ARIA
 * bindings, all in the markup. What cannot be written there is the DIVIDER ITSELF:
 *
 *   · the drag — pointer capture on the handle, and the pointer's position measured against the
 *     container, turned into a percentage;
 *   · the keys, the WAI-ARIA window splitter: ← → (↑ ↓ for a horizontal one) move by a step,
 *     Home and End go to the limits, Enter collapses the pane and restores it where it was.
 *
 * The public surface: size, collapsed, dragging, set(n), toggle(), reset().
 */
var SPLIT = Symbol("kit:split");

function splitHandle(scope) {
  var own = scope[SPLIT];
  return own ? own.context.element("handle") : null;
}

function splitNumber(handle, name, fallback) {
  var value = handle ? Number(handle.getAttribute(name)) : NaN;
  return handle && handle.hasAttribute(name) && isFinite(value) ? value : fallback;
}

function splitRange(scope) {
  var handle = splitHandle(scope);
  var min = Math.max(0, Math.min(100, splitNumber(handle, "aria-valuemin", 10)));
  var max = Math.max(min, Math.min(100, splitNumber(handle, "aria-valuemax", 90)));
  return { min: min, max: max };
}

function clampSize(scope, value) {
  var range = splitRange(scope);
  return Math.round(Math.min(range.max, Math.max(range.min, Number(value))) * 10) / 10;
}

// A vertical separator is a line running top to bottom, so it divides left from right: x.
function splitAxis(scope) {
  var handle = splitHandle(scope);
  return handle && handle.getAttribute("aria-orientation") === "horizontal" ? "y" : "x";
}

var splitDef = {
  size: 50,
  collapsed: false,
  dragging: false,

  set: function (value) {
    var next = clampSize(this, value);
    if (!isFinite(next)) return this.size;
    if (this.collapsed) this.collapsed = false;
    if (this.size !== next) this.size = next;
    return this.size;
  },

  toggle: function () {
    this.collapsed = !this.collapsed;
  },

  reset: function () {
    this.collapsed = false;
    this.size = this[SPLIT] ? this[SPLIT].initial : this.size;
  },

  init: function (context) {
    var scope = this;
    var handle = context.require("handle")[0] || null;
    // The size the markup says it starts at, before the first paint rewrites aria-valuenow.
    var initial = splitNumber(handle, "aria-valuenow", scope.size);
    scope[SPLIT] = { context: context, initial: 0, pointer: null };
    scope[SPLIT].initial = clampSize(scope, initial);
    scope.size = scope[SPLIT].initial;
    context.cleanup(function () { delete scope[SPLIT]; });

    function measure(event) {
      var rect = context.host.getBoundingClientRect();
      if (splitAxis(scope) === "y") return rect.height > 0 ? (event.clientY - rect.top) / rect.height * 100 : NaN;
      return rect.width > 0 ? (event.clientX - rect.left) / rect.width * 100 : NaN;
    }

    context.listen(context.host, "pointerdown", function (event) {
      var bar = splitHandle(scope);
      if (!bar || event.button !== 0 || !bar.contains(event.target)) return;
      event.preventDefault();
      scope[SPLIT].pointer = event.pointerId;
      scope.dragging = true;
      try { bar.setPointerCapture(event.pointerId); } catch (error) { } // a synthetic pointer has nothing to capture
    });
    context.listen(context.host, "pointermove", function (event) {
      if (!scope.dragging || event.pointerId !== scope[SPLIT].pointer) return;
      var value = measure(event);
      if (isFinite(value)) scope.set(value);
    });
    var release = function (event) {
      if (!scope.dragging || event.pointerId !== scope[SPLIT].pointer) return;
      scope.dragging = false;
      scope[SPLIT].pointer = null;
    };
    context.listen(context.host, "pointerup", release);
    context.listen(context.host, "pointercancel", release);

    context.listen(context.host, "keydown", function (event) {
      var bar = splitHandle(scope);
      if (!bar || event.target !== bar || event.altKey || event.ctrlKey || event.metaKey) return;
      var step = splitNumber(bar, "data-step", 5);
      var back = splitAxis(scope) === "y" ? "ArrowUp" : "ArrowLeft";
      var forward = splitAxis(scope) === "y" ? "ArrowDown" : "ArrowRight";
      var range = splitRange(scope);
      if (event.key === back) { event.preventDefault(); scope.set(scope.size - step); return; }
      if (event.key === forward) { event.preventDefault(); scope.set(scope.size + step); return; }
      if (event.key === "Home") { event.preventDefault(); scope.set(range.min); return; }
      if (event.key === "End") { event.preventDefault(); scope.set(range.max); return; }
      if (event.key === "Enter") { event.preventDefault(); scope.toggle(); }
    });
  }
};

window.kit.component("split", splitDef);
window.kit.component("split@v1.0.0", splitDef);
