;(function () {
"use strict";

var instances = new WeakMap();
var documents = new WeakMap();
var handledEscape = new WeakSet();
var FOCUSABLE = [
  "[data-dialog-initial-focus]",
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled]):not([type='hidden'])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]"
].join(",");

function result(value) {
  if (value === null || value === undefined) return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return "";
}

function partValue(element, fallback) {
  if (element && element.hasAttribute("data-dialog-value")) {
    return result(element.getAttribute("data-dialog-value"));
  }
  if (element && "value" in element) return result(element.value);
  return fallback;
}

function managerFor(ownerDocument) {
  var manager = documents.get(ownerDocument);
  if (manager) return manager;
  manager = {
    document: ownerDocument,
    stack: [],
    background: new Map(),
    overflow: "",
    overflowPriority: "",
    locked: false
  };
  documents.set(ownerDocument, manager);
  return manager;
}

function rememberBackground(manager, element) {
  if (manager.background.has(element)) return;
  manager.background.set(element, {
    inert: element.hasAttribute("inert"),
    ariaHidden: element.getAttribute("aria-hidden")
  });
}

function restoreBackground(manager, clear) {
  manager.background.forEach(function (before, element) {
    if (before.inert) element.setAttribute("inert", "");
    else element.removeAttribute("inert");
    if (before.ariaHidden === null) element.removeAttribute("aria-hidden");
    else element.setAttribute("aria-hidden", before.ariaHidden);
  });
  if (clear) manager.background.clear();
}

function lockBackground(manager) {
  restoreBackground(manager, false);
  var top = manager.stack.length ? manager.stack[manager.stack.length - 1] : null;
  var root = manager.document.documentElement;
  if (!top) {
    if (manager.locked && root && root.style) {
      root.style.setProperty("overflow", manager.overflow, manager.overflowPriority);
    }
    manager.locked = false;
    restoreBackground(manager, true);
    return;
  }
  if (!manager.locked && root && root.style) {
    manager.overflow = root.style.getPropertyValue("overflow");
    manager.overflowPriority = root.style.getPropertyPriority("overflow");
    root.style.setProperty("overflow", "hidden");
    manager.locked = true;
  }
  var node = top.panel;
  var body = manager.document.body;
  while (node && node !== body) {
    var parent = node.parentElement;
    if (!parent) break;
    Array.prototype.forEach.call(parent.children, function (sibling) {
      if (sibling === node) return;
      rememberBackground(manager, sibling);
      sibling.setAttribute("inert", "");
      sibling.setAttribute("aria-hidden", "true");
    });
    node = parent;
  }
}

function removeFromStack(data) {
  var stack = data.manager.stack;
  var index = stack.indexOf(data);
  if (index >= 0) stack.splice(index, 1);
  lockBackground(data.manager);
}

function ownedPart(data, target, selector) {
  if (!target || typeof target.closest !== "function") return null;
  var part = target.closest(selector);
  return part && data.context.owned(selector).indexOf(part) >= 0 ? part : null;
}

function panelFor(data) {
  var panels = data.context.owned("[data-dialog-panel]");
  return panels.length ? panels[0] : null;
}

function focusable(data) {
  var panel = data.panel;
  if (!panel || typeof panel.querySelectorAll !== "function") return [];
  return Array.prototype.filter.call(panel.querySelectorAll(FOCUSABLE), function (element) {
    if (!panel.contains(element) || element.hidden || element.hasAttribute("disabled") ||
      element.getAttribute("aria-disabled") === "true") return false;
    if (String(element.localName || "").toLowerCase() === "input" &&
      String(element.getAttribute("type") || "").toLowerCase() === "hidden") return false;
    var ancestor = element;
    while (ancestor && ancestor !== panel) {
      if (ancestor.hasAttribute("data-kit-ignore") || ancestor.hidden || ancestor.hasAttribute("inert") ||
        ancestor.getAttribute("aria-hidden") === "true") {
        return false;
      }
      ancestor = ancestor.parentElement;
    }
    return element.getAttribute("tabindex") !== "-1" || element.hasAttribute("data-dialog-initial-focus");
  });
}

function focusInside(data) {
  if (!data.active || data.manager.stack[data.manager.stack.length - 1] !== data) return;
  var candidates = focusable(data);
  var preferred = candidates.filter(function (element) {
    return element.hasAttribute("data-dialog-initial-focus");
  });
  var ordered = preferred.concat(candidates.filter(function (element) {
    return preferred.indexOf(element) < 0;
  }));
  for (var index = 0; index < ordered.length; index++) {
    var candidate = ordered[index];
    if (typeof candidate.focus !== "function") continue;
    candidate.focus();
    if (candidate.ownerDocument.activeElement === candidate) return;
  }
  if (data.panel && typeof data.panel.focus === "function") data.panel.focus();
}

function restoreFocus(data) {
  var target = data.restoreFocus;
  data.restoreFocus = null;
  if (target && target.isConnected && typeof target.focus === "function") target.focus();
}

function unwindAbove(data) {
  var stack = data.manager.stack;
  var position = stack.indexOf(data);
  while (position >= 0 && position + 1 < stack.length) {
    var child = stack[stack.length - 1];
    if (!child || child === data) break;
    var before = stack.length;
    if (child.scope && child.scope.open && typeof child.scope.cancel === "function") {
      child.scope.cancel("ancestor");
    }
    if (stack.length >= before && stack[stack.length - 1] === child) {
      child.active = false;
      removeFromStack(child);
    }
  }
}

