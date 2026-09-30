/* context-menu component @v1.0.0 — a menu that opens where it was asked for.
 *
 * Usage — the markup declares the panel and its position; the component answers where and when:
 *
 *   <div data-kit-component="context-menu" data-kit-click:outside="hide()">
 *     <div data-kit-element="target" tabindex="0">Right-click, or Shift+F10</div>
 *     <div role="menu" aria-label="File" data-kit-show="open" hidden class="fixed z-50"
 *          data-kit-style:left="x + 'px'" data-kit-style:top="y + 'px'">
 *       <button role="menuitem" data-kit-element="item" data-kit-click="rename(); hide()">Rename</button>
 *       <button role="menuitem" data-kit-element="item" disabled>Share</button>
 *     </div>
 *   </div>
 *
 * WHY THIS IS A COMPONENT. The panel, its items, what each does and where the panel sits are all
 * markup — the position is a style binding on x and y, visible to a reader. What markup cannot do:
 *
 *   · OPEN WHERE ASKED. A right-click opens at the pointer; the ContextMenu key or Shift+F10 on the
 *     target opens under it, so the menu is reachable without a mouse at all.
 *   · STAY ON SCREEN. A right-click near an edge would push the panel out of view; the component
 *     measures the panel once it is painted and pulls x and y back inside, 8px from the edge.
 *   · MOVE FOCUS. It lands on the first item that can be chosen; ↓↑ walk and wrap, skipping a
 *     disabled item; Home/End reach the ends; Escape closes and gives focus back to the target;
 *     Tab closes and lets focus move on.
 *
 * The public surface: open, x, y, show(x, y), hide().
 */
var CONTEXT_MENU = Symbol("kit:context-menu");

function contextItems(scope) {
  var own = scope[CONTEXT_MENU];
  if (!own) return [];
  return own.context.elements("item").filter(function (item) {
    return !item.disabled && item.getAttribute("aria-disabled") !== "true";
  });
}

function contextTarget(scope) {
  var own = scope[CONTEXT_MENU];
  return own ? own.context.element("target") : null;
}

function focusQuietly(element) {
  if (!element || typeof element.focus !== "function") return;
  try { element.focus({ preventScroll: true }); } catch (error) { element.focus(); }
}

// After the panel is painted where it was asked for: measure it, put it where it was asked for and
// on screen, and put focus on the first item. Both need the paint — a hidden panel has no size and
// cannot take focus.
//
// "Where it was asked for" is a point in the VIEWPORT (a pointer, or under the target), but x and y
// are what the markup binds to left and top, and a fixed panel is only positioned against the
// viewport when no ancestor has a transform, a filter or a backdrop-filter — any of those becomes
// its containing block instead, and left: 1290px lands 1290px from THAT box. So the first paint is
// also a measurement: where the panel actually landed, minus x and y, is the origin it is positioned
// against, and the bindings get the numbers that put it at the point in the viewport.
function settle(scope, pointX, pointY) {
  var own = scope[CONTEXT_MENU];
  if (!own) return;
  own.context.afterRender(function () {
    var panel = own.context.elements("item")[0];
    panel = panel ? panel.closest('[role="menu"]') : null;
    var view = own.context.host.ownerDocument.defaultView;
    if (panel && view) {
      var box = panel.getBoundingClientRect();
      var originX = box.left - scope.x, originY = box.top - scope.y;
      var left = Math.max(8, Math.min(pointX, view.innerWidth - box.width - 8));
      var top = Math.max(8, Math.min(pointY, view.innerHeight - box.height - 8));
      var x = Math.round(left - originX), y = Math.round(top - originY);
      if (x !== scope.x) scope.x = x;
      if (y !== scope.y) scope.y = y;
    }
    focusQuietly(contextItems(scope)[0]);
  });
}

function walk(scope, by) {
  var items = contextItems(scope);
  if (!items.length) return;
  var index = items.indexOf(scope[CONTEXT_MENU].context.host.ownerDocument.activeElement);
  var next = index < 0 ? (by > 0 ? 0 : items.length - 1) : (index + by + items.length) % items.length;
  focusQuietly(items[next]);
}

var contextMenuDef = {
  open: false,
  x: 0,
  y: 0,

  // x and y are a point in the viewport, as a pointer event reports it.
  show: function (x, y) {
    this.x = Math.round(Number(x) || 0);
    this.y = Math.round(Number(y) || 0);
    this.open = true;
    settle(this, this.x, this.y);
  },

  // Focus goes back to the target only when it was inside the menu: a click elsewhere closed it
  // too, and pulling focus back from where the person just went would undo their move.
  hide: function () {
    if (!this.open) return;
    var own = this[CONTEXT_MENU];
    var inside = own && own.context.host.contains(own.context.host.ownerDocument.activeElement) &&
      own.context.host.ownerDocument.activeElement !== contextTarget(this);
    this.open = false;
    if (inside) focusQuietly(contextTarget(this));
  },

  init: function (context) {
    var scope = this;
    scope[CONTEXT_MENU] = { context: context };
    context.cleanup(function () { delete scope[CONTEXT_MENU]; });

    context.listen(context.host, "contextmenu", function (event) {
      var target = contextTarget(scope);
      if (!target || !target.contains(event.target)) return;
      event.preventDefault();
      scope.show(event.clientX, event.clientY);
    });

    context.listen(context.host, "keydown", function (event) {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
      var target = contextTarget(scope);

      // Opening from the keyboard: the menu key, or Shift+F10, on the target.
      if (!scope.open) {
        if (target && event.target === target && (event.key === "ContextMenu" || (event.key === "F10" && event.shiftKey))) {
          event.preventDefault();
          var rect = target.getBoundingClientRect();
          scope.show(rect.left, rect.bottom);
        }
        return;
      }
      switch (event.key) {
        case "ArrowDown": event.preventDefault(); walk(scope, 1); return;
        case "ArrowUp": event.preventDefault(); walk(scope, -1); return;
        case "Home": event.preventDefault(); focusQuietly(contextItems(scope)[0]); return;
        case "End": event.preventDefault(); var all = contextItems(scope); focusQuietly(all[all.length - 1]); return;
        case "Escape": event.preventDefault(); scope.hide(); return;
        case "Tab": scope.open = false; return; // moving on is an answer; let it through
      }
    });
  }
};

window.kit.component("context-menu", contextMenuDef);
window.kit.component("context-menu@v1.0.0", contextMenuDef);
