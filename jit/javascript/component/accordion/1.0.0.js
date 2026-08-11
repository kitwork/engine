// KitJS component: accordion@1.0.0
;(function (kit) {
  "use strict";

  function triggers(host) {
    return Array.prototype.slice.call(host.querySelectorAll("[data-accordion-trigger]")).filter(function (trigger) {
      return trigger.closest("[data-kit-component]") === host &&
        !trigger.disabled && trigger.getAttribute("aria-disabled") !== "true";
    });
  }

  kit.component("accordion", {
    activeItem: null,

    toggle(item) {
      item = String(item || "");
      if (!item) return this.activeItem;
      this.activeItem = this.activeItem === item ? null : item;
      return this.activeItem;
    },

    open(item) {
      item = String(item || "");
      if (item) this.activeItem = item;
      return this.activeItem;
    },

    close(item) {
      if (item === undefined || this.activeItem === String(item || "")) this.activeItem = null;
      return this.activeItem;
    },

    isOpen(item) {
      return this.activeItem === String(item || "");
    },

    init() {
      var host = this.$host;

      function onKeydown(event) {
        var trigger = event.target.closest && event.target.closest("[data-accordion-trigger]");
        if (!trigger || !host.contains(trigger) || trigger.closest("[data-kit-component]") !== host) return;

        var items = triggers(host);
        var index = items.indexOf(trigger);
        var next = -1;
        if (event.key === "ArrowDown") next = (index + 1) % items.length;
        else if (event.key === "ArrowUp") next = (index - 1 + items.length) % items.length;
        else if (event.key === "Home") next = 0;
        else if (event.key === "End") next = items.length - 1;
        if (next < 0 || !items[next]) return;

        event.preventDefault();
        items[next].focus();
      }

      host.addEventListener("keydown", onKeydown);
      return function () {
        host.removeEventListener("keydown", onKeydown);
      };
    }
  });
})(globalThis.kit);
