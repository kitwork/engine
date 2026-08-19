;(function () {
"use strict";

var progressPrivate = new WeakMap();
var manualSequence = 0;

function finite(value) {
  value = Number(value);
  return Number.isFinite(value) ? value : null;
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
  scope.source = "";
}

function startManual(scope) {
  var state = progressPrivate.get(scope);
  if (!state) return "";
  manualSequence += 1;
  state.manualID = "progress-bar:" + manualSequence;
  kit.progress.start(state.manualID, { source: "component", total: 100 });
  return state.manualID;
}

function activeManual(scope) {
  var state = progressPrivate.get(scope);
  if (!state) return "";
  var snapshot = kit.progress.snapshot();
  if (!state.manualID || snapshot.id !== state.manualID ||
    snapshot.phase === "finish") {
    return startManual(scope);
  }
  return state.manualID;
}

function ownedManual(scope) {
  var state = progressPrivate.get(scope);
  if (!state || !state.manualID) return "";
  var snapshot = kit.progress.snapshot();
  if (snapshot.id !== state.manualID || snapshot.phase === "finish" ||
    snapshot.phase === "idle") {
    state.manualID = "";
    return "";
  }
  return state.manualID;
}

function applyProgress(scope, snapshot) {
  if (!snapshot) return;

  var state = progressPrivate.get(scope);
  if (!state) return;

  var phase = String(snapshot.phase || "");
  var componentSource = snapshot.source === "component";
  var navigationSource = snapshot.source === "navigation";
  if (phase === "idle" || snapshot.id !== state.manualID) state.manualID = "";
  if (phase === "idle") {
    clearHide(state);
    resetScope(scope);
    return;
  }

  if (phase === "start") {
    clearHide(state);
    var startTotal = finite(snapshot.total);
    scope.source = snapshot.source;
    scope.value = 0;
    scope.loaded = 0;
    scope.total = startTotal !== null && startTotal > 0 ? startTotal : null;
    scope.status = componentSource ? "running" : "loading";
    scope.message = componentSource ? "Running" :
      navigationSource ? "Loading page" : "Working";
    return;
  }

  if (phase === "progress") {
    clearHide(state);
    scope.source = snapshot.source;
    var loaded = finite(snapshot.loaded);
    var total = finite(snapshot.total);
    if (loaded === null || loaded < 0 || total === null || total <= 0) {
      scope.loaded = 0;
      scope.total = null;
      scope.value = 0;
      scope.status = "loading";
      scope.message = navigationSource ? "Loading page" : "Working";
      return;
    }
    scope.loaded = Math.min(loaded, total);
    scope.total = total;
    scope.value = Math.min(componentSource ? 100 : 99,
      Math.max(0, Math.floor(scope.loaded / total * 100)));
    scope.status = componentSource && scope.value >= 100 ? "completed" :
      componentSource ? "running" : "loading";
    scope.message = componentSource && scope.value >= 100 ? "Completed" :
      componentSource ? "Running - " + scope.value + "%" :
        navigationSource ? "Loading page - " + scope.value + "%" :
          "Working - " + scope.value + "%";
    return;
  }

  if (phase !== "finish") return;
  clearHide(state);
  if (snapshot.outcome === "loaded") {
    var finishLoaded = finite(snapshot.loaded);
    var finishTotal = finite(snapshot.total);
    scope.source = snapshot.source;
    scope.value = 100;
    scope.loaded = finishLoaded !== null && finishLoaded >= 0 &&
      finishTotal !== null && finishTotal > 0 ? Math.min(finishLoaded, finishTotal) : 0;
    scope.total = finishTotal !== null && finishTotal > 0 ? finishTotal : null;
    scope.status = "completed";
    scope.message = navigationSource ? "Page ready" : "Completed";
    state.manualID = "";
    if (componentSource) return;
    state.hideTimer = setTimeout(function () {
      state.hideTimer = 0;
      resetScope(scope);
    }, 300);
  } else if (snapshot.outcome === "cancelled") {
    state.manualID = "";
    resetScope(scope);
  } else if (snapshot.outcome === "error") {
    scope.source = snapshot.source;
    scope.status = "failed";
    scope.message = navigationSource ? "Navigation failed" : "Progress failed";
  } else if (snapshot.outcome === "fallback") {
    scope.source = snapshot.source;
    scope.status = "loading";
    scope.message = navigationSource ? "Opening with the browser" : "Continuing";
  }
}

kit.component("progress-bar", {
  value: 0,
  loaded: 0,
  total: null,
  status: "idle",
  message: "Ready",
  source: "",

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
    if (this.status === "completed") {
      return this.source === "navigation" ? "Page loaded" : "Completed";
    }
    if (this.status === "failed") return this.message;
    if (this.determinate) return this.value + "% loaded";
    return this.source === "navigation" ? "Loading page" : "Working";
  },

  start: function () {
    startManual(this);
  },

  set: function (value) {
    value = Math.min(100, Math.max(0, Number(value) || 0));
    var id = activeManual(this);
    if (id) kit.progress.update(id, value, 100);
    return value;
  },

  inc: function (amount) {
    amount = amount === null || amount === undefined ? 10 : Number(amount);
    return this.set(this.value + (Number.isFinite(amount) ? amount : 0));
  },

  done: function () {
    var id = activeManual(this);
    if (id) kit.progress.finish(id, "loaded");
    return 100;
  },

  reset: function () {
    var id = ownedManual(this);
    if (id) {
      kit.progress.finish(id, "cancelled");
      return;
    }
    clearHide(progressPrivate.get(this));
    resetScope(this);
  },

  init: function () {
    var scope = this;
    var state = { hideTimer: 0, manualID: "", unsubscribe: null };
    progressPrivate.set(scope, state);
    state.unsubscribe = kit.progress.subscribe(function (snapshot) {
      applyProgress(scope, snapshot);
    });
    return function () {
      var current = progressPrivate.get(scope);
      if (!current) return;
      clearHide(current);
      try {
        if (current.unsubscribe) current.unsubscribe();
      } finally {
        progressPrivate.delete(scope);
      }
    };
  }
});

})();
