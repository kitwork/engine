// KitJS component: menu@1.0.0
// Custom ARIA menu button with roving focus, typeahead and deterministic cleanup.

;(function (global, kit) {
  "use strict";

  var records = new WeakMap();

  function ownsNode(component, element) {
    var host = component.$host;
    return !!host && !!element && typeof element.closest === "function" &&
      element.closest("[data-kit-component]") === host;
  }

  function firstOwned(component, selector) {
    var host = component.$host;
    if (!host) return null;
    var elements = host.querySelectorAll(selector);
    for (var index = 0; index < elements.length; index++) {
      if (ownsNode(component, elements[index])) return elements[index];
    }
    return null;
  }

  function allItems(component) {
    var host = component.$host;
    if (!host) return [];
    return Array.prototype.slice.call(host.querySelectorAll('[role="menuitem"]')).filter(function (item) {
      return ownsNode(component, item);
    });
  }

  function disabled(item) {
    return item.disabled || item.getAttribute("aria-disabled") === "true";
  }

  function enabledItems(component) {
    return allItems(component).filter(function (item) {
      return !disabled(item) && !item.hidden;
    });
  }

  function indexOf(component, item) {
    return allItems(component).indexOf(item);
  }

  function focus(component, element, expectedOpen) {
    var host = component.$host;
    if (!host || !host.isConnected || component.open !== expectedOpen || !element || !element.isConnected) return;
    var active = host.ownerDocument.activeElement;
    Promise.resolve().then(function () {
      host = component.$host;
      var current = host && host.ownerDocument.activeElement;
      if (!host || !host.isConnected || component.open !== expectedOpen || !element.isConnected ||
        (current !== active && current !== host.ownerDocument.body && current !== host.ownerDocument.documentElement)) return;
      try { element.focus({ preventScroll: true }); } catch (_) { element.focus(); }
    });
  }

  function focusActive(component) {
    focus(component, allItems(component)[component.activeIndex], true);
  }

  function openMenu(component, edge) {
    var items = enabledItems(component);
    component.open = true;
    if (!items.length) {
      component.activeIndex = -1;
      return true;
    }
    var item = edge === "last" ? items[items.length - 1] : items[0];
    component.activeIndex = indexOf(component, item);
    focusActive(component);
    return true;
  }

  function closeMenu(component, restoreFocus) {
    var record = records.get(component);
    component.open = false;
    component.activeIndex = -1;
    if (restoreFocus !== false && record) focus(component, record.trigger, false);
    return false;
  }

  function move(component, direction) {
    var items = enabledItems(component);
    if (!items.length) {
      component.activeIndex = -1;
      return;
    }
    var position = items.indexOf(allItems(component)[component.activeIndex]);
    if (position < 0) position = direction > 0 ? -1 : 0;
    position = (position + direction + items.length) % items.length;
    component.activeIndex = indexOf(component, items[position]);
    focusActive(component);
  }

  function labelOf(item) {
    return String(item.getAttribute("data-label") || item.textContent || "").trim();
  }

  function valueOf(item) {
    var value = item.getAttribute("data-value");
    return value === null ? labelOf(item) : value;
  }

  kit.component("menu", {
    open: false,
    activeIndex: -1,
    selected: "",

    init: function () {
      var component = this;
      var host = this.$host;
      var trigger = this.$refs.trigger || firstOwned(component, "[data-menu-trigger]");
      var menu = this.$refs.menu || firstOwned(component, '[role="menu"]');
      if (!trigger || !menu) {
        throw new Error("component:menu requires data-kit-ref=\"trigger\" and data-kit-ref=\"menu\"");
      }

      var record = { trigger: trigger, menu: menu, search: "", timer: 0 };
      records.set(component, record);

      function onClick(event) {
        var button = event.target.closest && event.target.closest("[data-menu-trigger]");
        if (button && ownsNode(component, button)) {
          component.toggle();
          return;
        }
        var item = event.target.closest && event.target.closest('[role="menuitem"]');
        if (!item || !menu.contains(item) || !ownsNode(component, item) || disabled(item)) return;
        component.select(valueOf(item), indexOf(component, item));
      }

      function onKeyDown(event) {
        if (event.target === trigger) {
          if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            openMenu(component, "first");
          } else if (event.key === "ArrowUp") {
            event.preventDefault();
            openMenu(component, "last");
          }
          return;
        }
        var item = event.target.closest && event.target.closest('[role="menuitem"]');
        if (!component.open || !item || !menu.contains(item) || !ownsNode(component, item)) return;
        if (event.key === "ArrowDown") {
          event.preventDefault();
          move(component, 1);
        } else if (event.key === "ArrowUp") {
          event.preventDefault();
          move(component, -1);
        } else if (event.key === "Home") {
          event.preventDefault();
          openMenu(component, "first");
        } else if (event.key === "End") {
          event.preventDefault();
          openMenu(component, "last");
        } else if (event.key === "Escape") {
          event.preventDefault();
          closeMenu(component, true);
        } else if (event.key === "Tab") {
          closeMenu(component, false);
        } else if ((event.key === "Enter" || event.key === " ") && component.activeIndex >= 0) {
          var active = allItems(component)[component.activeIndex];
          if (active && !disabled(active)) {
            event.preventDefault();
            active.click();
          }
        } else if (event.key.length === 1 && !event.altKey && !event.ctrlKey && !event.metaKey) {
          global.clearTimeout(record.timer);
          record.search += event.key.toLocaleLowerCase();
          record.timer = global.setTimeout(function () { record.search = ""; }, 500);
          var items = enabledItems(component);
          var start = items.indexOf(allItems(component)[component.activeIndex]);
          for (var offset = 1; offset <= items.length; offset++) {
            var candidate = items[(start + offset + items.length) % items.length];
            if (labelOf(candidate).toLocaleLowerCase().indexOf(record.search) === 0) {
              event.preventDefault();
              component.activeIndex = indexOf(component, candidate);
              focusActive(component);
              break;
            }
          }
        }
      }

      function onOutsidePointerDown(event) {
        if (component.open && !host.contains(event.target)) closeMenu(component, false);
      }

      host.addEventListener("click", onClick);
      host.addEventListener("keydown", onKeyDown);
      global.document.addEventListener("pointerdown", onOutsidePointerDown);

      return function () {
        host.removeEventListener("click", onClick);
        host.removeEventListener("keydown", onKeyDown);
        global.document.removeEventListener("pointerdown", onOutsidePointerDown);
        global.clearTimeout(record.timer);
        records.delete(component);
      };
    },

    show: function () {
      return openMenu(this, "first");
    },

    close: function () {
      return closeMenu(this, true);
    },

    toggle: function () {
      return this.open ? closeMenu(this, true) : openMenu(this, "first");
    },

    select: function (value, index) {
      this.selected = String(value === null || value === undefined ? "" : value);
      this.activeIndex = Number.isInteger(index) ? index : -1;
      this.$host.dispatchEvent(new CustomEvent("kit:menu-select", {
        bubbles: true,
        detail: { value: this.selected }
      }));
      closeMenu(this, true);
      return this.selected;
    },

    isActive: function (index) {
      return this.activeIndex === index;
    },

    isSelected: function (value) {
      return this.selected === String(value === null || value === undefined ? "" : value);
    }
  });
})(globalThis, globalThis.kit);
