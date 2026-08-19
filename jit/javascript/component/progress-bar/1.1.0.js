;(function () {
"use strict";

var progressPrivate = new WeakMap();

function finite(value) {
  value = Number(value);
  return Number.isFinite(value) ? value : null;
}

function visitID(detail) {
  if (!detail || detail.id === null || detail.id === undefined) return "";
  if (typeof detail.id !== "string" && typeof detail.id !== "number") return "";
  return String(detail.id);
}

function clearHide(state) {
  if (!state || !state.hideTimer) return;
  clearTimeout(state.hideTimer);
  state.hideTimer = 0;
}

function resetScope(scope) {
  scope.value = 0;
  scope.loaded = 0;
  scope.total = null;
  scope.status = "idle";
  scope.message = "Ready";
}

function applyNavigation(scope, detail) {
  var id = visitID(detail);
  var phase = detail && String(detail.phase || "");
  if (!id || !phase) return;

  var state = progressPrivate.get(scope);
  if (!state) return;

  if (phase === "start") {
    clearHide(state);
    state.id = id;
    scope.value = 0;
    scope.loaded = 0;
    scope.total = null;
    scope.status = "loading";
    scope.message = "Loading page";
    return;
  }

  if (state.id !== id) return;

  if (phase === "progress") {
    var loaded = finite(detail.loaded);
    var total = finite(detail.total);
    if (loaded === null || loaded < 0 || total === null || total <= 0) {
      scope.loaded = 0;
      scope.total = null;
      scope.value = 0;
      scope.message = "Loading page";
      return;
    }
    scope.loaded = Math.min(loaded, total);
    scope.total = total;
    scope.value = Math.min(99, Math.max(0, Math.floor(scope.loaded / total * 100)));
    scope.message = "Loading page - " + scope.value + "%";
    return;
  }

  if (phase !== "finish") return;
  if (detail.outcome === "loaded") {
    state.id = "";
    scope.value = 100;
    scope.loaded = 0;
    scope.total = null;
    scope.status = "completed";
    scope.message = "Page ready";
    state.hideTimer = setTimeout(function () {
      state.hideTimer = 0;
      resetScope(scope);
    }, 300);
  } else if (detail.outcome === "cancelled") {
    state.id = "";
    resetScope(scope);
  } else if (detail.outcome === "error") {
    state.id = "";
    scope.status = "failed";
    scope.message = "Navigation failed";
  } else if (detail.outcome === "fallback") {
    state.id = "";
    scope.status = "loading";
    scope.message = "Opening with the browser";
  }
}

kit.component("progress-bar", {
  value: 0,
  loaded: 0,
  total: null,
  status: "idle",
  message: "Ready",

  get hidden() {
    return this.status === "idle";
  },

  get width() {
    return this.value + "%";
  },

  get visible() {
    return this.status !== "idle";
  },

  get active() {
    return this.status === "loading" || this.status === "running";
  },

  get determinate() {
    return this.total !== null || this.status === "completed";
  },

  get indeterminate() {
    return this.status === "loading" && !this.determinate;
  },

  get valueText() {
    if (this.status === "completed") return "Page loaded";
    if (this.status === "failed") return this.message;
    if (this.determinate) return this.value + "% loaded";
    return "Loading page";
  },

  start: function () {
    var state = progressPrivate.get(this);
    clearHide(state);
    if (state) state.id = "";
    this.value = 0;
    this.loaded = 0;
    this.total = 100;
    this.status = "running";
    this.message = "Running";
  },

  set: function (value) {
    var state = progressPrivate.get(this);
    clearHide(state);
    value = Math.min(100, Math.max(0, Number(value) || 0));
    this.value = value;
    this.loaded = value;
    this.total = 100;
    this.status = value >= 100 ? "completed" : "running";
    this.message = value >= 100 ? "Completed" : "Running - " + value + "%";
    return value;
  },

  inc: function (amount) {
    amount = amount === null || amount === undefined ? 10 : Number(amount);
    return this.set(this.value + (Number.isFinite(amount) ? amount : 0));
  },

  done: function () {
    return this.set(100);
  },

  reset: function () {
    var state = progressPrivate.get(this);
    clearHide(state);
    if (state) state.id = "";
    resetScope(this);
  },

  init: function () {
    var scope = this;
    var listener = function (event) {
      applyNavigation(scope, event && event.detail);
    };
    progressPrivate.set(scope, { id: "", hideTimer: 0 });
    document.addEventListener("kit:navigation", listener);
    return function () {
      document.removeEventListener("kit:navigation", listener);
      var state = progressPrivate.get(scope);
      clearHide(state);
      progressPrivate.delete(scope);
    };
  }
});

})();
