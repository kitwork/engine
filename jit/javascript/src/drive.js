; (function (global, document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "morph") throw new Error("KitJS: Drive fragment loaded out of order");
  if (core.reuse) { core.phase = "drive"; return; }
  if (typeof core.morph !== "function" || !Array.isArray(core.startHooks)) {
    throw new Error("KitJS: incomplete Hydrate runtime assembly");
  }

  var profileScript = document.currentScript;
  var stagedProfile = profileScript && profileScript.getAttribute("data-kitwork-jit") === "hydrate";
  var profileURL = profileScript && profileScript.src ? absoluteURL(profileScript.src, document.baseURI) : null;
  var profilePlan = profileScript && profileScript.hasAttribute("data-kitwork-plan")
    ? profileScript.getAttribute("data-kitwork-plan")
    : null;
  var HANDOFF = Symbol.for("kitjs:handoff");
  var activeVisit = null;
  var navigationCritical = false;
  var navigationTerminal = false;
  var deferredNavigation = null;
  var visitSequence = 0;
  var started = false;
  var scrollTimer = 0;
  var lastSavedScroll = null;
  var lastSavedURL = "";
  var documentPath = global.location.pathname;
  var documentSearch = global.location.search;
  var SCROLL_SAVE_DELAY = 250;
  var HANDOFF_LOAD_TIMEOUT = 10000;
  var HANDOFF_GRAPH_CACHE_LIMIT = 32;
  var NAVIGATION_EVENT = "kit:navigation";
  var THEME_PREPAINT_SOURCE = '(function(){var r=document.documentElement,c=r.classList,m="system";try{var t=localStorage.getItem("theme");t=t&&t.toLowerCase();if(t==="light"||t==="dark"||t==="system")m=t}catch(e){}if(m==="system"){try{m=typeof matchMedia==="function"&&matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light"}catch(e){m="light"}}if(m==="dark")c.add("dark");else c.remove("dark");try{r.style.colorScheme=m}catch(e){}})();';
  var handoffGraphs = new Map();
  var engineHandoffScripts = new WeakSet();
  var liveStagedScripts = null;
  var liveStagedSignatures = null;
  var liveThemePrepaint = null;
  var liveThemePrepaintSignature = null;
  var liveThemePrepaintCaptured = false;
  var liveExecutableTopology = null;
  var driveDisabledWarning = false;
  var ACTIVE_ELEMENTS = {
    applet: true,
    embed: true,
    fencedframe: true,
    frame: true,
    iframe: true,
    object: true,
    portal: true
  };
  var STAGED_ROLES = {
    runtime: true,
    hydrate: true,
    graph: true,
    service: true,
    component: true,
    components: true
  };
  var STAGED_SCRIPT_ATTRIBUTES = {
    "data-kitwork-jit": true,
    "data-kitwork-hash": true,
    src: true,
    integrity: true,
    crossorigin: true,
    defer: true
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
    if (visit.handoff && typeof visit.handoff.cancel === "function") {
      try { visit.handoff.cancel(); } catch (_) { /* Handoff cancellation is best effort. */ }
    }
    try { visit.controller.abort(); } catch (_) { /* AbortController is best effort. */ }
    return finishVisit(visit, "cancelled", visit.url);
  }

  function copiedVisitOptions(options) {
    options = options || {};
    var copied = {};
    if (Object.prototype.hasOwnProperty.call(options, "history")) copied.history = options.history;
    if (options.scroll) copied.scroll = { x: options.scroll.x, y: options.scroll.y };
    return copied;
  }

  function settleNavigation(intent, result, error) {
    if (!intent) return;
    if (error && typeof intent.reject === "function") intent.reject(error);
    else if (typeof intent.resolve === "function") intent.resolve(result);
  }

  function discardDeferredNavigation() {
    var intent = deferredNavigation;
    deferredNavigation = null;
    settleNavigation(intent, false);
  }

  function queueNavigation(intent) {
    var previous = deferredNavigation;
    deferredNavigation = intent;
    settleNavigation(previous, false);
  }

  function deferVisit(url, options) {
    return new Promise(function (resolve, reject) {
      queueNavigation({
        kind: "visit",
        source: url.href,
        options: copiedVisitOptions(options),
        resolve: resolve,
        reject: reject
      });
    });
  }

  function queueNativeAssign(source, resolve, reject) {
    queueNavigation({
      kind: "assign",
      source: String(source),
      resolve: resolve || null,
      reject: reject || null
    });
  }

  function queueScrollRestore(url, position) {
    queueNavigation({
      kind: "restore",
      source: url.href,
      position: position ? { x: position.x, y: position.y } : null
    });
  }

  function beginNavigationCritical() {
    if (navigationCritical) return false;
    navigationCritical = true;
    navigationTerminal = false;
    return true;
  }

  function terminateNavigationCritical() {
    navigationTerminal = true;
    discardDeferredNavigation();
  }

  function executeNavigationIntent(intent) {
    if (!intent) return;
    if (intent.kind === "visit") {
      var result;
      try { result = visit(intent.source, intent.options); }
      catch (error) {
        settleNavigation(intent, false, error);
        return;
      }
      Promise.resolve(result).then(function (loaded) {
        settleNavigation(intent, loaded);
      }, function (error) {
        settleNavigation(intent, false, error);
      });
      return;
    }

    if (activeVisit) {
      // Seed the native intent before cancellation. A synchronous finish
      // listener may replace it, and the latest authored intent must win.
      beginNavigationCritical();
      queueNavigation(intent);
      cancelVisit(activeVisit);
      endNavigationCritical(true);
      return;
    }

    if (intent.kind === "restore") {
      var url = sameOriginURL(intent.source);
      if (url) {
        if (intent.position) rememberScroll(intent.position);
        restoreScroll(url, intent.position);
      }
      settleNavigation(intent, true);
      return;
    }

    try {
      flushScrollSave();
      hardNavigate(intent.source);
      settleNavigation(intent, false);
    } catch (error) {
      core.report(error);
      settleNavigation(intent, false, error);
    }
  }

  function endNavigationCritical(drain) {
    if (!navigationCritical) return;
    var intent = drain && !navigationTerminal ? deferredNavigation : null;
    if (!intent) discardDeferredNavigation();
    else deferredNavigation = null;
    navigationCritical = false;
    navigationTerminal = false;
    if (intent) executeNavigationIntent(intent);
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
    if (stagedProfile) return sameStagedDelivery(incoming, responseURL);
    if (!profileURL || !incoming || !incoming.body) return false;
    var current = standaloneProfileScript(document, document.baseURI, true);
    var next = standaloneProfileScript(incoming, incomingBase(incoming, responseURL), false);
    return !!current && !!next && current.signature === next.signature;
  }

  function metaContentSecurityPolicies(head) {
    if (!head || !head.querySelectorAll) return [];
    return array(head.querySelectorAll("meta[http-equiv]")).filter(function (meta) {
      return meta.parentNode === head &&
        String(meta.getAttribute("http-equiv") || "").trim().toLowerCase() === "content-security-policy";
    }).map(function (meta) {
      return String(meta.getAttribute("content") || "");
    });
  }

  function compatibleContentSecurityPolicy(incoming) {
    if (!incoming || !incoming.head || !document.head) return false;
    var current = metaContentSecurityPolicies(document.head);
    var next = metaContentSecurityPolicies(incoming.head);
    if (current.length !== next.length) return false;
    for (var index = 0; index < current.length; index++) {
      if (current[index] !== next[index]) return false;
    }
    return true;
  }

  function hasContentSecurityPolicyHeader(response) {
    if (!response || !response.headers || typeof response.headers.get !== "function") return false;
    return !!(String(response.headers.get("content-security-policy") || "").trim() ||
      String(response.headers.get("content-security-policy-report-only") || "").trim());
  }

  function executableScriptKind(script) {
    var type = String(script.getAttribute("type") || "").trim().toLowerCase();
    var essence = type.split(";", 1)[0].trim();
    if (essence === "module" || essence === "importmap" || essence === "speculationrules") return essence;
    if (!essence || essence.indexOf("javascript") >= 0 || essence.indexOf("ecmascript") >= 0 ||
      essence === "text/jscript" || essence === "text/livescript") return "classic";
    return null;
  }

  function validIntegrityMetadata(source) {
    if (typeof global.atob !== "function" || typeof global.btoa !== "function") return false;
    var tokens = String(source || "").trim().split(/\s+/).filter(Boolean);
    if (!tokens.length) return false;
    return tokens.every(function (token) {
      var match = /^(sha256|sha384|sha512)-([A-Za-z0-9+/]+={0,2})$/.exec(token);
      if (!match) return false;
      try {
        var bytes = global.atob(match[2]);
        var length = match[1] === "sha256" ? 32 : match[1] === "sha384" ? 48 : 64;
        return bytes.length === length && global.btoa(bytes) === match[2];
      } catch (_) { return false; }
    });
  }

  function scriptDrivePolicy(script) {
    if (!script || !script.hasAttribute("data-kit-drive")) return "";
    var value = String(script.getAttribute("data-kit-drive") || "").trim().toLowerCase();
    return value === "stable" ? value : null;
  }

  function sameOriginScriptSource(source) {
    if (!source) return false;
    try {
      var url = new URL(source);
      return (url.protocol === "http:" || url.protocol === "https:") &&
        url.origin === global.location.origin;
    } catch (_) { return false; }
  }

  function compatibleScriptIdentity(script, source) {
    var policy = scriptDrivePolicy(script);
    if (policy === null) return false;
    if (policy === "stable" && !sameOriginScriptSource(source)) return false;
    if (script.hasAttribute("integrity")) {
      return validIntegrityMetadata(script.getAttribute("integrity"));
    }
    return policy === "stable";
  }

  function warnDriveDisabled() {
    if (!driveDisabledWarning && global.console && typeof global.console.warn === "function") {
      driveDisabledWarning = true;
      global.console.warn("KitJS Drive: disabled because the initial executable script topology is incompatible");
    }
    return false;
  }

  function scriptAttributeSignature(script) {
    var attributes = [];
    var valid = true;
    array(script && script.attributes).forEach(function (attribute) {
      var name = String(attribute.name || "").toLowerCase();
      if (/^on/.test(name)) valid = false;
      attributes.push(name + "=" + String(attribute.value || ""));
    });
    if (!valid) return null;
    attributes.sort();
    return attributes.join("\n");
  }

  function themePrepaintCandidate(root) {
    if (!root || !root.head || !root.querySelectorAll) return undefined;
    var marked = array(root.querySelectorAll("[data-kitwork-jit]")).filter(function (node) {
      return String(node.getAttribute("data-kitwork-jit") || "").trim().toLowerCase() === "theme";
    });
    if (!marked.length) return null;
    if (marked.length !== 1) return undefined;
    var script = marked[0];
    var attributes = array(script.attributes);
    if (String(script.localName || "").toLowerCase() !== "script" || script.parentNode !== root.head ||
      attributes.length !== 1 || String(attributes[0].name || "").toLowerCase() !== "data-kitwork-jit" ||
      script.getAttribute("data-kitwork-jit") !== "theme" || executableScriptKind(script) !== "classic" ||
      String(script.textContent || "") !== THEME_PREPAINT_SOURCE) return undefined;
    var signature = scriptAttributeSignature(script);
    if (signature === null) return undefined;
    return {
      node: script,
      signature: "prepaint\n" + signature + "\ntext=" + String(script.textContent || "")
    };
  }

  function captureLiveThemePrepaint() {
    var candidate = themePrepaintCandidate(document);
    if (candidate === undefined) return false;
    liveThemePrepaint = candidate && candidate.node;
    liveThemePrepaintSignature = candidate && candidate.signature;
    liveThemePrepaintCaptured = true;
    return true;
  }

  function themePrepaintForTopology(root) {
    var candidate = themePrepaintCandidate(root);
    if (candidate === undefined) return undefined;
    if (root !== document) return candidate;
    if (!liveThemePrepaintCaptured || !!candidate !== !!liveThemePrepaint) return undefined;
    if (candidate && (candidate.node !== liveThemePrepaint ||
      candidate.signature !== liveThemePrepaintSignature)) return undefined;
    return candidate;
  }

  function standaloneProfileScript(root, base, current) {
    if (!root || !root.querySelectorAll || !profileURL) return null;
    var matches = array(root.querySelectorAll("script[src]")).filter(function (script) {
      return absoluteURL(script.getAttribute("src"), base) === profileURL;
    });
    if (matches.length !== 1 || current && matches[0] !== profileScript) return null;
    var script = matches[0];
    if (!script.parentNode || script.parentNode !== root.head ||
      executableScriptKind(script) !== "classic" || !script.hasAttribute("defer") ||
      script.hasAttribute("async") || script.hasAttribute("nomodule") ||
      String(script.textContent || "")) return null;
    if (!compatibleScriptIdentity(script, profileURL)) return null;
    var attributes = scriptAttributeSignature(script);
    if (attributes === null) return null;
    return {
      node: script,
      signature: "profile\nurl=" + profileURL + "\n" + attributes
    };
  }

  function stableHeadScriptSignature(script, base) {
    if (!script || !script.parentNode || script.parentNode !== script.ownerDocument.head ||
      executableScriptKind(script) !== "classic" || !script.hasAttribute("src") ||
      !script.hasAttribute("defer") || script.hasAttribute("async") ||
      script.hasAttribute("nomodule") || String(script.textContent || "")) return null;
    var source = absoluteURL(script.getAttribute("src"), base);
    if (!source) return null;
    if (!compatibleScriptIdentity(script, source)) return null;
    var attributes = scriptAttributeSignature(script);
    if (attributes === null) return null;
    return "url=" + source + "\n" + attributes;
  }

  function stagedScriptSignature(script) {
    var attributes = scriptAttributeSignature(script);
    return attributes === null ? null : String(script.localName || "").toLowerCase() + "\n" +
      attributes + "\ntext=" + String(script.textContent || "");
  }

  function stagedRole(source) {
    var role = String(source || "").trim().toLowerCase();
    return STAGED_ROLES[role] ? role : "";
  }

  function reservedStagedNode(node) {
    if (!node || node.nodeType !== 1) return false;
    if (engineHandoffScripts.has(node)) return false;
    if (node.hasAttribute("data-kitwork-hash") || node.hasAttribute("data-kitwork-runtime") ||
      node.hasAttribute("data-kitwork-handoff")) return true;
    return String(node.localName || "").toLowerCase() === "script" &&
      !!stagedRole(node.getAttribute("data-kitwork-jit"));
  }

  function stagedReservedNodes(root) {
    if (!root || !root.querySelectorAll) return [];
    return array(root.querySelectorAll(
      "[data-kitwork-hash],[data-kitwork-runtime],[data-kitwork-handoff],[data-kitwork-jit]"
    )).filter(reservedStagedNode);
  }

  function exactStagedScriptAttributes(script) {
    var attributes = array(script && script.attributes);
    if (attributes.length !== 6) return false;
    for (var index = 0; index < attributes.length; index++) {
      if (!STAGED_SCRIPT_ATTRIBUTES[String(attributes[index].name || "").toLowerCase()]) return false;
    }
    return Object.keys(STAGED_SCRIPT_ATTRIBUTES).every(function (name) {
      return script.hasAttribute(name);
    });
  }

  function captureLiveStagedDelivery() {
    var candidate = stagedCandidate(document, global.location.href);
    if (!candidate || !sameCandidateDelivery(candidate, core.delivery)) return false;
    var signatures = candidate.scripts.map(stagedScriptSignature);
    if (signatures.some(function (signature) { return signature === null; })) return false;
    liveStagedScripts = candidate.scripts.slice();
    liveStagedSignatures = signatures.slice();
    return true;
  }

  function stagedScriptsForTopology(root, base) {
    var candidate = stagedCandidate(root, base);
    if (!candidate) return null;
    if (root !== document) return candidate.scripts;
    if (!liveStagedScripts || !liveStagedSignatures) return null;
    var signatures = candidate.scripts.map(stagedScriptSignature);
    if (signatures.some(function (signature) { return signature === null; })) return null;
    if (candidate.scripts.length !== liveStagedScripts.length ||
      candidate.scripts.some(function (script, index) {
        return script !== liveStagedScripts[index] || signatures[index] !== liveStagedSignatures[index];
      })) return null;
    return candidate.scripts;
  }

  function executableScriptTopology(root, base, current) {
    if (!root || !root.querySelectorAll) return null;
    var themePrepaint = themePrepaintForTopology(root);
    if (themePrepaint === undefined) return null;
    var managed;
    var marker;
    if (stagedProfile) {
      managed = stagedScriptsForTopology(root, base);
      marker = "managed=staged";
    } else {
      var profile = standaloneProfileScript(root, base, current);
      managed = profile ? [profile.node] : null;
      marker = profile && profile.signature;
    }
    if (!managed || !marker) return null;
    var managedSet = new Set(managed);
    var managedSeen = 0;
    var markerWritten = false;
    var managedBlockClosed = false;
    var signatures = [];
    var scripts = array(root.querySelectorAll("script"));
    for (var index = 0; index < scripts.length; index++) {
      var script = scripts[index];
      if (engineHandoffScripts.has(script)) continue;
      if (managedSet.has(script)) {
        if (managedBlockClosed) return null;
        managedSeen++;
        if (!markerWritten) {
          signatures.push(marker);
          markerWritten = true;
        }
        continue;
      }
      if (themePrepaint && script === themePrepaint.node) {
        if (markerWritten) managedBlockClosed = true;
        signatures.push(themePrepaint.signature);
        continue;
      }
      if (!executableScriptKind(script)) continue;
      if (markerWritten) managedBlockClosed = true;
      var signature = stableHeadScriptSignature(script, base);
      if (!signature) return null;
      signatures.push("authored\n" + signature);
    }
    return managedSeen === managed.length ? signatures : null;
  }

  function compatibleExecutableScripts(incoming, responseURL) {
    if (!incoming || !incoming.head || !document.head) return false;
    var current = executableScriptTopology(document, document.baseURI, true);
    var next = executableScriptTopology(incoming, incomingBase(incoming, responseURL), false);
    if (!liveExecutableTopology || !current || !next || current.length !== liveExecutableTopology.length ||
      current.length !== next.length) return false;
    for (var index = 0; index < current.length; index++) {
      if (current[index] !== liveExecutableTopology[index] || current[index] !== next[index]) return false;
    }
    return true;
  }

  function sameStagedDelivery(incoming, responseURL) {
    var delivery = core.delivery;
    if (!incoming || !incoming.body || !delivery || delivery.profile !== "hydrate" ||
      !Array.isArray(delivery.assets) || !delivery.graphHash) return false;
    var scripts = array(incoming.querySelectorAll(
      "script[data-kitwork-hash],script[data-kitwork-jit=\"runtime\"]," +
      "script[data-kitwork-jit=\"hydrate\"],script[data-kitwork-jit=\"graph\"]," +
      "script[data-kitwork-jit=\"service\"],script[data-kitwork-jit=\"component\"]," +
      "script[data-kitwork-jit=\"components\"]"
    ));
    if (scripts.length !== delivery.assets.length) return false;
    for (var index = 0; index < delivery.assets.length; index++) {
      var script = scripts[index];
      var asset = delivery.assets[index];
      var expectedSource = "/jit/" + asset.name;
      var rawSource = script.getAttribute("src");
      var type = String(script.getAttribute("type") || "").trim().toLowerCase();
      if (script.getAttribute("data-kitwork-jit") !== asset.role ||
        script.getAttribute("data-kitwork-hash") !== asset.hash ||
        rawSource !== expectedSource || absoluteURL(rawSource, responseURL) !== asset.url ||
        script.getAttribute("integrity") !== asset.integrity ||
        script.getAttribute("crossorigin") !== "anonymous" ||
        !script.hasAttribute("defer") || script.hasAttribute("async") ||
        script.hasAttribute("data-kitwork-handoff") || script.hasAttribute("nomodule") ||
        type && type !== "text/javascript" &&
        type !== "application/javascript") return false;
    }
    return true;
  }

  function stagedIntegrity(hash) {
    if (!/^[0-9a-f]{64}$/.test(String(hash || "")) || typeof global.btoa !== "function") return null;
    var binary = "";
    for (var index = 0; index < hash.length; index += 2) {
      binary += String.fromCharCode(parseInt(hash.slice(index, index + 2), 16));
    }
    return "sha256-" + global.btoa(binary);
  }

  function stagedCandidate(incoming, responseURL) {
    if (!stagedProfile || !incoming || !incoming.body || !incoming.querySelectorAll) return null;
    var scripts = stagedReservedNodes(incoming);
    if (scripts.length < 3) return null;
    var assets = [];
    for (var index = 0; index < scripts.length; index++) {
      var script = scripts[index];
      var role = String(script.getAttribute("data-kitwork-jit") || "");
      var hash = String(script.getAttribute("data-kitwork-hash") || "");
      var rawSource = script.getAttribute("src");
      var match = /^\/jit\/([0-9a-f]{64})\.([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)\.js$/.exec(String(rawSource || ""));
      var type = String(script.getAttribute("type") || "").trim().toLowerCase();
      var url = match ? absoluteURL(rawSource, responseURL) : null;
      if (String(script.localName || "").toLowerCase() !== "script" ||
        !incoming.head || script.parentNode !== incoming.head || !exactStagedScriptAttributes(script) ||
        index > 0 && scripts[index - 1].nextElementSibling !== script ||
        String(script.textContent || "") || !match || match[1] !== hash || !url ||
        new URL(url).origin !== global.location.origin ||
        script.getAttribute("integrity") !== stagedIntegrity(hash) ||
        script.getAttribute("crossorigin") !== "anonymous" || !script.hasAttribute("defer") ||
        script.hasAttribute("async") || script.hasAttribute("data-kitwork-handoff") ||
        script.hasAttribute("nomodule") || type &&
        type !== "text/javascript" && type !== "application/javascript") return null;
      assets.push({
        node: script,
        role: role,
        hash: hash,
        integrity: script.getAttribute("integrity"),
        name: match[1] + "." + match[2] + ".js",
        rawSource: rawSource,
        url: url
      });
    }
    if (assets[0].role !== "runtime" || assets[1].role !== "hydrate" || assets[2].role !== "graph") {
      return null;
    }
    var phase = "service";
    var bundleSeen = false;
    for (var offset = 3; offset < assets.length; offset++) {
      var nextRole = assets[offset].role;
      if (nextRole === "service" && phase === "service") continue;
      if (nextRole === "components" && phase !== "component" && !bundleSeen) {
        phase = "components";
        bundleSeen = true;
        continue;
      }
      if (nextRole === "component") {
        phase = "component";
        continue;
      }
      return null;
    }
    return { scripts: scripts, assets: assets, graph: assets[2] };
  }

  function sameStagedAsset(left, right) {
    return !!left && !!right && left.role === right.role && left.hash === right.hash &&
      left.integrity === right.integrity && left.name === right.name && left.url === right.url;
  }

  function sameCandidateDelivery(candidate, delivery) {
    if (!candidate || !delivery || delivery.profile !== "hydrate" ||
      !Array.isArray(delivery.assets) || delivery.assets.length !== candidate.assets.length ||
      delivery.graphHash !== candidate.graph.hash) return false;
    for (var index = 0; index < candidate.assets.length; index++) {
      if (!sameStagedAsset(candidate.assets[index], delivery.assets[index])) return false;
    }
    return true;
  }

  function stableHandoffRuntime(candidate) {
    var delivery = core.delivery;
    return !!delivery && delivery.profile === "hydrate" && Array.isArray(delivery.assets) &&
      delivery.assets.length >= 3 && sameStagedAsset(candidate.assets[0], delivery.assets[0]) &&
      sameStagedAsset(candidate.assets[1], delivery.assets[1]);
  }

  function sameHandoffServices(target) {
    var current = core.delivery;
    if (!current || !target || !Array.isArray(current.assets) || !Array.isArray(target.assets)) return false;
    var currentServices = current.assets.filter(function (asset) { return asset.role === "service"; });
    var targetServices = target.assets.filter(function (asset) { return asset.role === "service"; });
    if (currentServices.length !== targetServices.length) return false;
    for (var index = 0; index < currentServices.length; index++) {
      var left = currentServices[index];
      var right = targetServices[index];
      if (!sameStagedAsset(left, right) || left.package !== right.package || left.version !== right.version) {
        return false;
      }
    }
    return true;
  }

  function sameCandidateServiceAssets(candidate) {
    var delivery = core.delivery;
    if (!delivery || !Array.isArray(delivery.assets)) return false;
    var current = delivery.assets.filter(function (asset) { return asset.role === "service"; });
    var incoming = candidate.assets.filter(function (asset) { return asset.role === "service"; });
    if (current.length !== incoming.length) return false;
    for (var index = 0; index < current.length; index++) {
      if (!sameStagedAsset(current[index], incoming[index])) return false;
    }
    return true;
  }

  function rememberHandoffGraph(hash, value) {
    if (!/^[0-9a-f]{64}$/.test(String(hash || "")) || !value) return false;
    if (handoffGraphs.has(hash)) handoffGraphs.delete(hash);
    while (handoffGraphs.size >= HANDOFF_GRAPH_CACHE_LIMIT) {
      var oldest = handoffGraphs.keys().next();
      if (oldest.done) break;
      handoffGraphs.delete(oldest.value);
    }
    handoffGraphs.set(hash, value);
    return true;
  }

  function handoffPackageKey(value) {
    return value.name + "\u0000" + value.version + "\u0000" + value.sourceHash;
  }

  function componentPackagesForAsset(graph, asset) {
    if (!graph || !graph.components || !graph.componentHashes || !asset) return null;
    var packages = [];
    if ((asset.role !== "component" && asset.role !== "components") ||
      !Array.isArray(asset.components) || !asset.components.length) return null;
    asset.components.forEach(function (source) {
      packages.push({ name: source.name, version: source.version, sourceHash: source.sourceHash });
    });
    if (asset.role === "component" && (packages.length !== 1 ||
      asset.package !== packages[0].name || asset.version !== packages[0].version ||
      asset.sourceHash !== packages[0].sourceHash)) return null;
    if (asset.role === "components" && packages.length < 2) return null;
    var seen = Object.create(null);
    for (var offset = 0; offset < packages.length; offset++) {
      var entry = packages[offset];
      if (!entry || typeof entry.name !== "string" || typeof entry.version !== "string" ||
        !/^[0-9a-f]{64}$/.test(String(entry.sourceHash || "")) ||
        graph.components[entry.name] !== entry.version ||
        graph.componentHashes[entry.name] !== entry.sourceHash) return null;
      var key = handoffPackageKey(entry);
      if (seen[key]) return null;
      seen[key] = true;
    }
    return packages;
  }

  function missingHandoffAssets(graph, delivery, missing) {
    if (!Array.isArray(missing) || !delivery || !Array.isArray(delivery.assets)) return null;
    var needed = Object.create(null);
    for (var index = 0; index < missing.length; index++) {
      var requirement = missing[index];
      if (!requirement || !/^[0-9a-f]{64}$/.test(String(requirement.sourceHash || ""))) return null;
      var key = handoffPackageKey(requirement);
      if (needed[key]) return null;
      needed[key] = true;
    }
    var selected = [];
    for (var assetIndex = 0; assetIndex < delivery.assets.length; assetIndex++) {
      var asset = delivery.assets[assetIndex];
      if (asset.role !== "component" && asset.role !== "components") continue;
      var packages = componentPackagesForAsset(graph, asset);
      if (!packages || !packages.length) return null;
      var count = 0;
      for (var packageIndex = 0; packageIndex < packages.length; packageIndex++) {
        if (needed[handoffPackageKey(packages[packageIndex])]) count++;
      }
      // A shared chunk is atomic. Re-executing only part of it would duplicate
      // a cached component registration, so partial overlap hard-falls back.
      if (asset.role === "components" && count > 0 && count !== packages.length) return null;
      if (count > 0) {
        selected.push(asset);
        packages.forEach(function (entry) { delete needed[handoffPackageKey(entry)]; });
      }
    }
    return Object.keys(needed).length === 0 ? selected : null;
  }

  function handoffAbortError() {
    var error = new Error("KitJS: component handoff was cancelled");
    error.name = "AbortError";
    return error;
  }

  function assertHandoffScript(script, expected) {
    if (!script || !expected || String(script.localName || "").toLowerCase() !== "script" ||
      script.getAttribute("data-kitwork-jit") !== expected.role ||
      script.getAttribute("data-kitwork-hash") !== expected.hash ||
      script.getAttribute("integrity") !== expected.integrity ||
      script.getAttribute("crossorigin") !== "anonymous" || script.crossOrigin !== "anonymous" ||
      script.getAttribute("data-kitwork-handoff") !== "" ||
      script.getAttribute("src") !== expected.url || script.src !== expected.url ||
      !script.hasAttribute("defer") || script.defer !== true || script.async === true ||
      script.hasAttribute("nomodule") || script.noModule === true) {
      throw new Error("KitJS: component handoff script does not match the sealed asset");
    }
    var type = String(script.getAttribute("type") || "").trim().toLowerCase();
    if (type && type !== "text/javascript" && type !== "application/javascript") {
      throw new Error("KitJS: component handoff requires classic JavaScript");
    }
    return true;
  }

  function createHandoffState(visit, candidate) {
    if (document[HANDOFF] !== undefined || typeof core.beginComponentHandoff !== "function") return null;
    var state = {
      visit: visit,
      candidate: candidate,
      expected: null,
      expectedNode: null,
      accepted: false,
      currentCancel: null,
      target: null,
      transaction: null,
      error: null,
      cancelled: false,
      closed: false
    };

    function fail(error) {
      state.error = error instanceof Error ? error : new Error(String(error || "KitJS: component handoff failed"));
      throw state.error;
    }

    function acceptGraph(script, graph, delivery, dynamic) {
      // A cancelled dynamic script can still finish evaluating after a newer
      // visit has installed its own bridge. It must be inert rather than
      // poisoning the newer transaction.
      if (dynamic && script !== state.expectedNode) return false;
      try {
        if (state.cancelled || !currentVisit(visit) || state.target) throw handoffAbortError();
        if (dynamic) {
          if (state.expected !== candidate.graph || state.accepted) {
            throw new Error("KitJS: unexpected component graph handoff");
          }
          assertHandoffScript(script, state.expected);
        }
        if (!sameCandidateDelivery(candidate, delivery) || !sameHandoffServices(delivery)) {
          throw new Error("KitJS: component graph changes its sealed runtime or services");
        }
        var transaction = core.beginComponentHandoff(graph, delivery);
        if (!transaction || !transaction.graph || !transaction.delivery ||
          typeof transaction.missing !== "function" ||
          typeof transaction.register !== "function" || typeof transaction.ready !== "function" ||
          typeof transaction.commit !== "function" || typeof transaction.abort !== "function") {
          throw new Error("KitJS: component handoff transaction is unavailable");
        }
        state.transaction = transaction;
        state.target = { graph: transaction.graph, delivery: transaction.delivery };
        state.accepted = true;
        return state.target;
      } catch (error) { return fail(error); }
    }

    function expectedPackages() {
      var entries = state.target && componentPackagesForAsset(state.target.graph, state.expected);
      if (!entries || !entries.length) fail(new Error("KitJS: component handoff asset has no sealed packages"));
      return entries;
    }

    function registerPackage(source, expected) {
      if (!source || source.name !== expected.name || source.version !== expected.version ||
        source.sourceHash !== expected.sourceHash || typeof source.install !== "function") {
        fail(new Error("KitJS: component handoff package identity does not match its asset"));
      }
      state.transaction.register(source.name, source.version, source.sourceHash, source.install);
    }

    var bridge = Object.freeze({
      graph: function (script, graph, delivery) {
        return acceptGraph(script, graph, delivery, true);
      },
      component: function (script, componentPackage) {
        if (script !== state.expectedNode) return false;
        try {
          if (state.cancelled || !currentVisit(visit) || !state.target || state.accepted ||
            !state.expected || state.expected.role !== "component") throw handoffAbortError();
          assertHandoffScript(script, state.expected);
          var entries = expectedPackages();
          if (entries.length !== 1) throw new Error("KitJS: individual component asset is ambiguous");
          registerPackage(componentPackage, entries[0]);
          state.accepted = true;
        } catch (error) { return fail(error); }
      },
      components: function (script, componentPackages) {
        if (script !== state.expectedNode) return false;
        try {
          if (state.cancelled || !currentVisit(visit) || !state.target || state.accepted ||
            !state.expected || state.expected.role !== "components" || !Array.isArray(componentPackages)) {
            throw handoffAbortError();
          }
          assertHandoffScript(script, state.expected);
          var entries = expectedPackages();
          if (entries.length !== componentPackages.length) {
            throw new Error("KitJS: component bundle registration is incomplete");
          }
          for (var index = 0; index < entries.length; index++) {
            registerPackage(componentPackages[index], entries[index]);
          }
          state.accepted = true;
        } catch (error) { return fail(error); }
      }
    });

    try {
      Object.defineProperty(document, HANDOFF, { value: bridge, configurable: true });
    } catch (_) { return null; }

    state.acceptCached = function (value) {
      return acceptGraph(null, value.graph, value.delivery, false);
    };
    state.cancel = function () {
      if (state.cancelled || state.closed) return;
      state.cancelled = true;
      if (state.currentCancel) state.currentCancel(handoffAbortError());
      if (state.transaction) {
        try { state.transaction.abort(); } catch (_) { /* The old active graph remains authoritative. */ }
      }
      if (document[HANDOFF] === bridge) delete document[HANDOFF];
    };
    state.close = function () {
      if (state.closed) return;
      state.closed = true;
      state.currentCancel = null;
      if (document[HANDOFF] === bridge) delete document[HANDOFF];
    };
    visit.handoff = state;
    return state;
  }

  function loadHandoffScript(visit, state, asset) {
    return new Promise(function (resolve, reject) {
      if (!currentVisit(visit) || state.cancelled || !document.head) {
        reject(handoffAbortError());
        return;
      }
      var script = document.createElement("script");
      engineHandoffScripts.add(script);
      var settled = false;
      var timer = 0;
      state.expected = asset;
      state.expectedNode = script;
      state.accepted = false;
      state.error = null;

      function finish(error) {
        if (settled) return;
        settled = true;
        if (timer) global.clearTimeout(timer);
        script.onload = null;
        script.onerror = null;
        if (script.parentNode) script.parentNode.removeChild(script);
        if (state.expectedNode === script) state.expectedNode = null;
        state.currentCancel = null;
        if (error) reject(error);
        else resolve(true);
      }

      state.currentCancel = finish;
      script.setAttribute("data-kitwork-jit", asset.role);
      script.setAttribute("data-kitwork-hash", asset.hash);
      script.setAttribute("integrity", asset.integrity);
      script.setAttribute("crossorigin", "anonymous");
      script.setAttribute("data-kitwork-handoff", "");
      script.setAttribute("defer", "");
      script.defer = true;
      script.async = false;
      script.setAttribute("src", asset.url);
      script.onload = function () {
        if (!currentVisit(visit) || state.cancelled) finish(handoffAbortError());
        else if (state.error) finish(state.error);
        else if (!state.accepted) finish(new Error("KitJS: component handoff script did not register its sealed payload"));
        else finish(null);
      };
      script.onerror = function () {
        finish(new Error("KitJS: component handoff asset could not be loaded"));
      };
      timer = global.setTimeout(function () {
        finish(new Error("KitJS: component handoff asset timed out"));
      }, HANDOFF_LOAD_TIMEOUT);
      try { document.head.appendChild(script); }
      catch (error) { finish(error); }
    });
  }

  function loadHandoffAssets(visit, state, assets, index) {
    if (index >= assets.length) return Promise.resolve(true);
    return loadHandoffScript(visit, state, assets[index]).then(function () {
      return loadHandoffAssets(visit, state, assets, index + 1);
    });
  }

  function prepareComponentHandoff(incoming, responseURL, visit) {
    var candidate = stagedCandidate(incoming, responseURL);
    if (!candidate || !stableHandoffRuntime(candidate) || !sameCandidateServiceAssets(candidate)) {
      return Promise.resolve(null);
    }
    var state = createHandoffState(visit, candidate);
    if (!state) return Promise.resolve(null);
    var cached = handoffGraphs.get(candidate.graph.hash) || null;
    var graphReady;
    try {
      if (cached) {
        state.acceptCached(cached);
        graphReady = Promise.resolve(true);
      } else graphReady = loadHandoffScript(visit, state, candidate.graph);
    } catch (error) {
      state.cancel();
      return Promise.reject(error);
    }
    return graphReady.then(function () {
      if (!state.target || !currentVisit(visit)) throw handoffAbortError();
      if (!cached) rememberHandoffGraph(candidate.graph.hash, state.target);
      var missing = state.transaction.missing();
      var assets = missingHandoffAssets(state.target.graph, state.target.delivery, missing);
      if (!assets) throw new Error("KitJS: component handoff cannot load the sealed package delta");
      return loadHandoffAssets(visit, state, assets, 0);
    }).then(function () {
      if (!currentVisit(visit) || state.cancelled || !state.transaction.ready()) throw handoffAbortError();
      var transaction = state.transaction;
      var settled = false;
      var activating = false;
      state.close();
      visit.handoff = null;
      return Object.freeze({
        graph: state.target.graph,
        activate: Object.freeze(function () {
          if (settled || activating) throw new Error("KitJS: component handoff transition is already settled");
          activating = true;
          try {
            var rollback = transaction.commit();
            if (typeof rollback !== "function") {
              throw new Error("KitJS: component handoff did not provide rollback");
            }
            settled = true;
            return rollback;
          } catch (error) {
            try { transaction.abort(); } catch (_) { /* The active graph was not changed. */ }
            settled = true;
            throw error;
          } finally {
            activating = false;
          }
        }),
        abort: Object.freeze(function () {
          if (settled) return false;
          settled = true;
          return transaction.abort();
        })
      });
    }).catch(function (error) {
      state.cancel();
      visit.handoff = null;
      throw error;
    });
  }

  function collectComponents(root, output) {
    if (!root || root.nodeType === 1 && core.ignoredForRuntime(root)) return;
    if (root.nodeType === 1 && (root.hasAttribute("data-kit-component") ||
      root.hasAttribute("data-kit-version") || root.hasAttribute("data-kit-local"))) output.push(root);
    if (!root.querySelectorAll) return;
    array(root.querySelectorAll("[data-kit-component],[data-kit-version],[data-kit-local]")).forEach(function (element) {
      if (!core.ignoredForRuntime(element)) output.push(element);
    });
    array(root.querySelectorAll("template")).forEach(function (template) {
      if (!core.ignoredForRuntime(template) && template.content) collectComponents(template.content, output);
    });
  }

  function knownComponents(incoming) {
    if (!incoming || !incoming.documentElement || !incoming.body ||
      typeof core.componentMetadata !== "function" ||
      typeof core.hasComponentDefinition !== "function") return false;
    var components = [];
    var root = incoming.documentElement;
    if (root.hasAttribute("data-kit-component") || root.hasAttribute("data-kit-version") ||
      root.hasAttribute("data-kit-local")) {
      // The document root is outside body morphing, so data-kit-ignore cannot
      // exempt its component identity from the incoming graph check.
      components.push(root);
    }
    collectComponents(incoming.body, components);
    return components.every(function (element) {
      var request = core.componentMetadata(element, false);
      return !!request && core.hasComponentDefinition(request);
    });
  }

  function knownComponentsForGraph(incoming, graph) {
    if (!incoming || !incoming.documentElement || !incoming.body || !graph || !graph.components ||
      typeof core.componentMetadataForGraph !== "function" ||
      typeof core.hasComponentDefinition !== "function") return false;
    var components = [];
    var root = incoming.documentElement;
    if (root.hasAttribute("data-kit-component") || root.hasAttribute("data-kit-version") ||
      root.hasAttribute("data-kit-local")) {
      components.push(root);
    }
    collectComponents(incoming.body, components);
    return components.every(function (element) {
      var request = core.componentMetadataForGraph(element, graph);
      return !!request && (request.lane === "managed" || core.hasComponentDefinition(request));
    });
  }

  function rootMetadata(element, name) {
    if (!element || !element.hasAttribute(name)) return null;
    return String(element.getAttribute(name) || "").trim();
  }

  function compatibleDocumentBoundary(incoming) {
    if (!incoming || !incoming.documentElement || !document.documentElement) return false;
    var current = document.documentElement;
    var next = incoming.documentElement;
    if (current.hasAttribute("data-kit-component") !== next.hasAttribute("data-kit-component")) return false;
    if (current.hasAttribute("data-kit-component")) {
      var currentRequest = core.componentMetadata(current, false);
      var nextRequest = core.componentMetadata(next, false);
      if (!currentRequest || !nextRequest || currentRequest.name !== nextRequest.name ||
        currentRequest.version !== nextRequest.version || currentRequest.lane !== nextRequest.lane) return false;
    }
    return ["data-kit-as", "data-kit-scope"].every(function (name) {
      return current.hasAttribute(name) === next.hasAttribute(name) &&
        rootMetadata(current, name) === rootMetadata(next, name);
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

  function compatibleHandoffDocument(incoming, responseURL) {
    return compatibleContentSecurityPolicy(incoming) &&
      compatibleDocumentBoundary(incoming) && compatibleBase(incoming, responseURL) &&
      !hasActiveDocumentContent(incoming.documentElement);
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

  function sameScroll(left, right) {
    return !!left && !!right && left.x === right.x && left.y === right.y;
  }

  function rememberScroll(position) {
    lastSavedScroll = { x: position.x, y: position.y };
    lastSavedURL = String(global.location.href);
  }

  function writeHistory(method, state, url) {
    if (!global.history || typeof global.history[method] !== "function") return false;
    try {
      global.history[method](state, "", url);
      return true;
    } catch (_) {
      // History is unavailable for some opaque or constrained documents.
      return false;
    }
  }

  function saveScroll(position) {
    if (!global.history || typeof global.history.replaceState !== "function") return;
    position = position || scrollPosition();
    try {
      var href = String(global.location.href);
      var state = global.history.state;
      var stored = savedScroll(state);
      if (lastSavedURL === href && sameScroll(lastSavedScroll, position) && sameScroll(stored, position)) return;
      if (writeHistory("replaceState", historyState(state, position), href)) rememberScroll(position);
    } catch (_) { /* History is unavailable for some opaque documents. */ }
  }

  function scheduleScrollSave() {
    if (scrollTimer || activeVisit) return;
    scrollTimer = global.setTimeout(function () {
      scrollTimer = 0;
      saveScroll();
    }, SCROLL_SAVE_DELAY);
  }

  function cancelScrollSave() {
    if (!scrollTimer) return;
    global.clearTimeout(scrollTimer);
    scrollTimer = 0;
  }

  function flushScrollSave() {
    cancelScrollSave();
    saveScroll();
  }

  function savedScroll(state) {
    var value = state && state.__kitjs_drive__ && state.__kitjs_drive__.scroll;
    if (!value || !Number.isFinite(Number(value.x)) || !Number.isFinite(Number(value.y))) return null;
    return { x: Number(value.x), y: Number(value.y) };
  }

  function hashTarget(hash, explicit) {
    // URL.hash is an empty string for both "no fragment" and an explicit
    // trailing "#". Keep the latter mapped to the document root.
    if (!hash) return explicit ? document.documentElement : null;
    var raw = hash.slice(1);
    if (!raw) return document.documentElement;
    var target = fragmentIdentifierTarget(raw);
    if (target) return target;
    var decoded = decodeFragmentIdentifier(raw);
    if (decoded !== raw) target = fragmentIdentifierTarget(decoded);
    if (target) return target;
    return decoded.toLowerCase() === "top" ? document.documentElement : null;
  }

  function decodeFragmentIdentifier(raw) {
    try { return decodeURIComponent(raw); }
    catch (_) { /* Invalid UTF-8 uses the platform's replacement semantics below. */ }
    if (typeof global.TextDecoder !== "function" || typeof global.TextEncoder !== "function" ||
      typeof global.Uint8Array !== "function") return raw;
    try {
      var bytes = [];
      var encoder = new global.TextEncoder();
      for (var index = 0; index < raw.length;) {
        if (raw.charAt(index) === "%" && /^[0-9a-f]{2}$/i.test(raw.slice(index + 1, index + 3))) {
          bytes.push(parseInt(raw.slice(index + 1, index + 3), 16));
          index += 3;
          continue;
        }
        var nextPercent = raw.indexOf("%", index);
        var end = nextPercent < 0 ? raw.length : nextPercent;
        if (end === index) end++;
        var encoded = encoder.encode(raw.slice(index, end));
        for (var offset = 0; offset < encoded.length; offset++) bytes.push(encoded[offset]);
        index = end;
      }
      return new global.TextDecoder("utf-8").decode(new global.Uint8Array(bytes));
    } catch (_) {
      return raw;
    }
  }

  function fragmentIdentifierTarget(id) {
    var target = document.getElementById(id);
    if (target) return target;
    var named = document.getElementsByName(id);
    for (var index = 0; index < named.length; index++) {
      if (String(named[index].localName || "").toLowerCase() === "a") return named[index];
    }
    return null;
  }

  function hasFragment(url) {
    return url && String(url.href).indexOf("#") >= 0;
  }

  function preserveRequestedFragment(source, requested) {
    var href = absoluteURL(source, document.baseURI);
    if (!href) return String(source || requested && requested.href || "");
    if (hasFragment(requested) && href.indexOf("#") < 0) {
      href += requested.href.slice(requested.href.indexOf("#"));
    }
    return href;
  }

  function sameDocument(url) {
    return !!url && url.pathname === documentPath && url.search === documentSearch;
  }

  function focusRoute(url) {
    var target = hashTarget(url.hash, hasFragment(url)) ||
      document.querySelector("[autofocus],main,[role='main'],h1") || document.body;
    if (!target || typeof target.focus !== "function") return;
    var name = String(target.localName || "").toLowerCase();
    var intrinsic = /^(button|input|select|textarea)$/.test(name) ||
      (name === "a" && target.hasAttribute("href"));
    var temporary = !target.hasAttribute("tabindex") && !intrinsic;
    if (temporary) target.setAttribute("tabindex", "-1");
    try { target.focus({ preventScroll: true }); }
    catch (_) { try { target.focus(); } catch (_) { /* Non-focusable browser host. */ } }
  }

  function restoreScroll(url, position) {
    if (position) {
      try { global.scrollTo(position.x, position.y); } catch (_) { /* Non-visual browser. */ }
      return;
    }
    scrollToFragment(url);
  }

  function scrollToFragment(url) {
    var target = hashTarget(url.hash, hasFragment(url));
    if (target && typeof target.scrollIntoView === "function") target.scrollIntoView();
    else {
      try { global.scrollTo(0, 0); } catch (_) { /* Non-visual browser. */ }
    }
  }

  function hardNavigate(source) {
    global.location.assign(String(source));
  }

  function fallbackVisit(visit, source, outcome, error) {
    source = String(source || visit.url);
    var ownsCritical = beginNavigationCritical();
    if (!ownsCritical) terminateNavigationCritical();
    if (error) {
      try { core.report(error); }
      catch (_) { /* Reporting must not interrupt a terminal fallback. */ }
    }
    finishVisit(visit, outcome, source);
    // A fallback is terminal for the current document. A synchronous finish
    // listener must not start work that the pending native navigation stomps.
    terminateNavigationCritical();
    try { hardNavigate(source); }
    catch (navigationError) {
      try { core.report(navigationError); }
      catch (_) { /* Reporting must not leave the critical section armed. */ }
    }
    if (ownsCritical) endNavigationCritical(false);
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
    if (!sameProfile(incoming, url.href) || !compatibleContentSecurityPolicy(incoming) ||
      !compatibleExecutableScripts(incoming, url.href) ||
      !knownComponents(incoming) ||
      !compatibleDocumentBoundary(incoming) || !compatibleRetains(incoming) ||
      !compatibleBase(incoming, url.href) || hasActiveDocumentContent(incoming.documentElement)) {
      return false;
    }

    if (options.leavingScroll) saveScroll(options.leavingScroll);
    // Advance the document URL only after every compatibility check, but before
    // inserting relative body resources so they resolve against the new route.
    if (options.history === "push") {
      if (!writeHistory("pushState", historyState(null, { x: 0, y: 0 }), url.href)) return false;
      rememberScroll({ x: 0, y: 0 });
    } else if (options.history === "replace") {
      var state;
      try { state = global.history.state; }
      catch (_) { return false; }
      if (!writeHistory("replaceState", historyState(state, { x: 0, y: 0 }), url.href)) return false;
      rememberScroll({ x: 0, y: 0 });
    }
    documentPath = url.pathname;
    documentSearch = url.search;
    reconcileHead(incoming, url.href);
    reconcileDocumentAttributes(incoming);
    core.morph(document.body, incoming.body);
    if (document.title !== incoming.title) document.title = incoming.title;
    focusRoute(url);
    if (options.scroll) restoreScroll(url, options.scroll);
    else {
      scrollToFragment(url);
      saveScroll();
    }
    return true;
  }

  function discardTransition(rollback, transition) {
    try {
      if (rollback) rollback();
      else if (transition && transition.abort) transition.abort();
    } catch (error) {
      try { core.report(error); }
      catch (_) { /* Transaction cleanup remains best effort. */ }
    }
  }

  function visit(source, options) {
    options = options || {};
    var url = source instanceof URL ? source : sameOriginURL(source);
    if (!url) {
      if (navigationCritical) {
        return new Promise(function (resolve, reject) {
          queueNativeAssign(source, resolve, reject);
        });
      }
      hardNavigate(source);
      return Promise.resolve(false);
    }
    if (navigationCritical) return deferVisit(url, options);

    var leavingScroll = null;
    var controller = null;
    var visitRecord = null;
    beginNavigationCritical();
    try {
      // A popstate has already activated the destination history entry.
      // Writing the still-rendered page's viewport now would corrupt it.
      if (options.history === "none") cancelScrollSave();
      else flushScrollSave();
      leavingScroll = options.history !== "none" ? scrollPosition() : null;
      if (activeVisit) cancelVisit(activeVisit);
      // pagehide may have run from a synchronous cancellation listener. Never
      // create a new visit in a document whose navigation is now terminal.
      if (navigationTerminal) {
        endNavigationCritical(false);
        return Promise.resolve(false);
      }
      controller = new AbortController();
      var sequence = ++visitSequence;
      visitRecord = {
        controller: controller,
        sequence: sequence,
        url: url.href,
        history: options.history || "push",
        finished: false
      };
      activeVisit = visitRecord;
      emitNavigation(visitRecord, "start");
    }
    catch (error) {
      endNavigationCritical(false);
      if (!visitRecord) {
        var ownsFailureCritical = beginNavigationCritical();
        terminateNavigationCritical();
        try { core.report(error); }
        catch (_) { /* Reporting must not interrupt a terminal navigation. */ }
        terminateNavigationCritical();
        try { hardNavigate(url.href); }
        catch (navigationError) {
          try { core.report(navigationError); }
          catch (_) { /* Reporting must not leave the critical section armed. */ }
        }
        if (ownsFailureCritical) endNavigationCritical(false);
        return Promise.resolve(false);
      }
      return Promise.resolve(fallbackVisit(visitRecord, url.href, "error", error));
    }
    endNavigationCritical(true);
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
      var responseURL = preserveRequestedFragment(response.url || url.href, url);
      var finalURL = sameOriginURL(responseURL);
      if (!response.ok || !isHTML(response) || !finalURL || hasContentSecurityPolicyHeader(response)) {
        fallbackVisit(visitRecord, responseURL, "fallback");
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
      if (!loaded || !currentVisit(visitRecord)) return null;
      if (!compatibleExecutableScripts(loaded.document, loaded.url.href)) {
        return { loaded: loaded, transition: false };
      }
      if (sameProfile(loaded.document, loaded.url.href)) {
        return { loaded: loaded, transition: null };
      }
      if (!stagedProfile || !compatibleHandoffDocument(loaded.document, loaded.url.href)) {
        return { loaded: loaded, transition: false };
      }
      return prepareComponentHandoff(loaded.document, loaded.url.href, visitRecord).then(function (transition) {
        return { loaded: loaded, transition: transition || false };
      });
    }).then(function (prepared) {
      if (!prepared) return false;
      var loaded = prepared.loaded;
      var transition = prepared.transition;
      var rollback = null;
      if (!currentVisit(visitRecord)) {
        if (transition && transition.abort) transition.abort();
        return false;
      }
      if (transition === false) return fallbackVisit(visitRecord, loaded.url.href, "fallback");
      if (transition && (!knownComponentsForGraph(loaded.document, transition.graph) ||
        !compatibleRetains(loaded.document))) {
        transition.abort();
        return fallbackVisit(visitRecord, loaded.url.href, "fallback");
      }
      var ownsCritical = beginNavigationCritical();
      try {
        // Activate the exact graph in the same synchronous turn as Morph. No
        // observer, authored microtask, or newer navigation can see target
        // authority paired with the old document.
        if (transition) {
          rollback = transition.activate();
          // Direct terminal navigation can still invalidate the visit. Drive
          // visits triggered here are deferred until the commit is complete.
          if (!currentVisit(visitRecord)) {
            discardTransition(rollback, transition);
            endNavigationCritical(false);
            ownsCritical = false;
            return false;
          }
        }
        var committed = commit(loaded.document, loaded.url, {
          history: options.history || "push",
          scroll: options.scroll || null,
          leavingScroll: leavingScroll
        });
        if (!committed) {
          discardTransition(rollback, transition);
          endNavigationCritical(false);
          ownsCritical = false;
          return fallbackVisit(visitRecord, loaded.url.href, "fallback");
        }
        if (!currentVisit(visitRecord)) {
          endNavigationCritical(false);
          ownsCritical = false;
          return false;
        }
        finishVisit(visitRecord, "loaded", loaded.url.href);
        endNavigationCritical(true);
        ownsCritical = false;
      } catch (error) {
        discardTransition(rollback, transition);
        if (ownsCritical) endNavigationCritical(false);
        if (visitRecord.finished) return false;
        // Complete the terminal fallback in this turn. Deferred-intent promise
        // reactions must not run between a failed commit and its native load.
        return fallbackVisit(visitRecord, loaded.url.href, "error", error);
      }
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
    if (hasFragment(url) && sameDocument(url)) {
      // Save the leaving entry before the browser creates its native fragment
      // entry, then preserve the platform's default :target/hashchange behavior.
      if (navigationCritical) {
        event.preventDefault();
        queueNativeAssign(url.href);
        return;
      }
      if (activeVisit) {
        event.preventDefault();
        beginNavigationCritical();
        queueNativeAssign(url.href);
        cancelVisit(activeVisit);
        endNavigationCritical(true);
        return;
      }
      flushScrollSave();
      return;
    }
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
    if (!url) return;
    var position = savedScroll(event.state);
    if (sameDocument(url)) {
      if (navigationCritical) {
        queueScrollRestore(url, position);
        return;
      }
      if (activeVisit) {
        beginNavigationCritical();
        queueScrollRestore(url, position);
        cancelVisit(activeVisit);
        endNavigationCritical(true);
        return;
      }
      if (position) rememberScroll(position);
      restoreScroll(url, position);
      return;
    }
    visit(url, { history: "none", scroll: position });
  }

  function onPageHide() {
    // A cross-document popstate has already activated its destination entry,
    // while the old document can remain rendered until Drive commits. Never
    // write that old viewport into the destination during pagehide. Comparing
    // rendered and address-bar routes also covers a failed popstate fetch after
    // its active visit has already been cleared for a hard-navigation fallback.
    var url = sameOriginURL(global.location.href);
    if (url && !sameDocument(url)) cancelScrollSave();
    else flushScrollSave();
    var ownsCritical = beginNavigationCritical();
    terminateNavigationCritical();
    if (activeVisit) cancelVisit(activeVisit);
    terminateNavigationCritical();
    if (ownsCritical) endNavigationCritical(false);
  }

  function start() {
    if (started || !profileURL || typeof global.fetch !== "function" ||
      typeof global.DOMParser !== "function" || typeof global.AbortController !== "function" ||
      !global.history || typeof global.history.pushState !== "function") return false;
    if (!captureLiveThemePrepaint()) return warnDriveDisabled();
    if (stagedProfile && !captureLiveStagedDelivery()) return warnDriveDisabled();
    liveExecutableTopology = executableScriptTopology(document, document.baseURI, true);
    if (!liveExecutableTopology) return warnDriveDisabled();
    started = true;
    if (stagedProfile && core.graph && core.delivery && core.delivery.graphHash) {
      rememberHandoffGraph(core.delivery.graphHash, { graph: core.graph, delivery: core.delivery });
    }
    try {
      if ("scrollRestoration" in global.history) global.history.scrollRestoration = "manual";
    } catch (_) { /* History is unavailable for some opaque documents. */ }
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
