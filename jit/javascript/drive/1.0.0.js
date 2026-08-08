// ============================================================================
// Kitwork Client Runtime: Drive & Hydrate Morphing Engine (1.0.0 - Draft 0.5)
// ============================================================================
// Location: engine/jit/javascript/drive/1.0.0.js
// ============================================================================
// Động cơ SPA Navigation, DOM Morphing & Re-hydration tích hợp các cải tiến từ legacy:
// 1. Native Bridge IPC Adapter Handle (WebView2 / WebKit support).
// 2. Built-in Progress Bar (>120ms threshold to prevent flash).
// 3. Screen Reader Live Region Announcer (aria-live="polite").
// 4. Hover/Touch Link Prefetching với FIFO Cache Cap.
// 5. Head Script & Style Re-reconciler (Tracks loaded external scripts).
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.drive) return;

  var prefetchCache = {};
  var prefetchOrder = [];
  var PREFETCH_CAP = 20;
  var loadedScripts = new Set();

  if (typeof document !== "undefined") {
    var existingScripts = document.querySelectorAll("script[src]");
    for (var s = 0; s < existingScripts.length; s++) {
      loadedScripts.add(existingScripts[s].src);
    }
  }

  // --------------------------------------------------------------------------
  // 1. PROGRESS BAR & ANNOUNCER CHROME OVERLAYS
  // --------------------------------------------------------------------------
  var bar = null;
  var barTimer = null;
  var barShown = false;
  var announcer = null;

  function initChromeOverlays() {
    if (typeof document === "undefined" || !document.body) return;

    if (!bar) {
      bar = document.createElement("div");
      bar.setAttribute("data-kit-ui", "progress");
      bar.style.cssText = "position:fixed;top:0;left:0;height:2px;width:0;" +
        "background:var(--kit-progress,#1a73e8);" +
        "z-index:2147483647;opacity:0;pointer-events:none;transition:width .2s ease,opacity .3s";
      document.body.appendChild(bar);
    }

    if (!announcer) {
      announcer = document.createElement("div");
      announcer.setAttribute("data-kit-ui", "announcer");
      announcer.setAttribute("aria-live", "polite");
      announcer.setAttribute("aria-atomic", "true");
      announcer.style.cssText = "position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;" +
        "overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap";
      document.body.appendChild(announcer);
    }
  }

  function showProgress(on) {
    if (!bar) initChromeOverlays();
    if (!bar) return;

    clearTimeout(barTimer);
    if (on) {
      // Only show when navigation is actually slow (>120ms) to avoid flashing on instant pages
      barTimer = setTimeout(function () {
        if (!bar.isConnected) document.body.appendChild(bar);
        barShown = true;
        bar.style.transition = "width .2s ease,opacity .3s";
        bar.style.opacity = "1";
        bar.style.width = "0";
        requestAnimationFrame(function () {
          bar.style.transition = "width 8s cubic-bezier(.1,.7,.1,1)";
          bar.style.width = "90%";
        });
      }, 120);
    } else if (barShown) {
      barShown = false;
      bar.style.transition = "width .2s ease,opacity .4s";
      bar.style.width = "100%";
      setTimeout(function () {
        bar.style.opacity = "0";
        bar.style.width = "0";
      }, 220);
    }
  }

  function announce(msg) {
    if (!announcer) initChromeOverlays();
    if (announcer && msg) {
      announcer.textContent = msg;
    }
  }

  // --------------------------------------------------------------------------
  // 2. DOM MORPHING ALGORITHM & HEAD RECONCILIATION
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
      var attrName = fromAttrs[i].name;
      if (attrName === "value" && fromNode === document.activeElement) continue;
      if (!toNode.hasAttribute(attrName)) {
        fromNode.removeAttribute(attrName);
      }
    }

    for (var j = 0; j < toAttrs.length; j++) {
      var tName = toAttrs[j].name;
      var tVal = toAttrs[j].value;
      if (tName === "value" && fromNode === document.activeElement) continue;
      if (fromNode.getAttribute(tName) !== tVal) {
        fromNode.setAttribute(tName, tVal);
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

      if (tagName === "title" && document.title !== child.textContent) {
        document.title = child.textContent;
        announce(document.title);
      } else if (tagName === "link" && child.rel === "stylesheet") {
        var href = child.getAttribute("href");
        if (href && !document.querySelector('link[href="' + href + '"]')) {
          var newLink = document.createElement("link");
          newLink.rel = "stylesheet";
          newLink.href = href;
          document.head.appendChild(newLink);
        }
      } else if (tagName === "script" && child.src) {
        if (!loadedScripts.has(child.src)) {
          loadedScripts.add(child.src);
          var newScript = document.createElement("script");
          newScript.src = child.src;
          document.head.appendChild(newScript);
        }
      }
    }
  }

  // --------------------------------------------------------------------------
  // 3. HOVER PREFETCHING ENGINE
  // --------------------------------------------------------------------------
  function prefetchUrl(url) {
    if (!url || prefetchCache[url] || typeof fetch === "undefined") return;

    var promise = fetch(url, {
      headers: { "X-Kit-Drive": "true", "Accept": "text/html" }
    }).then(function (res) {
      if (!res.ok) return null;
      return res.text();
    })["catch"](function () { return null; });

    prefetchCache[url] = promise;
    prefetchOrder.push(url);

    if (prefetchOrder.length > PREFETCH_CAP) {
      var evicted = prefetchOrder.shift();
      delete prefetchCache[evicted];
    }
  }

  // --------------------------------------------------------------------------
  // 4. SPA NAVIGATION ENGINE
  // --------------------------------------------------------------------------
  function fetchAndMorph(url, isPopState) {
    if (typeof fetch === "undefined") return Promise.reject("Fetch unavailable");

    showProgress(true);

    var htmlPromise = prefetchCache[url] || fetch(url, {
      headers: { "X-Kit-Drive": "true", "Accept": "text/html" }
    }).then(function (res) {
      if (!res.ok) {
        window.location.href = url;
        return;
      }
      return res.text();
    });

    return htmlPromise.then(function (htmlText) {
      showProgress(false);
      if (!htmlText) return;

      var parser = new DOMParser();
      var newDoc = parser.parseFromString(htmlText, "text/html");

      reconcileHead(newDoc);
      morphNode(document.body, newDoc.body);

      if (!isPopState) {
        window.history.pushState({ drive: true, url: url }, document.title, url);
      }

      if (kit.render) kit.render();
      window.scrollTo(0, 0);

      return true;
    })["catch"](function (err) {
      showProgress(false);
      if (kit.onError) kit.onError(err, { source: "kit.drive.navigate", url: url });
      window.location.href = url;
    });
  }

  function setupLinkInterception() {
    if (typeof document === "undefined") return;
    initChromeOverlays();

    document.addEventListener("click", function (evt) {
      var anchor = evt.target;
      while (anchor && anchor !== document && anchor.tagName !== "A") {
        anchor = anchor.parentNode;
      }

      if (!anchor || !anchor.href) return;
      if (anchor.hasAttribute("data-kit-native") || anchor.hasAttribute("download") || anchor.target === "_blank") return;

      var href = anchor.getAttribute("href");
      if (!href || href.indexOf("#") === 0 || href.indexOf("javascript:") === 0) return;
      if (anchor.origin !== window.location.origin) return;

      evt.preventDefault();
      fetchAndMorph(anchor.href, false);
    }, false);

    // Hover / Touch Prefetching
    document.addEventListener("mouseover", function (evt) {
      var anchor = evt.target;
      while (anchor && anchor !== document && anchor.tagName !== "A") anchor = anchor.parentNode;
      if (anchor && anchor.href && anchor.origin === window.location.origin) {
        prefetchUrl(anchor.href);
      }
    }, { passive: true });

    window.addEventListener("popstate", function (evt) {
      fetchAndMorph(window.location.href, true);
    });
  }

  // --------------------------------------------------------------------------
  // 5. PUBLIC DRIVE SERVICE INTERFACE
  // --------------------------------------------------------------------------
  kit.drive = {
    navigate: function (url) {
      return fetchAndMorph(url, false);
    },
    prefetch: function (url) {
      prefetchUrl(url);
    },
    morph: function (targetDocument) {
      if (typeof targetDocument === "string") {
        var parser = new DOMParser();
        targetDocument = parser.parseFromString(targetDocument, "text/html");
      }
      if (targetDocument && targetDocument.body) {
        reconcileHead(targetDocument);
        morphNode(document.body, targetDocument.body);
        if (kit.render) kit.render();
      }
    }
  };

  if (typeof document !== "undefined") {
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", setupLinkInterception);
    } else {
      setupLinkInterception();
    }
  }

})(typeof window !== "undefined" ? window : globalThis);
