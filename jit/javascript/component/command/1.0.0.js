;(function () {
"use strict";

// The command palette: ⌘K opens a search over the page's own commands. The
// items are authored — buttons or links marked data-command-item, with their
// label as text and an optional data-command-keywords — so the palette owns
// only what markup cannot: the filter, the highlighted row, the keyboard, the
// shortcut that opens it, and focus going in and coming back out.
//
//   <div data-kit-component="command">
//     <button data-command-trigger>Search…</button>
//     <div data-command-panel hidden>
//       <input data-command-input>
//       <button data-command-item data-command-keywords="deploy ship">Deploy site</button>
//       <div data-command-empty hidden>No match</div>
//     </div>
//   </div>

var INSTANCE = Symbol("kit:command");

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function text(value) {
  return typeof value === "string" ? value : "";
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
  return parts(data, "[data-command-item]");
}

function haystack(item) {
  return (String(item.textContent || "") + " " + String(item.getAttribute("data-command-keywords") || ""))
    .toLocaleLowerCase();
}

// The rows that match the query, in authored order — the palette shows these
// and hides the rest, so a group whose rows all hid can hide itself with :has.
function matching(scope, data) {
  var query = text(scope.query).trim().toLocaleLowerCase();
  var words = query ? query.split(/\s+/) : [];
  var visible = [];
  items(data).forEach(function (item, index) {
    if (item.hasAttribute("disabled") || item.getAttribute("aria-disabled") === "true") return;
    var body = haystack(item);
    var hit = words.every(function (word) { return body.indexOf(word) >= 0; });
    if (hit) visible.push(index);
  });
  return visible;
}

function sync(scope, data) {
  if (data.disposed) return;
  var all = items(data);
  var visible = matching(scope, data);
  var active = validIndex(scope.activeIndex, all.length);
  if (visible.indexOf(active) < 0) {
    active = visible.length ? visible[0] : -1;
    scope.activeIndex = active;
  }
  all.forEach(function (item, index) {
    var shown = visible.indexOf(index) >= 0;
    item.hidden = !shown;
    item.setAttribute("aria-selected", shown && index === active ? "true" : "false");
    item.setAttribute("data-state", shown && index === active ? "active" : "inactive");
    item.setAttribute("tabindex", "-1");
  });
  parts(data, "[data-command-panel]").forEach(function (panel) { panel.hidden = !scope.open; });
  parts(data, "[data-command-trigger]").forEach(function (trigger) {
    trigger.setAttribute("aria-expanded", scope.open ? "true" : "false");
  });
  parts(data, "[data-command-empty]").forEach(function (empty) {
    empty.hidden = !scope.open || visible.length > 0;
  });
  parts(data, "[data-command-input]").forEach(function (field) {
    var activeItem = active >= 0 ? all[active] : null;
    if (activeItem && activeItem.id) field.setAttribute("aria-activedescendant", activeItem.id);
    else field.removeAttribute("aria-activedescendant");
    field.setAttribute("aria-expanded", scope.open ? "true" : "false");
    if (!scope.open && field.value) field.value = "";
  });
  if (scope.count !== visible.length) scope.count = visible.length;
  if (data.focusPending) {
    data.focusPending = false;
    var input = parts(data, "[data-command-input]")[0];
    if (input && typeof input.focus === "function") input.focus();
  }
  if (data.restorePending) {
    data.restorePending = false;
    var back = data.lastTrigger;
    data.lastTrigger = null;
    if (back && back.isConnected && typeof back.focus === "function") back.focus();
  }
}

function move(scope, data, step, edge) {
  var visible = matching(scope, data);
  if (!visible.length) return -1;
  var current = visible.indexOf(validIndex(scope.activeIndex, items(data).length));
  var next;
  if (edge === "first") next = 0;
  else if (edge === "last") next = visible.length - 1;
  else next = (current + step + visible.length) % visible.length;
  scope.activeIndex = visible[next];
  sync(scope, data);
  return scope.activeIndex;
}

function open(scope, data, trigger) {
  if (data) data.lastTrigger = trigger || data.lastTrigger;
  scope.open = true;
  scope.query = "";
  scope.activeIndex = -1;
  if (data) {
    data.focusPending = true;
    sync(scope, data);
  }
  return true;
}

function close(scope, data, restore) {
  scope.open = false;
  scope.query = "";
  scope.activeIndex = -1;
  if (data) {
    data.restorePending = !!restore;
    sync(scope, data);
  }
  return false;
}

function isShortcut(event, chord) {
  var key = String(event.key || "").toLowerCase();
  var wanted = String(chord || "").toLowerCase();
  if (!wanted) return false;
  var modifier = event.metaKey || event.ctrlKey;
  return modifier && !event.altKey && !event.shiftKey && key === wanted;
}

kit.component("command", {
  open: false,
  query: "",
  activeIndex: -1,
  count: 0,
  shortcut: "k",

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false, focusPending: false, restorePending: false, lastTrigger: null };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    sync(scope, data);

    context.listen(context.host, "click", function (event) {
      var trigger = ownedPart(data, event.target, "[data-command-trigger]");
      if (trigger) {
        open(scope, data, trigger);
        return;
      }
      var item = ownedPart(data, event.target, "[data-command-item]");
      if (item) {
        // The click itself runs the item (a link navigates, a button acts); the
        // palette only has to get out of the way.
        if (!data.choosing) close(scope, data, false);
        return;
      }
      if (ownedPart(data, event.target, "[data-command-backdrop]")) close(scope, data, true);
    });

    context.listen(context.host, "input", function (event) {
      if (!ownedPart(data, event.target, "[data-command-input]")) return;
      scope.query = String(event.target.value || "");
      scope.activeIndex = -1;
      sync(scope, data);
    });

    context.listen(context.host, "keydown", function (event) {
      if (!scope.open) return;
      var key = event.key;
      if (key === "ArrowDown") move(scope, data, 1);
      else if (key === "ArrowUp") move(scope, data, -1);
      else if (key === "Home") move(scope, data, 0, "first");
      else if (key === "End") move(scope, data, 0, "last");
      else if (key === "Enter") scope.chooseActive();
      else if (key === "Escape") close(scope, data, true);
      else return;
      event.preventDefault();
    });

    // The chord opens from anywhere on the page — and toggles, so the same
    // chord closes what it opened.
    context.listen(context.host.ownerDocument, "keydown", function (event) {
      if (!isShortcut(event, scope.shortcut)) return;
      event.preventDefault();
      if (scope.open) close(scope, data, true);
      else open(scope, data, null);
    });

    function afterRender() {
      if (data.disposed) return;
      sync(scope, data);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () { data.disposed = true; });
  },

  show: function () { return open(this, instance(this), null); },
  hide: function () { return close(this, instance(this), true); },
  toggle: function () { return this.open ? this.hide() : this.show(); },

  search: function (value) {
    this.query = text(value);
    this.activeIndex = -1;
    sync(this, instance(this));
    return this.count;
  },

  next: function () { return move(this, instance(this), 1); },
  previous: function () { return move(this, instance(this), -1); },

  isActive: function (index) {
    return validIndex(index, items(instance(this)).length) === validIndex(this.activeIndex, items(instance(this)).length);
  },

  // Choosing from the keyboard runs the item as a click would — a link
  // navigates, a button acts — after the palette has closed, so what the
  // command opens is not under it.
  chooseIndex: function (index) {
    var data = instance(this);
    var all = items(data);
    var chosen = validIndex(index, all.length);
    if (chosen < 0 || all[chosen].hidden) return false;
    var item = all[chosen];
    close(this, data, false);
    if (typeof item.click === "function") {
      data.choosing = true;
      try { item.click(); } finally { data.choosing = false; }
    }
    return true;
  },

  chooseActive: function () {
    return this.chooseIndex(this.activeIndex);
  }
});

})();
