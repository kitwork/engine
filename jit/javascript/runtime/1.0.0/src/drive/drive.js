"use strict";

var errors = require("../core/errors.js");
var utils = require("../core/utils.js");
var createRuntimeError = errors.createRuntimeError;
var enqueueMicrotask = utils.enqueueMicrotask;

function createDriveManager(runtime) {
  var global = runtime.global;
  var document = runtime.document;
  var states = new WeakMap();
  var globalInstalled = false;
  var historyScrollRestoration = null;

  function stateFor(app) {
    var state = states.get(app);
    if (!state) {
      state = {
        app: app,
        enabled: false,
        controller: null,
        cache: new Map(),
        cleanups: [],
        navigating: false,
        hoverTimer: null,
        sequence: 0,
        currentUrl: global.location ? global.location.href : ""
      };
      states.set(app, state);
      app.drive = state;
    }
    return state;
  }

  function appFrom(value) {
    if (!value) {
      var enabled = Array.from(runtime.apps).filter(function (app) { return stateFor(app).enabled; });
      return enabled[0] || Array.from(runtime.apps)[0] || null;
    }
    if (value.root && runtime.apps.has(value)) return value;
    if (value.nodeType) return runtime.appsByRoot.get(value) || runtime.appForNode(value);
    if (typeof value === "string") {
      var found = null;
      runtime.apps.forEach(function (app) { if (!found && app.name === value) found = app; });
      return found;
    }
    return runtime.appForNode(value);
  }

  function dispatch(app, name, detail, cancelable) {
    if (!app || !app.root || !app.root.dispatchEvent || typeof global.CustomEvent !== "function") return true;
    var event = new global.CustomEvent(name, {
      bubbles: true,
      cancelable: !!cancelable,
      detail: Object.assign({ app: app, root: app.root }, detail || {})
    });
    return app.root.dispatchEvent(event);
  }

  function driveEnabledValue(root) {
    if (!root || !root.hasAttribute || !root.hasAttribute("data-kit-drive")) return false;
    var value = String(root.getAttribute("data-kit-drive") || "").trim().toLowerCase();
    return value !== "false" && value !== "off" && value !== "0";
  }

  function currentBaseUrl() {
    var locationHref = global.location && global.location.href;
    try {
      if (locationHref) {
        var locationUrl = new global.URL(locationHref);
        if (locationUrl.protocol === "http:" || locationUrl.protocol === "https:") return locationHref;
      }
    } catch (_) {}
    return document.baseURI || locationHref || "http://localhost/";
  }

  function sameOrigin(url) {
    try { return url.origin === new global.URL(currentBaseUrl()).origin; }
    catch (_) { return true; }
  }

  function normalizeUrl(input) {
    return new global.URL(String(input), currentBaseUrl());
  }

  function shouldHandleUrl(url) {
    return (url.protocol === "http:" || url.protocol === "https:") && sameOrigin(url);
  }

  function linkForEvent(event) {
    if (!event || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return null;
    var link = event.target && event.target.closest ? event.target.closest("a[href]") : null;
    if (!link) return null;
    if (link.hasAttribute("download")) return null;
    var target = String(link.getAttribute("target") || "").toLowerCase();
    if (target && target !== "_self") return null;
    if (String(link.getAttribute("rel") || "").toLowerCase().split(/\s+/).indexOf("external") >= 0) return null;
    if (link.closest("[data-kit-no-drive]") || String(link.getAttribute("data-kit-drive") || "").toLowerCase() === "false") return null;
    return link;
  }

  function saveCurrentScroll() {
    if (!global.history || !global.location) return;
    var previous = global.history.state && typeof global.history.state === "object"
      ? Object.assign({}, global.history.state)
      : {};
    previous.kitwork = previous.kitwork || true;
    previous.scroll = { x: global.scrollX || 0, y: global.scrollY || 0 };
    try { global.history.replaceState(previous, "", global.location.href); } catch (_) {}
  }

  function updateHistory(app, url, mode) {
    if (!global.history || mode === false || mode === "none") return;
    var state = { kitwork: true, app: app.name, url: String(url), scroll: { x: 0, y: 0 } };
    try {
      if (mode === "replace") global.history.replaceState(state, "", String(url));
      else global.history.pushState(state, "", String(url));
    } catch (error) {
      runtime.warn("KIT_DRIVE_HISTORY", "History update was rejected by the browser", { error: error, url: String(url) });
    }
  }

  function restoreScroll(url, options) {
    options = options || {};
    enqueueMicrotask(function () {
      if (options.scroll && typeof options.scroll.x === "number") {
        global.scrollTo(options.scroll.x, options.scroll.y || 0);
        return;
      }
      if (url.hash) {
        var id = decodeURIComponent(url.hash.slice(1));
        var target = document.getElementById(id) || document.querySelector('[name="' + id.replace(/"/g, '\\"') + '"]');
        if (target && typeof target.scrollIntoView === "function") {
          target.scrollIntoView();
          return;
        }
      }
      if (options.preserveScroll) return;
      global.scrollTo(0, 0);
    });
  }

  function selectIncomingRoot(app, incomingDocument) {
    if (app.root === document.documentElement) return incomingDocument.documentElement;
    var candidates = Array.prototype.slice.call(incomingDocument.querySelectorAll("[data-kit-app]"));
    for (var i = 0; i < candidates.length; i++) {
      if (String(candidates[i].getAttribute("data-kit-app") || "main") === String(app.name)) return candidates[i];
    }
    if (candidates.length === 1) return candidates[0];
    if (app.root.id) return incomingDocument.getElementById(app.root.id);
    return null;
  }

  function normalizeHtmlResult(result, requestedUrl) {
    if (!result || !result.response) throw createRuntimeError("KIT_DRIVE_RESPONSE", "Drive received no response", { url: String(requestedUrl) });
    if (!result.ok) {
      throw createRuntimeError("KIT_DRIVE_HTTP", "Drive navigation failed with HTTP " + result.status, {
        url: String(requestedUrl),
        status: result.status,
        response: result.response
      });
    }
    if (typeof result.data !== "string") {
      throw createRuntimeError("KIT_DRIVE_CONTENT", "Drive navigation requires an HTML response", {
        url: String(requestedUrl),
        contentType: result.response.headers.get("content-type")
      });
    }
    var parser = new global.DOMParser();
    var incomingDocument = parser.parseFromString(result.data, "text/html");
    var parserError = incomingDocument.querySelector("parsererror");
    if (parserError) throw createRuntimeError("KIT_DRIVE_PARSE", "Drive could not parse the HTML response", { url: String(requestedUrl) });
    return {
      document: incomingDocument,
      url: normalizeUrl(result.response.url || requestedUrl)
    };
  }

  function fetchPage(app, url, options) {
    options = options || {};
    var state = stateFor(app);
    var key = "drive:" + app.name;
    var headers = new global.Headers(options.headers || {});
    headers.set("Accept", "text/html, application/xhtml+xml");
    headers.set("X-Kitwork-Drive", "1");
    return runtime.request.request(url.toString(), {
      method: options.method || "GET",
      body: options.body,
      headers: headers,
      credentials: options.credentials || "same-origin",
      signal: options.signal,
      timeout: options.timeout || 30000,
      key: key
    }).then(function (result) {
      return normalizeHtmlResult(result, url);
    }).finally(function () {
      if (state.controller && state.controller.signal && state.controller.signal.aborted) state.controller = null;
    });
  }

  function cachedPage(app, url, options) {
    var state = stateFor(app);
    var entry = state.cache.get(url.href);
    var ttl = Number(options && options.prefetchTtl) || Number(runtime.options.prefetchTtl) || 30000;
    if (entry && Date.now() - entry.time <= ttl) {
      state.cache.delete(url.href);
      return Promise.resolve(entry.page);
    }
    if (entry) state.cache.delete(url.href);
    return fetchPage(app, url, options);
  }

  function swap(app, page, options) {
    options = options || {};
    var incomingRoot = selectIncomingRoot(app, page.document);
    if (!incomingRoot) {
      throw createRuntimeError("KIT_DRIVE_ROOT_MISSING", "The HTML response does not contain the expected Kitwork app root", {
        app: app.name,
        url: page.url.href
      });
    }

    if (!dispatch(app, "kitwork:before-swap", { url: page.url.href, incomingDocument: page.document }, true)) {
      throw createRuntimeError("KIT_DRIVE_CANCELLED", "Drive swap was cancelled", { url: page.url.href });
    }

    runtime.pauseObserver(app);
    try {
      runtime.head.reconcile(page.document);
      runtime.morph.morphRoot(app, incomingRoot, options);
    } finally {
      runtime.resumeObserver(app);
    }
    var scriptRoot = app.root === document.documentElement ? document.body : app.root;
    var scripts = runtime.head.activateScripts(scriptRoot);
    return Promise.all(scripts).then(function () {
      runtime.hydrateTree(app.root, app);
      runtime.scheduler.invalidate(app, app.root, { type: "drive-swap", url: page.url.href });
      runtime.scheduler.flush(app);
      dispatch(app, "kitwork:after-swap", { url: page.url.href, incomingDocument: page.document }, false);
      dispatch(app, "kitwork:load", { url: page.url.href, navigation: true }, false);
      return page;
    });
  }

  function visit(input, visitOptions) {
    visitOptions = Object.assign({}, visitOptions || {});
    var app = appFrom(visitOptions.app || visitOptions.source);
    if (!app) return Promise.reject(createRuntimeError("KIT_APP_MISSING", "No Kitwork application is available for Drive navigation"));
    var state = stateFor(app);
    var url = normalizeUrl(input);
    if (!shouldHandleUrl(url)) {
      if (visitOptions.hard !== false && global.location) global.location.href = url.href;
      return Promise.resolve({ hard: true, url: url.href });
    }

    var current = global.location ? normalizeUrl(global.location.href) : null;
    if (current && current.origin === url.origin && current.pathname === url.pathname && current.search === url.search && current.hash !== url.hash) {
      restoreScroll(url, visitOptions);
      return Promise.resolve({ hash: true, url: url.href });
    }

    if (!dispatch(app, "kitwork:navigation-start", { url: url.href, options: visitOptions }, true)) {
      return Promise.reject(createRuntimeError("KIT_DRIVE_CANCELLED", "Drive navigation was cancelled", { url: url.href }));
    }
    saveCurrentScroll();
    state.navigating = true;
    state.sequence++;
    var sequence = state.sequence;

    var method = String(visitOptions.method || "GET").toUpperCase();
    var pagePromise = method === "GET" && !visitOptions.body
      ? cachedPage(app, url, visitOptions)
      : fetchPage(app, url, visitOptions);

    return pagePromise.then(function (page) {
      if (sequence !== state.sequence) throw createRuntimeError("KIT_DRIVE_SUPERSEDED", "Drive navigation was superseded", { url: url.href });
      return swap(app, page, visitOptions);
    }).then(function (page) {
      var historyMode = visitOptions.history;
      if (historyMode === undefined) historyMode = visitOptions.replace ? "replace" : "push";
      updateHistory(app, page.url, historyMode);
      state.currentUrl = page.url.href;
      restoreScroll(page.url, visitOptions);
      if (sequence === state.sequence) state.navigating = false;
      dispatch(app, "kitwork:navigation-complete", { url: page.url.href }, false);
      return { app: app, url: page.url.href, document: page.document, root: app.root };
    }).catch(function (error) {
      if (sequence === state.sequence) state.navigating = false;
      if (error && (error.name === "AbortError" || error.code === "KIT_DRIVE_SUPERSEDED")) throw error;
      runtime.reportError(error, { app: app, phase: "drive", directive: "data-kit-drive", source: url.href });
      dispatch(app, "kitwork:navigation-error", { url: url.href, error: error }, false);
      if (visitOptions.fallback !== false && global.location && !visitOptions.test) {
        try { global.location.href = url.href; } catch (_) {}
      }
      throw error;
    });
  }

  function prefetch(input, prefetchOptions) {
    prefetchOptions = Object.assign({}, prefetchOptions || {});
    var app = appFrom(prefetchOptions.app || prefetchOptions.source);
    if (!app) return Promise.resolve(null);
    var url = normalizeUrl(input);
    if (!shouldHandleUrl(url)) return Promise.resolve(null);
    var state = stateFor(app);
    var existing = state.cache.get(url.href);
    var ttl = Number(prefetchOptions.prefetchTtl) || Number(runtime.options.prefetchTtl) || 30000;
    if (existing && Date.now() - existing.time <= ttl) return Promise.resolve(existing.page);

    var headers = new global.Headers(prefetchOptions.headers || {});
    headers.set("Accept", "text/html, application/xhtml+xml");
    headers.set("X-Kitwork-Prefetch", "1");
    return runtime.request.request(url.href, {
      method: "GET",
      headers: headers,
      credentials: "same-origin",
      timeout: prefetchOptions.timeout || 15000
    }).then(function (result) {
      var page = normalizeHtmlResult(result, url);
      state.cache.set(url.href, { page: page, time: Date.now() });
      var max = Number(runtime.options.prefetchMax) || 15;
      while (state.cache.size > max) state.cache.delete(state.cache.keys().next().value);
      return page;
    }).catch(function () { return null; });
  }

  function submit(form, submitter, options) {
    options = Object.assign({}, options || {});
    var app = appFrom(options.app || form);
    if (!app) return Promise.reject(createRuntimeError("KIT_APP_MISSING", "No app owns this form"));
    var method = String(form.getAttribute("method") || "GET").toUpperCase();
    var action = normalizeUrl(form.getAttribute("action") || global.location.href);
    var data = new global.FormData(form);
    if (submitter && submitter.name && !data.has(submitter.name)) data.append(submitter.name, submitter.value || "");
    if (method === "GET") {
      data.forEach(function (value, key) { action.searchParams.append(key, String(value)); });
      return visit(action.href, Object.assign(options, { app: app, method: "GET" }));
    }
    return visit(action.href, Object.assign(options, { app: app, method: method, body: data }));
  }

  function installGlobalListeners() {
    if (globalInstalled || !document) return;
    globalInstalled = true;
    runtime.listen(document, "click", function (event) {
      var link = linkForEvent(event);
      if (!link) return;
      var app = runtime.appForNode(link);
      if (!app || !stateFor(app).enabled) return;
      var url;
      try { url = normalizeUrl(link.href); } catch (_) { return; }
      if (!shouldHandleUrl(url)) return;
      event.preventDefault();
      visit(url.href, { app: app, source: link }).catch(function () {});
    }, false);

    runtime.listen(document, "submit", function (event) {
      if (event.defaultPrevented) return;
      var form = event.target;
      if (!form || String(form.tagName || "").toLowerCase() !== "form") return;
      var app = runtime.appForNode(form);
      if (!app || !stateFor(app).enabled) return;
      if (form.closest("[data-kit-no-drive]") || String(form.getAttribute("data-kit-drive") || "").toLowerCase() === "false") return;
      var target = String(form.getAttribute("target") || "").toLowerCase();
      if (target && target !== "_self") return;
      var action;
      try { action = normalizeUrl(form.getAttribute("action") || global.location.href); } catch (_) { return; }
      if (!shouldHandleUrl(action)) return;
      event.preventDefault();
      submit(form, event.submitter || null, { app: app, source: form }).catch(function () {});
    }, false);

    function prefetchEvent(event) {
      var link = event.target && event.target.closest ? event.target.closest("a[href]") : null;
      if (!link) return;
      var app = runtime.appForNode(link);
      if (!app || !stateFor(app).enabled) return;
      var setting = String(link.getAttribute("data-kit-prefetch") || app.root.getAttribute("data-kit-prefetch") || "hover").toLowerCase();
      if (setting === "false" || setting === "off" || setting === "0") return;
      var url;
      try { url = normalizeUrl(link.href); } catch (_) { return; }
      if (!shouldHandleUrl(url)) return;
      var state = stateFor(app);
      if (state.hoverTimer) clearTimeout(state.hoverTimer);
      state.hoverTimer = setTimeout(function () {
        state.hoverTimer = null;
        prefetch(url.href, { app: app, source: link });
      }, Number(runtime.options.prefetchDelay) || 65);
    }
    runtime.listen(document, "pointerover", prefetchEvent, true);
    runtime.listen(document, "focusin", prefetchEvent, true);

    if (global.history && "scrollRestoration" in global.history) {
      historyScrollRestoration = global.history.scrollRestoration;
      global.history.scrollRestoration = "manual";
    }
    runtime.listen(global, "popstate", function (event) {
      var state = event.state || {};
      var app = appFrom(state.app);
      if (!app || !stateFor(app).enabled) return;
      visit(global.location.href, {
        app: app,
        history: false,
        fallback: false,
        scroll: state.scroll || null,
        action: "restore"
      }).catch(function () {});
    }, false);
  }

  function attach(app, force) {
    if (!app || app.destroyed) return null;
    var state = stateFor(app);
    state.enabled = force === undefined ? (driveEnabledValue(app.root) || !!runtime.options.drive) : !!force;
    if (state.enabled) installGlobalListeners();
    return state;
  }

  function detach(app) {
    var state = app && states.get(app);
    if (!state) return;
    state.enabled = false;
    if (state.hoverTimer) clearTimeout(state.hoverTimer);
    if (runtime.request) runtime.request.abort("drive:" + app.name, "drive-detach");
    state.cache.clear();
  }

  function destroy() {
    runtime.apps.forEach(detach);
    if (global.history && historyScrollRestoration != null) {
      try { global.history.scrollRestoration = historyScrollRestoration; } catch (_) {}
    }
  }

  return Object.freeze({
    attach: attach,
    detach: detach,
    visit: visit,
    prefetch: prefetch,
    submit: submit,
    back: function () { if (global.history) global.history.back(); },
    forward: function () { if (global.history) global.history.forward(); },
    reload: function () { if (global.location) global.location.reload(); },
    enabled: function (value) { var app = appFrom(value); return !!(app && stateFor(app).enabled); },
    destroy: destroy,
    stateFor: stateFor
  });
}

module.exports = {
  createDriveManager: createDriveManager
};
