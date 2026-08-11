; (function (global, document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "morph") throw new Error("KitJS: Drive fragment loaded out of order");
  if (core.reuse) { core.phase = "drive"; return; }
  if (typeof core.morph !== "function" || !Array.isArray(core.startHooks)) {
    throw new Error("KitJS: incomplete Hydrate runtime assembly");
  }

  var profileScript = document.currentScript;
  var profileURL = profileScript && profileScript.src ? absoluteURL(profileScript.src, document.baseURI) : null;
  var profilePlan = profileScript && profileScript.hasAttribute("data-kitwork-plan")
    ? profileScript.getAttribute("data-kitwork-plan")
    : null;
  var profileIntegrity = profileScript && profileScript.hasAttribute("integrity")
    ? profileScript.getAttribute("integrity")
    : null;
  var activeVisit = null;
  var visitSequence = 0;
  var started = false;
  var scrollFrame = 0;
  var NAVIGATION_EVENT = "kit:navigation";
  var ACTIVE_ELEMENTS = {
    applet: true,
    embed: true,
    fencedframe: true,
    frame: true,
    iframe: true,
    object: true,
    portal: true
  };

  function array(value) {
    return Array.prototype.slice.call(value || []);
  }

  function absoluteURL(source, base) {
    try { return new URL(source, base).href; }
    catch (_) { return null; }
  }

  function sameOriginURL(source) {
    var href = absoluteURL(source, document.baseURI);
    if (!href) return null;
    var url = new URL(href);
    if ((url.protocol !== "http:" && url.protocol !== "https:") || url.origin !== global.location.origin) {
      return null;
    }
    return url;
  }

  function emitNavigation(visit, phase, values) {
    values = values || {};
    var detail = {
      id: visit.sequence,
      phase: phase,
      url: String(values.url || visit.url)
    };
    if (phase === "progress") {
      detail.loaded = values.loaded;
      detail.total = values.total;
    } else if (phase === "finish") detail.outcome = values.outcome;
    Object.freeze(detail);

    var event;
    if (typeof global.CustomEvent === "function") {
      event = new global.CustomEvent(NAVIGATION_EVENT, {
        detail: detail,
        bubbles: false,
        cancelable: false,
        composed: false
      });
    } else {
      event = document.createEvent("CustomEvent");
      event.initCustomEvent(NAVIGATION_EVENT, false, false, detail);
    }
    document.dispatchEvent(event);
  }

  function currentVisit(visit) {
    return !!visit && !visit.finished && activeVisit === visit;
  }

  function finishVisit(visit, outcome, url) {
    if (!visit || visit.finished) return false;
    visit.finished = true;
    if (activeVisit === visit) activeVisit = null;
    emitNavigation(visit, "finish", { url: url || visit.url, outcome: outcome });
    return true;
  }

  function cancelVisit(visit) {
    if (!visit || visit.finished) return false;
    try { visit.controller.abort(); } catch (_) { /* AbortController is best effort. */ }
    return finishVisit(visit, "cancelled", visit.url);
  }

  function exactBodyLength(response) {
    if (!response || !response.headers || typeof response.headers.get !== "function") return 0;
    var encoding = String(response.headers.get("content-encoding") || "").trim().toLowerCase();
    if (encoding && encoding !== "identity") return 0;
    var source = String(response.headers.get("content-length") || "").trim();
    if (!/^[1-9][0-9]*$/.test(source)) return 0;
    var total = Number(source);
    return Number.isSafeInteger(total) && total > 0 ? total : 0;
  }

  function responseText(response, visit) {
    var total = exactBodyLength(response);
    var body = response && response.body;
    if (!total || !body || typeof body.getReader !== "function" ||
      typeof global.TextDecoder !== "function") return response.text();

    var decoder;
    var reader;
    try {
      decoder = new global.TextDecoder();
      reader = body.getReader();
    } catch (_) {
      if (reader && typeof reader.releaseLock === "function") reader.releaseLock();
      return response.text();
    }

    var chunks = [];
    var loaded = 0;
    var lastPercent = 0;
    function cancelReader() {
      try {
        var cancelled = reader.cancel();
        if (cancelled && typeof cancelled.catch === "function") cancelled.catch(function () {});
      } catch (_) { /* The visit controller also aborts the stream. */ }
    }
    function read() {
      if (!currentVisit(visit)) {
        cancelReader();
        return Promise.resolve(null);
      }
      return reader.read().then(function (result) {
        if (!currentVisit(visit)) {
          cancelReader();
          return null;
        }
        if (result.done) {
          var tail = decoder.decode();
          if (tail) chunks.push(tail);
          if (typeof reader.releaseLock === "function") {
            try { reader.releaseLock(); } catch (_) { /* Completed readers may already be unlocked. */ }
          }
          return chunks.join("");
        }
        var value = result.value;
        var size = value && Number(value.byteLength);
        if (!Number.isSafeInteger(size) || size < 0) {
          throw new TypeError("KitJS: invalid navigation response chunk");
        }
        loaded += size;
        if (!Number.isSafeInteger(loaded)) {
          throw new TypeError("KitJS: navigation response is too large");
        }
        var text = decoder.decode(value, { stream: true });
        if (text) chunks.push(text);
        if (loaded < total) {
          var percent = Math.floor(loaded / total * 100);
          if (percent > lastPercent) {
            lastPercent = percent;
            emitNavigation(visit, "progress", {
              url: visit.url,
              loaded: loaded,
              total: total
            });
          }
        }
        return read();
      });
    }
    return read();
  }

  function incomingBase(incoming, responseURL) {
    var base = incoming.head && incoming.head.querySelector("base[href]");
    return base ? absoluteURL(base.getAttribute("href"), responseURL) || responseURL : responseURL;
  }

  function compatibleBase(incoming, responseURL) {
    if (!document.head || !incoming.head) return false;
    var currentHref = document.head.querySelector("base[href]");
    var nextHref = incoming.head.querySelector("base[href]");
    if (!!currentHref !== !!nextHref) return false;
    if (currentHref) {
      var currentURL = absoluteURL(currentHref.getAttribute("href"), global.location.href);
      var nextURL = absoluteURL(nextHref.getAttribute("href"), responseURL);
      if (!currentURL || !nextURL || currentURL !== nextURL) return false;
    }

    var currentTarget = document.head.querySelector("base[target]");
    var nextTarget = incoming.head.querySelector("base[target]");
    if (!!currentTarget !== !!nextTarget) return false;
    return !currentTarget || currentTarget.getAttribute("target") === nextTarget.getAttribute("target");
  }

  function hasActiveDocumentContent(root) {
    if (!root) return false;
    if (root.nodeType === 1) {
      var name = String(root.localName || "").toLowerCase();
      if (ACTIVE_ELEMENTS[name] || name === "meta" &&
        String(root.getAttribute("http-equiv") || "").trim().toLowerCase() === "refresh") {
        return true;
      }
      if (name === "template" && root.content && hasActiveDocumentContent(root.content)) return true;
    }
    var child = root.firstChild;
    while (child) {
      if (hasActiveDocumentContent(child)) return true;
      child = child.nextSibling;
    }
    return false;
  }

  function sameProfile(incoming, responseURL) {
    if (!profileURL || !incoming || !incoming.body) return false;
    var base = incomingBase(incoming, responseURL);
    var matches = array(incoming.querySelectorAll("script[src]")).filter(function (script) {
      if (absoluteURL(script.getAttribute("src"), base) !== profileURL) return false;
      var type = String(script.getAttribute("type") || "").trim().toLowerCase();
      if (script.hasAttribute("nomodule") || type && type !== "text/javascript" && type !== "application/javascript") {
        return false;
      }
      if (profileIntegrity !== null && (!script.hasAttribute("integrity") ||
        script.getAttribute("integrity") !== profileIntegrity)) return false;
      return profilePlan === null || script.hasAttribute("data-kitwork-plan") &&
        script.getAttribute("data-kitwork-plan") === profilePlan;
    });
    return matches.length === 1;
  }

  function collectComponents(root, output) {
    if (!root) return;
    if (root.nodeType === 1 && (root.hasAttribute("data-kit-component") ||
      root.hasAttribute("data-kit-version"))) output.push(root);
    if (!root.querySelectorAll) return;
    array(root.querySelectorAll("[data-kit-component],[data-kit-version]")).forEach(function (element) {
      output.push(element);
    });
    array(root.querySelectorAll("template")).forEach(function (template) {
      if (template.content) collectComponents(template.content, output);
    });
  }

  function knownComponents(incoming) {
    if (!incoming || !incoming.body || !core.registry || typeof core.registry.has !== "function" ||
      typeof core.componentMetadata !== "function") return false;
    var components = [];
    collectComponents(incoming.body, components);
    return components.every(function (element) {
      var request = core.componentMetadata(element, false);
      return !!request && core.registry.has(request.name);
    });
  }

  function compatibleRetains(incoming) {
    if (!incoming || !incoming.body || !document.body ||
      typeof core.validateMorphRetains !== "function") return false;
    try {
      core.validateMorphRetains(document.body, incoming.body);
      return true;
    } catch (error) {
      core.report(error);
      return false;
    }
  }

  function disabled(element) {
    var node = element;
    while (node && node.nodeType === 1) {
      if (node.hasAttribute("data-kit-drive") &&
        String(node.getAttribute("data-kit-drive") || "").trim().toLowerCase() === "false") return true;
      if (node === document.body) break;
      node = node.parentElement;
    }
    return false;
  }

  function eventElement(event) {
    var target = event.target;
    return target && target.nodeType === 1 ? target : target && target.parentElement;
  }

  function eligibleLink(event) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey ||
      event.shiftKey || event.altKey || !document.body) return null;
    var origin = eventElement(event);
    var link = origin && origin.closest ? origin.closest("a[href],area[href]") : null;
    if (!link || !document.body.contains(link) || disabled(link) || link.hasAttribute("download")) return null;
    var target = String(link.getAttribute("target") || "").toLowerCase();
    if (target && target !== "_self") return null;
    if ((" " + String(link.getAttribute("rel") || "").toLowerCase() + " ").indexOf(" external ") >= 0) {
      return null;
    }
    var url = sameOriginURL(link.getAttribute("href"));
    if (!url) return null;
    if (url.pathname === global.location.pathname && url.search === global.location.search && url.hash) {
      return null;
    }
    return url;
  }

  function formURL(form, submitter) {
    var method = submitter && submitter.getAttribute("formmethod") || form.getAttribute("method") || "get";
    if (String(method).toLowerCase() !== "get") return null;
    var target = submitter && submitter.getAttribute("formtarget") || form.getAttribute("target") || "";
    target = String(target).toLowerCase();
    if (target && target !== "_self") return null;
    var action = submitter && submitter.getAttribute("formaction") || form.getAttribute("action") || global.location.href;
    var url = sameOriginURL(action);
    if (!url) return null;

    var values;
    try { values = submitter ? new FormData(form, submitter) : new FormData(form); }
    catch (_) {
      values = new FormData(form);
      if (submitter && submitter.name && !submitter.disabled) values.append(submitter.name, submitter.value);
    }
    url.search = "";
    values.forEach(function (value, name) {
      if (typeof value === "string") url.searchParams.append(name, value);
    });
    return url;
  }

  function eligibleForm(event) {
    if (event.defaultPrevented || !document.body) return null;
    var form = event.target;
    if (!form || String(form.localName || "").toLowerCase() !== "form" ||
      !document.body.contains(form) || disabled(form)) return null;
    var submitter = event.submitter || null;
    if (submitter && disabled(submitter)) return null;
    return formURL(form, submitter);
  }

  function scrollPosition() {
    return {
      x: Number(global.scrollX || global.pageXOffset || 0),
      y: Number(global.scrollY || global.pageYOffset || 0)
    };
  }

  function historyState(state, position) {
    var next = Object.create(null);
    if (state && typeof state === "object") {
      Object.keys(state).forEach(function (key) { next[key] = state[key]; });
    }
    next.__kitjs_drive__ = { scroll: position };
    return next;
  }

  function saveScroll() {
    if (!global.history || typeof global.history.replaceState !== "function") return;
    try {
      global.history.replaceState(historyState(global.history.state, scrollPosition()), "", global.location.href);
    } catch (_) { /* History is unavailable for some opaque documents. */ }
  }

  function scheduleScrollSave() {
    if (scrollFrame || activeVisit) return;
    var frame = global.requestAnimationFrame || function (callback) { return global.setTimeout(callback, 0); };
    scrollFrame = frame(function () {
      scrollFrame = 0;
      saveScroll();
    });
  }

  function cancelScrollSave() {
    if (!scrollFrame) return;
    if (typeof global.cancelAnimationFrame === "function") global.cancelAnimationFrame(scrollFrame);
    else global.clearTimeout(scrollFrame);
    scrollFrame = 0;
  }

  function savedScroll(state) {
    var value = state && state.__kitjs_drive__ && state.__kitjs_drive__.scroll;
    if (!value || !Number.isFinite(Number(value.x)) || !Number.isFinite(Number(value.y))) return null;
    return { x: Number(value.x), y: Number(value.y) };
  }

  function hashTarget(hash) {
    if (!hash) return null;
    var id;
    try { id = decodeURIComponent(hash.slice(1)); }
    catch (_) { id = hash.slice(1); }
    if (!id) return document.documentElement;
    return document.getElementById(id) || (document.getElementsByName(id)[0] || null);
  }

  function focusRoute(url) {
    var target = hashTarget(url.hash) || document.querySelector("[autofocus],main,[role='main'],h1") || document.body;
    if (!target || typeof target.focus !== "function") return;
    var temporary = !target.hasAttribute("tabindex") && !/^(a|button|input|select|textarea)$/i.test(target.localName || "");
    if (temporary) target.setAttribute("tabindex", "-1");
    try { target.focus({ preventScroll: true }); }
    catch (_) { try { target.focus(); } catch (_) { /* Non-focusable browser host. */ } }
  }

  function restoreScroll(url, position) {
    var frame = global.requestAnimationFrame || function (callback) { return global.setTimeout(callback, 0); };
    frame(function () {
      if (position) {
        try { global.scrollTo(position.x, position.y); } catch (_) { /* Non-visual browser. */ }
        return;
      }
      var target = hashTarget(url.hash);
      if (target && typeof target.scrollIntoView === "function") target.scrollIntoView();
      else {
        try { global.scrollTo(0, 0); } catch (_) { /* Non-visual browser. */ }
      }
    });
  }

  function hardNavigate(source) {
    global.location.assign(String(source));
  }

  function fallbackVisit(visit, source, outcome, error) {
    source = String(source || visit.url);
    if (error) core.report(error);
    finishVisit(visit, outcome, source);
    try { hardNavigate(source); }
    catch (navigationError) { core.report(navigationError); }
    return false;
  }

  function isHTML(response) {
    var type = response.headers && response.headers.get
      ? String(response.headers.get("content-type") || "").toLowerCase()
      : "";
    return type.indexOf("text/html") >= 0 || type.indexOf("application/xhtml+xml") >= 0;
  }

  function safeHeadNode(node) {
    var name = String(node.localName || "").toLowerCase();
    if (name === "meta") return !node.hasAttribute("http-equiv") &&
      (node.hasAttribute("name") || node.hasAttribute("property"));
    if (name === "style") return node.hasAttribute("data-kitwork-jit") || node.hasAttribute("data-kit-head");
    if (name !== "link") return false;
    var allowed = {
      alternate: true, canonical: true, icon: true, manifest: true,
      stylesheet: true, "apple-touch-icon": true, "mask-icon": true
    };
    var relations = String(node.getAttribute("rel") || "").toLowerCase().split(/\s+/).filter(Boolean);
    return relations.length > 0 && relations.every(function (relation) { return allowed[relation] === true; });
  }

  function safeHeadClone(source, base, nonce) {
    var clone = document.importNode ? document.importNode(source, true) : source.cloneNode(true);
    array(clone.attributes).forEach(function (attribute) {
      if (/^on/i.test(attribute.name) || String(attribute.name).toLowerCase() === "srcdoc") {
        clone.removeAttribute(attribute.name);
      }
    });
    if (String(clone.localName || "").toLowerCase() === "link" && clone.hasAttribute("href")) {
      var href = absoluteURL(clone.getAttribute("href"), base);
      if (!href || /^(javascript|vbscript):/i.test(href)) return null;
      clone.setAttribute("href", href);
    }
    if (String(clone.localName || "").toLowerCase() === "style" && nonce !== null) clone.nonce = nonce;
    return clone;
  }

  function headSignature(node) {
    return String(node.outerHTML || "");
  }

  function reconcileHead(incoming, responseURL) {
    if (!document.head || !incoming.head) return;
    var base = incomingBase(incoming, responseURL);
    var nonceNode = document.head.querySelector("script[nonce],style[nonce]");
    var nonce = nonceNode ? String(nonceNode.nonce || nonceNode.getAttribute("nonce") || "") : null;
    var current = array(document.head.children).filter(safeHeadNode);
    var bySignature = new Map();
    current.forEach(function (node) {
      var signature = headSignature(node);
      if (!bySignature.has(signature)) bySignature.set(signature, []);
      bySignature.get(signature).push(node);
    });
    var used = new Set();
    var anchor = document.head.querySelector("script") || null;
    array(incoming.head.children).filter(safeHeadNode).forEach(function (source) {
      var clone = safeHeadClone(source, base, nonce);
      if (!clone) return;
      var signature = headSignature(clone);
      var candidates = bySignature.get(signature);
      var node = candidates && candidates.length ? candidates.shift() : clone;
      used.add(node);
      document.head.insertBefore(node, anchor);
    });
    current.forEach(function (node) {
      if (!used.has(node) && node.parentNode === document.head) document.head.removeChild(node);
    });
  }

  function reconcileDocumentAttributes(incoming) {
    ["lang", "dir"].forEach(function (name) {
      if (incoming.documentElement.hasAttribute(name)) {
        document.documentElement.setAttribute(name, incoming.documentElement.getAttribute(name));
      } else document.documentElement.removeAttribute(name);
    });
  }

  function commit(incoming, url, options) {
    // Artifact/plan compatibility is complete before title, head, history, or
    // the live body can be changed.
    if (!sameProfile(incoming, url.href) || !knownComponents(incoming) || !compatibleRetains(incoming) ||
      !compatibleBase(incoming, url.href) || hasActiveDocumentContent(incoming.documentElement)) {
      return false;
    }

    if (options.leavingScroll) {
      global.history.replaceState(
        historyState(global.history.state, options.leavingScroll),
        "",
        global.location.href
      );
    }
    // Advance the document URL only after every compatibility check, but before
    // inserting relative body resources so they resolve against the new route.
    if (options.history === "push") {
      global.history.pushState(historyState(null, { x: 0, y: 0 }), "", url.href);
    } else if (options.history === "replace") {
      global.history.replaceState(historyState(global.history.state, { x: 0, y: 0 }), "", url.href);
    }
    reconcileHead(incoming, url.href);
    reconcileDocumentAttributes(incoming);
    core.morph(document.body, incoming.body);
    if (document.title !== incoming.title) document.title = incoming.title;
    focusRoute(url);
    restoreScroll(url, options.scroll || null);
    return true;
  }

  function visit(source, options) {
    options = options || {};
    var url = source instanceof URL ? source : sameOriginURL(source);
    if (!url) {
      hardNavigate(source);
      return Promise.resolve(false);
    }
    cancelScrollSave();
    var leavingScroll = options.history !== "none" ? scrollPosition() : null;
    if (activeVisit) cancelVisit(activeVisit);
    var controller = new AbortController();
    var sequence = ++visitSequence;
    var visitRecord = {
      controller: controller,
      sequence: sequence,
      url: url.href,
      finished: false
    };
    activeVisit = visitRecord;
    try { emitNavigation(visitRecord, "start"); }
    catch (error) {
      return Promise.resolve(fallbackVisit(visitRecord, url.href, "error", error));
    }
    if (!currentVisit(visitRecord)) {
      if (!visitRecord.finished) finishVisit(visitRecord, "cancelled", visitRecord.url);
      return Promise.resolve(false);
    }

    var request;
    try {
      request = global.fetch(url.href, {
        method: "GET",
        credentials: "same-origin",
        redirect: "follow",
        signal: controller.signal,
        headers: {
          "Accept": "text/html, application/xhtml+xml",
          "X-KitJS-Drive": "1"
        }
      });
    } catch (error) {
      return Promise.resolve(fallbackVisit(visitRecord, url.href, "error", error));
    }

    return Promise.resolve(request).then(function (response) {
      if (!currentVisit(visitRecord)) return null;
      var finalURL = sameOriginURL(response.url || url.href);
      if (!response.ok || !isHTML(response) || !finalURL) {
        fallbackVisit(visitRecord, response.url || url.href, "fallback");
        return null;
      }
      visitRecord.url = finalURL.href;
      return responseText(response, visitRecord).then(function (sourceText) {
        if (sourceText === null || !currentVisit(visitRecord)) return null;
        return {
          document: new DOMParser().parseFromString(sourceText, "text/html"),
          url: finalURL
        };
      });
    }).then(function (loaded) {
      if (!loaded || !currentVisit(visitRecord)) return false;
      var committed = commit(loaded.document, loaded.url, {
        history: options.history || "push",
        scroll: options.scroll || null,
        leavingScroll: leavingScroll
      });
      if (!committed) return fallbackVisit(visitRecord, loaded.url.href, "fallback");
      finishVisit(visitRecord, "loaded", loaded.url.href);
      return true;
    }).catch(function (error) {
      if (visitRecord.finished) return false;
      if (controller.signal.aborted || error && error.name === "AbortError") {
        finishVisit(visitRecord, "cancelled", visitRecord.url);
        return false;
      }
      return fallbackVisit(visitRecord, visitRecord.url, "error", error);
    }).finally(function () {
      if (!visitRecord.finished) {
        finishVisit(visitRecord, controller.signal.aborted ? "cancelled" : "error", visitRecord.url);
      }
    });
  }

  function onClick(event) {
    var url = eligibleLink(event);
    if (!url) return;
    event.preventDefault();
    visit(url, { history: "push" });
  }

  function onSubmit(event) {
    var url = eligibleForm(event);
    if (!url) return;
    event.preventDefault();
    visit(url, { history: "push" });
  }

  function onPopState(event) {
    cancelScrollSave();
    var url = sameOriginURL(global.location.href);
    if (url) visit(url, { history: "none", scroll: savedScroll(event.state) });
  }

  function onPageHide() {
    saveScroll();
    if (activeVisit) cancelVisit(activeVisit);
  }

  function start() {
    if (started || !profileURL || typeof global.fetch !== "function" ||
      typeof global.DOMParser !== "function" || typeof global.AbortController !== "function" ||
      !global.history || typeof global.history.pushState !== "function") return false;
    started = true;
    if ("scrollRestoration" in global.history) global.history.scrollRestoration = "manual";
    document.addEventListener("click", onClick);
    document.addEventListener("submit", onSubmit);
    global.addEventListener("popstate", onPopState);
    global.addEventListener("scroll", scheduleScrollSave, { passive: true });
    global.addEventListener("pagehide", onPageHide);
    saveScroll();
    return true;
  }

  core.startHooks.push(start);
  core.phase = "drive";
})(globalThis, document);
