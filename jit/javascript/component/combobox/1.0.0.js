// KitJS component: combobox@1.0.0
// Custom ARIA combobox with filtering, active-descendant navigation and cleanup.

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

  function allOptions(component) {
    var host = component.$host;
    if (!host) return [];
    return Array.prototype.slice.call(host.querySelectorAll('[role="option"]')).filter(function (option) {
      return ownsNode(component, option);
    });
  }

  function disabled(option) {
    return option.disabled || option.getAttribute("aria-disabled") === "true";
  }

  function labelOf(option) {
    return String(option.getAttribute("data-label") || option.textContent || "").trim();
  }

  function valueOf(option) {
    var value = option.getAttribute("data-value");
    return value === null ? labelOf(option) : value;
  }

  function normalized(value) {
    return String(value === null || value === undefined ? "" : value).trim().toLocaleLowerCase();
  }

  function matchingOptions(component) {
    var query = normalized(component.query);
    return allOptions(component).filter(function (option) {
      return !disabled(option) && (!query || normalized(labelOf(option)).indexOf(query) !== -1);
    });
  }

  function indexOf(component, option) {
    return allOptions(component).indexOf(option);
  }

  function firstIndex(component) {
    var options = matchingOptions(component);
    return options.length ? indexOf(component, options[0]) : -1;
  }

  function move(component, direction) {
    var options = matchingOptions(component);
    component.open = true;
    if (!options.length) {
      component.activeIndex = -1;
      return;
    }
    var current = allOptions(component)[component.activeIndex];
    var position = options.indexOf(current);
    if (position < 0) position = direction > 0 ? -1 : 0;
    position = (position + direction + options.length) % options.length;
    component.activeIndex = indexOf(component, options[position]);
  }

  function focusInput(component) {
    var record = records.get(component);
    var input = record && record.input;
    if (!input || !input.isConnected) return;
    Promise.resolve().then(function () {
      if (!input.isConnected) return;
      try { input.focus({ preventScroll: true }); } catch (_) { input.focus(); }
    });
  }

  kit.component("combobox", {
    query: "",
    value: "",
    open: false,
    activeIndex: -1,

    get activeId() {
      var option = allOptions(this)[this.activeIndex];
      return option && option.id ? option.id : null;
    },

    get hasMatches() {
      return matchingOptions(this).length > 0;
    },

    init: function () {
      var component = this;
      var host = this.$host;
      var input = this.$refs.input || firstOwned(component, '[role="combobox"]');
      if (!input) throw new Error("component:combobox requires data-kit-ref=\"input\"");

      records.set(component, { input: input });

      function onInput(event) {
        if (event.target === input) component.search(input.value);
      }

      function onFocusIn(event) {
        if (event.target === input) component.show();
      }

      function onKeyDown(event) {
        if (event.target !== input || event.isComposing) return;
        if (event.key === "ArrowDown") {
          event.preventDefault();
          move(component, 1);
        } else if (event.key === "ArrowUp") {
          event.preventDefault();
          move(component, -1);
        } else if (event.key === "Enter" && component.open && component.activeIndex >= 0) {
          var option = allOptions(component)[component.activeIndex];
          if (option && !disabled(option)) {
            event.preventDefault();
            component.choose(valueOf(option), labelOf(option), component.activeIndex);
          }
        } else if (event.key === "Escape" && component.open) {
          event.preventDefault();
          component.close();
        } else if (event.key === "Tab") {
          component.close();
        }
      }

      function onPointerDown(event) {
        var option = event.target.closest && event.target.closest('[role="option"]');
        if (option && ownsNode(component, option) && !disabled(option)) event.preventDefault();
      }

      function onClick(event) {
        var option = event.target.closest && event.target.closest('[role="option"]');
        if (!option || !ownsNode(component, option) || disabled(option)) return;
        component.choose(valueOf(option), labelOf(option), indexOf(component, option));
      }

      function onOutsidePointerDown(event) {
        if (component.open && !host.contains(event.target)) component.close();
      }

      host.addEventListener("input", onInput);
      host.addEventListener("focusin", onFocusIn);
      host.addEventListener("keydown", onKeyDown);
      host.addEventListener("pointerdown", onPointerDown);
      host.addEventListener("click", onClick);
      global.document.addEventListener("pointerdown", onOutsidePointerDown);

      return function () {
        host.removeEventListener("input", onInput);
        host.removeEventListener("focusin", onFocusIn);
        host.removeEventListener("keydown", onKeyDown);
        host.removeEventListener("pointerdown", onPointerDown);
        host.removeEventListener("click", onClick);
        global.document.removeEventListener("pointerdown", onOutsidePointerDown);
        records.delete(component);
      };
    },

    search: function (value) {
      this.query = String(value === null || value === undefined ? "" : value);
      this.open = true;
      this.activeIndex = firstIndex(this);
    },

    show: function () {
      this.open = true;
      if (this.activeIndex < 0 || matchingOptions(this).indexOf(allOptions(this)[this.activeIndex]) === -1) {
        this.activeIndex = firstIndex(this);
      }
    },

    close: function () {
      this.open = false;
      this.activeIndex = -1;
    },

    clear: function () {
      this.query = "";
      this.value = "";
      this.open = true;
      this.activeIndex = firstIndex(this);
      focusInput(this);
    },

    choose: function (value, label, index) {
      this.value = String(value === null || value === undefined ? "" : value);
      this.query = String(label === null || label === undefined ? this.value : label);
      this.activeIndex = Number.isInteger(index) ? index : -1;
      this.open = false;
      this.$host.dispatchEvent(new CustomEvent("kit:combobox-select", {
        bubbles: true,
        detail: { value: this.value, label: this.query }
      }));
      focusInput(this);
      return this.value;
    },

    matches: function (label) {
      var query = normalized(this.query);
      return !query || normalized(label).indexOf(query) !== -1;
    },

    isActive: function (index) {
      return this.activeIndex === index;
    },

    isSelected: function (value) {
      return this.value === String(value === null || value === undefined ? "" : value);
    }
  });
})(globalThis, globalThis.kit);
