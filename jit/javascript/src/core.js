; (function (global, document) {
  "use strict";

  var VERSION = "0.9.0-next.12";
  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var INSTALL = Symbol.for("kitjs:runtime");
  var ownKit = Object.prototype.hasOwnProperty.call(global, "kit");
  var currentKit = global.kit;
  var nextObservation = 0;

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

  function syntax(message, source, position) {
    throw new SyntaxError("KitJS: " + message + " in \"" + source + "\" at " + position);
  }
  function report(error) {
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
  function attachObservation(value, then, tickets) {
    var settled = false;
    function settle(error, rejected) {
      if (settled) return;
      settled = true;
      if (rejected) report(error);
      var waiting = new Set(tickets);
      document.querySelectorAll("[data-kit-component],[data-kit-scope]").forEach(function (element) {
        if (ignoredForRuntime(element)) return;
        var record = core.scopes.get(element);
        if (!record || record.disposed || !record.observations) return;
        tickets.forEach(function (ticket) {
          if (!waiting.has(ticket) || !record.observations.delete(ticket)) return;
          waiting.delete(ticket);
          core.invalidate(record);
        });
      });
      tickets.length = 0;
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
    var tickets = [];
    owners.forEach(function (record) {
      if (!record || record.disposed || seen.has(record)) return;
      seen.add(record);
      var ticket = ++nextObservation;
      if (!record.observations) record.observations = new Set();
      record.observations.add(ticket);
      tickets.push(ticket);
    });
    attachObservation(value, then, tickets);
  };

  Object.defineProperty(document, ASSEMBLY, {
    value: core,
    configurable: true
  });
})(globalThis, document);
