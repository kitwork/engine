"use strict";

function createRequestService(globalObject) {
  const active = new Map();
  const globalFetch = globalObject && globalObject.fetch
    ? globalObject.fetch.bind(globalObject)
    : null;

  function csrfToken() {
    const doc = globalObject && globalObject.document;
    if (!doc) return "";
    const meta = doc.querySelector('meta[name="csrf-token"],meta[name="csrf"]');
    return meta ? String(meta.content || "") : "";
  }

  function composeSignal(signal, timeout, key) {
    const Controller = globalObject.AbortController || AbortController;
    const controller = new Controller();
    let timeoutId = null;
    const abort = (reason) => {
      try { controller.abort(reason); } catch (_) { /* noop */ }
    };
    if (signal) {
      if (signal.aborted) abort(signal.reason);
      else signal.addEventListener("abort", () => abort(signal.reason), { once: true });
    }
    if (timeout > 0) timeoutId = setTimeout(() => abort("timeout"), timeout);
    if (key != null) {
      const previous = active.get(key);
      if (previous) previous.abort("superseded");
      active.set(key, controller);
    }
    return {
      controller,
      cleanup() {
        if (timeoutId) clearTimeout(timeoutId);
        if (key != null && active.get(key) === controller) active.delete(key);
      }
    };
  }

  async function parseResponse(response) {
    const type = String(response.headers.get("content-type") || "").toLowerCase();
    let data;
    if (type.indexOf("application/json") >= 0) data = await response.json();
    else data = await response.text();
    return {
      ok: response.ok,
      status: response.status,
      statusText: response.statusText,
      headers: response.headers,
      data,
      response
    };
  }

  async function request(url, requestOptions) {
    if (!globalFetch) throw new Error("Fetch API is not available");
    requestOptions = Object.assign({}, requestOptions || {});
    const key = requestOptions.key;
    const timeout = Math.max(0, Number(requestOptions.timeout) || 0);
    const signalRecord = composeSignal(requestOptions.signal, timeout, key);
    delete requestOptions.key;
    delete requestOptions.timeout;
    requestOptions.signal = signalRecord.controller.signal;
    const HeadersCtor = globalObject.Headers || Headers;
    requestOptions.headers = new HeadersCtor(requestOptions.headers || {});

    const method = String(requestOptions.method || "GET").toUpperCase();
    const token = csrfToken();
    if (token && method !== "GET" && method !== "HEAD" && !requestOptions.headers.has("X-CSRF-Token")) {
      requestOptions.headers.set("X-CSRF-Token", token);
    }

    if (requestOptions.json !== undefined) {
      requestOptions.body = JSON.stringify(requestOptions.json);
      requestOptions.headers.set("Content-Type", "application/json");
      delete requestOptions.json;
    }

    try {
      return await parseResponse(await globalFetch(url, requestOptions));
    } finally {
      signalRecord.cleanup();
    }
  }

  function get(url, options) {
    return request(url, Object.assign({}, options || {}, { method: "GET" }));
  }

  function post(url, body, options) {
    options = Object.assign({}, options || {}, { method: "POST" });
    const FormDataCtor = globalObject.FormData;
    const BlobCtor = globalObject.Blob;
    if ((FormDataCtor && body instanceof FormDataCtor) || typeof body === "string" || (BlobCtor && body instanceof BlobCtor)) {
      options.body = body;
    } else {
      options.json = body;
    }
    return request(url, options);
  }

  function submit(form, options) {
    if (!form || !form.tagName || String(form.tagName).toLowerCase() !== "form") {
      throw new TypeError("kit.request.submit() requires a form element");
    }
    options = Object.assign({}, options || {});
    const method = String(options.method || form.method || "GET").toUpperCase();
    const action = options.url || form.action || (globalObject.location && globalObject.location.href) || "";
    const data = new globalObject.FormData(form);
    if (method === "GET") {
      const url = new globalObject.URL(action, globalObject.location && globalObject.location.href);
      for (const pair of data.entries()) url.searchParams.append(pair[0], String(pair[1]));
      return request(url.toString(), Object.assign(options, { method: "GET" }));
    }
    return request(action, Object.assign(options, { method, body: data }));
  }

  function abort(key, reason) {
    const controller = active.get(key);
    if (!controller) return false;
    controller.abort(reason || "aborted");
    active.delete(key);
    return true;
  }

  return Object.freeze({ request, get, post, submit, abort });
}

module.exports = { createRequestService };
