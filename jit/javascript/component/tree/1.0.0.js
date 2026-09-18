;(function () {
"use strict";

// The tree view: nested lists the reader can open, close and walk with the
// arrow keys. Items are authored — li[role=treeitem] with a data-tree-id, a
// data-tree-toggle button, and a nested ul[role=group] — and the component
// owns what markup cannot: which branches are open, which item is selected,
// the roving focus, and the arrow keys' meaning.
//
//   <ul role="tree" data-kit-component="tree" data-kit-scope="expanded: ['src']">
//     <li role="treeitem" data-tree-item data-tree-id="src">
//       <button data-tree-toggle>src</button>
//       <ul role="group" data-tree-group>…</ul>

var INSTANCE = Symbol("kit:tree");

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function id(value) {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return "";
}

function ids(value) {
  return Array.isArray(value) ? value.map(id).filter(function (name) { return name !== ""; }) : [];
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
  return parts(data, "[data-tree-item]");
}

function itemID(item) {
  return id(item.getAttribute("data-tree-id"));
}

function group(item) {
  for (var child = item.firstElementChild; child; child = child.nextElementSibling) {
    if (child.hasAttribute("data-tree-group")) return child;
  }
  return null;
}

function parentItem(data, item) {
  var host = data.context.host;
  var node = item.parentElement;
  while (node && node !== host) {
    if (node.hasAttribute("data-tree-item")) return node;
    node = node.parentElement;
  }
  return null;
}

function isOpen(scope, item) {
  return ids(scope.expanded).indexOf(itemID(item)) >= 0;
}

// An item is visible when every ancestor branch is open.
function visibleItems(scope, data) {
  return items(data).filter(function (item) {
    var parent = parentItem(data, item);
    while (parent) {
      if (!isOpen(scope, parent)) return false;
      parent = parentItem(data, parent);
    }
    return true;
  });
}

function focusItem(item) {
  var toggle = null;
  for (var child = item.firstElementChild; child; child = child.nextElementSibling) {
    if (child.hasAttribute("data-tree-toggle")) { toggle = child; break; }
  }
  var target = toggle || item;
  if (typeof target.focus === "function") target.focus();
}

function sync(scope, data) {
  if (data.disposed) return;
  var visible = visibleItems(scope, data);
  var focusable = id(scope.focused);
  if (!visible.some(function (item) { return itemID(item) === focusable; })) {
    focusable = visible.length ? itemID(visible[0]) : "";
  }
  items(data).forEach(function (item) {
    var branch = group(item);
    var open = branch ? isOpen(scope, item) : false;
    if (branch) {
      item.setAttribute("aria-expanded", open ? "true" : "false");
      branch.hidden = !open;
    } else {
      item.removeAttribute("aria-expanded");
    }
    var selected = itemID(item) === id(scope.selected);
    item.setAttribute("aria-selected", selected ? "true" : "false");
    item.setAttribute("data-state", selected ? "selected" : (open ? "open" : "closed"));
    var mine = itemID(item) === focusable;
    var toggle = null;
    for (var child = item.firstElementChild; child; child = child.nextElementSibling) {
      if (child.hasAttribute("data-tree-toggle")) { toggle = child; break; }
    }
    (toggle || item).setAttribute("tabindex", mine ? "0" : "-1");
  });
  if (data.focusPending) {
    data.focusPending = false;
    var target = items(data).filter(function (item) { return itemID(item) === focusable; })[0];
    if (target) focusItem(target);
  }
}

function setExpanded(scope, name, open) {
  var current = ids(scope.expanded);
  var index = current.indexOf(name);
  if (open && index < 0) current.push(name);
  else if (!open && index >= 0) current.splice(index, 1);
  else return false;
  scope.expanded = current;
  return true;
}

function moveFocus(scope, data, step, edge) {
  var visible = visibleItems(scope, data);
  if (!visible.length) return "";
  var current = visible.map(itemID).indexOf(id(scope.focused));
  var next;
  if (edge === "first") next = 0;
  else if (edge === "last") next = visible.length - 1;
  else next = Math.min(visible.length - 1, Math.max(0, (current < 0 ? 0 : current) + step));
  scope.focused = itemID(visible[next]);
  data.focusPending = true;
  sync(scope, data);
  return scope.focused;
}

kit.component("tree", {
  expanded: [],
  selected: "",
  focused: "",

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false, focusPending: false };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    scope.expanded = ids(scope.expanded);
    sync(scope, data);

    context.listen(context.host, "click", function (event) {
      var toggle = ownedPart(data, event.target, "[data-tree-toggle]");
      if (!toggle) return;
      var item = ownedPart(data, toggle, "[data-tree-item]");
      if (!item) return;
      var name = itemID(item);
      scope.focused = name;
      if (group(item)) setExpanded(scope, name, !isOpen(scope, item));
      scope.selected = name;
      sync(scope, data);
    });

    context.listen(context.host, "keydown", function (event) {
      var item = ownedPart(data, event.target, "[data-tree-item]");
      if (!item) return;
      var name = itemID(item);
      var key = event.key;
      if (key === "ArrowDown") moveFocus(scope, data, 1);
      else if (key === "ArrowUp") moveFocus(scope, data, -1);
      else if (key === "Home") moveFocus(scope, data, 0, "first");
      else if (key === "End") moveFocus(scope, data, 0, "last");
      else if (key === "ArrowRight") {
        if (group(item) && !isOpen(scope, item)) { setExpanded(scope, name, true); scope.focused = name; sync(scope, data); }
        else if (group(item)) moveFocus(scope, data, 1);
      } else if (key === "ArrowLeft") {
        if (group(item) && isOpen(scope, item)) { setExpanded(scope, name, false); scope.focused = name; sync(scope, data); }
        else {
          var parent = parentItem(data, item);
          if (parent) { scope.focused = itemID(parent); data.focusPending = true; sync(scope, data); }
        }
      } else if (key === "Enter" || key === " ") {
        scope.selected = name;
        scope.focused = name;
        if (group(item)) setExpanded(scope, name, !isOpen(scope, item));
        sync(scope, data);
      } else return;
      event.preventDefault();
    });

    function afterRender() {
      if (data.disposed) return;
      sync(scope, data);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () { data.disposed = true; });
  },

  toggle: function (name) {
    var data = instance(this);
    name = id(name);
    var open = ids(this.expanded).indexOf(name) >= 0;
    setExpanded(this, name, !open);
    if (data) sync(this, data);
    return !open;
  },
  expand: function (name) { setExpanded(this, id(name), true); if (instance(this)) sync(this, instance(this)); return true; },
  collapse: function (name) { setExpanded(this, id(name), false); if (instance(this)) sync(this, instance(this)); return false; },
  isExpanded: function (name) { return ids(this.expanded).indexOf(id(name)) >= 0; },

  expandAll: function () {
    var data = instance(this);
    this.expanded = items(data).filter(function (item) { return !!group(item); }).map(itemID);
    if (data) sync(this, data);
    return this.expanded;
  },
  collapseAll: function () {
    this.expanded = [];
    if (instance(this)) sync(this, instance(this));
    return this.expanded;
  },

  select: function (name) {
    this.selected = id(name);
    if (instance(this)) sync(this, instance(this));
    return this.selected;
  },
  isSelected: function (name) { return id(name) !== "" && this.selected === id(name); }
});

})();
