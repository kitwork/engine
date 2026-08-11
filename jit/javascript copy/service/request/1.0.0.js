// ============================================================================
// Kitwork Service: Request Client (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/service/request/1.0.0.js
// ============================================================================
// HTTP Client Service dành riêng cho HTML-First Applications:
// - Outbound Fetch với CSRF Token auto-header.
// - Form Progressive Enhancement Submission (`submit`).
// - AbortController management (`abort`).
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.request) return;

  var activeControllers = {};

  function getCsrfToken() {
    if (typeof document === "undefined") return "";
    var meta = document.querySelector('meta[name="csrf-token"]') || document.querySelector('meta[name="csrf"]');
    return meta ? meta.getAttribute("content") : "";
  }

  function request(url, options) {
    options = options || {};
    var method = (options.method || "GET").toUpperCase();
    var key = options.key || url;

    // Abort previous request with same key if requested
    if (options.dedupe && activeControllers[key]) {
      activeControllers[key].abort();
    }

    var controller = new AbortController();
    activeControllers[key] = controller;

    var headers = options.headers || {};
    headers["X-Requested-With"] = "XMLHttpRequest";

    var csrf = getCsrfToken();
    if (csrf) headers["X-CSRF-Token"] = csrf;

    var fetchOpts = {
      method: method,
      headers: headers,
      signal: controller.signal,
      credentials: options.credentials || "same-origin"
    };

    if (options.body) {
      if (typeof options.body === "object" && !(options.body instanceof FormData)) {
        headers["Content-Type"] = "application/json";
        fetchOpts.body = JSON.stringify(options.body);
      } else {
        fetchOpts.body = options.body;
      }
    }

    return fetch(url, fetchOpts).then(function (res) {
      delete activeControllers[key];
      if (!res.ok) {
        throw new Error("HTTP_ERROR_" + res.status);
      }
      var contentType = res.headers.get("content-type") || "";
      if (contentType.indexOf("application/json") !== -1) {
        return res.json();
      }
      return res.text();
    })["catch"](function (err) {
      delete activeControllers[key];
      throw err;
    });
  }

  kit.request = {
    get: function (url, options) {
      options = options || {};
      options.method = "GET";
      return request(url, options);
    },

    post: function (url, body, options) {
      options = options || {};
      options.method = "POST";
      options.body = body;
      return request(url, options);
    },

    submit: function (formElement, options) {
      if (!formElement || formElement.tagName !== "FORM") {
        return Promise.reject("Invalid form element");
      }
      options = options || {};
      var action = options.action || formElement.getAttribute("action") || window.location.href;
      var method = options.method || formElement.getAttribute("method") || "POST";
      
      options.method = method;
      options.body = new FormData(formElement);
      return request(action, options);
    },

    abort: function (key) {
      if (activeControllers[key]) {
        activeControllers[key].abort();
        delete activeControllers[key];
        return true;
      }
      return false;
    }
  };

})(typeof window !== "undefined" ? window : globalThis);
