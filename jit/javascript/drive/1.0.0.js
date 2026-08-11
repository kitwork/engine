// KitJS module: drive@1.0.0
// Production SPA Navigation, Link Interception, Hover Prefetching & DOM Morphing Engine
;(function (global) {
  "use strict";

  var kit = (global.kit = global.kit || {});
  if (kit.drive && kit.drive.version === "1.0.0") return;

  var prefetchCache = Object.create(null);
  var prefetchOrder = [];
  var PREFETCH_CAP = 30;
  var loadedScripts = new Set();

  if (typeof global.document !== "undefined") {
    var existing = global.document.querySelectorAll("script[src]");
    for (var index = 0; index < existing.length; index++) {
      if (existing[index].src) loadedScripts.add(existing[index].src);
    }
  }

  // --------------------------------------------------------------------------
  // 1. PROGRESS BAR OVERLAY
  // --------------------------------------------------------------------------
  var bar = null;
  var barTimer = null;
  var barShown = false;

  function initProgressBar() {
    if (typeof global.document === "undefined" || !global.document.body || bar) return;
    bar = global.document.createElement("div");
    bar.setAttribute("data-kit-ui", "progress");
    bar.style.cssText =
      "position:fixed;top:0;left:0;height:2px;width:0;" +
      "background:var(--kit-progress,#4f46e5);z-index:2147483647;" +
      "opacity:0;pointer-events:none;transition:width .2s ease,opacity .3s;";
    global.document.body.appendChild(bar);
  }

  function showProgress(on) {
    initProgressBar();
    if (!bar) return;

    global.clearTimeout(barTimer);
    if (on) {
      barTimer = global.setTimeout(function () {
        if (!bar.isConnected) global.document.body.appendChild(bar);
        barShown = true;
        bar.style.transition = "width .2s ease,opacity .3s";
        bar.style.opacity = "1";
        bar.style.width = "0";
        if (typeof global.requestAnimationFrame === "function") {
          global.requestAnimationFrame(function () {
            bar.style.transition = "width 8s cubic-bezier(.1,.7,.1,1)";
            bar.style.width = "90%";
          });
        } else {
          bar.style.width = "90%";
        }
      }, 120);
    } else if (barShown) {
      barShown = false;
      bar.style.transition = "width .2s ease,opacity .4s";
      bar.style.width = "100%";
      global.setTimeout(function () {
        bar.style.opacity = "0";
        bar.style.width = "0";
      }, 220);
    }
  }

  // --------------------------------------------------------------------------
  // 2. DOM MORPHING ALGORITHM
  // --------------------------------------------------------------------------
  function isSameNode(n1, n2) {
    if (n1.nodeType !== n2.nodeType) return false;
    if (n1.nodeType === 1) {
      if (n1.tagName !== n2.tagName) return false;
      var persist1 = n1.getAttribute("data-kit-persist");
      var persist2 = n2.getAttribute("data-kit-persist");
      if (persist1 || persist2) return persist1 === persist2;

      var key1 = n1.getAttribute("data-kit-key");
      var key2 = n2.getAttribute("data-kit-key");
      if (key1 || key2) return key1 === key2;

      var id1 = n1.id;
      var id2 = n2.id;
      if (id1 || id2) return id1 === id2;
    }
    return true;
  }

  function morphAttributes(fromNode, toNode) {
    var fromAttrs = fromNode.attributes;
    var toAttrs = toNode.attributes;
    if (!fromAttrs || !toAttrs) return;

    for (var i = fromAttrs.length - 1; i >= 0; i--) {
      var name = fromAttrs[i].name;
      if (name === "value" && fromNode === global.document.activeElement) continue;
      if (!toNode.hasAttribute(name)) {
        fromNode.removeAttribute(name);
      }
    }

    for (var j = 0; j < toAttrs.length; j++) {
      var attrName = toAttrs[j].name;
      var attrVal = toAttrs[j].value;
      if (attrName === "value" && fromNode === global.document.activeElement) continue;
      if (fromNode.getAttribute(attrName) !== attrVal) {
        fromNode.setAttribute(attrName, attrVal);
      }
    }
  }

  function morphChildren(fromParent, toParent) {
    var fromChild = fromParent.firstChild;
    var toChild = toParent.firstChild;

    while (toChild) {
      if (!fromChild) {
        fromParent.appendChild(toChild.cloneNode(true));
        toChild = toChild.nextSibling;
        continue;
      }

      if (isSameNode(fromChild, toChild)) {
        morphNode(fromChild, toChild);
        fromChild = fromChild.nextSibling;
        toChild = toChild.nextSibling;
      } else {
        var foundMatch = false;
        var cur = fromChild.nextSibling;
        while (cur) {
          if (isSameNode(cur, toChild)) {
            fromParent.insertBefore(cur, fromChild);
            morphNode(cur, toChild);
            fromChild = cur.nextSibling;
            foundMatch = true;
            break;
          }
          cur = cur.nextSibling;
        }

        if (!foundMatch) {
          var newClone = toChild.cloneNode(true);
          fromParent.insertBefore(newClone, fromChild);
        }
        toChild = toChild.nextSibling;
      }
    }

    while (fromChild) {
      var next = fromChild.nextSibling;
      if (fromChild.nodeType === 1 && (fromChild.hasAttribute("data-kit-persist") || fromChild.hasAttribute("data-kit-ui"))) {
        fromChild = next;
        continue;
      }
      fromParent.removeChild(fromChild);
      fromChild = next;
    }
  }

  function morphNode(fromNode, toNode) {
    if (fromNode.nodeType === 3) {
      if (fromNode.nodeValue !== toNode.nodeValue) {
        fromNode.nodeValue = toNode.nodeValue;
      }
      return;
    }

    if (fromNode.nodeType === 1) {
      if (fromNode.hasAttribute("data-kit-persist") || fromNode.hasAttribute("data-kit-ui")) return;

      morphAttributes(fromNode, toNode);
      morphChildren(fromNode, toNode);
    }
  }

  function reconcileHead(newDoc) {
    if (!newDoc || !newDoc.head) return;
    var newHeadChildren = newDoc.head.children;

    for (var i = 0; i < newHeadChildren.length; i++) {
      var child = newHeadChildren[i];
      var tagName = child.tagName.toLowerCase();

      if (tagName === "title" && global.document.title !== child.textContent) {
        global.document.title = child.textContent;
        if (kit.announce && typeof kit.announce.polite === "function") {
          kit.announce.polite(global.document.title);
        }
      } else if (tagName === "link" && child.rel === "stylesheet") {
        var href = child.getAttribute("href");
        if (href && !global.document.querySelector('link[href="' + href + '"]')) {
          var newLink = global.document.createElement("link");
          newLink.rel = "stylesheet";
          newLink.href = href;
          global.document.head.appendChild(newLink);
        }
      } else if (tagName === "script" && child.src) {
        if (!loadedScripts.has(child.src)) {
          loadedScripts.add(child.src);
          var newScript = global.document.createElement("script");
          newScript.src = child.src;
          global.document.head.appendChild(newScript);
        }
      }
    }
  }

  // --------------------------------------------------------------------------
  // 3. HOVER PREFETCHING ENGINE
  // --------------------------------------------------------------------------
  function prefetchUrl(url) {
    if (!url || prefetchCache[url] || typeof global.fetch === "undefined") return;

    var promise = global
      .fetch(url, { headers: { "X-Kit-Drive": "true", Accept: "text/html" } })
      .then(function (res) { return res.ok ? res.text() : null; })
      ["catch"](function () { return null; });

    prefetchCache[url] = promise;
    prefetchOrder.push(url);

    if (prefetchOrder.length > PREFETCH_CAP) {
      var evicted = prefetchOrder.shift();
      delete prefetchCache[evicted];
    }
  }

  // --------------------------------------------------------------------------
  // 4. SPA NAVIGATION & MORPH EXECUTION
  // --------------------------------------------------------------------------
  function triggerRuntimeInvalidate() {
    if (kit.__kitwork_core__ && typeof kit.__kitwork_core__.refreshRuntime === "function") {
      kit.__kitwork_core__.refreshRuntime();
    }
  }

  function fetchAndMorph(url, isPopState) {
    if (typeof global.fetch === "undefined") return Promise.reject("Fetch unavailable");

    showProgress(true);

    var htmlPromise =
      prefetchCache[url] ||
      global.fetch(url, { headers: { "X-Kit-Drive": "true", Accept: "text/html" } }).then(function (res) {
        if (!res.ok) {
          global.location.href = url;
          return null;
        }
        return res.text();
      });

    return Promise.resolve(htmlPromise)
      .then(function (htmlText) {
        showProgress(false);
        if (!htmlText) return;

        var parser = new global.DOMParser();
        var newDoc = parser.parseFromString(htmlText, "text/html");

        reconcileHead(newDoc);
        morphNode(global.document.body, newDoc.body);

        if (!isPopState && global.history && typeof global.history.pushState === "function") {
          global.history.pushState({ drive: true, url: url }, global.document.title, url);
        }

        triggerRuntimeInvalidate();
        global.scrollTo(0, 0);
        return true;
      })
      ["catch"](function (err) {
        showProgress(false);
        console.warn("kit.drive navigation error:", err);
        global.location.href = url;
      });
  }

  function setupLinkInterception() {
    if (typeof global.document === "undefined") return;
    initProgressBar();

    global.document.addEventListener(
      "click",
      function (evt) {
        var anchor = evt.target;
        while (anchor && anchor !== global.document && anchor.tagName !== "A") {
          anchor = anchor.parentNode;
        }

        if (!anchor || !anchor.href) return;
        if (
          anchor.hasAttribute("data-kit-native") ||
          anchor.hasAttribute("download") ||
          anchor.target === "_blank"
        ) {
          return;
        }

        var href = anchor.getAttribute("href");
        if (!href || href.indexOf("#") === 0 || href.indexOf("javascript:") === 0) return;
        if (anchor.origin !== global.location.origin) return;

        evt.preventDefault();
        fetchAndMorph(anchor.href, false);
      },
      false
    );

    // Hover Prefetching
    global.document.addEventListener(
      "mouseover",
      function (evt) {
        var anchor = evt.target;
        while (anchor && anchor !== global.document && anchor.tagName !== "A") {
          anchor = anchor.parentNode;
        }
        if (anchor && anchor.href && anchor.origin === global.location.origin) {
          prefetchUrl(anchor.href);
        }
      },
      { passive: true }
    );

    global.addEventListener("popstate", function () {
      fetchAndMorph(global.location.href, true);
    });
  }

  // --------------------------------------------------------------------------
  // 5. PUBLIC DRIVE SERVICE INTERFACE
  // --------------------------------------------------------------------------
  var driveService = {
    version: "1.0.0",
    navigate: function (url) {
      return fetchAndMorph(url, false);
    },
    prefetch: function (url) {
      prefetchUrl(url);
    },
    morph: function (targetDocument) {
      if (typeof targetDocument === "string") {
        var parser = new global.DOMParser();
        targetDocument = parser.parseFromString(targetDocument, "text/html");
      }
      if (targetDocument && targetDocument.body) {
        reconcileHead(targetDocument);
        morphNode(global.document.body, targetDocument.body);
        triggerRuntimeInvalidate();
      }
    }
  };

  Object.freeze(driveService);
  Object.defineProperty(kit, "drive", {
    value: driveService,
    enumerable: true,
    configurable: false,
    writable: false
  });

  if (typeof global.document !== "undefined") {
    if (global.document.readyState === "loading") {
      global.document.addEventListener("DOMContentLoaded", setupLinkInterception);
    } else {
      setupLinkInterception();
    }
  }
})(globalThis);
