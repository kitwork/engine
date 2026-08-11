// KitJS service: request@1.0.0
;(function (global) {
  "use strict";

  var kit = global.kit;
  var version = "1.0.0";
  var OWN = Object.prototype.hasOwnProperty;
  var active = new Map();
  var MAX_ACTIVE = 256;

  if (!kit || !OWN.call(kit, "component") || typeof kit.component !== "function") {
    throw new Error("KitJS core must be loaded before service:request");
  }
  if (OWN.call(kit, "request")) {
    if (kit.request.version === version) return;
    throw new Error("KitJS service conflict: request");
  }

  function csrfToken() {
    var document = global.document;
    if (!document) return "";
    var meta = document.querySelector('meta[name="csrf-token"], meta[name="csrf"]');
    return meta ? String(meta.getAttribute("content") || "") : "";
  }

  function sameOrigin(url) {
    try {
      return new global.URL(String(url), global.location && global.location.href).origin === global.location.origin;
    } catch (_) {
      return false;
    }
  }

  function requestURL(value) {
    if (typeof value === "string") return value;
    if (typeof global.URL === "function" && value instanceof global.URL) return value.href;
    throw new TypeError("kit.request requires a URL string or URL object");
  }

  function abortController(controller, reason) {
    try { controller.abort(reason); }
    catch (_) { controller.abort(); }
  }

  function abortReason(name, message) {
    if (typeof global.DOMException === "function") return new global.DOMException(message, name);
    var error = new Error(message);
    error.name = name;
    return error;
  }

  function cancellation(reason, fallback) {
    if (reason && typeof reason === "object" && typeof reason.name === "string") return reason;
    return abortReason("AbortError", reason === undefined || reason === null || reason === "" ? fallback : String(reason));
  }

  function signals(options) {
    var controller = new global.AbortController();
    var external = options.signal || null;
    var timeout = Math.min(120000, Math.max(0, Number(options.timeout) || 0));
    var timeoutID = null;
    var key = options.key === undefined || options.key === null ? null : String(options.key);
    var onAbort = null;

    if (external) {
      onAbort = function () { abortController(controller, external.reason); };
      if (external.aborted) onAbort();
      else external.addEventListener("abort", onAbort, { once: true });
    }
    if (timeout) {
      timeoutID = global.setTimeout(function () {
        abortController(controller, abortReason("TimeoutError", "Request timed out"));
      }, timeout);
    }
    if (key !== null) {
      var previous = active.get(key);
      if (previous) abortController(previous, abortReason("AbortError", "Request was superseded"));
      while (!previous && active.size >= MAX_ACTIVE) {
        var oldest = active.entries().next().value;
        if (!oldest) break;
        abortController(oldest[1], abortReason("AbortError", "Request was cancelled to enforce capacity"));
        active.delete(oldest[0]);
      }
      active.set(key, controller);
    }

    return {
      controller: controller,
      cleanup: function () {
        if (timeoutID !== null) global.clearTimeout(timeoutID);
        if (external && onAbort) external.removeEventListener("abort", onAbort);
        if (key !== null && active.get(key) === controller) active.delete(key);
      }
    };
  }

  function responseData(response) {
    if (response.status === 204 || response.status === 205) return Promise.resolve(null);
    var contentType = String(response.headers.get("content-type") || "").toLowerCase();
    if (contentType.indexOf("application/json") !== -1 || contentType.indexOf("+json") !== -1) {
      return response.json();
    }
    return response.text();
  }

  function request(url, input) {
    if (typeof global.fetch !== "function") return Promise.reject(new Error("Fetch API is unavailable"));
    if (typeof global.AbortController !== "function") return Promise.reject(new Error("AbortController is unavailable"));
    if (typeof global.Headers !== "function") return Promise.reject(new Error("Headers API is unavailable"));

    var options;
    var record;
    try {
      url = requestURL(url);
      options = Object.assign(Object.create(null), input || {});
      record = signals(options);
      var headers = new global.Headers(options.headers || {});
      var method = String(options.method || "GET").toUpperCase();
      var useJSON = Object.prototype.hasOwnProperty.call(options, "json");
      var json = options.json;

      delete options.key;
      delete options.timeout;
      delete options.json;
      options.method = method;
      options.headers = headers;
      options.signal = record.controller.signal;
      if (!options.credentials) options.credentials = "same-origin";

      if (sameOrigin(url)) {
        if (!headers.has("X-Requested-With")) headers.set("X-Requested-With", "XMLHttpRequest");
        var token = csrfToken();
        if (token && method !== "GET" && method !== "HEAD" && !headers.has("X-CSRF-Token")) {
          headers.set("X-CSRF-Token", token);
        }
      }
      if (useJSON) {
        var encoded = JSON.stringify(json);
        if (encoded === undefined) throw new TypeError("JSON body must be serializable");
        options.body = encoded;
        if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
      }
    } catch (error) {
      if (record) record.cleanup();
      return Promise.reject(error);
    }

    var pending;
    try { pending = global.fetch(url, options); }
    catch (error) {
      record.cleanup();
      return Promise.reject(error);
    }

    return Promise.resolve(pending).then(function (response) {
      return responseData(response).then(function (data) {
        if (response.ok) return data;
        var error = new Error("Request failed with HTTP " + response.status);
        error.name = "KitRequestError";
        error.status = response.status;
        error.data = data;
        error.response = response;
        throw error;
      });
    }).finally(record.cleanup);
  }

  function get(url, options) {
    return request(url, Object.assign(Object.create(null), options || {}, { method: "GET" }));
  }

  function bodyOptions(body, options) {
    options = Object.assign(Object.create(null), options || {});
    var FormData = global.FormData;
    var Blob = global.Blob;
    var URLSearchParams = global.URLSearchParams;
    var ArrayBuffer = global.ArrayBuffer;
    if (body === undefined) {
      return options;
    }
    if (typeof body === "string" ||
        (FormData && body instanceof FormData) ||
        (Blob && body instanceof Blob) ||
        (URLSearchParams && body instanceof URLSearchParams) ||
        (ArrayBuffer && (body instanceof ArrayBuffer || ArrayBuffer.isView(body)))) {
      options.body = body;
    } else {
      options.json = body;
    }
    return options;
  }

  function post(url, body, options) {
    return request(url, Object.assign(bodyOptions(body, options), { method: "POST" }));
  }

  function submit(form, options) {
    if (!form || String(form.tagName || "").toLowerCase() !== "form") {
      throw new TypeError("kit.request.submit requires a form element");
    }
    options = Object.assign(Object.create(null), options || {});
    var method = String(options.method || form.method || "GET").toUpperCase();
    var action = options.url || form.action || global.location.href;
    var data = new global.FormData(form);
    delete options.url;

    if (method === "GET") {
      var url = new global.URL(action, global.location.href);
      data.forEach(function (value, name) { url.searchParams.append(name, String(value)); });
      return request(url.href, Object.assign(options, { method: "GET" }));
    }
    return request(action, Object.assign(options, { method: method, body: data }));
  }

  function abort(key, reason) {
    key = String(key);
    var controller = active.get(key);
    if (!controller) return false;
    active.delete(key);
    abortController(controller, cancellation(reason, "Request was aborted"));
    return true;
  }

  var requestService = {
    request: request,
    get: get,
    post: post,
    submit: submit,
    abort: abort
  };
  Object.defineProperty(requestService, "version", { value: version, enumerable: false });
  Object.freeze(requestService);
  Object.defineProperty(kit, "request", {
    value: requestService,
    enumerable: true,
    configurable: false,
    writable: false
  });
})(globalThis);
