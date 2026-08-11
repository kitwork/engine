// KitJS component: command-palette@1.0.0
// Custom modal overlay with command filtering, focus containment and Ctrl/Cmd+K.

;(function (global, kit) {
  "use strict";

  var records = new WeakMap();
  var instances = [];
  var moduleOwner = null;
  var overlay = kit.__kitwork_core__.overlay;

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

  function allCommands(component) {
    var host = component.$host;
    if (!host) return [];
    return Array.prototype.slice.call(host.querySelectorAll("[data-command]")).filter(function (command) {
      return ownsNode(component, command);
    });
  }

  function disabled(command) {
    return command.disabled || command.getAttribute("aria-disabled") === "true";
  }

  function labelOf(command) {
    return String(command.getAttribute("data-label") || command.textContent || "").trim();
  }

  function valueOf(command) {
    var value = command.getAttribute("data-value");
    return value === null ? labelOf(command) : value;
  }

  function searchable(command) {
    return labelOf(command) + " " + String(command.getAttribute("data-keywords") || "");
  }

  function normalized(value) {
    return String(value === null || value === undefined ? "" : value).trim().toLocaleLowerCase();
  }

  function matchingCommands(component) {
    var query = normalized(component.query);
    return allCommands(component).filter(function (command) {
      return !disabled(command) && (!query || normalized(searchable(command)).indexOf(query) !== -1);
    });
  }

  function indexOf(component, command) {
    return allCommands(component).indexOf(command);
  }

  function firstIndex(component) {
    var commands = matchingCommands(component);
    return commands.length ? indexOf(component, commands[0]) : -1;
  }

  function isOverlayOwner(component) {
    return overlay.isOwner(component);
  }

  function focusOwned(component, element) {
    if (!component.open || !isOverlayOwner(component) || !element || !element.isConnected) return;
    Promise.resolve().then(function () {
      if (!component.open || !isOverlayOwner(component) || !element.isConnected) return;
      try { element.focus({ preventScroll: true }); } catch (_) { element.focus(); }
    });
  }

  function claimOverlay(component) {
    overlay.claim(component, function (shouldRestore) { return closePalette(component, shouldRestore); });
    moduleOwner = component;
  }

  function registerInstance(component) {
    if (instances.indexOf(component) === -1) instances.push(component);
    if (!moduleOwner) moduleOwner = component;
  }

  function unregisterInstance(component) {
    var index = instances.indexOf(component);
    if (index !== -1) instances.splice(index, 1);
    if (moduleOwner === component) moduleOwner = instances.length ? instances[0] : null;
  }

  function openPalette(component) {
    var record = records.get(component);
    claimOverlay(component);
    component.open = true;
    if (component.activeIndex < 0 || matchingCommands(component).indexOf(allCommands(component)[component.activeIndex]) === -1) {
      component.activeIndex = firstIndex(component);
    }
    if (record) focusOwned(component, record.input);
    return true;
  }

  function closePalette(component, shouldRestore) {
    component.open = false;
    component.query = "";
    component.activeIndex = -1;
    overlay.release(component, shouldRestore);
    return false;
  }

  function move(component, direction) {
    var commands = matchingCommands(component);
    if (!commands.length) {
      component.activeIndex = -1;
      return;
    }
    var current = allCommands(component)[component.activeIndex];
    var position = commands.indexOf(current);
    if (position < 0) position = direction > 0 ? -1 : 0;
    position = (position + direction + commands.length) % commands.length;
    component.activeIndex = indexOf(component, commands[position]);
  }

  function focusables(panel) {
    return Array.prototype.slice.call(panel.querySelectorAll(
      'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'
    )).filter(function (element) {
      return element.tabIndex >= 0 && !element.hidden && !element.closest("[hidden]") && element.getAttribute("aria-hidden") !== "true";
    });
  }

  kit.component("command-palette", {
    open: false,
    query: "",
    activeIndex: -1,
    selected: "",

    get activeId() {
      var command = allCommands(this)[this.activeIndex];
      return command && command.id ? command.id : null;
    },

    get hasMatches() {
      return matchingCommands(this).length > 0;
    },

    init: function () {
      var component = this;
      var host = this.$host;
      var input = this.$refs.input || firstOwned(component, '[role="combobox"]');
      var panel = this.$refs.panel || firstOwned(component, '[role="dialog"]');
      if (!input || !panel) {
        throw new Error("component:command-palette requires data-kit-ref=\"input\" and data-kit-ref=\"panel\"");
      }
      records.set(component, { input: input, panel: panel });
      registerInstance(component);

      function onGlobalKeyDown(event) {
        if (moduleOwner !== component) return;
        if (event.defaultPrevented || event.altKey || event.shiftKey || (!event.metaKey && !event.ctrlKey)) return;
        if (String(event.key || "").toLocaleLowerCase() !== "k") return;
        event.preventDefault();
        component.toggle();
      }

      function onInput(event) {
        if (event.target === input) component.search(input.value);
      }

      function onClick(event) {
        var trigger = event.target.closest && event.target.closest("[data-command-trigger]");
        if (trigger && ownsNode(component, trigger)) {
          component.toggle();
          return;
        }
        var command = event.target.closest && event.target.closest("[data-command]");
        if (command && ownsNode(component, command) && !disabled(command)) {
          component.run(valueOf(command), labelOf(command), indexOf(component, command));
          return;
        }
        if (event.target.hasAttribute && event.target.hasAttribute("data-command-backdrop") && ownsNode(component, event.target)) {
          component.close();
        }
      }

      function onPointerDown(event) {
        var command = event.target.closest && event.target.closest("[data-command]");
        if (command && ownsNode(component, command) && !disabled(command)) event.preventDefault();
      }

      function onKeyDown(event) {
        if (!component.open || !isOverlayOwner(component)) return;
        if (event.key === "Escape") {
          event.preventDefault();
          component.close();
          return;
        }
        if (event.key === "Tab") {
          var elements = focusables(panel);
          if (!elements.length) {
            event.preventDefault();
            focusOwned(component, input);
          } else if (event.shiftKey && global.document.activeElement === elements[0]) {
            event.preventDefault();
            focusOwned(component, elements[elements.length - 1]);
          } else if (!event.shiftKey && global.document.activeElement === elements[elements.length - 1]) {
            event.preventDefault();
            focusOwned(component, elements[0]);
          }
          return;
        }
        if (event.target !== input || event.isComposing) return;
        if (event.key === "ArrowDown") {
          event.preventDefault();
          move(component, 1);
        } else if (event.key === "ArrowUp") {
          event.preventDefault();
          move(component, -1);
        } else if (event.key === "Home") {
          event.preventDefault();
          component.activeIndex = firstIndex(component);
        } else if (event.key === "End") {
          event.preventDefault();
          var commands = matchingCommands(component);
          component.activeIndex = commands.length ? indexOf(component, commands[commands.length - 1]) : -1;
        } else if (event.key === "Enter" && component.activeIndex >= 0) {
          var command = allCommands(component)[component.activeIndex];
          if (command && !disabled(command)) {
            event.preventDefault();
            component.run(valueOf(command), labelOf(command), component.activeIndex);
          }
        }
      }

      function onFocusIn(event) {
        if (component.open && isOverlayOwner(component) && !panel.contains(event.target)) {
          focusOwned(component, input);
        }
      }

      global.document.addEventListener("keydown", onGlobalKeyDown);
      global.document.addEventListener("focusin", onFocusIn);
      host.addEventListener("input", onInput);
      host.addEventListener("click", onClick);
      host.addEventListener("pointerdown", onPointerDown);
      host.addEventListener("keydown", onKeyDown);

      return function () {
        global.document.removeEventListener("keydown", onGlobalKeyDown);
        global.document.removeEventListener("focusin", onFocusIn);
        host.removeEventListener("input", onInput);
        host.removeEventListener("click", onClick);
        host.removeEventListener("pointerdown", onPointerDown);
        host.removeEventListener("keydown", onKeyDown);
        if (component.open || isOverlayOwner(component)) closePalette(component, true);
        unregisterInstance(component);
        records.delete(component);
      };
    },

    show: function () {
      return openPalette(this);
    },

    close: function () {
      return closePalette(this, true);
    },

    toggle: function () {
      return this.open ? closePalette(this, true) : openPalette(this);
    },

    search: function (value) {
      this.query = String(value === null || value === undefined ? "" : value);
      claimOverlay(this);
      this.open = true;
      this.activeIndex = firstIndex(this);
    },

    run: function (value, label, index) {
      this.selected = String(value === null || value === undefined ? "" : value);
      this.activeIndex = Number.isInteger(index) ? index : -1;
      this.$host.dispatchEvent(new CustomEvent("kit:command", {
        bubbles: true,
        detail: {
          value: this.selected,
          label: String(label === null || label === undefined ? this.selected : label)
        }
      }));
      closePalette(this, true);
      return this.selected;
    },

    matches: function (label, keywords) {
      var query = normalized(this.query);
      return !query || normalized(String(label || "") + " " + String(keywords || "")).indexOf(query) !== -1;
    },

    isActive: function (index) {
      return this.activeIndex === index;
    }
  });
})(globalThis, globalThis.kit);
