// KitJS component: toast@1.0.0
;(function (global, kit) {
  "use strict";

  var privateState = new WeakMap();

  function stateFor(instance) {
    var value = privateState.get(instance);
    if (!value) {
      value = { timer: null };
      privateState.set(instance, value);
    }
    return value;
  }

  function clearTimer(instance) {
    var value = stateFor(instance);
    if (!value.timer) return;
    global.clearTimeout(value.timer);
    value.timer = null;
  }

  function toneOf(value) {
    value = String(value || "info").toLowerCase();
    return value === "success" || value === "warning" || value === "error" ? value : "info";
  }

  function durationOf(value) {
    if (value === undefined || value === null || value === "") return 4000;
    value = Number(value);
    if (!Number.isFinite(value) || value < 0) return 4000;
    if (value === 0) return 0;
    return Math.min(value, 2147483647);
  }

  kit.component("toast", {
    visible: false,
    message: "",
    tone: "info",
    duration: 4000,

    init: function () {
      var instance = this;
      stateFor(instance);
      return function () {
        clearTimer(instance);
        privateState.delete(instance);
      };
    },

    show: function (message, tone, duration) {
      message = message === undefined || message === null ? "Notification" : String(message).trim();
      if (!message) message = "Notification";
      tone = toneOf(tone);
      duration = durationOf(duration);

      clearTimer(this);
      this.message = message;
      this.tone = tone;
      this.duration = duration;
      this.visible = true;

      if (duration > 0) {
        var instance = this;
        stateFor(instance).timer = global.setTimeout(function () {
          var details = stateFor(instance);
          details.timer = null;
          instance.visible = false;
        }, duration);
      }

      return tone === "error"
        ? kit.announce.assertive(message)
        : kit.announce.polite(message);
    },

    close: function () {
      clearTimer(this);
      this.visible = false;
      return false;
    }
  });
})(globalThis, globalThis.kit);
