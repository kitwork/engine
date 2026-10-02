;(function (global, kit) {
"use strict";

// KitJS service: network@1.1.0
var listeners = new Set();
var deliveries = [];
var delivering = false;
var attached = false;
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;

function readOnline() {
  try {
    var navigator = global.navigator;
    return !navigator || navigator.onLine !== false;
  } catch (_) {
    return true;
  }
}

function freeze(online) {
  return Object.freeze({ online: online === true });
}

var current = freeze(readOnline());

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") {
      global.console.error(error);
    }
  } catch (_) { /* Reporting must not break another subscriber. */ }
}

function deliver(subscription, value) {
  if (!subscription.listener) return;
  try { subscription.listener(value); }
  catch (error) { report(error); }
}

function publish(online) {
  if (current.online === online) return current;
  current = freeze(online);
  deliveries.push({
    value: current,
    subscriptions: Array.from(listeners)
  });
  if (delivering) return current;

  delivering = true;
  try {
    var index = 0;
    while (index < deliveries.length) {
      var delivery = deliveries[index];
      deliveries[index] = null;
      index++;
      delivery.subscriptions.forEach(function (subscription) {
        deliver(subscription, delivery.value);
      });
    }
  } finally {
    deliveries.length = 0;
    delivering = false;
  }
  return current;
}

function change() {
  publish(readOnline());
}

function attach() {
  if (attached) return;
  if (typeof global.addEventListener !== "function" ||
    typeof global.removeEventListener !== "function") {
    throw new TypeError("Network subscriptions require browser event listeners");
  }
  global.addEventListener("online", change);
  try {
    global.addEventListener("offline", change);
  } catch (error) {
    try { global.removeEventListener("online", change); }
    catch (_) { /* The original attachment error is authoritative. */ }
    throw error;
  }
  attached = true;
}

function detach() {
  if (!attached) return;
  attached = false;
  try { global.removeEventListener("online", change); }
  catch (error) { report(error); }
  try { global.removeEventListener("offline", change); }
  catch (error) { report(error); }
}

function snapshot() {
  if (!attached) {
    var online = readOnline();
    if (current.online !== online) current = freeze(online);
  }
  return current;
}

function subscribe(listener) {
  if (typeof listener !== "function") {
    throw new TypeError("Network subscriber must be a function");
  }
  var subscription = { listener: listener };
  listeners.add(subscription);
  if (listeners.size === 1) {
    try {
      attach();
      var online = readOnline();
      if (current.online !== online) current = freeze(online);
    } catch (error) {
      listeners.delete(subscription);
      subscription.listener = null;
      listener = null;
      throw error;
    }
  }
  deliver(subscription, current);

  var subscribed = true;
  return function () {
    if (!subscribed) return;
    subscribed = false;
    listeners.delete(subscription);
    subscription.listener = null;
    listener = null;
    if (listeners.size === 0) detach();
  };
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host errors never escape this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  return "FAILED";
}

function networkError(code) {
  var messages = {
    DENIED: "Network status permission was denied",
    CANCELLED: "Network status query was cancelled",
    UNAVAILABLE: "Network status is unavailable",
    TIMEOUT: "Network status query timed out",
    OVERLOADED: "Network status is busy",
    FAILED: "Network status query failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitNetworkError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: "status", enumerable: true }
  });
  return Object.freeze(error);
}

function status() {
  if (arguments.length !== 0) throw new TypeError("network.status does not accept parameters");
  if (!nativeHost) return Promise.resolve(snapshot());
  try {
    return Promise.resolve(nativeHost.call("network.status", {})).then(
      function (value) {
        var prototype = value && Object.getPrototypeOf(value);
        if (!value || prototype !== Object.prototype && prototype !== null ||
          Object.getOwnPropertySymbols(value).length || Object.keys(value).join(",") !== "online" ||
          typeof value.online !== "boolean") {
          throw networkError("FAILED");
        }
        return publish(value.online);
      },
      function (error) { throw networkError(errorCode(error)); }
    );
  } catch (error) {
    return Promise.reject(networkError(errorCode(error)));
  }
}

