// Optional Kitwork Drive module: same-origin navigation, head reconciliation and scroll restore.
(function (window, document) {
  "use strict";
  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || !kit.module || kit.has("drive")) return;
  var listen = kit.internal.listen;
  var cleanup = kit.cleanup;

  // ---- Drive: optional SPA navigation over the kernel's morph/lifecycle contracts ----
  // Ported from the proven standalone hydrate.js. Activates at boot ONLY when the SITE asked for it
  // — router.drive(), which the engine flags on its own runtime script tag — AND no drive is already
  // running: kit.hydrate is the two-way lock, so a page still carrying the legacy standalone file
  // keeps it and the two never double-drive.
  // Contract unchanged: intercept same-origin links + GET forms (unless data-kit-reload="true"),
  // fetch with X-Kitwork-Hydrate, swap <main> (fallback <body>), morph, mergeHead swaps
  // data-kitwork-jit blocks, history + scroll restoration and bounded hover prefetch.
  function start() {
    if (typeof kit.morph !== "function") return;
    if (kit.hydrate || !window.history.pushState || !window.fetch || !window.DOMParser) return;
    if (!document.querySelector('script[data-kitwork-jit="runtime"][data-kitwork-drive]')) return;
    kit.hydrate = true;

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
    // Colour: --kitwork-progress, set in CSS, else #1a73e8, the browser-blue default. (A
    // data-kit-progress attribute that set the same variable went on 02/10: no page wrote one.)
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
    // A window opened with app.desktop({ chrome: "html" }) has a title bar the page draws itself,
    // marked data-kit-drag="true". The bar goes UNDER it, where a browser's own progress sits, not
    // across the window's controls. Read at show time: the title bar may come and go with a swap.
    function barTop() {
      var titleBar = document.querySelector('[data-kit-drag="true"]');
      if (!titleBar) return 0;
      var box = titleBar.getBoundingClientRect();
      return box.top <= 0 && box.bottom > 0 ? box.bottom : 0;
    }
    function pageShowsProgress() {
      return !!document.querySelector('[data-kit-component="progressbar"],[data-kit-component^="progressbar@"]');
    }
    function progress(on) {
      cancelAnimationFrame(rafId);
      clearTimeout(barTimer);
      if (on) {
        // Show only when the navigation is actually slow (>120ms) — prefetched/fast pages swap
        // before this fires and never flash the bar, so its behaviour reads as consistent.
        barTimer = setTimeout(function () {
          barShown = true;
          // The navigation is work in flight like any other: it reports to kit.progress. A page that
          // shows kit.progress itself — a progressbar component — gets no second bar from Drive.
          if (kit.progress) kit.progress.start();
          if (pageShowsProgress()) return;
          if (!bar.isConnected) document.body.appendChild(bar);
          bar.style.top = barTop() + "px";
          bar.style.transition = "width .2s ease,opacity .3s";
          bar.style.opacity = "1"; bar.style.width = "0";
          rafId = requestAnimationFrame(function () { bar.style.transition = "width 8s cubic-bezier(.1,.7,.1,1)"; bar.style.width = "90%"; });
        }, 120);
      } else if (barShown) {
        barShown = false;
        if (kit.progress) kit.progress.finish();
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

    function region(doc) { return doc.querySelector("main") || doc.body; }
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
      // The author's own opt-out: data-kit-reload="true" means "load this the ordinary way".
      // It is read on the link and on any ancestor, so a whole region can step out at once.
      if (a.getAttribute("data-kit-reload") === "true") return false;
      if (a.closest && a.closest('[data-kit-reload="true"]')) return false;
      if (a.getAttribute("data-kit-click")) return false; // an expression link does not navigate
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
    //
    // A script the page arriving needs and this one never loaded — a component file of its own — is
    // CREATED, not cloned: a script parsed by DOMParser never runs, and neither does a copy of one.
    // The returned promises settle when each has loaded (or failed), so the swap can wait for them:
    // morphing first would put the component's host on the page before its file had registered it.
    function mergeHead(doc) {
      var have = new Set(), waits = [];
      document.head.querySelectorAll("link[rel=stylesheet][href]").forEach(function (n) { have.add(n.href); });
      document.head.querySelectorAll("script[src]").forEach(function (n) { have.add(n.src); });
      doc.head.querySelectorAll('link[rel="stylesheet"][href]').forEach(function (n) {
        if (!have.has(n.href)) document.head.appendChild(n.cloneNode(true));
      });
      doc.head.querySelectorAll("script[src]").forEach(function (n) {
        if (have.has(n.src) || loadedSrc.has(n.src)) return;
        loadedSrc.add(n.src);
        var script = document.createElement("script");
        for (var i = 0; i < n.attributes.length; i++) script.setAttribute(n.attributes[i].name, n.attributes[i].value);
        script.async = false; // run in document order, as they would have on a full load
        waits.push(new Promise(function (resolve) { script.onload = script.onerror = resolve; }));
        document.head.appendChild(script);
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
      return waits;
    }

    function swap(html, url, push, scrollY, responseLayout) {
      var doc = new DOMParser().parseFromString(html, "text/html");

      // (There used to be a version check here: it read `mode@version` off the root marker and forced
      // a hard reload when the value differed. It never fired — every page of a site shares one shell,
      // so the value was always identical — and the value itself was arbitrary: 17 of this repo's 21
      // shells carried strings like "v1.0.0", "seoer" or "kitwork-canary" that nothing read. Forcing a
      // reload across a deploy is a real need, but it has to be answered with something the engine
      // actually knows, not with an attribute an author typed for another reason.)

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
      var waits = mergeHead(doc);
      if (waits.length) Promise.all(waits).then(finish);
      else finish();

      function finish() {
        kit.morph(cur, next);
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
      if (f.getAttribute("data-kit-reload") === "true") return;
      if (f.closest && f.closest('[data-kit-reload="true"]')) return;
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
      kit.hydrate = false;
    });

    drive.visit = visit;
    drive.prefetch = doPrefetch;
    if ("scrollRestoration" in history) history.scrollRestoration = "manual";
    history.replaceState({ kitwork: true }, "", location.href);
  }

  var drive = { start: start };
  kit.drive = drive;
  kit.module("drive", drive);
  kit.onStart(start);
})(window, document);
