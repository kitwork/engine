;(function () {
"use strict";

// The context menu: a right-click (or the Menu key, or Shift+F10) on the host
// opens an authored menu at the pointer, kept inside the viewport. The items
// are the page's own buttons and links marked data-context-item; the component
// owns the opening, the position, the keyboard within the menu, and closing on
// Escape, an outside pointer, or a choice.
//
//   <div data-kit-component="context-menu">
//     <div data-context-target>Right-click me</div>
//     <div data-context-menu role="menu" hidden>
//       <button role="menuitem" data-context-item>Rename</button>
//     </div>
//   </div>

var INSTANCE = Symbol("kit:context-menu");

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function validIndex(value, length) {
  value = Number(value);
  return Number.isInteger(value) && value >= 0 && value < length ? value : -1;
}

function parts(data, selector) {
  return data ? data.context.owned(selector) : [];
}

function ownedPart(data, target, selector) {
  if (!target || typeof target.closest !== "function") return null;
  var part = target.closest(selector);
  return part && data.context.owned(selector).indexOf(part) >= 0 ? part : null;
}

function items(data) {
  return parts(data, "[data-context-item]");
}

function usable(data) {
  var out = [];
  items(data).forEach(function (item, index) {
    if (item.hasAttribute("disabled") || item.getAttribute("aria-disabled") === "true") return;
    out.push(index);
  });
  return out;
}

function menu(data) {
  var menus = parts(data, "[data-context-menu]");
  return menus.length ? menus[0] : null;
}

// Place the menu at the pointer, then pull it back inside the viewport so it
// never opens off-screen near an edge.
function place(data, x, y) {
  var panel = menu(data);
  if (!panel) return;
  var view = panel.ownerDocument.defaultView;
  var width = view ? view.innerWidth : 0;
  var height = view ? view.innerHeight : 0;
  panel.style.position = "fixed";
  panel.style.left = "0px";
  panel.style.top = "0px";
  panel.hidden = false;
  var box = panel.getBoundingClientRect();
  var left = x;
  var top = y;
  if (width && left + box.width > width - 8) left = Math.max(8, width - box.width - 8);
  if (height && top + box.height > height - 8) top = Math.max(8, height - box.height - 8);
  panel.style.left = Math.round(left) + "px";
  panel.style.top = Math.round(top) + "px";
}

function sync(scope, data) {
  if (data.disposed) return;
  var panel = menu(data);
  var all = items(data);
  var active = validIndex(scope.activeIndex, all.length);
  if (panel) {
    panel.hidden = !scope.open;
    if (scope.open) place(data, scope.x, scope.y);
  }
  all.forEach(function (item, index) {
    var mine = scope.open && index === active;
    item.setAttribute("tabindex", mine ? "0" : "-1");
    item.setAttribute("data-state", mine ? "active" : "inactive");
  });
  if (data.focusPending) {
    data.focusPending = false;
    var target = active >= 0 ? all[active] : panel;
    if (target && typeof target.focus === "function") target.focus();
  }
  if (data.restorePending) {
    data.restorePending = false;
    var back = data.origin;
    data.origin = null;
    if (back && back.isConnected && typeof back.focus === "function") back.focus();
  }
}

function open(scope, data, x, y, origin) {
  data.origin = origin || null;
  scope.x = Number(x) || 0;
  scope.y = Number(y) || 0;
  scope.open = true;
  var first = usable(data);
  scope.activeIndex = first.length ? first[0] : -1;
  data.focusPending = true;
  sync(scope, data);
  return true;
}

function close(scope, data, restore) {
  scope.open = false;
  scope.activeIndex = -1;
  if (data) {
    data.restorePending = !!restore;
    sync(scope, data);
  }
  return false;
}

function move(scope, data, step, edge) {
  var candidates = usable(data);
  if (!candidates.length) return -1;
  var current = candidates.indexOf(validIndex(scope.activeIndex, items(data).length));
  var next;
  if (edge === "first") next = 0;
  else if (edge === "last") next = candidates.length - 1;
  else next = (current + step + candidates.length) % candidates.length;
  scope.activeIndex = candidates[next];
  data.focusPending = true;
  sync(scope, data);
  return scope.activeIndex;
}

kit.component("context-menu", {
  open: false,
  x: 0,
  y: 0,
  activeIndex: -1,

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false, focusPending: false, restorePending: false, origin: null };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    var doc = context.host.ownerDocument;
    sync(scope, data);

    context.listen(context.host, "contextmenu", function (event) {
      var target = ownedPart(data, event.target, "[data-context-target]");
      if (!target) return;
      event.preventDefault();
      open(scope, data, event.clientX, event.clientY, target);
    });

    // The keyboard's right-click: Shift+F10 or the ContextMenu key on the target
    // opens the menu at the target's corner.
    context.listen(context.host, "keydown", function (event) {
      var target = ownedPart(data, event.target, "[data-context-target]");
      if (target && !scope.open && (event.key === "ContextMenu" || (event.key === "F10" && event.shiftKey))) {
        event.preventDefault();
        var box = target.getBoundingClientRect();
        open(scope, data, box.left + 8, box.top + 8, target);
        return;
      }
      if (!scope.open) return;
      var key = event.key;
      if (key === "ArrowDown") move(scope, data, 1);
      else if (key === "ArrowUp") move(scope, data, -1);
      else if (key === "Home") move(scope, data, 0, "first");
      else if (key === "End") move(scope, data, 0, "last");
      else if (key === "Escape") close(scope, data, true);
      else if (key === "Tab") close(scope, data, false);
      else return;
      if (key !== "Tab") event.preventDefault();
    });

    context.listen(context.host, "click", function (event) {
      if (ownedPart(data, event.target, "[data-context-item]")) close(scope, data, false);
    });

    context.listen(doc, "pointerdown", function (event) {
      if (!scope.open) return;
      var panel = menu(data);
      if (panel && panel.contains(event.target)) return;
      close(scope, data, false);
    });

    context.listen(doc, "keydown", function (event) {
      if (scope.open && event.key === "Escape" && !context.host.contains(event.target)) close(scope, data, true);
    });

    function afterRender() {
      if (data.disposed) return;
      sync(scope, data);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () { data.disposed = true; });
  },

  show: function (x, y) { return open(this, instance(this), x, y, null); },
  hide: function () { return close(this, instance(this), true); },
  next: function () { return move(this, instance(this), 1); },
  previous: function () { return move(this, instance(this), -1); },
  isActive: function (index) {
    var length = items(instance(this)).length;
    return this.open && validIndex(index, length) === validIndex(this.activeIndex, length);
  }
});

})();
