;(function () {
"use strict";

var instances = new WeakMap();
var TYPEAHEAD_DELAY = 500;

function values(value) {
  return Array.isArray(value) ? value : [];
}

function validIndex(value, length) {
  value = Number(value);
  return Number.isInteger(value) && value >= 0 && value < length ? value : -1;
}

function selection(value) {
  if (value === null || value === undefined) return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return value;
  }
  return "";
}

function ownedPart(data, target, selector) {
  if (!target || typeof target.closest !== "function") return null;
  var part = target.closest(selector);
  return part && data.context.owned(selector).indexOf(part) >= 0 ? part : null;
}

function parts(data, selector) {
  return data ? data.context.owned(selector) : [];
}

function unavailable(data, item) {
  if (!item || item.hasAttribute("disabled") || item.getAttribute("aria-disabled") === "true") return true;
  var element = item;
  while (element && element !== data.context.host) {
    var controlledMenu = element.hasAttribute && element.hasAttribute("data-dropdown-menu");
    if (!controlledMenu && element.hidden || element.hasAttribute("inert") ||
      element.getAttribute("aria-hidden") === "true") return true;
    element = element.parentElement;
  }
  return false;
}

function itemParts(data) {
  return parts(data, "[data-dropdown-item]");
}

function usableIndexes(data, scope) {
  if (data) {
    var output = [];
    var items = itemParts(data);
    items.forEach(function (item, index) {
      if (!unavailable(data, item)) output.push(index);
    });
    if (items.length) return output;
  }
  return values(scope.items).map(function (_, index) { return index; });
}

function normalizedIndex(scope, data) {
  var authored = data ? itemParts(data) : [];
  var length = authored.length || values(scope.items).length;
  return validIndex(scope.activeIndex, length);
}

function firstUsable(scope, data) {
  var indexes = usableIndexes(data, scope);
  return indexes.length ? indexes[0] : -1;
}

function lastUsable(scope, data) {
  var indexes = usableIndexes(data, scope);
  return indexes.length ? indexes[indexes.length - 1] : -1;
}

function move(scope, step) {
  var data = instances.get(scope);
  var indexes = usableIndexes(data, scope);
  if (!indexes.length) return -1;
  var current = indexes.indexOf(normalizedIndex(scope, data));
  var next = step > 0 ? (current < 0 || current + 1 >= indexes.length ? 0 : current + 1) :
    (current <= 0 ? indexes.length - 1 : current - 1);
  scope.open = true;
  scope.activeIndex = indexes[next];
  if (data) {
    data.focusPending = true;
    sync(data, scope);
  }
  return scope.activeIndex;
}

function itemValue(item) {
  if (item.hasAttribute("data-dropdown-value")) return selection(item.getAttribute("data-dropdown-value"));
  if ("value" in item) return selection(item.value);
  return selection(String(item.textContent || "").trim());
}

function focusActive(data, scope) {
  if (!scope.open) return;
  var items = itemParts(data);
  var index = validIndex(scope.activeIndex, items.length);
  if (index >= 0 && !unavailable(data, items[index]) && typeof items[index].focus === "function") items[index].focus();
}

function restoreTrigger(data) {
  var trigger = data.lastTrigger;
  data.lastTrigger = null;
  if (trigger && trigger.isConnected && typeof trigger.focus === "function") trigger.focus();
}

function sync(data, scope) {
  if (data.disposed) return;
  var triggers = parts(data, "[data-dropdown-trigger]");
  var menus = parts(data, "[data-dropdown-menu]");
  var items = itemParts(data);
  var previousItem = data.activeItem;
  var activeElement = data.context.host.ownerDocument.activeElement;
  var normalized = false;
  if (scope.open) {
    var usable = usableIndexes(data, scope);
    var current = validIndex(scope.activeIndex, items.length);
    if (usable.indexOf(current) < 0) {
      scope.activeIndex = usable.length ? usable[0] : -1;
      normalized = true;
    }
  }
  var activeIndex = validIndex(scope.activeIndex, items.length);
  triggers.forEach(function (trigger) {
    trigger.setAttribute("aria-expanded", scope.open ? "true" : "false");
  });
  menus.forEach(function (menu) { menu.hidden = !scope.open; });
  items.forEach(function (item, index) {
    item.setAttribute("tabindex", scope.open && index === activeIndex &&
      !unavailable(data, item) ? "0" : "-1");
  });
  data.activeItem = scope.open && activeIndex >= 0 ? items[activeIndex] : null;
  var focusLost = previousItem && (!previousItem.isConnected || unavailable(data, previousItem)) && (!activeElement ||
    activeElement === data.context.host.ownerDocument.body ||
    activeElement === data.context.host.ownerDocument.documentElement);
  if (data.activeItem && previousItem !== data.activeItem &&
    (activeElement === previousItem || focusLost)) {
    data.focusPending = true;
  } else if (normalized && data.activeItem && (activeElement === previousItem ||
    !activeElement || activeElement === data.context.host.ownerDocument.body ||
    activeElement === data.context.host.ownerDocument.documentElement)) {
    data.focusPending = true;
  }
  if (data.focusPending) {
    data.focusPending = false;
    focusActive(data, scope);
  }
  if (data.restorePending) {
    data.restorePending = false;
    restoreTrigger(data);
  }
}

function close(scope, restore) {
  var data = instances.get(scope);
  scope.open = false;
  scope.activeIndex = -1;
  if (data) {
    data.focusPending = false;
    data.restorePending = !!restore;
    sync(data, scope);
  }
  return false;
}

function activate(scope, index) {
  var data = instances.get(scope);
  var indexes = usableIndexes(data, scope);
  if (indexes.indexOf(index) < 0) return scope.activeIndex;
  scope.open = true;
  scope.activeIndex = index;
  if (data) {
    data.focusPending = true;
    sync(data, scope);
  }
  return index;
}

function typeahead(data, scope, key) {
  if (data.typeTimer) clearTimeout(data.typeTimer);
  data.typeBuffer += key.toLocaleLowerCase();
  data.typeTimer = setTimeout(function () {
    data.typeTimer = 0;
    data.typeBuffer = "";
  }, TYPEAHEAD_DELAY);
  var items = itemParts(data);
  if (!items.length) return -1;
  var start = validIndex(scope.activeIndex, items.length);
  for (var offset = 1; offset <= items.length; offset++) {
    var index = (Math.max(start, -1) + offset) % items.length;
    if (unavailable(data, items[index])) continue;
    var label = String(items[index].textContent || "").trim().toLocaleLowerCase();
    if (label.indexOf(data.typeBuffer) === 0) return activate(scope, index);
  }
  return scope.activeIndex;
}

kit.component("dropdown", {
  open: false,
  items: [],
  activeIndex: -1,
  selected: "",

  init: function (context) {
    var scope = this;
    var data = {
      context: context,
      lastTrigger: null,
      focusPending: false,
      restorePending: false,
      activeItem: null,
      typeBuffer: "",
      typeTimer: 0,
      disposed: false
    };
    instances.set(scope, data);
    if (scope.open && usableIndexes(data, scope).indexOf(normalizedIndex(scope, data)) < 0) {
      scope.activeIndex = firstUsable(scope, data);
    }

    context.listen(context.host, "click", function (event) {
      var trigger = ownedPart(data, event.target, "[data-dropdown-trigger]");
      if (trigger) {
        data.lastTrigger = trigger;
        scope.toggle();
        return;
      }
      var item = ownedPart(data, event.target, "[data-dropdown-item]");
      if (!item) return;
      if (unavailable(data, item)) {
        event.preventDefault();
        return;
      }
      scope.choose(itemValue(item));
    });

    context.listen(context.host, "keydown", function (event) {
      var trigger = ownedPart(data, event.target, "[data-dropdown-trigger]");
      if (trigger) {
        if (event.key === "Escape" && scope.open) {
          event.preventDefault();
          event.stopPropagation();
          close(scope, true);
          return;
        }
        if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
        event.preventDefault();
        data.lastTrigger = trigger;
        if (event.key === "ArrowDown") scope.first();
        else scope.last();
        return;
      }
      var item = ownedPart(data, event.target, "[data-dropdown-item]");
      var menu = ownedPart(data, event.target, "[data-dropdown-menu]");
      if (!item && !menu && event.key !== "Escape") return;
      if (event.key === "ArrowDown") {
        event.preventDefault();
        scope.next();
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        scope.previous();
      } else if (event.key === "Home") {
        event.preventDefault();
        scope.first();
      } else if (event.key === "End") {
        event.preventDefault();
        scope.last();
      } else if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        close(scope, true);
      } else if (event.key === "Tab") {
        close(scope, false);
      } else if (item && (event.key === "Enter" || event.key === " ")) {
        event.preventDefault();
        if (unavailable(data, item)) return;
        if (typeof item.click === "function") item.click();
      } else if (event.key.length === 1 && !event.ctrlKey && !event.altKey && !event.metaKey) {
        event.preventDefault();
        typeahead(data, scope, event.key);
      }
    });

    context.listen(context.host.ownerDocument, "pointerdown", function (event) {
      if (!scope.open || context.host.getAttribute("data-dropdown-outside") === "manual" ||
        context.host.contains(event.target)) return;
      close(scope, false);
    });

    function afterRender() {
      if (data.disposed) return;
      sync(data, scope);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () {
      data.disposed = true;
      if (data.typeTimer) clearTimeout(data.typeTimer);
      data.typeTimer = 0;
      data.typeBuffer = "";
      instances.delete(scope);
    });
  },

  show: function () {
    var data = instances.get(this);
    this.open = true;
    if (usableIndexes(data, this).indexOf(normalizedIndex(this, data)) < 0) {
      this.activeIndex = firstUsable(this, data);
    }
    if (data) {
      data.focusPending = true;
      sync(data, this);
    }
    return true;
  },

  hide: function () {
    return close(this, true);
  },

  toggle: function () {
    return this.open ? this.hide() : this.show();
  },

  next: function () {
    return move(this, 1);
  },

  previous: function () {
    return move(this, -1);
  },

  first: function () {
    return activate(this, firstUsable(this, instances.get(this)));
  },

  last: function () {
    return activate(this, lastUsable(this, instances.get(this)));
  },

  choose: function (value) {
    this.selected = selection(value);
    close(this, true);
    return this.selected;
  },

  isActive: function (index) {
    var data = instances.get(this);
    var authored = data ? itemParts(data) : [];
    var length = authored.length || values(this.items).length;
    return validIndex(index, length) === validIndex(this.activeIndex, length) &&
      validIndex(index, length) >= 0;
  }
});

})();
