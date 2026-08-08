"use strict";

function createTaskService(options) {
  options = options || {};
  var globalObject = options.globalObject || (typeof globalThis !== "undefined" ? globalThis : {});
  var AbortControllerCtor = globalObject.AbortController || (typeof AbortController !== "undefined" ? AbortController : null);
  var DOMExceptionCtor = globalObject.DOMException || (typeof DOMException !== "undefined" ? DOMException : null);
  var owners = new WeakMap();

  function abortError(reason) {
    if (DOMExceptionCtor) return new DOMExceptionCtor(reason || "Aborted", "AbortError");
    var error = new Error(reason || "Aborted");
    error.name = "AbortError";
    return error;
  }

  function ownerMap(owner) {
    if (!owner || (typeof owner !== "object" && typeof owner !== "function")) {
      throw new TypeError("kit.task owner must be an object");
    }
    var map = owners.get(owner);
    if (!map) {
      map = new Map();
      owners.set(owner, map);
    }
    return map;
  }

  function abortRecord(record, reason) {
    if (!record || record.done) return false;
    record.aborted = true;
    try { record.controller.abort(abortError(reason || "aborted")); } catch (_) { /* noop */ }
    return true;
  }

  function run(owner, task, taskOptions) {
    taskOptions = taskOptions || {};
    if (!AbortControllerCtor) throw new Error("AbortController is required by kit.task");

    var key = taskOptions.key !== undefined ? taskOptions.key : Symbol("task");
    var map = ownerMap(owner);
    if (taskOptions.latest && map.has(key)) abortRecord(map.get(key), "superseded");

    var controller = new AbortControllerCtor();
    var record = {
      owner: owner,
      key: key,
      controller: controller,
      done: false,
      aborted: false,
      promise: null
    };
    map.set(key, record);

    var result;
    try {
      result = typeof task === "function"
        ? task({ signal: controller.signal, key: key, owner: owner })
        : task;
    } catch (error) {
      map.delete(key);
      record.done = true;
      throw error;
    }

    var promise = Promise.resolve(result).finally(function () {
      record.done = true;
      if (map.get(key) === record) map.delete(key);
    });
    record.promise = promise;
    if (typeof options.onTask === "function") options.onTask(record);
    return promise;
  }

  function latest(owner, key, task) {
    return run(owner, task, { key: key, latest: true });
  }

  function abort(owner, key, reason) {
    var map = owners.get(owner);
    if (!map) return false;

    // Passing `undefined` as the key intentionally aborts every task owned by
    // the component/application. This keeps lifecycle code explicit without
    // requiring a second public method.
    if (key !== undefined) {
      var record = map.get(key);
      if (!record) return false;
      var changed = abortRecord(record, reason || "aborted");
      map.delete(key);
      return changed;
    }

    var aborted = false;
    map.forEach(function (record) {
      aborted = abortRecord(record, reason || "aborted") || aborted;
    });
    map.clear();
    return aborted;
  }

  function delay(ms, delayOptions) {
    delayOptions = delayOptions || {};
    return new Promise(function (resolve, reject) {
      var timer = setTimeout(resolve, Math.max(0, Number(ms) || 0));
      var signal = delayOptions.signal;
      if (!signal) return;
      if (signal.aborted) {
        clearTimeout(timer);
        reject(signal.reason instanceof Error ? signal.reason : abortError(signal.reason || "Aborted"));
        return;
      }
      signal.addEventListener("abort", function () {
        clearTimeout(timer);
        reject(signal.reason instanceof Error ? signal.reason : abortError(signal.reason || "Aborted"));
      }, { once: true });
    });
  }

  function pending(owner) {
    var map = owners.get(owner);
    return map ? map.size : 0;
  }

  return Object.freeze({
    run: run,
    latest: latest,
    abort: abort,
    delay: delay,
    pending: pending
  });
}

module.exports = { createTaskService: createTaskService };
