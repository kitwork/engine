;(function () {
"use strict";

var timers = new WeakMap();

function text(value) {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return "";
}

function resetDelay(value) {
  value = Number(value);
  if (!Number.isFinite(value) || value <= 0) return 1500;
  return Math.min(value, 60000);
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

kit.component("copy", {
  copied: false,
  text: "",
  delay: 1500,

  init: function (context) {
    var scope = this;
    // Convention (marker-free): when no text was seeded and no [data-copy-source]
    // is used, copy the boundary's own code block. This lets an authored
    // data-kit-click="copy()" work with zero marker attributes, because a method
    // cannot reach the DOM but init can. Explicit text or markers always win.
    if (!text(scope.text) && !context.owned("[data-copy-source]").length) {
      var block = context.host.querySelector("pre, code");
      if (block) scope.text = String(block.textContent || "");
    }
    // A [data-copy-trigger] click inside the boundary copies the seeded text or,
    // absent that, the boundary's [data-copy-source] element (e.g. a code block).
    // The listener runs with the captured scope, so it owns reactive state and
    // DOM ownership that an authored data-kit-click method could not.
    context.listen(context.host, "click", function (event) {
      var target = event.target;
      if (!target || typeof target.closest !== "function") return;
      var trigger = target.closest("[data-copy-trigger]");
      if (!trigger || context.owned("[data-copy-trigger]").indexOf(trigger) < 0) return;
      var payload = text(scope.text);
      if (!payload) {
        var owned = context.owned("[data-copy-source]");
        if (owned.length) payload = String(owned[0].textContent || "");
      }
      write(scope, payload);
    });
    return function () { clear(scope); };
  },

  copy: function (value) {
    return write(this, text(value === undefined ? this.text : value));
  },

  reset: function () {
    clear(this);
    this.copied = false;
    return false;
  }
});

})();
