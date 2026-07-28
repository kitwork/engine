// Optional Kitwork Drive module: same-origin navigation, head reconciliation and scroll restore.
(function (window, document) {
  "use strict";
  var kitwork = window.kitwork;
  if (!kitwork || !kitwork.module || kitwork.has("drive")) return;
  var listen = kitwork.internal.listen;
  var cleanup = kitwork.cleanup;

  // ---- Drive: optional SPA navigation over the kernel's morph/lifecycle contracts ----
  // Ported from the proven standalone hydrate.js. Activates at boot ONLY when the page declares a
  // hydrate region ([data-kitwork-hydrate] / [data-kit-hydrate]) AND no drive is already running:
  // kitwork.hydrate is the two-way lock — the legacy standalone file sets the same flag, so old
  // pages keep their file, new pages use the kernel, and the two never double-drive.
  // Contract unchanged: intercept same-origin links + GET forms, fetch with X-Kitwork-Hydrate,
  // swap the region (fallback <main>), morph, mergeHead swaps data-kitwork-jit blocks, history +
  // scroll restoration and bounded hover prefetch. Everything is delegated.
  function start() {
    if (typeof kitwork.morph !== "function") return;
    if (kitwork.hydrate || !window.history.pushState || !window.fetch || !window.DOMParser) return;
    var appEl = document.querySelector("[data-kitwork-app],[data-kit-app],[data-kitwork-hydrate],[data-kit-hydrate]");
    if (!appEl) return;
    kitwork.hydrate = true;

    // mode & version are parsed globally by initAppConfig at boot

    var inflight = null;                  // AbortController for the current visit
    var watchdog = null;                  // fetch-timeout watchdog timer
    var prefetch = Object.create(null);   // url -> Promise<{html,url,layout}>
    var prefetchOrder = [];               // FIFO of prefetched urls (cache cap)
    var loadedSrc = new Set();            // external scripts already executed (never re-run)
    document.querySelectorAll("script[src]").forEach(function (s) { loadedSrc.add(s.src); });

    // Self-contained top progress bar — no markup needed in the shell. data-kitwork-ui marks it as
    // a kernel overlay so morph leaves it alone; the isConnected re-append is belt-and-braces for
    // any other code that clears <body>.
    var bar = document.createElement("div");
    bar.setAttribute("data-kitwork-ui", "progress");
    // Colour resolution, most specific first:
    //   data-kit-progress="#0af" on the app root — this bar only
    //   --kitwork-progress                       — same, from CSS
    //   #1a73e8                                  — the browser-blue default
    //
    // The default is deliberately NOT the site's brand. A loading bar is chrome, not content: blue
    // is what a browser's own progress reads as, so an unconfigured site gets something that looks
    // like part of the browser rather than a stripe of its accent colour. --kitwork-brand is left
    // out of the chain on purpose — it is always emitted now, so including it would mean the blue
    // default could never appear. A site that DOES want the bar to track its accent says so in one
    // line: --kitwork-progress: var(--kitwork-brand).
    bar.style.cssText = "position:fixed;top:0;left:0;height:2px;width:0;" +
      "background:var(--kitwork-progress,#1a73e8);" +
      "z-index:2147483647;opacity:0;pointer-events:none;transition:width .2s ease,opacity .3s";
    document.body.appendChild(bar);
    var rafId = 0, barTimer = 0, barShown = false;
    function progress(on) {
      cancelAnimationFrame(rafId);
      clearTimeout(barTimer);
      if (on) {
        // Show only when the navigation is actually slow (>120ms) — prefetched/fast pages swap
        // before this fires and never flash the bar, so its behaviour reads as consistent.
        barTimer = setTimeout(function () {
          if (!bar.isConnected) document.body.appendChild(bar);
          barShown = true;
          bar.style.transition = "width .2s ease,opacity .3s";
          bar.style.opacity = "1"; bar.style.width = "0";
          rafId = requestAnimationFrame(function () { bar.style.transition = "width 8s cubic-bezier(.1,.7,.1,1)"; bar.style.width = "90%"; });
        }, 120);
      } else if (barShown) {
        barShown = false;
        bar.style.transition = "width .2s ease,opacity .4s"; bar.style.width = "100%";
        setTimeout(function () { bar.style.opacity = "0"; bar.style.width = "0"; }, 220);
      }
    }

    // Visually-hidden live region so screen readers hear page changes. Kernel overlay like the bar.
    var announcer = document.createElement("div");
    announcer.setAttribute("data-kitwork-ui", "announcer");
    announcer.setAttribute("aria-live", "polite");
    announcer.setAttribute("aria-atomic", "true");
    announcer.style.cssText = "position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;" +
      "overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap";
    document.body.appendChild(announcer);

    function region(doc) { return doc.querySelector("[data-kitwork-app],[data-kit-app],[data-kitwork-hydrate],[data-kit-hydrate]") || doc.querySelector("main"); }
    function layoutKey(doc) { return doc.body ? (doc.body.getAttribute("data-kitwork-layout") || "") : ""; }
    function sameOrigin(url) { try { return new URL(url, location.href).origin === location.origin; } catch (e) { return false; } }

    // Run a scroll op with smooth-scrolling forced OFF, then restore.
    function instant(fn) {
      var de = document.documentElement, bd = document.body;
      var p1 = de.style.scrollBehavior, p2 = bd ? bd.style.scrollBehavior : "";
      de.style.scrollBehavior = "auto"; if (bd) bd.style.scrollBehavior = "auto";
      fn();
      de.style.scrollBehavior = p1; if (bd) bd.style.scrollBehavior = p2;
    }

    // Should this click be driven, or left to the browser (or to another kernel behavior)?
    function drivable(a, e) {
      if (!a || !a.href) return false;
      if (e && (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey)) return false;
      if (a.target === "_blank" || a.hasAttribute("download") || a.getAttribute("rel") === "external") return false;
      if (a.getAttribute("data-kitwork-app") === "false" || a.getAttribute("data-kit-app") === "false" ||
        a.getAttribute("data-kitwork-hydrate") === "false" || a.getAttribute("data-kit-hydrate") === "false") return false;
      if (a.closest && (
        a.closest("[data-kitwork-app='false'],[data-kit-app='false']") ||
        a.closest("[data-kitwork-hydrate='false'],[data-kit-hydrate='false']")
      )) return false;
      if (a.getAttribute("data-kitwork-action") || a.getAttribute("data-kit-action")) return false; // verbs own their triggers
      if (a.getAttribute("data-kit-click") || a.getAttribute("data-kitwork-click")) return false; // expression links (source or IR) don't navigate
      if (!sameOrigin(a.href)) return false;
      var u = new URL(a.href);
      if (u.pathname === location.pathname && u.search === location.search && u.hash) return false; // in-page #anchor
      return true;
    }

    // Fetch a page; resolves to { html, url, layout } where url is the FINAL url (after redirects).
    function fetchPage(url, signal) {
      return fetch(url, { headers: { "X-Kitwork-Hydrate": "1" }, signal: signal, credentials: "same-origin" })
        .then(function (r) {
          var ct = r.headers.get("content-type") || "";
          if (!r.ok || ct.indexOf("text/html") === -1) throw new Error("not drivable: " + r.status);
          var layout = r.headers.get("X-Kitwork-Layout") || "";
          return r.text().then(function (html) { return { html: html, url: r.url || url, layout: layout }; });
        });
    }

    // Re-create <script> nodes so they execute; skip external src already loaded (no double-run).
    function runScripts(root) {
      root.querySelectorAll("script").forEach(function (old) {
        if (old.src) { if (loadedSrc.has(old.src)) { old.remove(); return; } loadedSrc.add(old.src); }
        var s = document.createElement("script");
        for (var i = 0; i < old.attributes.length; i++) s.setAttribute(old.attributes[i].name, old.attributes[i].value);
        s.textContent = old.textContent;
        old.replaceWith(s);
      });
    }

    // Reconcile <head> with the fetched page: bring over genuinely-new EXTERNAL assets (deduped),
    // REPLACE the per-page JIT stylesheets, and re-CREATE the jitjs script so it executes — the
    // kernel is boot-guarded, so re-running never adds listeners; verb registration is idempotent.
    function mergeHead(doc) {
      var have = new Set();
      document.head.querySelectorAll("link[rel=stylesheet][href]").forEach(function (n) { have.add(n.href); });
      document.head.querySelectorAll("script[src]").forEach(function (n) { have.add(n.src); });
      doc.head.querySelectorAll('link[rel="stylesheet"][href]').forEach(function (n) {
        if (!have.has(n.href)) document.head.appendChild(n.cloneNode(true));
      });
      doc.head.querySelectorAll("script[src]").forEach(function (n) {
        if (!have.has(n.src)) document.head.appendChild(n.cloneNode(true));
      });
      document.head.querySelectorAll("style[data-kitwork-jit]").forEach(function (n) { n.remove(); });
      doc.head.querySelectorAll("style[data-kitwork-jit]").forEach(function (n) {
        document.head.appendChild(n.cloneNode(true));
      });
      var oldRun = document.head.querySelector('script[data-kitwork-jit="js"]');
      if (oldRun) oldRun.remove();
      var newRun = doc.head.querySelector('script[data-kitwork-jit="js"]');
      if (newRun) {
        var run = document.createElement("script");
        run.setAttribute("data-kitwork-jit", "js");
        run.textContent = newRun.textContent;
        document.head.appendChild(run);
      }
    }

    function swap(html, url, push, scrollY, responseLayout) {
      var doc = new DOMParser().parseFromString(html, "text/html");

      // Version Mismatch Check: force hard reload if the new page version differs
      var newAppEl = doc.querySelector("[data-kitwork-app],[data-kit-app],[data-kitwork-hydrate],[data-kit-hydrate]");
      if (newAppEl) {
        var newAppVal = newAppEl.getAttribute("data-kitwork-app") || newAppEl.getAttribute("data-kit-app") || "";
        var newVersion = "latest";
        if (newAppVal) {
          var parts = newAppVal.split("@");
          if (parts.length === 2) {
            newVersion = parts[1];
          } else if (parts.length === 1 && parts[0]) {
            var val = parts[0];
            if (val.charAt(0) === "v" || (val.charAt(0) >= "0" && val.charAt(0) <= "9")) {
              newVersion = val;
            }
          }
        }
        if (newVersion !== kitwork.version) {
          location.assign(url);
          return;
        }
      }

      var docLayout = responseLayout || layoutKey(doc);
      var sameLayout = docLayout === layoutKey(document);
      var cur = sameLayout ? region(document) : document.querySelector("[data-kitwork-shell]");
      var next = sameLayout ? region(doc) : doc.querySelector("[data-kitwork-shell]");

      if (sameLayout && cur && !next) {
        // Raw fragment (no shell): wrap it in a virtual node matching the current region.
        next = doc.createElement(cur.tagName);
        for (var i = 0; i < cur.attributes.length; i++) next.setAttribute(cur.attributes[i].name, cur.attributes[i].value);
        var body = doc.body || doc.documentElement;
        while (body.firstChild) next.appendChild(body.firstChild);
      }
      if (!cur || !next) { location.assign(url); return; } // nothing safe to swap → real navigation

      document.dispatchEvent(new CustomEvent("kitwork:before-swap", { detail: { url: url } }));
      if (doc.title) document.title = doc.title;
      mergeHead(doc);
      kitwork.morph(cur, next);
      if (!sameLayout && doc.body) {
        document.body.className = doc.body.className;
        document.body.setAttribute("data-kitwork-layout", layoutKey(doc));
      }
      runScripts(cur);

      if (push) history.pushState({ kitwork: true }, "", url);

      var hash = ""; try { hash = new URL(url, location.href).hash; } catch (e) { }
      var anchor = null;
      if (hash) { try { anchor = document.getElementById(decodeURIComponent(hash.slice(1))); } catch (e) { } }
      instant(function () {
        if (anchor) anchor.scrollIntoView();
        else window.scrollTo(0, scrollY || 0);
      });

      var t = region(document);
      if (t) { t.setAttribute("tabindex", "-1"); try { t.focus({ preventScroll: true }); } catch (e) { } }
      if (!announcer.isConnected) document.body.appendChild(announcer);
      announcer.textContent = "";
      setTimeout(function () { announcer.textContent = document.title; }, 50);
      document.dispatchEvent(new CustomEvent("kitwork:load", { detail: { url: url } }));
    }

    function visit(url, push, scrollY) {
      if (inflight) inflight.abort();
      clearTimeout(watchdog);
      inflight = new AbortController();
      document.documentElement.classList.add("kitwork-loading");
      progress(true);
      document.dispatchEvent(new CustomEvent("kitwork:before-visit", { detail: { url: url } }));
      watchdog = setTimeout(function () {
        if (inflight) { inflight.abort(); location.assign(url); }
      }, 3000);

      var p = prefetch[url] || fetchPage(url, inflight.signal);
      delete prefetch[url];
      p.then(function (res) {
        clearTimeout(watchdog);
        var finalUrl = (res && res.url) || url;
        var h = ""; try { h = new URL(url, location.href).hash; } catch (e) { }
        if (h) { try { if (!new URL(finalUrl, location.href).hash) finalUrl += h; } catch (e) { } }
        swap(res.html, finalUrl, push, scrollY, res.layout);
      })
        .catch(function (err) {
          clearTimeout(watchdog);
          if (!err || err.name !== "AbortError") location.assign(url);
        })
        .then(function () {
          document.documentElement.classList.remove("kitwork-loading");
          progress(false);
          inflight = null;
        });
    }

    // Bounded hover-prefetch cache.
    function doPrefetch(url) {
      if (prefetch[url]) return;
      prefetch[url] = fetchPage(url).catch(function () { delete prefetch[url]; return Promise.reject(); });
      prefetchOrder.push(url);
      if (prefetchOrder.length > 15) { var old = prefetchOrder.shift(); delete prefetch[old]; }
    }

    // Click navigation (delegated → survives swaps).
    listen(document, "click", function (e) {
      var a = e.target.closest && e.target.closest("a[href]");
      if (!a || !drivable(a, e)) return;
      var dest; try { dest = new URL(a.href, location.href); } catch (x) { return; }
      e.preventDefault();
      if (dest.pathname === location.pathname && dest.search === location.search && !dest.hash) {
        instant(function () { window.scrollTo(0, 0); });
        return;
      }
      history.replaceState({ kitwork: true, y: window.scrollY }, "", location.href);
      visit(a.href, true, 0);
    });

    // GET form submits — POST and other methods fall through (the kernel's validate gate, which
    // runs in the capture phase, has already stopped invalid forms: defaultPrevented is respected).
    listen(document, "submit", function (e) {
      if (e.defaultPrevented) return;
      var f = e.target;
      if (!f || f.tagName !== "FORM" || (f.method || "get").toLowerCase() !== "get") return;
      if (f.getAttribute("data-kitwork-app") === "false" || f.getAttribute("data-kit-app") === "false" ||
        f.getAttribute("data-kitwork-hydrate") === "false" || f.getAttribute("data-kit-hydrate") === "false") return;
      if (f.getAttribute("data-kitwork-action") || f.getAttribute("data-kit-action")) return;
      var u; try { u = new URL(f.action || location.href, location.href); } catch (x) { return; }
      if (!sameOrigin(u.href)) return;
      try { u.search = new URLSearchParams(new FormData(f)).toString(); } catch (x) { return; }
      e.preventDefault();
      history.replaceState({ kitwork: true, y: window.scrollY }, "", location.href);
      visit(u.href, true, 0);
    });

    // Prefetch on hover (delayed so quick fly-overs don't fetch).
    var hoverTimer;
    listen(document, "mouseover", function (e) {
      var a = e.target.closest && e.target.closest("a[href]");
      if (!a || !drivable(a, null) || prefetch[a.href]) return;
      var href = a.href;
      clearTimeout(hoverTimer);
      hoverTimer = setTimeout(function () { doPrefetch(href); }, 65);
    });

    // Preserve normal browser Back semantics and restore the scroll position recorded in history.
    listen(window, "popstate", function (e) {
      visit(location.href, false, (e.state && e.state.y) || 0);
    });

    cleanup(function () {
      if (inflight) inflight.abort();
      clearTimeout(watchdog);
      clearTimeout(hoverTimer);
      clearTimeout(barTimer);
      cancelAnimationFrame(rafId);
      if (bar.parentNode) bar.parentNode.removeChild(bar);
      if (announcer.parentNode) announcer.parentNode.removeChild(announcer);
      kitwork.hydrate = false;
    });

    drive.visit = visit;
    drive.prefetch = doPrefetch;
    if ("scrollRestoration" in history) history.scrollRestoration = "manual";
    history.replaceState({ kitwork: true }, "", location.href);
  }

  var drive = { start: start };
  kitwork.drive = drive;
  kitwork.module("drive", drive);
  kitwork.onStart(start);
})(window, document);
