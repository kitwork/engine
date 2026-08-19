;(function (global, document, kit) {
"use strict";

// KitJS service: request@1.0.0
var OWN = Object.prototype.hasOwnProperty;
var METHODS = Object.freeze({
  GET: true,
  HEAD: true,
  POST: true,
  PUT: true,
  PATCH: true,
  DELETE: true
});
var OPTION_KEYS = Object.freeze({
  method: true,
  headers: true,
  data: true,
  key: true,
  timeout: true
});
var MAX_ACTIVE = 256;
var MAX_KEY_LENGTH = 128;
var MAX_RESPONSE_BYTES = 8 * 1024 * 1024;
var active = new Map();
var sequence = 0;

function plainSnapshot(value, label) {
  var prototype = value && Object.getPrototypeOf(value);
  if (!value || prototype !== Object.prototype && prototype !== null ||
    Object.getOwnPropertySymbols(value).length) {
    throw new TypeError(label + " must be a plain object");
  }
  var descriptors = Object.getOwnPropertyDescriptors(value);
  var output = Object.create(null);
  Object.keys(descriptors).forEach(function (name) {
    var descriptor = descriptors[name];
    if (!OWN.call(descriptor, "value")) {
      throw new TypeError(label + " must not contain accessors");
    }
    output[name] = descriptor.value;
  });
  return output;
}

function optionsOf(value) {
  if (value === undefined) value = Object.create(null);
  var options = plainSnapshot(value, "Request options");
  Object.keys(options).forEach(function (name) {
    if (!OWN.call(OPTION_KEYS, name)) {
      throw new TypeError("Unknown request option: " + name);
    }
  });
  return options;
}

function methodOf(value) {
  if (value === undefined) return "GET";
  if (typeof value !== "string" || value !== value.trim()) {
    throw new TypeError("Request method must be GET, HEAD, POST, PUT, PATCH, or DELETE");
  }
  var method = value.toUpperCase();
  if (!OWN.call(METHODS, method)) {
    throw new TypeError("Request method must be GET, HEAD, POST, PUT, PATCH, or DELETE");
  }
  return method;
}

function keyOf(value) {
  if (value === undefined) return null;
  if (typeof value !== "string" || !value || value !== value.trim() || value.length > MAX_KEY_LENGTH) {
    throw new TypeError("Request key must be a non-empty string up to 128 characters without surrounding whitespace");
  }
  return value;
}

function timeoutOf(value) {
  if (value === undefined) return 0;
  if (typeof value !== "number" || !Number.isInteger(value) || value < 0 || value > 120000) {
    throw new TypeError("Request timeout must be an integer from 0 to 120000 milliseconds");
  }
  return value;
}

function urlOf(value) {
  if (typeof value !== "string" || !value) throw new TypeError("Request URL must be a non-empty string");
  var url;
  try { url = new global.URL(value, global.location.href); }
  catch (_) { throw new TypeError("Request URL is invalid"); }
  if ((url.protocol !== "http:" && url.protocol !== "https:") ||
    url.origin !== global.location.origin || url.username || url.password) {
    throw new TypeError("Request URL must be a same-origin HTTP(S) URL");
  }
  url.hash = "";
  return url.href;
}

function headersOf(value) {
  var headers = new global.Headers();
  if (value === undefined) return headers;
  var entries = plainSnapshot(value, "Request headers");
  Object.keys(entries).forEach(function (name) {
    if (typeof entries[name] !== "string") {
      throw new TypeError("Request header values must be strings");
    }
    headers.set(name, entries[name]);
  });
  return headers;
}

function isJSONType(value) {
  var type = String(value || "").split(";", 1)[0].trim().toLowerCase();
  return type === "application/json" || /\+json$/.test(type);
}

function csrfToken() {
  var meta = document.querySelector('meta[name="csrf-token"]');
  return meta ? String(meta.getAttribute("content") || "") : "";
}

function validatePlatform() {
  if (typeof global.URL !== "function" || typeof global.fetch !== "function" ||
    typeof global.AbortController !== "function" || typeof global.Headers !== "function" ||
    typeof global.TextDecoder !== "function" || !global.location ||
    typeof global.location.href !== "string" || typeof global.location.origin !== "string") {
    throw new TypeError("Request requires URL, fetch, AbortController, Headers, TextDecoder, and location");
  }
}

function requestError(code, message, status, url, data) {
  var error = new Error(message);
  Object.defineProperties(error, {
    name: { value: "KitRequestError" },
    code: { value: code, enumerable: true },
    status: { value: status || 0, enumerable: true },
    url: { value: url || "", enumerable: true },
    data: { value: data === undefined ? null : data, enumerable: true }
  });
  return Object.freeze(error);
}

function resultOf(status, url, data) {
  return Object.freeze(Object.assign(Object.create(null), {
    status: status,
    url: url,
    data: data
  }));
}

function cancel(record, message) {
  if (!record || record.cancelled || record.timedOut || record.done) return false;
  record.cancelled = true;
  record.cancelMessage = message;
  try { record.controller.abort(); }
  catch (_) { /* Cancellation state still wins if AbortController is best effort. */ }
  return true;
}

function cancelError(record) {
  return requestError("CANCELLED", record.cancelMessage || "Request was cancelled", 0, record.url, null);
}

function activate(record) {
  if (record.key === null) return;
  var previous = active.get(record.key);
  if (previous) {
    active.delete(record.key);
    cancel(previous, "Request was superseded");
  } else if (active.size >= MAX_ACTIVE) {
    var oldest = active.entries().next().value;
    if (oldest) {
      active.delete(oldest[0]);
      cancel(oldest[1], "Request was cancelled to enforce capacity");
    }
  }
  active.set(record.key, record);
}

function release(record) {
  record.done = true;
  if (record.timeoutID !== null) {
    global.clearTimeout(record.timeoutID);
    record.timeoutID = null;
  }
  if (record.key !== null && active.get(record.key) === record) active.delete(record.key);
}

function progressID() {
  sequence++;
  if (!Number.isSafeInteger(sequence)) sequence = 1;
  return "request:" + sequence;
}

function finalURL(response, fallback) {
  var url;
  try { url = new global.URL(response.url || fallback, fallback); }
  catch (_) { return ""; }
  if ((url.protocol !== "http:" && url.protocol !== "https:") ||
    url.origin !== global.location.origin || url.username || url.password) return "";
  url.hash = "";
  return url.href;
}

function exactLength(response) {
  var encoding = String(response.headers.get("content-encoding") || "").trim().toLowerCase();
  if (encoding && encoding !== "identity") return null;
  var source = String(response.headers.get("content-length") || "").trim();
  if (!/^(?:0|[1-9][0-9]*)$/.test(source)) return null;
  var total = Number(source);
  return Number.isSafeInteger(total) ? total : null;
}

function cancelBody(response) {
  try {
    if (response.body && typeof response.body.cancel === "function") {
      var pending = response.body.cancel();
      if (pending && typeof pending.catch === "function") pending.catch(function () {});
    }
  } catch (_) { /* The response will be released with the request. */ }
}

async function responseBytes(response, record, status, url) {
  var total;
  try { total = exactLength(response); }
  catch (_) {
    throw requestError("INVALID_RESPONSE", "Request returned invalid response headers", status, url, null);
  }
  if (total !== null && total > MAX_RESPONSE_BYTES) {
    cancelBody(response);
    throw requestError("TOO_LARGE", "Response exceeds the 8 MiB limit", status, url, null);
  }
  if (!response.body) {
    if (total !== null && total !== 0) {
      throw requestError("INVALID_RESPONSE", "Response body length did not match its headers", status, url, null);
    }
    return new Uint8Array(0);
  }
  if (typeof response.body.getReader !== "function") {
    cancelBody(response);
    throw requestError("INVALID_RESPONSE", "Response body is not readable", status, url, null);
  }

  var reader;
  try { reader = response.body.getReader(); }
  catch (_) {
    throw requestError("INVALID_RESPONSE", "Response body is not readable", status, url, null);
  }
  var chunks = [];
  var loaded = 0;
  try {
    for (;;) {
      if (record.cancelled) throw cancelError(record);
      var item = await reader.read();
      if (record.cancelled) throw cancelError(record);
      if (item.done) break;
      var value = item.value;
      if (!value || !(value instanceof Uint8Array)) {
        throw requestError("INVALID_RESPONSE", "Response body contained an invalid chunk", status, url, null);
      }
      if (value.byteLength === 0) continue;
      if (loaded > MAX_RESPONSE_BYTES - value.byteLength) {
        throw requestError("TOO_LARGE", "Response exceeds the 8 MiB limit", status, url, null);
      }
      loaded += value.byteLength;
      chunks.push(value.slice());
      if (total !== null && total > 0) {
        if (loaded > total) {
          throw requestError("INVALID_RESPONSE", "Response body length did not match its headers", status, url, null);
        }
        kit.progress.update(record.progressID, loaded, total);
      }
    }
  } catch (error) {
    try {
      var cancelled = reader.cancel();
      if (cancelled && typeof cancelled.catch === "function") cancelled.catch(function () {});
    } catch (_) { /* AbortController also owns cancellation. */ }
    throw error;
  } finally {
    if (typeof reader.releaseLock === "function") {
      try { reader.releaseLock(); }
      catch (_) { /* Completed or cancelled readers may already be unlocked. */ }
    }
  }
  if (total !== null && loaded !== total) {
    throw requestError("INVALID_RESPONSE", "Response body length did not match its headers", status, url, null);
  }
  var bytes = new Uint8Array(loaded);
  var offset = 0;
  chunks.forEach(function (chunk) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  });
  chunks.length = 0;
  return bytes;
}

async function responseData(response, record, status, url, method) {
  if (method === "HEAD" || status === 204 || status === 205) {
    cancelBody(response);
    return null;
  }
  var bytes = await responseBytes(response, record, status, url);
  var text;
  try { text = new global.TextDecoder().decode(bytes); }
  catch (_) {
    throw requestError("INVALID_RESPONSE", "Response text could not be decoded", status, url, null);
  }
  var contentType;
  try { contentType = response.headers.get("content-type"); }
  catch (_) {
    throw requestError("INVALID_RESPONSE", "Request returned invalid response headers", status, url, null);
  }
  if (!isJSONType(contentType)) return text;
  try { return JSON.parse(text); }
  catch (_) {
    throw requestError("INVALID_RESPONSE", "Response JSON is invalid", status, url, null);
  }
}

function prepare(url, input) {
  validatePlatform();
  var options = optionsOf(input);
  var method = methodOf(options.method);
  var headers = headersOf(options.headers);
  var hasData = OWN.call(options, "data");
  var body;
  if (hasData && (method === "GET" || method === "HEAD")) {
    throw new TypeError(method + " requests cannot contain data");
  }
  if (hasData) {
    try { body = JSON.stringify(options.data); }
    catch (_) { throw new TypeError("Request data must be JSON-serializable"); }
    if (body === undefined) throw new TypeError("Request data must be JSON-serializable");
    var contentType = headers.get("content-type");
    if (contentType && !isJSONType(contentType)) {
      throw new TypeError("Request data requires a JSON Content-Type");
    }
    if (!contentType) headers.set("Content-Type", "application/json");
  }
  if (method !== "GET" && method !== "HEAD" && !headers.has("X-CSRF-Token")) {
    var token = csrfToken();
    if (token) headers.set("X-CSRF-Token", token);
  }
  return {
    url: urlOf(url),
    method: method,
    headers: headers,
    body: body,
    key: keyOf(options.key),
    timeout: timeoutOf(options.timeout)
  };
}

async function execute(plan) {
  var record = {
    controller: new global.AbortController(),
    key: plan.key,
    url: plan.url,
    progressID: progressID(),
    timeoutID: null,
    cancelled: false,
    cancelMessage: "",
    timedOut: false,
    done: false
  };
  activate(record);
  if (plan.timeout) {
    record.timeoutID = global.setTimeout(function () {
      if (record.done || record.cancelled) return;
      record.timedOut = true;
      try { record.controller.abort(); }
      catch (_) { /* Timeout state still wins if AbortController is best effort. */ }
    }, plan.timeout);
  }
  kit.progress.start(record.progressID, {
    source: "request",
    url: plan.url
  });

  var outcome = "error";
  try {
    var response = await global.fetch(plan.url, {
      method: plan.method,
      headers: plan.headers,
      body: plan.body,
      signal: record.controller.signal,
      credentials: "same-origin",
      mode: "same-origin",
      redirect: "follow"
    });
    if (record.cancelled) throw cancelError(record);
    if (record.timedOut) {
      throw requestError("TIMEOUT", "Request timed out", 0, plan.url, null);
    }
    if (!response || !Number.isInteger(response.status) || response.status < 100 || response.status > 599 ||
      !response.headers || typeof response.headers.get !== "function") {
      throw requestError("INVALID_RESPONSE", "Request returned an invalid response", 0, plan.url, null);
    }
    var url = finalURL(response, plan.url);
    if (!url) {
      cancelBody(response);
      throw requestError("INVALID_RESPONSE", "Request redirected outside its origin", response.status, plan.url, null);
    }
    var data = await responseData(response, record, response.status, url, plan.method);
    if (record.cancelled) throw cancelError(record);
    if (record.timedOut) {
      throw requestError("TIMEOUT", "Request timed out", 0, plan.url, null);
    }
    if (response.status < 200 || response.status > 299) {
      throw requestError("HTTP", "Request failed with HTTP " + response.status, response.status, url, data);
    }
    outcome = "loaded";
    return resultOf(response.status, url, data);
  } catch (error) {
    if (record.cancelled) {
      outcome = "cancelled";
      throw cancelError(record);
    }
    if (record.timedOut) {
      throw requestError("TIMEOUT", "Request timed out", 0, plan.url, null);
    }
    if (error && error.name === "KitRequestError") throw error;
    throw requestError("NETWORK", "Request failed", 0, plan.url, null);
  } finally {
    release(record);
    kit.progress.finish(record.progressID, outcome);
  }
}

function send(url, options) {
  return execute(prepare(url, options));
}

function convenienceOptions(value, method, data, withData) {
  var options = optionsOf(value);
  if (OWN.call(options, "method")) throw new TypeError(method + " options cannot override method");
  if (OWN.call(options, "data")) throw new TypeError(method + " options cannot contain data");
  options.method = method;
  if (withData) options.data = data;
  return options;
}

function get(url, options) {
  return send(url, convenienceOptions(options, "GET", null, false));
}

function post(url, data, options) {
  return send(url, convenienceOptions(options, "POST", data, true));
}

function abort(key) {
  if (key === undefined) throw new TypeError("Request abort requires a key");
  key = keyOf(key);
  var record = active.get(key);
  if (!record) return false;
  active.delete(key);
  return cancel(record, "Request was aborted");
}

kit.service("request", {
  send: send,
  get: get,
  post: post,
  abort: abort
});
})(globalThis, document, kit);
