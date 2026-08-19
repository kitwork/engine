;(function () {
"use strict";

var instances = new WeakMap();

function tabID(value) {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return "";
}

function tabIDs(value) {
  if (!Array.isArray(value)) return [];
  return value.map(tabID).filter(function (id) { return id !== ""; });
}

function ownedPart(data, target, selector) {
  if (!target || typeof target.closest !== "function") return null;
  var part = target.closest(selector);
  return part && data.context.owned(selector).indexOf(part) >= 0 ? part : null;
}

function tabParts(data) {
  return data ? data.context.owned("[data-tab]") : [];
}

function unavailable(data, tab) {
  if (!tab || tab.hasAttribute("disabled") || tab.getAttribute("aria-disabled") === "true") return true;
  var element = tab;
  while (element && element !== data.context.host) {
    if (element.hidden || element.hasAttribute("inert") || element.getAttribute("aria-hidden") === "true") return true;
    element = element.parentElement;
  }
  return false;
}

function runtimeIDs(data, scope, enabledOnly) {
  var parts = tabParts(data);
  if (!parts.length) return tabIDs(scope.tabs);
  var ids = [];
  parts.forEach(function (tab) {
    var id = tabID(tab.getAttribute("data-tab"));
    if (id && (!enabledOnly || !unavailable(data, tab)) && ids.indexOf(id) < 0) ids.push(id);
  });
  return ids;
}

function enabledIDs(data, scope) {
  return runtimeIDs(data, scope, true);
}

function activePart(data, id) {
  var match = null;
  tabParts(data).some(function (tab) {
    if (tabID(tab.getAttribute("data-tab")) !== id || unavailable(data, tab)) return false;
    match = tab;
    return true;
  });
  return match;
}

function sync(data, scope) {
  if (data.disposed) return;
  var previousPart = data.focusPart;
  var ownerDocument = data.context.host.ownerDocument;
  var activeElement = ownerDocument.activeElement;
  var active = tabID(scope.active);
  var enabled = enabledIDs(data, scope);
  if (enabled.indexOf(active) < 0 && enabled.length) {
    active = enabled[0];
    scope.active = active;
  }
  var focusID = data.focusID && enabled.indexOf(data.focusID) >= 0 ?
    data.focusID : active;
  data.focusID = focusID;
  var claimedSelected = false;
  var claimedFocus = false;
  var focusPart = null;
  tabParts(data).forEach(function (tab) {
    var id = tabID(tab.getAttribute("data-tab"));
    var selected = !claimedSelected && id !== "" && id === active && !unavailable(data, tab);
    var focused = !claimedFocus && id !== "" && id === focusID && !unavailable(data, tab);
    if (selected) claimedSelected = true;
    if (focused) {
      claimedFocus = true;
      focusPart = tab;
    }
    tab.setAttribute("aria-selected", selected ? "true" : "false");
    tab.setAttribute("tabindex", focused ? "0" : "-1");
  });
  var claimedPanel = false;
  data.context.owned("[data-panel]").forEach(function (panel) {
    var shown = !claimedPanel && tabID(panel.getAttribute("data-panel")) === active;
    if (shown) claimedPanel = true;
    panel.hidden = !shown;
  });
  data.focusPart = focusPart;
  var focusLost = previousPart && (!previousPart.isConnected || unavailable(data, previousPart)) &&
    (!activeElement || activeElement === ownerDocument.body || activeElement === ownerDocument.documentElement);
  if (previousPart && focusPart && previousPart !== focusPart &&
    (activeElement === previousPart || focusLost)) data.focusPending = true;
  if (data.focusPending) {
    data.focusPending = false;
    var target = activePart(data, focusID);
    if (target && typeof target.focus === "function") target.focus();
  }
}

function select(scope, value, focus) {
  var id = tabID(value);
  var data = instances.get(scope);
  var items = enabledIDs(data, scope);
  if (!id || items.indexOf(id) < 0) return scope.active;
  scope.active = id;
  if (data) {
    data.focusID = id;
    data.focusPending = !!focus;
    sync(data, scope);
  }
  return id;
}

function move(scope, step, activate) {
  var data = instances.get(scope);
  var items = enabledIDs(data, scope);
  if (!items.length) return scope.active;
  var current = data && data.focusID ? items.indexOf(data.focusID) : -1;
  if (current < 0) current = items.indexOf(tabID(scope.active));
  var next = step > 0 ? (current < 0 || current + 1 >= items.length ? 0 : current + 1) :
    (current <= 0 ? items.length - 1 : current - 1);
  var id = items[next];
  if (activate) return select(scope, id, true);
  if (data) {
    data.focusID = id;
    data.focusPending = true;
    sync(data, scope);
  }
  return id;
}

function edge(scope, last, activate) {
  var data = instances.get(scope);
  var items = enabledIDs(data, scope);
  if (!items.length) return scope.active;
  var id = items[last ? items.length - 1 : 0];
  if (activate) return select(scope, id, true);
  if (data) {
    data.focusID = id;
    data.focusPending = true;
    sync(data, scope);
  }
  return id;
}

function automatic(data) {
  return data.context.host.getAttribute("data-tabs-activation") !== "manual";
}

function vertical(data) {
  var lists = data.context.owned("[data-tabs-list]");
  return lists.length && lists[0].getAttribute("aria-orientation") === "vertical";
}

kit.component("tabs", {
  tabs: [],
  active: "",

  init: function (context) {
    var scope = this;
    var data = {
      context: context,
      focusID: "",
      focusPart: null,
      focusPending: false,
      disposed: false
    };
    instances.set(scope, data);
    var authored = runtimeIDs(data, scope, false);
    if (!tabIDs(scope.tabs).length && authored.length) scope.tabs = authored;
    var enabled = enabledIDs(data, scope);
    if (enabled.indexOf(tabID(scope.active)) < 0 && enabled.length) scope.active = enabled[0];
    data.focusID = tabID(scope.active);

    context.listen(context.host, "click", function (event) {
      var tab = ownedPart(data, event.target, "[data-tab]");
      if (!tab || unavailable(data, tab)) return;
      select(scope, tab.getAttribute("data-tab"), true);
    });

    context.listen(context.host, "keydown", function (event) {
      var tab = ownedPart(data, event.target, "[data-tab]");
      if (!tab || unavailable(data, tab)) return;
      data.focusID = tabID(tab.getAttribute("data-tab"));
      var isVertical = vertical(data);
      var forward = isVertical ? "ArrowDown" : "ArrowRight";
      var backward = isVertical ? "ArrowUp" : "ArrowLeft";
      if (event.key === forward) {
        event.preventDefault();
        move(scope, 1, automatic(data));
      } else if (event.key === backward) {
        event.preventDefault();
        move(scope, -1, automatic(data));
      } else if (event.key === "Home") {
        event.preventDefault();
        edge(scope, false, automatic(data));
      } else if (event.key === "End") {
        event.preventDefault();
        edge(scope, true, automatic(data));
      } else if (!automatic(data) && (event.key === "Enter" || event.key === " ")) {
        event.preventDefault();
        select(scope, data.focusID, true);
      }
    });

    function afterRender() {
      if (data.disposed) return;
      sync(data, scope);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () {
      data.disposed = true;
      instances.delete(scope);
    });
  },

  select: function (value) {
    return select(this, value, false);
  },

  next: function () {
    return move(this, 1, true);
  },

  previous: function () {
    return move(this, -1, true);
  },

  first: function () {
    return edge(this, false, true);
  },

  last: function () {
    return edge(this, true, true);
  },

  isActive: function (value) {
    var id = tabID(value);
    return id !== "" && tabID(this.active) === id;
  }
});

})();
