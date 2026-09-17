;(function () {
"use strict";

// The terminal: a code block with a title bar. One file or several — a tab per
// file, the active one shown — and a copy control on the bar that copies the
// file on screen. The markup is the standard: the component owns only what a
// method cannot reach — which panel is showing, where focus goes among the
// tabs, and what the clipboard receives.
//
//   <figure data-kit-component="terminal">
//     <div class="bar">
//       <button data-terminal-tab="html">page.html</button>
//       <button data-terminal-tab="js">page.js</button>
//       <button data-terminal-copy>Copy</button>
//     </div>
//     <pre data-terminal-panel="html"><code>…</code></pre>
//     <pre data-terminal-panel="js"><code>…</code></pre>
//   </figure>
//
// A single file needs no tabs: one panel, a title, the copy control.

// The instance data hangs off the scope under a symbol. A method a directive invokes
// receives an action proxy as `this`, and the proxy hands symbol keys through to the
// scope — which a WeakMap keyed by the raw scope object would not survive.
var INSTANCE = Symbol("kit:terminal");

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}
var timers = new WeakMap();

function id(value) {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return "";
}

function resetDelay(value) {
  value = Number(value);
  if (!Number.isFinite(value) || value <= 0) return 1500;
  return Math.min(value, 60000);
}

function parts(data, selector) {
  return data ? data.context.owned(selector) : [];
}

function ownedPart(data, target, selector) {
  if (!target || typeof target.closest !== "function") return null;
  var part = target.closest(selector);
  return part && data.context.owned(selector).indexOf(part) >= 0 ? part : null;
}

function panelIDs(data) {
  return parts(data, "[data-terminal-panel]").map(function (panel) {
    return id(panel.getAttribute("data-terminal-panel"));
  }).filter(function (name) { return name !== ""; });
}

// The active file: the authored one when it names a panel, else the first panel.
function activeID(scope, data) {
  var names = panelIDs(data);
  var current = id(scope.active);
  if (names.indexOf(current) >= 0) return current;
  return names.length ? names[0] : current;
}

function activePanel(scope, data) {
  var name = activeID(scope, data);
  var panels = parts(data, "[data-terminal-panel]");
  for (var index = 0; index < panels.length; index++) {
    if (id(panels[index].getAttribute("data-terminal-panel")) === name) return panels[index];
  }
  return panels.length ? panels[0] : null;
}

function sync(scope, data) {
  var name = activeID(scope, data);
  parts(data, "[data-terminal-panel]").forEach(function (panel) {
    var mine = id(panel.getAttribute("data-terminal-panel")) === name;
    panel.hidden = !mine;
    panel.setAttribute("data-state", mine ? "active" : "inactive");
  });
  parts(data, "[data-terminal-tab]").forEach(function (tab) {
    var mine = id(tab.getAttribute("data-terminal-tab")) === name;
    tab.setAttribute("aria-selected", mine ? "true" : "false");
    tab.setAttribute("tabindex", mine ? "0" : "-1");
    tab.setAttribute("data-state", mine ? "active" : "inactive");
  });
}

function move(scope, data, step, edge) {
  var names = panelIDs(data);
  if (!names.length) return activeID(scope, data);
  var index = names.indexOf(activeID(scope, data));
  var next;
  if (edge === "first") next = 0;
  else if (edge === "last") next = names.length - 1;
  else next = (index + step + names.length) % names.length;
  scope.active = names[next];
  sync(scope, data);
  return scope.active;
}

function focusTab(scope, data) {
  var name = activeID(scope, data);
  parts(data, "[data-terminal-tab]").forEach(function (tab) {
    if (id(tab.getAttribute("data-terminal-tab")) === name && typeof tab.focus === "function") tab.focus();
  });
}

function clear(scope) {
  var timer = timers.get(scope);
  if (timer) {
    clearTimeout(timer);
    timers.delete(scope);
  }
}

function schedule(scope) {
  clear(scope);
  var timer = setTimeout(function () {
    timers.delete(scope);
    scope.copied = false;
  }, resetDelay(scope.delay));
  timers.set(scope, timer);
}

function write(scope, payload) {
  if (!payload) {
    scope.copied = false;
    return false;
  }
  kit.clipboard.writeText(payload).then(
    function () {
      scope.copied = true;
      schedule(scope);
    },
    function () {
      clear(scope);
      scope.copied = false;
    }
  );
  return true;
}

function copyActive(scope, data) {
  var panel = activePanel(scope, data);
  return write(scope, panel ? String(panel.textContent || "") : "");
}

kit.component("terminal", {
  active: "",
  copied: false,
  delay: 1500,

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    scope.active = activeID(scope, data);
    sync(scope, data);

    context.listen(context.host, "click", function (event) {
      var tab = ownedPart(data, event.target, "[data-terminal-tab]");
      if (tab) {
        scope.select(tab.getAttribute("data-terminal-tab"));
        return;
      }
      if (ownedPart(data, event.target, "[data-terminal-copy]")) copyActive(scope, data);
    });

    // Arrow keys move between files the way a tablist does; the panel follows.
    context.listen(context.host, "keydown", function (event) {
      if (!ownedPart(data, event.target, "[data-terminal-tab]")) return;
      var key = event.key;
      if (key === "ArrowRight" || key === "ArrowDown") move(scope, data, 1);
      else if (key === "ArrowLeft" || key === "ArrowUp") move(scope, data, -1);
      else if (key === "Home") move(scope, data, 0, "first");
      else if (key === "End") move(scope, data, 0, "last");
      else return;
      event.preventDefault();
      focusTab(scope, data);
    });

    function afterRender() {
      if (data.disposed) return;
      sync(scope, data);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () {
      data.disposed = true;
      clear(scope);
    });
  },

  select: function (value) {
    var data = instance(this);
    var name = id(value);
    if (!name || panelIDs(data).indexOf(name) < 0) return activeID(this, data);
    this.active = name;
    sync(this, data);
    return name;
  },

  next: function () { return move(this, instance(this), 1); },
  previous: function () { return move(this, instance(this), -1); },

  isActive: function (value) {
    var name = id(value);
    return name !== "" && activeID(this, instance(this)) === name;
  },

  copy: function () {
    return copyActive(this, instance(this));
  },

  reset: function () {
    clear(this);
    this.copied = false;
    return false;
  }
});

})();