function sync(data, scope) {
  if (data.disposed) return;
  var panel = panelFor(data);
  var previousPanel = data.panel;
  var panelChanged = panel !== previousPanel;
  if (panelChanged && previousPanel && data.addedTabIndex) {
    previousPanel.removeAttribute("tabindex");
  }
  if (panelChanged && data.active && scope.open && panel) {
    unwindAbove(data);
    var position = data.manager.stack.indexOf(data);
    if (position >= 0) data.manager.stack.splice(position, 1);
    data.panel = panel;
    data.addedTabIndex = false;
    if (!panel.hasAttribute("tabindex")) {
      panel.setAttribute("tabindex", "-1");
      data.addedTabIndex = true;
    }
    data.manager.stack.push(data);
    lockBackground(data.manager);
    data.focusPending = true;
  } else if (panelChanged) {
    data.panel = panel;
    data.addedTabIndex = false;
  }
  data.context.owned("[data-dialog-trigger]").forEach(function (trigger) {
    trigger.setAttribute("aria-expanded", scope.open ? "true" : "false");
  });
  if (!panel) {
    if (data.active) {
      unwindAbove(data);
      data.active = false;
      removeFromStack(data);
      restoreFocus(data);
    }
    return;
  }
  if (!scope.open && data.active) unwindAbove(data);
  panel.hidden = !scope.open;
  if (scope.open && !data.active) {
    data.active = true;
    if (!panel.hasAttribute("tabindex")) {
      panel.setAttribute("tabindex", "-1");
      data.addedTabIndex = true;
    }
    var active = panel.ownerDocument.activeElement;
    if (!data.restoreFocus && active && active !== panel.ownerDocument.body && !panel.contains(active)) {
      data.restoreFocus = active;
    }
    var prior = data.manager.stack.indexOf(data);
    if (prior >= 0) data.manager.stack.splice(prior, 1);
    data.manager.stack.push(data);
    lockBackground(data.manager);
    data.focusPending = true;
  } else if (!scope.open && data.active) {
    data.active = false;
    removeFromStack(data);
    restoreFocus(data);
  }
  if (data.focusPending && scope.open) {
    data.focusPending = false;
    focusInside(data);
  }
}

function setOpen(scope, open, value, cancelled) {
  var data = instances.get(scope);
  if (open) {
    scope.returnValue = "";
    scope.cancelled = false;
    scope.open = true;
    if (data) data.focusPending = true;
  } else {
    if (data && data.active) unwindAbove(data);
    scope.returnValue = result(value);
    scope.cancelled = !!cancelled;
    scope.open = false;
  }
  if (data) sync(data, scope);
  return !!open;
}

kit.component("dialog", {
  open: false,
  returnValue: "",
  cancelled: false,

  init: function (context) {
    var scope = this;
    var data = {
      context: context,
      scope: scope,
      manager: managerFor(context.host.ownerDocument),
      panel: null,
      restoreFocus: null,
      active: false,
      focusPending: false,
      addedTabIndex: false,
      disposed: false
    };
    instances.set(scope, data);

    context.listen(context.host, "click", function (event) {
      var trigger = ownedPart(data, event.target, "[data-dialog-trigger]");
      if (trigger) {
        data.restoreFocus = trigger;
        scope.show();
        return;
      }
      var cancel = ownedPart(data, event.target, "[data-dialog-cancel]");
      if (cancel) {
        scope.cancel(partValue(cancel, ""));
        return;
      }
      var close = ownedPart(data, event.target, "[data-dialog-close]");
      if (close) scope.close(partValue(close, ""));
    });

    context.listen(context.host.ownerDocument, "keydown", function (event) {
      if (handledEscape.has(event)) return;
      if (!data.active || data.manager.stack[data.manager.stack.length - 1] !== data) return;
      if (event.key === "Escape") {
        handledEscape.add(event);
        event.preventDefault();
        event.stopPropagation();
        scope.cancel("escape");
        return;
      }
      if (event.key !== "Tab") return;
      var candidates = focusable(data);
      if (!candidates.length) {
        event.preventDefault();
        focusInside(data);
        return;
      }
      var current = data.panel.ownerDocument.activeElement;
      var index = candidates.indexOf(current);
      event.preventDefault();
      for (var offset = 1; offset <= candidates.length; offset++) {
        var next = event.shiftKey ?
          (index < 0 ? candidates.length - offset : (index - offset + candidates.length) % candidates.length) :
          (index + offset) % candidates.length;
        candidates[next].focus();
        if (data.panel.ownerDocument.activeElement === candidates[next]) return;
      }
      focusInside(data);
    });

    context.listen(context.host.ownerDocument, "focusin", function (event) {
      if (!data.active || data.manager.stack[data.manager.stack.length - 1] !== data ||
        !data.panel || data.panel.contains(event.target)) return;
      focusInside(data);
    });

    function afterRender() {
      if (data.disposed) return;
      sync(data, scope);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () {
      data.disposed = true;
      if (data.active) {
        unwindAbove(data);
        data.active = false;
        removeFromStack(data);
      }
      if (data.panel && data.addedTabIndex) data.panel.removeAttribute("tabindex");
      restoreFocus(data);
      data.scope = null;
      instances.delete(scope);
    });
  },

  show: function () {
    return setOpen(this, true, "", false);
  },

  close: function (value) {
    return setOpen(this, false, value, false);
  },

  cancel: function (value) {
    return setOpen(this, false, value, true);
  },

  toggle: function () {
    return this.open ? this.close("") : this.show();
  }
});

})();
