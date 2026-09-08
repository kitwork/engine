;(function () {
"use strict";

var revisions = new WeakMap();

function ignoredTarget(host, target) {
  if (!target || typeof target.closest !== "function") return false;
  var ignored = target.closest("[data-titlebar-no-drag],[data-kit-no-drag],[data-kitwork-no-drag]");
  return !!ignored && (ignored === host || host.contains(ignored));
}

function dragTarget(host, target) {
  if (!target || typeof target.closest !== "function") return false;
  var region = target.closest("[data-titlebar-drag]");
  return !!region && (region === host || host.contains(region));
}

function ignoreFailure(result) {
  if (result && typeof result.catch === "function") {
    result.catch(function () { /* Native title-bar gestures are best-effort. */ });
  }
}

function applyToggle(scope, revision, maximized) {
  if (revisions.get(scope) !== revision) return false;
  var next = !maximized;
  scope.maximized = next;

  var result;
  try {
    result = next ? kit.window.maximize() : kit.window.restore();
  } catch (error) {
    if (revisions.get(scope) === revision) scope.maximized = !next;
    return Promise.reject(error);
  }

  return Promise.resolve(result).then(
    function (value) {
      if (value !== true && revisions.get(scope) === revision) scope.maximized = maximized;
      return value;
    },
    function (error) {
      if (revisions.get(scope) === revision) scope.maximized = maximized;
      throw error;
    }
  );
}

function toggle(scope) {
  var revision = (revisions.get(scope) || 0) + 1;
  revisions.set(scope, revision);
  var fallback = scope.maximized === true;
  var result;
  try { result = kit.window.isMaximized(); }
  catch (_) { return applyToggle(scope, revision, fallback); }
  return Promise.resolve(result).then(
    function (maximized) { return applyToggle(scope, revision, maximized === true); },
    function () { return applyToggle(scope, revision, fallback); }
  );
}

function syncMaximized(scope) {
  var revision = revisions.get(scope) || 0;
  var result;
  try { result = kit.window.isMaximized(); }
  catch (_) { return; }
  Promise.resolve(result).then(
    function (maximized) {
      if (revisions.get(scope) === revision) scope.maximized = maximized === true;
    },
    function () { /* Keep the conservative local fallback. */ }
  );
}

function browserNavigation() {
  var owner;
  try { owner = globalThis.navigation; }
  catch (_) { return null; }
  if (!owner || typeof owner.addEventListener !== "function" ||
    typeof owner.removeEventListener !== "function") return null;
  return owner;
}

function syncNavigation(scope, owner) {
  var back = false;
  var forward = false;
  if (owner) {
    try {
      back = owner.canGoBack === true;
      forward = owner.canGoForward === true;
    } catch (_) { /* Unsupported or disabled Navigation API stays fail-closed. */ }
  }
  scope.canGoBack = back;
  scope.canGoForward = forward;
}

kit.component("desktop-titlebar", {
  maximized: false,
  canGoBack: false,
  canGoForward: false,

  init: function (context) {
    var scope = this;
    var host = context.host;
    var navigation = browserNavigation();
    revisions.set(scope, 0);
    syncMaximized(scope);
    syncNavigation(scope, navigation);

    if (navigation) {
      context.listen(navigation, "currententrychange", function () {
        syncNavigation(scope, navigation);
      });
    }

    context.listen(host, "mousedown", function (event) {
      if (!event || event.defaultPrevented || event.button !== 0 ||
        !dragTarget(host, event.target) || ignoredTarget(host, event.target)) return;
      if (typeof event.preventDefault === "function") event.preventDefault();
      ignoreFailure(kit.window.drag());
    });

    context.listen(host, "dblclick", function (event) {
      if (!event || event.defaultPrevented || event.button !== 0 ||
        !dragTarget(host, event.target) || ignoredTarget(host, event.target)) return;
      if (typeof event.preventDefault === "function") event.preventDefault();
      ignoreFailure(scope.toggleMaximize());
    });

    return function () {
      revisions.delete(scope);
      scope = null;
      navigation = null;
    };
  },

  back: function () {
    if (this.canGoBack !== true) return false;
    kit.navigation.back();
    return true;
  },

  forward: function () {
    if (this.canGoForward !== true) return false;
    kit.navigation.forward();
    return true;
  },

  reload: function () {
    kit.navigation.reload();
    return true;
  },

  minimize: function () {
    return kit.window.minimize();
  },

  toggleMaximize: function () {
    return toggle(this);
  },

  close: function () {
    return kit.window.close();
  }
});

})();