// Text HTTP and FileRef upload share a private operation runner. The files
// package consumes the upload boundary during exact graph installation.
var HTTP_KEYS = Object.freeze({ url: true, method: true, headers: true, body: true, timeoutMS: true, signal: true });
var UPLOAD_KEYS = Object.freeze({ url: true, method: true, headers: true, timeoutMS: true, signal: true });
var UPLOAD_BOUNDARY = Symbol.for("kitjs:network:upload:v1");
var HTTP_OPERATION = /^[A-Za-z0-9_-]{32}$/;
var setTimer = typeof global.setTimeout === "function" && global.setTimeout.bind(global);
var clearTimer = typeof global.clearTimeout === "function" && global.clearTimeout.bind(global);
var signalGetter = global.AbortSignal && Object.getOwnPropertyDescriptor(global.AbortSignal.prototype, "aborted").get;
var addSignalEvent = global.EventTarget && Function.prototype.call.bind(global.EventTarget.prototype.addEventListener);
var removeSignalEvent = global.EventTarget && Function.prototype.call.bind(global.EventTarget.prototype.removeEventListener);
var activeRequests = 0;

function httpError(value) {
  var code = "FAILED";
  try { code = typeof value === "string" ? value : value && value.code; }
  catch (_) { /* Host errors cannot expose accessors or private messages. */ }
  var messages = {
    INVALID_ARGUMENT: "Invalid HTTPS request", ORIGIN_DENIED: "HTTPS origin is not granted",
    ADDRESS_DENIED: "HTTPS destination is not public", LIMIT_EXCEEDED: "HTTPS transfer exceeds its limit",
    BUSY: "Too many HTTPS requests", CANCELLED: "HTTPS request cancelled", TIMEOUT: "HTTPS request timed out",
    CLOSED: "HTTPS client is closed", NETWORK_ERROR: "HTTPS request failed", REDIRECT_DENIED: "HTTPS redirect denied",
    INVALID_RESPONSE: "Invalid HTTPS response", DENIED: "HTTPS permission denied", UNAVAILABLE: "Native HTTPS is unavailable",
    OVERLOADED: "Native HTTPS is busy", FAILED: "HTTPS request failed"
  };
  if (!Object.prototype.hasOwnProperty.call(messages, code)) code = "FAILED";
  var error = new Error(messages[code]);
  Object.defineProperties(error, { name: { value: "KitNetworkError" }, code: { value: code, enumerable: true }, operation: { value: "request", enumerable: true } });
  return Object.freeze(error);
}

function httpObject(value, allowed) {
  if (!value || typeof value !== "object" ||
    Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null ||
    Object.getOwnPropertySymbols(value).length) throw new TypeError("HTTPS options must be a plain object");
  var descriptors = Object.getOwnPropertyDescriptors(value);
  var output = Object.create(null);
  Object.keys(descriptors).forEach(function (key) {
    var descriptor = descriptors[key];
    if (allowed && !Object.prototype.hasOwnProperty.call(allowed, key) ||
      !Object.prototype.hasOwnProperty.call(descriptor, "value") || descriptor.get || descriptor.set) {
      throw new TypeError("HTTPS options contain an unknown property or accessor");
    }
    output[key] = descriptor.value;
  });
  return output;
}

function textBytes(value, limit) {
  if (typeof value !== "string") throw new TypeError("HTTPS text must be a string");
  var count = 0;
  for (var i = 0; i < value.length; i++) {
    var n = value.charCodeAt(i);
    if (n < 128) count++;
    else if (n < 2048) count += 2;
    else if (n >= 0xD800 && n <= 0xDBFF) {
      var next = value.charCodeAt(++i);
      if (!(next >= 0xDC00 && next <= 0xDFFF)) throw new TypeError("HTTPS text must be valid UTF-8");
      count += 4;
    } else if (n >= 0xDC00 && n <= 0xDFFF) throw new TypeError("HTTPS text must be valid UTF-8");
    else count += 3;
    if (count > limit) throw new TypeError("HTTPS text exceeds its byte limit");
  }
  return count;
}

