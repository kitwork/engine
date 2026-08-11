// KitJS same-plan Drive navigation.
// Classic browser script: compose after core/morph.js and before core/boot.js.
(function (window, document) {
  "use strict";

  var kit = window.kit;
  var core = kit && kit.__kitwork_core__;
  if (!core) throw new Error("KitJS core must be loaded before core/drive.js");
  if (core.reuse) return;
  if (core.phase !== "lifecycle") throw new Error("KitJS core fragment order error before core/drive.js");
  if (typeof core.morph !== "function") throw new Error("KitJS morph must be loaded before core/drive.js");
  if (core.drive) throw new Error("KitJS Drive is already installed");

  // Capture private references before core/boot.js removes the assembly capsule.
  var morph = core.morph;
  var report = core.report;
  var activeVisit = null;
  var visitSequence = 0;
  var started = false;
  var scrollFrame = 0;
  var priorScrollRestoration = null;

  function array(value) {
    return Array.prototype.slice.call(value || []);
  }

  function markerDisabled(value) {
    // Keep this byte-for-byte semantic aligned with the generation scanner:
    // only the exact authored value "false" disables a marker. Application
    // identities are opaque, so whitespace is significant rather than silently
    // normalized by the browser after Go selected the graph.
    return String(value === null ? "" : value) === "false";
  }

  function insideIgnored(element) {
    return !!(element && element.closest && element.closest("[data-kit-ignore]"));
  }

  function boundaryOf(doc) {
    var markers = [];
    array(doc.querySelectorAll("[data-kit-app],[data-kit-hydrate]")).forEach(function (element) {
      if (insideIgnored(element)) return;
      ["data-kit-app", "data-kit-hydrate"].forEach(function (attribute) {
        if (!element.hasAttribute(attribute)) return;
        var value = element.getAttribute(attribute);
        if (markerDisabled(value)) return;
        markers.push({
          marker: element,
          attribute: attribute,
          identity: value === null ? "" : String(value)
        });
      });
    });
    if (markers.length !== 1) {
      return { valid: false, reason: markers.length ? "multiple-app-markers" : "missing-app-marker" };
    }

    var selected = markers[0];
    var html = selected.marker === doc.documentElement;
    var target = html ? doc.body : selected.marker;
    if (!target) return { valid: false, reason: "missing-app-target" };
    selected.valid = true;
    selected.html = html;
    selected.target = target;
    return selected;
  }

  function planOf(doc) {
    var scripts = array(doc.querySelectorAll("script[data-kitwork-runtime]"));
    if (scripts.length > 1) return { valid: false, reason: "multiple-runtime-scripts" };
    if (!scripts.length) return { valid: false, reason: "missing-runtime-script" };
    if (!scripts[0].hasAttribute("data-kitwork-plan")) return { valid: false, reason: "missing-plan-marker" };
    var value = String(scripts[0].getAttribute("data-kitwork-plan") || "");
    if (!value) return { valid: false, reason: "empty-plan-marker" };
    if (!/^[0-9a-f]{64}$/.test(value)) return { valid: false, reason: "invalid-plan-marker" };
    return {
      valid: true,
      value: value
    };
  }

  function validateTransition(currentDocument, incomingDocument) {
    var currentBoundary = boundaryOf(currentDocument);
    if (!currentBoundary.valid) return { valid: false, reason: currentBoundary.reason };
    var incomingBoundary = boundaryOf(incomingDocument);
    if (!incomingBoundary.valid) return { valid: false, reason: incomingBoundary.reason };
    if (currentBoundary.identity !== incomingBoundary.identity) {
      return { valid: false, reason: "app-identity-mismatch" };
    }
    if (currentBoundary.html !== incomingBoundary.html) {
      return { valid: false, reason: "app-boundary-mismatch" };
    }
    if (!currentBoundary.html && (
      currentBoundary.marker.namespaceURI !== incomingBoundary.marker.namespaceURI ||
      String(currentBoundary.marker.localName || "").toLowerCase() !==
      String(incomingBoundary.marker.localName || "").toLowerCase()
    )) {
      return { valid: false, reason: "app-element-mismatch" };
    }

    var currentPlan = planOf(currentDocument);
    var incomingPlan = planOf(incomingDocument);
    if (!currentPlan.valid) return { valid: false, reason: currentPlan.reason };
    if (!incomingPlan.valid) return { valid: false, reason: incomingPlan.reason };
    if (currentPlan.value !== incomingPlan.value) {
      return { valid: false, reason: "plan-mismatch" };
    }
    return {
      valid: true,
      current: currentBoundary,
      incoming: incomingBoundary,
      plan: currentPlan.value
    };
  }

  function optedOut(element, boundary) {
    var node = element;
    while (node && node.nodeType === 1) {
      if (node.hasAttribute("data-kit-drive") && markerDisabled(node.getAttribute("data-kit-drive"))) return true;
      if (node === boundary.marker) break;
      node = node.parentElement;
    }
    return false;
  }

  function sameOriginURL(source) {
    var url;
    try { url = new URL(source, document.baseURI); } catch (_) { return null; }
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    if (url.origin !== window.location.origin) return null;
    return url;
  }

  function eventElement(event) {
    var node = event.target;
    return node && node.nodeType === 1 ? node : node && node.parentElement;
  }

  function linkFor(event, boundary) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return null;
    var origin = eventElement(event);
    var link = origin && origin.closest ? origin.closest("a[href],area[href]") : null;
    if (!link || insideIgnored(link) || !boundary.target.contains(link) || optedOut(link, boundary)) return null;
    if (link.hasAttribute("download")) return null;
    var target = String(link.getAttribute("target") || "").toLowerCase();
    if (target && target !== "_self") return null;
    if ((" " + String(link.getAttribute("rel") || "").toLowerCase() + " ").indexOf(" external ") !== -1) return null;
    var url = sameOriginURL(link.href);
    if (!url) return null;
    if (url.pathname === window.location.pathname && url.search === window.location.search && url.hash) return null;
    return url;
  }

  function formURL(form, submitter) {
    var overrideMethod = submitter && submitter.getAttribute("formmethod");
    var method = String(overrideMethod || form.getAttribute("method") || "get").toLowerCase();
    if (method !== "get") return null;
    var overrideTarget = submitter && submitter.getAttribute("formtarget");
    var target = String(overrideTarget || form.getAttribute("target") || "").toLowerCase();
    if (target && target !== "_self") return null;
    var action = submitter && submitter.getAttribute("formaction");
    var url = sameOriginURL(action || form.getAttribute("action") || window.location.href);
    if (!url) return null;

    var data;
    try {
      data = submitter ? new FormData(form, submitter) : new FormData(form);
    } catch (_) {
      data = new FormData(form);
      if (submitter && submitter.name && !submitter.disabled) data.append(submitter.name, submitter.value);
    }
    url.search = "";
    data.forEach(function (value, name) {
      if (typeof value === "string") url.searchParams.append(name, value);
    });
    return url;
  }

  function formFor(event, boundary) {
    if (event.defaultPrevented) return null;
    var form = event.target;
    if (!form || String(form.localName || "").toLowerCase() !== "form") return null;
    if (insideIgnored(form) || !boundary.target.contains(form) || optedOut(form, boundary)) return null;
    var submitter = event.submitter || null;
    if (submitter && (insideIgnored(submitter) || optedOut(submitter, boundary))) return null;
    return formURL(form, submitter);
  }

  function scrollPosition() {
    return {
      x: Number(window.scrollX || window.pageXOffset || 0),
      y: Number(window.scrollY || window.pageYOffset || 0)
    };
  }

  function historyStateWithScroll(state, position) {
    var next = {};
    if (state && typeof state === "object") {
      Object.keys(state).forEach(function (key) { next[key] = state[key]; });
    }
    next.__kitwork_drive__ = { scroll: position };
    return next;
  }

  function saveScroll() {
    if (!window.history || typeof window.history.replaceState !== "function") return;
    try {
      window.history.replaceState(
        historyStateWithScroll(window.history.state, scrollPosition()),
        "",
        window.location.href
      );
    } catch (_) { /* History may be unavailable for opaque origins. */ }
  }

  function scheduleScrollSave() {
    if (scrollFrame) return;
    var frame = window.requestAnimationFrame || function (callback) { return window.setTimeout(callback, 0); };
    scrollFrame = frame(function () {
      scrollFrame = 0;
      saveScroll();
    });
  }

  function cancelScrollSave() {
    if (!scrollFrame) return;
    if (typeof window.cancelAnimationFrame === "function") window.cancelAnimationFrame(scrollFrame);
    else window.clearTimeout(scrollFrame);
    scrollFrame = 0;
  }

  function historyScroll(state) {
    var value = state && state.__kitwork_drive__ && state.__kitwork_drive__.scroll;
    if (!value || !Number.isFinite(Number(value.x)) || !Number.isFinite(Number(value.y))) return null;
    return { x: Number(value.x), y: Number(value.y) };
  }

  function focusBoundary(target) {
    var active = document.activeElement;
    if (active && target.contains(active) && /^(input|textarea|select)$/i.test(active.localName || "")) return;
    if (!target || typeof target.focus !== "function") return;
    var authoredTabIndex = target.hasAttribute("tabindex");
    if (!authoredTabIndex) target.setAttribute("tabindex", "-1");
    try { target.focus({ preventScroll: true }); } catch (_) { target.focus(); }
    if (!authoredTabIndex) target.removeAttribute("tabindex");
  }

  function hashTarget(hash) {
    if (!hash) return null;
    var id;
    try { id = decodeURIComponent(hash.substring(1)); } catch (_) { id = hash.substring(1); }
    if (!id) return document.documentElement;
    var target = document.getElementById(id);
    if (target) return target;
    var named = array(document.getElementsByName ? document.getElementsByName(id) : []);
    return named.length ? named[0] : null;
  }

  function restoreNavigationPosition(url, position) {
    var frame = window.requestAnimationFrame || function (callback) { return window.setTimeout(callback, 0); };
    frame(function () {
      if (position) {
        try { window.scrollTo(position.x, position.y); } catch (_) { /* Non-visual browser. */ }
        return;
      }
      var target = hashTarget(url.hash);
      if (target && typeof target.scrollIntoView === "function") target.scrollIntoView();
      else {
        try { window.scrollTo(0, 0); } catch (_) { /* Non-visual browser. */ }
      }
    });
  }

  function hardNavigate(url) {
    window.location.assign(String(url));
  }

  function responseIsHTML(response) {
    var type = response.headers && response.headers.get ? String(response.headers.get("content-type") || "").toLowerCase() : "";
    return type.indexOf("text/html") !== -1 || type.indexOf("application/xhtml+xml") !== -1;
  }

  function parseHTML(source) {
    return new DOMParser().parseFromString(source, "text/html");
  }

  function jitStyles(head) {
    if (!head) return [];
    return array(head.querySelectorAll(
      'style[data-kitwork-jit="css"],' +
      'style[data-kitwork-jit="material"],' +
      'style[data-kitwork-jit="icons"],' +
      'style[data-kitwork-jit="logo"],' +
      'style[data-kitwork-jit="fonts"]'
    ));
  }

  function cloneJITStyle(source, liveNonce) {
    var style = document.createElement("style");
    style.setAttribute("data-kitwork-jit", source.getAttribute("data-kitwork-jit"));
    ["media", "title"].forEach(function (name) {
      if (source.hasAttribute(name)) style.setAttribute(name, source.getAttribute(name));
    });
    if (liveNonce !== null) style.nonce = liveNonce;
    else if (source.nonce || source.hasAttribute("nonce")) style.nonce = source.nonce || source.getAttribute("nonce");
    style.disabled = !!source.disabled;
    style.textContent = source.textContent || "";
    return style;
  }

  function reconcileJITStyles(incomingDocument) {
    var oldStyles = jitStyles(document.head);
    var nonceNode = document.head && document.head.querySelector("script[nonce],style[nonce]");
    var liveNonce = nonceNode ? String(nonceNode.nonce || nonceNode.getAttribute("nonce") || "") : null;
    var nextStyles = jitStyles(incomingDocument.head).map(function (style) {
      return cloneJITStyle(style, liveNonce);
    });
    nextStyles.forEach(function (style) { document.head.appendChild(style); });
    oldStyles.forEach(function (style) {
      if (style.parentNode) style.parentNode.removeChild(style);
    });
  }

  function commit(incomingDocument, url, options) {
    var transition = validateTransition(document, incomingDocument);
    if (!transition.valid) {
      hardNavigate(url);
      return false;
    }

    // Validation above is deliberately complete before title or live DOM changes.
    reconcileJITStyles(incomingDocument);
    var target = morph(transition.current.target, transition.incoming.target);
    if (incomingDocument.title !== document.title) document.title = incomingDocument.title;

    if (options.history === "push") {
      window.history.pushState(historyStateWithScroll(null, { x: 0, y: 0 }), "", url.href);
    } else if (options.history === "replace") {
      window.history.replaceState(historyStateWithScroll(window.history.state, { x: 0, y: 0 }), "", url.href);
    }
    focusBoundary(target);
    restoreNavigationPosition(url, options.scroll || null);
    return true;
  }

  function visit(source, options) {
    options = options || {};
    var url = source instanceof URL ? source : sameOriginURL(source);
    if (!url) {
      hardNavigate(source);
      return Promise.resolve(false);
    }

    // A popstate has already moved history to the destination entry. Replacing
    // state here would overwrite that entry's saved position with the page we
    // are about to leave; ordinary pushes still persist the current entry.
    if (options.history !== "none") saveScroll();
    if (activeVisit) activeVisit.controller.abort();
    var controller = new AbortController();
    var sequence = ++visitSequence;
    activeVisit = { controller: controller, sequence: sequence };

    return window.fetch(url.href, {
      method: "GET",
      credentials: "same-origin",
      redirect: "follow",
      signal: controller.signal,
      headers: {
        "Accept": "text/html, application/xhtml+xml",
        "X-Kitwork-Drive": "1"
      }
    }).then(function (response) {
      if (sequence !== visitSequence) return null;
      var finalURL = sameOriginURL(response.url || url.href);
      if (!response.ok || !responseIsHTML(response) || !finalURL) {
        hardNavigate(response.url || url.href);
        return null;
      }
      return response.text().then(function (sourceText) {
        return { document: parseHTML(sourceText), url: finalURL };
      });
    }).then(function (loaded) {
      if (!loaded || sequence !== visitSequence) return false;
      return commit(loaded.document, loaded.url, {
        history: options.history || "push",
        scroll: options.scroll || null
      });
    }).catch(function (error) {
      if (sequence !== visitSequence || controller.signal.aborted || (error && error.name === "AbortError")) return false;
      if (typeof report === "function") report(error, { lifecycle: "drive", url: url.href });
      hardNavigate(url.href);
      return false;
    }).finally(function () {
      if (activeVisit && activeVisit.sequence === sequence) activeVisit = null;
    });
  }

  function onClick(event) {
    var boundary = boundaryOf(document);
    if (!boundary.valid) return;
    var url = linkFor(event, boundary);
    if (!url) return;
    event.preventDefault();
    visit(url, { history: "push" });
  }

  function onSubmit(event) {
    var boundary = boundaryOf(document);
    if (!boundary.valid) return;
    var url = formFor(event, boundary);
    if (!url) return;
    event.preventDefault();
    visit(url, { history: "push" });
  }

  function onPopState(event) {
    // A queued scroll write belongs to the entry we just left. It must not run
    // after the browser has activated the destination history entry.
    cancelScrollSave();
    var url = sameOriginURL(window.location.href);
    if (!url) return;
    visit(url, { history: "none", scroll: historyScroll(event.state) });
  }

  function start() {
    if (started || !boundaryOf(document).valid || !planOf(document).valid || typeof window.fetch !== "function" ||
      typeof window.DOMParser !== "function" || typeof window.AbortController !== "function") return false;
    started = true;
    if (window.history && "scrollRestoration" in window.history) {
      priorScrollRestoration = window.history.scrollRestoration;
      window.history.scrollRestoration = "manual";
    }
    document.addEventListener("click", onClick);
    document.addEventListener("submit", onSubmit);
    window.addEventListener("popstate", onPopState);
    window.addEventListener("scroll", scheduleScrollSave, { passive: true });
    window.addEventListener("pagehide", saveScroll);
    saveScroll();
    return true;
  }

  function stop() {
    if (!started) return false;
    started = false;
    cancelScrollSave();
    if (activeVisit) activeVisit.controller.abort();
    activeVisit = null;
    visitSequence++;
    document.removeEventListener("click", onClick);
    document.removeEventListener("submit", onSubmit);
    window.removeEventListener("popstate", onPopState);
    window.removeEventListener("scroll", scheduleScrollSave);
    window.removeEventListener("pagehide", saveScroll);
    if (priorScrollRestoration !== null && window.history && "scrollRestoration" in window.history) {
      window.history.scrollRestoration = priorScrollRestoration;
    }
    priorScrollRestoration = null;
    return true;
  }

  core.drive = Object.freeze({
    start: start,
    stop: stop,
    visit: visit,
    validate: validateTransition
  });

  // Lifecycle ownership stays in the private core capsule. Partial subtree
  // disposal deliberately does not stop document-level navigation.
  core.startHooks.push(start);
  core.destroyHooks.push(stop);

})(window, document);
