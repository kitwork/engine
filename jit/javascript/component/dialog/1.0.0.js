// KitJS component: dialog@1.0.0
;(function (global, kit) {
  "use strict";

  var overlay = kit.__kitwork_core__.overlay;
  var FOCUSABLE =
    "a[href],button:not([disabled]),input:not([disabled]),select:not([disabled])," +
    "textarea:not([disabled]),[tabindex]:not([tabindex='-1'])";

  function enqueue(callback) {
    if (typeof global.queueMicrotask === "function") global.queueMicrotask(callback);
    else Promise.resolve().then(callback);
  }

  function safeFocus(element) {
    if (!element || element.isConnected === false || typeof element.focus !== "function") return;
    try { element.focus({ preventScroll: true }); }
    catch (_) { try { element.focus(); } catch (_) {} }
  }

  function focusable(host) {
    if (!host || host.isConnected === false) return [];
    return Array.prototype.filter.call(host.querySelectorAll(FOCUSABLE), function (element) {
      return element.tabIndex >= 0 && !element.hidden && !element.closest("[hidden]");
    });
  }

  function focusDialog(instance) {
    if (!overlay.isOwner(instance) || !instance.open) return;
    var host = instance.$host;
    if (!host || host.isConnected === false) return;
    var panel = instance.$refs.panel || host;
    var items = focusable(host);
    safeFocus(items[0] || panel);
  }

  function containFocus(instance, event) {
    if (!overlay.isOwner(instance) || !instance.open) return;
    var host = instance.$host;
    if (!host || host.isConnected === false || host.contains(event.target)) return;
    enqueue(function () { focusDialog(instance); });
  }

  function trapFocus(instance, event) {
    if (!overlay.isOwner(instance) || !instance.open) return;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      instance.close();
      return;
    }
    if (event.key !== "Tab") return;

    var host = instance.$host;
    var items = focusable(host);
    if (!items.length) {
      event.preventDefault();
      safeFocus(instance.$refs.panel || host);
      return;
    }

    var first = items[0];
    var last = items[items.length - 1];
    var current = global.document.activeElement;
    if (event.shiftKey && (current === first || !host.contains(current))) {
      event.preventDefault();
      safeFocus(last);
    } else if (!event.shiftKey && (current === last || !host.contains(current))) {
      event.preventDefault();
      safeFocus(first);
    }
  }

  kit.component("dialog", {
    open: false,
    title: "Dialog",
    message: "",

    init: function () {
      var instance = this;
      var onKeydown = function (event) { trapFocus(instance, event); };
      var onFocusin = function (event) { containFocus(instance, event); };
      global.document.addEventListener("keydown", onKeydown, true);
      global.document.addEventListener("focusin", onFocusin, true);
      return function () {
        global.document.removeEventListener("keydown", onKeydown, true);
        global.document.removeEventListener("focusin", onFocusin, true);
        overlay.release(instance, true);
      };
    },

    show: function (title, message) {
      if (title !== undefined && title !== null) this.title = String(title);
      if (message !== undefined && message !== null) this.message = String(message);
      var instance = this;
      overlay.claim(instance, function (restoreFocus) { return instance.close(restoreFocus); });
      this.open = true;
      enqueue(function () { focusDialog(instance); });
      return true;
    },

    close: function (restoreFocus) {
      this.open = false;
      overlay.release(this, restoreFocus);
      return false;
    },

    toggle: function () {
      return this.open ? this.close() : this.show();
    }
  });
})(globalThis, globalThis.kit);