function httpHeaders(value) {
  var input = value === undefined ? Object.create(null) : httpObject(value, null);
  var result = Object.create(null);
  var names = Object.keys(input), count = 0;
  if (names.length > 64) throw new TypeError("HTTPS permits at most 64 headers");
  names.forEach(function (name) {
    var lower = name.toLowerCase(), value = input[name];
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) ||
      /^(?:host|cookie2?|origin|referer|connection|keep-alive|transfer-encoding|te|trailer|upgrade|content-length|accept-encoding|expect|set-cookie)$/.test(lower) ||
      /^(?:sec-|proxy-)/.test(lower) || Object.prototype.hasOwnProperty.call(result, lower)) throw new TypeError("HTTPS header is not permitted");
    count += textBytes(name, 16384) + textBytes(value, 16384);
    if (count > 16384 || /[\x00-\x1f\x7f]/.test(value)) throw new TypeError("Invalid HTTPS header value");
    result[lower] = value;
  });
  return result;
}

function httpOptions(input, upload) {
  var options = httpObject(input, upload ? UPLOAD_KEYS : HTTP_KEYS);
  textBytes(options.url, 8192);
  if (!/^https:\/\/[a-z0-9.-]+(?::443)?(?:[/?]|$)/.test(options.url) || /[\\#]/.test(options.url)) {
    throw new TypeError("HTTPS URL must use a canonical public DNS origin");
  }
  var method = options.method === undefined ? (upload ? "POST" : "GET") : options.method;
  if (typeof method !== "string" || !(upload ? /^(POST|PUT|PATCH)$/ : /^(GET|HEAD|POST|PUT|PATCH|DELETE)$/).test(method)) {
    throw new TypeError("Unsupported HTTPS method");
  }
  var timeout = options.timeoutMS === undefined ? 15000 : options.timeoutMS;
  if (!Number.isSafeInteger(timeout) || timeout < 1 || timeout > 30000) throw new TypeError("HTTPS timeout must be 1–30000 milliseconds");
  var body = options.body === undefined ? "" : options.body;
  textBytes(body, 1024 * 1024);
  if ((method === "GET" || method === "HEAD") && body !== "") throw new TypeError("GET/HEAD cannot have a body");
  var signal = options.signal;
  if (signal !== undefined) {
    if (!signalGetter || !addSignalEvent || !removeSignalEvent) throw new TypeError("signal must be an AbortSignal");
    try { signalGetter.call(signal); } catch (_) { throw new TypeError("signal must be an AbortSignal"); }
  }
  var params = { url: options.url, method: method, headers: httpHeaders(options.headers), timeoutMS: timeout };
  if (!upload) params.body = body;
  return { params: params, signal: signal };
}

function responseValue(value) {
  var response = httpObject(value, { status: true, headers: true, body: true });
  if (Object.keys(response).length !== 3 || !Number.isSafeInteger(response.status) || response.status < 200 || response.status > 599) throw httpError("INVALID_RESPONSE");
  textBytes(response.body, 1024 * 1024);
  var headers = httpObject(response.headers, null), count = 0;
  Object.keys(headers).forEach(function (name) {
    count += textBytes(name, 32768) + textBytes(headers[name], 32768);
    if (count > 32768) throw httpError("INVALID_RESPONSE");
  });
  return Object.freeze({ status: response.status, headers: Object.freeze(headers), body: response.body });
}

function runRequest(options, handle) {
  var signal = options.signal;
  if (signal && signalGetter.call(signal)) return Promise.reject(httpError("CANCELLED"));
  if (!nativeHost || !setTimer || !clearTimer) return Promise.reject(httpError("UNAVAILABLE"));
  if (activeRequests >= 4) return Promise.reject(httpError("BUSY"));
  activeRequests++;
  return new Promise(function (resolve, reject) {
    var operation = null, terminal = false, released = false, timer = null, deadline = null, polls = 0;
    function release() {
      if (!operation || released) return;
      released = true;
      try { Promise.resolve(nativeHost.call("network.releaseRequest", { operation: operation })).catch(function () {}); }
      catch (_) { /* Host TTL remains the final cleanup fence. */ }
    }
    function finish(error, value) {
      if (terminal) return;
      terminal = true;
      activeRequests--;
      if (timer !== null) clearTimer(timer);
      if (deadline !== null) clearTimer(deadline);
      if (signal) removeSignalEvent(signal, "abort", abort);
      release();
      if (error) reject(httpError(error)); else resolve(value);
    }
    function abort() { finish("CANCELLED"); }
    function callNative(action, params) {
      try { return Promise.resolve(nativeHost.call(action, params)); }
      catch (error) { return Promise.reject(httpError(error)); }
    }
    function poll() {
      if (terminal) return;
      if (++polls > 234) { finish("TIMEOUT"); return; }
      callNative("network.pollRequest", { operation: operation }).then(function (value) {
        if (terminal) return;
        try {
          var result = httpObject(value, { state: true, response: true, code: true });
          if (result.state === "pending" && Object.keys(result).length === 1) {
            timer = setTimer(poll, 150); return;
          }
          if (result.state === "failed" && Object.keys(result).length === 2 && typeof result.code === "string") {
            finish(result.code); return;
          }
          if (result.state === "completed" && Object.keys(result).length === 2 && result.response) {
            finish(null, responseValue(result.response)); return;
          }
          finish("INVALID_RESPONSE");
        } catch (_) { finish("INVALID_RESPONSE"); }
      }, function (error) { finish(error); });
    }
    try {
      deadline = setTimer(function () { finish("TIMEOUT"); }, 35000);
      if (signal) addSignalEvent(signal, "abort", abort, { once: true });
      if (signal && signalGetter.call(signal)) { abort(); return; }
      if (handle !== undefined) options.params.handle = handle;
      callNative(handle === undefined ? "network.beginRequest" : "network.beginUpload", options.params).then(function (value) {
        try {
          var result = httpObject(value, { operation: true });
          if (Object.keys(result).length !== 1 || typeof result.operation !== "string" || !HTTP_OPERATION.test(result.operation)) throw httpError("INVALID_RESPONSE");
          operation = result.operation;
        } catch (_) { finish("INVALID_RESPONSE"); return; }
        if (terminal) { release(); return; }
        poll();
      }, function (error) { finish(error); });
    } catch (error) { finish(error); }
  });
}

function request(options) {
  if (arguments.length !== 1) throw new TypeError("network.request expects one options object");
  return runRequest(httpOptions(options, false));
}

function installUploadBoundary() {
  var graph = assembly && assembly.graph;
  if (!graph || !graph.services || graph.services.network !== "1.1.0" || graph.services.files !== "1.4.0") return;
  if (Object.prototype.hasOwnProperty.call(assembly, UPLOAD_BOUNDARY)) throw new Error("KitJS: upload boundary already installed");
  Object.defineProperty(assembly, UPLOAD_BOUNDARY, { configurable: true, value: Object.freeze(function (handle, options) {
    if (typeof handle !== "string" || !HTTP_OPERATION.test(handle)) throw new TypeError("Invalid native file identity");
    return runRequest(httpOptions(options, true), handle);
  }) });
}

var namespace = Object.create(null);
Object.defineProperty(namespace, "online", {
  enumerable: true,
  get: function () { return snapshot().online; }
});
namespace.snapshot = snapshot;
namespace.subscribe = subscribe;
namespace.status = status;
namespace.request = request;

kit.service("network", namespace);
installUploadBoundary();
})(globalThis, kit);
