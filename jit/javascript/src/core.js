; (function (global, document) {
  "use strict";

  var VERSION = "1.0.0-rc.2";
  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var INSTALL = Symbol.for("kitjs:runtime");
  var ownKit = Object.prototype.hasOwnProperty.call(global, "kit");
  var currentKit = global.kit;

  if (Object.prototype.hasOwnProperty.call(document, ASSEMBLY)) {
    throw new Error("KitJS: another assembly is already in progress");
  }
  if (currentKit && currentKit[INSTALL] === VERSION &&
    currentKit.version === VERSION && typeof currentKit.component === "function") {
    Object.defineProperty(document, ASSEMBLY, {
      value: { phase: "core", reuse: true },
      configurable: true
    });
    return;
  }
  if (ownKit || currentKit !== undefined) {
    throw new Error("KitJS: globalThis.kit is already owned by another script");
  }

  var OWN = Object.prototype.hasOwnProperty;
  function words(source) {
    var output = Object.create(null);
    source.split(" ").forEach(function (word) { if (word) output[word] = true; });
    return output;
  }
  var BLOCKED = words(
    "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
    "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
    "window globalThis top parent self caller callee arguments"
  );
  var FORBIDDEN = words(
    "var let const function class return if else for while do switch case new " +
    "delete void typeof instanceof in await yield throw try catch finally import export " +
    "this super with debugger of async document location navigator Function eval " +
    "undefined NaN Infinity"
  );
  var INVALID_MEMBER = {};
  var EXPRESSION_SOURCE_LIMIT = 65536;

  function syntax(message, source, position) {
    throw new SyntaxError("KitJS: " + message + " in \"" + source + "\" at " + position);
  }
  // report hands an error to the nearest error boundary — the closest ancestor carrying
  // data-kit-error (ideaship-final §2) — when the failing element is known; the boundary's action
  // runs with $error. An error nowhere near a boundary, or one raised while a boundary handles
  // another, reaches the console as before. Errors do not travel past the first boundary.
  var reporting = false;
  function report(error, element, directive) {
    var boundary = element && element.nodeType === 1 && element.closest ? element.closest("[data-kit-error]") : null;
    if (boundary && !reporting && core.handleError && !ignoredForRuntime(boundary)) {
      reporting = true;
      try {
        if (core.handleError(boundary, error, element, directive || "")) return;
      } catch (failure) {
        if (global.console && typeof global.console.error === "function") global.console.error(failure);
      } finally {
        reporting = false;
      }
    }
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  }
  function equal(left, right) {
    return left === right || left !== left && right !== right;
  }
  function blocked(value) {
    return typeof value === "string" && BLOCKED[value] === true;
  }
  function ignoredForRuntime(element) {
    while (element && element !== document) {
      if (element.nodeType === 1 && element.hasAttribute && element.hasAttribute("data-kit-ignore")) {
        return true;
      }
      element = element.parentElement;
    }
    return false;
  }
  function memberKey(value) {
    if (typeof value === "number") {
      if (!Number.isFinite(value)) return INVALID_MEMBER;
      value = String(value);
    } else if (typeof value !== "string") return INVALID_MEMBER;
    return blocked(value) ? INVALID_MEMBER : value;
  }
  function expressionSource(value) {
    var source = typeof value === "string" ? value : "";
    if (source.length > EXPRESSION_SOURCE_LIMIT) {
      throw new RangeError(
        "KitJS: expression source exceeds " + EXPRESSION_SOURCE_LIMIT + " UTF-16 code units"
      );
    }
    return source;
  }

  var core = {
    phase: "core",
    reuse: false,
    version: VERSION,
    install: INSTALL,
    assembly: ASSEMBLY,
    OWN: OWN,
    FORBIDDEN: FORBIDDEN,
    INVALID_MEMBER: INVALID_MEMBER,
    syntax: syntax,
    report: report,
    equal: equal,
    blocked: blocked,
    ignoredForRuntime: ignoredForRuntime,
    memberKey: memberKey,
    expressionSourceLimit: EXPRESSION_SOURCE_LIMIT,
    expressionSource: expressionSource,
    registry: new Map(),
    compiled: new Map(),
    scopes: new WeakMap(),
    scopeRecords: new WeakMap(),
    records: new WeakMap(),
    cacheLimit: 256,
    booted: false,
    dirtyAll: false,
    dirtyRecords: new Set(),
    queued: false,
    render: null,
    renderPending: null,
    startHooks: []
  };

  core.invalidate = function (record) {
    if (record && typeof record === "object") {
      if (record.disposed || core.renderPending && core.renderPending.has(record)) return;
      core.dirtyRecords.add(record);
    }
    else core.dirtyAll = true;
    if (core.queued) return;
    core.queued = true;
    queueMicrotask(function () {
      core.queued = false;
      if (!core.render || !core.dirtyAll && !core.dirtyRecords.size) return;
      var all = core.dirtyAll;
      var records = all ? null : Array.from(core.dirtyRecords);
      core.dirtyAll = false;
      core.dirtyRecords.clear();
      core.render(records);
    });
  };
  core.resetDirty = function () {
    core.dirtyAll = false;
    core.dirtyRecords.clear();
  };
  function activeObservationRecord(observation) {
    var owner = observation.owner;
    observation.owner = null;
    var record = owner;
    if (!record || !record.observations || !record.observations.delete(observation)) return null;
    if (typeof core.releaseObservationOwner === "function") core.releaseObservationOwner(record);
    var host = record.host;
    if (record.disposed || !host || host.ownerDocument !== document || !document.contains(host) ||
      ignoredForRuntime(host) ||
      (!host.hasAttribute("data-kit-component") && !host.hasAttribute("data-kit-scope")) ||
      core.scopes.get(host) !== record) return null;
    return record;
  }
  function attachObservation(value, then, observations) {
    var settled = false;
    function settle(error, rejected) {
      if (settled) return;
      settled = true;
      if (rejected) report(error);
      var records = [];
      observations.forEach(function (observation) {
        var record = activeObservationRecord(observation);
        if (record) records.push(record);
      });
      records.sort(function (left, right) {
        if (left.host === right.host) return 0;
        var position = left.host.compareDocumentPosition(right.host);
        if (position & 2) return 1;
        if (position & 4) return -1;
        return 0;
      });
      records.forEach(function (record) { core.invalidate(record); });
      observations.length = 0;
    }
    try {
      then.call(value, function () { settle(null, false); }, function (error) { settle(error, true); });
    } catch (error) {
      settle(error, true);
    }
  }
  core.observe = function (value, owners) {
    if (!value) return;
    var then;
    try { then = value.then; }
    catch (error) { report(error); return; }
    if (typeof then !== "function") return;
    if (!Array.isArray(owners)) owners = owners ? [owners] : [];
    var seen = new Set();
    var observations = [];
    owners.forEach(function (record) {
      if (!record || record.disposed || seen.has(record)) return;
      seen.add(record);
      var observation = { owner: record };
      if (!record.observations) record.observations = new Set();
      record.observations.add(observation);
      if (typeof core.retainObservationOwner === "function") core.retainObservationOwner(record);
      observations.push(observation);
    });
    attachObservation(value, then, observations);
  };
  core.cancelObservations = function (record) {
    if (!record || !record.observations) return;
    record.observations.forEach(function (observation) {
      observation.owner = null;
    });
    record.observations.clear();
    if (typeof core.releaseObservationOwner === "function") core.releaseObservationOwner(record);
  };

  Object.defineProperty(document, ASSEMBLY, {
    value: core,
    configurable: true
  });
})(globalThis, document);
