// Optional Kitwork Drive module: same-origin navigation, head reconciliation and scroll restore.
(function (window, document) {
  "use strict";
  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || !kit.module || kit.has("drive")) return;
  var listen = kit.internal.listen;
  var cleanup = kit.cleanup;

  // ---- Drive: optional SPA navigation over the kernel's morph/lifecycle contracts ----
  // Ported from the proven standalone hydrate.js. This file is on the page only when the SITE asked
  // for Drive (router.reload(false), the default of a Kitwork UI site): the engine delivers it to no
  // other page, so being loaded is the answer and there is no flag to read. It activates at boot
  // unless a drive is already running: kit.hydrate is the two-way lock, so a page still carrying the
  // legacy standalone file keeps it and the two never double-drive.
  // Contract unchanged: intercept same-origin links + GET forms (unless data-kit-reload="true"),
  // fetch with X-Kitwork-Hydrate, swap <main> (fallback <body>), morph, mergeHead swaps
  // data-kitwork-generated blocks — each of the page's stylesheets that changes is loaded before the
  // swap, which waits for it —,
  // history + scroll restoration and bounded hover prefetch.
  function start() {
    if (typeof kit.morph !== "function") return;
    if (kit.hydrate || !window.history.pushState || !window.fetch || !window.DOMParser) return;
    kit.hydrate = true;

    var inflight = null;                  // AbortController for the current visit
    var watchdog = null;                  // fetch-timeout watchdog timer
    var prefetch = Object.create(null);   // url -> Promise<{html,url,layout}>
    var prefetchOrder = [];               // FIFO of prefetched urls (cache cap)
    var loadedSrc = new Set();            // external scripts already executed (never re-run)
    document.querySelectorAll("script[src]").forEach(function (s) { loadedSrc.add(s.src); });

    // The bar between pages is the progressbar COMPONENT, not something Drive draws (10/10/2026): a
    // Kitwork UI page has one first in <page>, and the engine puts one at the top of <body> on any
    // other page of a site that moves without a reload. Drive only reports the move to kit.progress,
    // which the component shows (and which it places under a title bar the page draws itself). One
    // bar, written once, with the same color / thickness / delay everywhere.
    var barTimer = 0, barShown = false;
    // The index of the history entry on screen (see popstate): a reload keeps the entry's own.
    var historyIndex = history.state && typeof history.state.i === "number" ? history.state.i : 0;
    function progressHost() {
      return document.querySelector('[data-kit-component="progressbar"],[data-kit-component^="progressbar@"],' +
        '[data-kitwork-component="progressbar"],[data-kitwork-component^="progressbar@"],[data-kitwork-component^="progressbar:"]');
    }
    // How long a move may take before it counts as slow and the bar shows: 120ms, or what the page's
    // progressbar says (<box component="progressbar" delay="0" /> → data-kitwork-delay). Read at each move, since
    // the progressbar lives in the page and the page may change.
    function progressDelay() {
      var host = progressHost();
      var ms = host ? parseInt(host.getAttribute("data-kitwork-delay"), 10) : NaN;
      return ms >= 0 && ms <= 10000 ? ms : 120;
    }
    function progress(on) {
      clearTimeout(barTimer);
      if (on) {
        // Show only when the navigation is actually slow (progressDelay(), 120ms unless the page says) — prefetched/fast pages swap
        // before this fires and never flash the bar, so its behaviour reads as consistent. A page
        // that asked for delay 0 wants the bar on EVERY move, so it starts now, on the click: a page
        // fetched ahead (the pointer rested on the link) swaps within this same turn, before any
        // timer could fire, and would otherwise never show it.
        var show = function () {
          barShown = true;
          if (kit.progress) kit.progress.start();
        };
        var wait = progressDelay();
        if (wait === 0) show();
        else barTimer = setTimeout(show, wait);
      } else if (barShown) {
        barShown = false;
        if (kit.progress) kit.progress.finish();
      }
    }

    // Elements this file makes for itself: morph and mergeHead leave them alone (morph.js, clientOwned).
    var clientOwned = kit.internal.clientOwned || (kit.internal.clientOwned = new WeakSet());

    function region(doc) { return doc.querySelector("main") || doc.body; }

    // Links the swap keeps (a Kitwork UI page's header and navbar, marked data-kitwork-current by the
    // engine) still name the page being viewed. Mark them again by the server's rule: "page" for this
    // page, "true" for a folder it sits in ("/" and data-kitwork-current="exact" only for themselves).
    // A link without the mark is the site's own business and is never touched.
    function trimPath(p) { return p.length > 1 ? p.replace(/\/+$/, "") : p; }
    function markCurrent() {
      var here = trimPath(location.pathname);
      document.querySelectorAll("a[data-kitwork-current]").forEach(function (a) {
        var to; try { to = new URL(a.href); } catch (e) { return; }
        if (to.origin !== location.origin) return;
        var path = trimPath(to.pathname), state = "";
        if (path === here) state = "page";
        else if (path !== "/" && a.getAttribute("data-kitwork-current") !== "exact" && here.indexOf(path + "/") === 0) state = "true";
        if (state) a.setAttribute("aria-current", state); else a.removeAttribute("aria-current");
      });
    }
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

    // A Kitwork UI link with a click handler carries it in data-kitwork-event ("click: open()",
    // "click.prevent: …"): read with the kernel's own splitter, so a ';' inside a string is not a pair.
    // A value that does not read has no handler the kernel runs either.
    function clicks(a) {
      var list = [];
      try { list = kit.internal.grouped.pairs(a.getAttribute("data-kitwork-event") || ""); } catch (e) { }
      return list.some(function (pair) { return pair[0].split(".")[0] === "click"; });
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
      if (a.getAttribute("data-kit-click") || clicks(a)) return false; // an expression link does not navigate
      if (!sameOrigin(a.href)) return false;
      var u = new URL(a.href);
      if (u.pathname === location.pathname && u.search === location.search && u.hash) return false; // in-page #anchor
      return true;
    }

    // Fetch a page; resolves to { html, url, layout } where url is the FINAL url (after redirects).
    // A 404 is a page too: the site's not-found page sits in the same frame (a Kitwork UI site's
    // notfound.ui inside page.ui), so it swaps like any other. Loading it whole would drop what the
    // swap keeps — and the next request would only answer 404 again.
    function fetchPage(url, signal) {
      return fetch(url, { headers: { "X-Kitwork-Hydrate": "1" }, signal: signal, credentials: "same-origin" })
        .then(function (r) {
          var ct = r.headers.get("content-type") || "";
          if ((!r.ok && r.status !== 404) || ct.indexOf("text/html") === -1) throw new Error("not drivable: " + r.status);
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
    var GENERATED_STYLE = "style[data-kitwork-generated],style[data-kitwork-jit]";
    function mergeHead(doc) {
      var have = new Set(), waits = [];
      document.head.querySelectorAll("link[rel=stylesheet][href]").forEach(function (n) { have.add(n.href); });
      document.head.querySelectorAll("script[src]").forEach(function (n) { have.add(n.src); });
      // The page's own stylesheets (data-kitwork-generated) are traded, not added: stageStylesheets.
      doc.head.querySelectorAll('link[rel="stylesheet"][href]:not([data-kitwork-generated])').forEach(function (n) {
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
      // A part a .ui page writes in itself (router.bundle javascript "inline": <script type="module"
      // data-kitwork-part="<the file it stands for>">) that this page has not run runs now, before the
      // swap, as a file the page arriving adds would; one it has run is skipped (window.kitworkParts,
      // which every such part and every bundled one marks).
      doc.head.querySelectorAll("script[data-kitwork-part]:not([src])").forEach(function (n) {
        var part = n.getAttribute("data-kitwork-part"), ran = window.kitworkParts || {};
        if (ran[part]) return;
        var script = document.createElement("script");
        script.setAttribute("data-kitwork-part", part);
        script.textContent = n.textContent; // a classic script added by a script runs at once
        document.head.appendChild(script);
      });
      return waits;
    }

    // ---- the page's stylesheets ----
    // The engine's CSS for a page stands in its head in the order of the cascade, each part a file named
    // by its role and its bytes or a block written in the page (a Kitwork UI page's by router.bundle; a
    // template page's JIT blocks, which are not layered, so for them the order IS the cascade):
    //   <style data-kitwork-generated="site">…</style>   or <link rel="stylesheet" href="/_kitwork/site.<hash>.css" data-kitwork-generated="site">
    //   <style data-kitwork-generated="icons">…</style>  or <link … href="/_kitwork/icons.<hash>.css" data-kitwork-generated="icons">
    //   <style data-kitwork-generated="css">…</style>    or <link … href="/_kitwork/page.<hash>.css" data-kitwork-generated="css">
    // A move trades them as one list, in the arriving page's order. A file the page arriving links as
    // well (the same href), or a block it carries byte for byte, is left as it is: the same element,
    // nothing fetched or read again (a block taken off and put back would read its @font-face again).
    // Every other part is added BEFORE the swap, kept off the screen (media="print") so the page still
    // on screen does not take its rules, right after the part before it in the arriving page's order
    // (the first one: before the first part kept after it, else where the stylesheets stand now), so
    // after the swap every part stands where a whole load of that page puts it, whichever were kept. The
    // swap waits for every new file (load or error, each at most STYLESHEET_WAIT ms; a block is read at
    // once). Then, inside finish() with the morph, the new parts are switched on and every part the page
    // arriving does not carry is removed, in the same step: under a View Transition that is the update
    // callback, so the old snapshot is taken in the old page's look and the new one in the new page's.
    // The new content is never shown before its CSS, nor the old in the new look, and no moment passes
    // without a stylesheet either page needs. What the client made for itself (the transition
    // stylesheet) stays. A page rendered before 10/10/2026 marks its blocks data-kitwork-jit, and a
    // cached copy of one may be either side of the swap, so both names are read. A part staged for a
    // move a newer one replaced is dropped by the newer one.
    var GENERATED_LINK = 'link[rel="stylesheet"][data-kitwork-generated]';
    var GENERATED_CSS = GENERATED_LINK + "," + GENERATED_STYLE;
    var STYLESHEET_WAIT = 3000;
    var staging = [];
    function stageStylesheets(doc) {
      staging.forEach(function (s) { s.link.remove(); });
      staging = [];
      var same = function (n) { return n.nodeName === "LINK" ? n.href : n.outerHTML; }; // a file by its URL, a block by its bytes
      var on = {}, live = new Set(), order = [], waits = [];
      var current = Array.prototype.filter.call(document.head.querySelectorAll(GENERATED_CSS), function (n) { return !clientOwned.has(n); });
      current.forEach(function (n) { (on[same(n)] = on[same(n)] || []).push(n); });
      doc.head.querySelectorAll(GENERATED_CSS).forEach(function (n) {
        var kept = (on[same(n)] || []).shift() || null;
        if (kept) live.add(kept);
        order.push({ n: n, kept: kept });
      });
      var placed = null; // the element of the previous part in the arriving page's order
      order.forEach(function (o, at) {
        if (o.kept) { placed = o.kept; return; } // the same part: left alone
        var n = o.n, media = n.getAttribute("media"), part = n.cloneNode(true);
        if (n.nodeName === "LINK") {
          part = document.createElement("link");
          for (var i = 0; i < n.attributes.length; i++) part.setAttribute(n.attributes[i].name, n.attributes[i].value);
          waits.push(new Promise(function (resolve) {
            var timer = setTimeout(resolve, STYLESHEET_WAIT);
            part.onload = part.onerror = function () { clearTimeout(timer); resolve(); };
          }));
        }
        part.setAttribute("media", "print");
        staging.push({ link: part, media: media });
        live.add(part);
        if (placed) {
          placed.parentNode.insertBefore(part, placed.nextSibling); // right after the part before it
        } else {
          var before = null;
          for (var k = at + 1; k < order.length && !before; k++) before = order[k].kept;
          before = before || current[0] || null;
          if (before) before.parentNode.insertBefore(part, before);
          else document.head.appendChild(part);
        }
        placed = part;
      });
      return { live: live, staged: staging, waits: waits };
    }
    function swapStylesheets(stage) {
      stage.staged.forEach(function (s) {
        if (s.media) s.link.setAttribute("media", s.media);
        else s.link.removeAttribute("media");
      });
      if (staging === stage.staged) staging = [];
      document.head.querySelectorAll(GENERATED_CSS).forEach(function (n) {
        if (!clientOwned.has(n) && !stage.live.has(n)) n.remove();
      });
    }

    // The colour a mobile browser paints its bar in is the arriving page's <meta name="theme-color"> (a
    // Kitwork UI page with a backdrop writes one per mode), as a whole load would show: one that names
    // none goes back to the browser's own. A meta that carries both modes' colours (data-kitwork-light,
    // data-kitwork-dark) takes the mode shown, as the pre-paint and kit.appearance set it: its colour, no
    // media, so a visitor's choice of the other mode than the device's holds across the move.
    function swapThemeColor(doc) {
      var key = function (list) { return Array.prototype.map.call(list, function (n) { return n.outerHTML; }).join(""); };
      var shown = "data-kitwork-" + document.documentElement.getAttribute("data-theme");
      var cur = document.head.querySelectorAll('meta[name="theme-color"]');
      var next = Array.prototype.map.call(doc.head ? doc.head.querySelectorAll('meta[name="theme-color"]') : [], function (n) {
        var copy = n.cloneNode(true);
        if (copy.hasAttribute(shown)) {
          copy.setAttribute("content", copy.getAttribute(shown));
          copy.removeAttribute("media");
        }
        return copy;
      });
      if (key(cur) === key(next)) return;
      cur.forEach(function (n) { n.remove(); });
      next.forEach(function (n) { document.head.appendChild(n); });
    }

    // <page background="cirrus"> is painted on a layer fixed to the screen, first in <body>, outside the
    // region a move swaps (data-kitwork-backdrop). The same backdrop on the next page keeps the layer on
    // screen, the same node, so nothing repaints; another takes its place; none removes it. A layer
    // inside the region (a page without <main>) is the morph's. On a Kitwork UI site the backdrop is
    // page.ui's, and a move to another page.ui is a whole load (swap, below): there only the first case
    // runs; the other two serve a page whose shell stays while its backdrop changes.
    function swapBackdrop(doc, region) {
      var BACKDROP = "[data-kitwork-backdrop]";
      var cur = document.querySelector(BACKDROP), next = doc.querySelector(BACKDROP);
      if (cur && region.contains(cur)) return;
      if (cur && next && cur.className === next.className) return;
      if (cur) cur.remove();
      if (next && document.body) document.body.insertBefore(document.importNode(next, true), document.body.firstChild);
    }

    // The tab's icon is the arriving page's (router.favicon of its nearest folder), as a whole load of it
    // would show. A page that names no icon keeps the one on screen: an older shell writes it once.
    function swapIcons(doc) {
      var next = doc.head ? doc.head.querySelectorAll('link[rel~="icon"]') : [];
      if (!next.length) return;
      var key = function (list) { return Array.prototype.map.call(list, function (n) { return n.outerHTML; }).join(""); };
      var cur = document.head.querySelectorAll('link[rel~="icon"]');
      if (key(cur) === key(next)) return;
      cur.forEach(function (n) { n.remove(); });
      next.forEach(function (n) { document.head.appendChild(n.cloneNode(true)); });
    }

    // swapGenerated trades this page's generated inline script for the next page's, inside finish() with
    // the morph. Its stylesheets, the blocks among them, are stageStylesheets' and swapStylesheets'.
    function swapGenerated(doc) {
      var oldRun = document.head.querySelector('script[data-kitwork-generated="js"],script[data-kitwork-jit="js"]');
      if (oldRun) oldRun.remove();
      var newRun = doc.head.querySelector('script[data-kitwork-generated="js"],script[data-kitwork-jit="js"]');
      if (newRun) {
        var run = document.createElement("script");
        run.setAttribute("data-kitwork-generated", "js");
        run.textContent = newRun.textContent;
        document.head.appendChild(run);
      }
    }

    // ---- router.appearance: the light/dark mode a page shows before the visitor chooses ----
    // The page arriving says it on <html data-kitwork-appearance="light|dark"> (nothing for "system",
    // the default), and a branch of a site may say another than its root. A whole load would paint
    // that page in its own default when the visitor has stored no choice, so a move does too: the
    // attribute is carried over, and when it differs from the page left behind, kit.appearance
    // shows the mode again by its one rule (stored choice, then this default, then the device — the
    // web module's showAppearance). Equal on both sides, nothing is repainted: a site that never
    // speaks of appearance is never touched.
    var APPEARANCE = "data-kitwork-appearance";
    function carryAppearance(next) {
      var root = document.documentElement;
      if (root.getAttribute(APPEARANCE) === next) return;
      if (next === null) root.removeAttribute(APPEARANCE);
      else root.setAttribute(APPEARANCE, next);
      var web = kit.module("web");
      if (web && typeof web.showAppearance === "function") web.showAppearance();
    }

    // ---- router.transition: how <content /> changes on a move (none | fade | slide) ----
    // The page arriving says it, on <html data-kitwork-transition>, so each branch of a site moves its
    // own way. Only the content moves: the root's own cross-fade is turned off, so the header, the
    // footer and the progressbar stay still. "slide" goes forward on a link and back on the Back
    // button. No View Transitions in this browser, or a person who asked their system for less
    // motion: the swap happens at once, as "none".
    var MOVING = "data-kitwork-moving";
    var currentMove = null; // the move that owns data-kitwork-moving: a skipped earlier one must not clear it
    var transitionCSS =
      "html[" + MOVING + "]::view-transition-old(root),html[" + MOVING + "]::view-transition-new(root){animation:none}" +
      "html[" + MOVING + "] main{view-transition-name:kitwork-content}" +
      "html[" + MOVING + "]::view-transition-group(kitwork-content){animation-duration:.2s}" +
      "html[" + MOVING + '="fade"]::view-transition-old(kitwork-content){animation:kitwork-out .15s ease-in both}' +
      "html[" + MOVING + '="fade"]::view-transition-new(kitwork-content){animation:kitwork-in .2s ease-out both}' +
      "html[" + MOVING + '="slide"]::view-transition-old(kitwork-content){animation:kitwork-out-left .2s ease-in both}' +
      "html[" + MOVING + '="slide"]::view-transition-new(kitwork-content){animation:kitwork-in-right .2s ease-out both}' +
      "html[" + MOVING + '="slide back"]::view-transition-old(kitwork-content){animation:kitwork-out-right .2s ease-in both}' +
      "html[" + MOVING + '="slide back"]::view-transition-new(kitwork-content){animation:kitwork-in-left .2s ease-out both}' +
      "@keyframes kitwork-out{to{opacity:0}}@keyframes kitwork-in{from{opacity:0}}" +
      "@keyframes kitwork-out-left{to{opacity:0;transform:translateX(-12%)}}" +
      "@keyframes kitwork-in-right{from{opacity:0;transform:translateX(12%)}}" +
      "@keyframes kitwork-out-right{to{opacity:0;transform:translateX(12%)}}" +
      "@keyframes kitwork-in-left{from{opacity:0;transform:translateX(-12%)}}";
    function transitionOf(doc) {
      var name = doc.documentElement ? doc.documentElement.getAttribute("data-kitwork-transition") || "" : "";
      if (name !== "fade" && name !== "slide") return "";
      if (typeof document.startViewTransition !== "function") return "";
      if (window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return "";
      return name;
    }
    function moveWith(name, forward, update) {
      if (!document.querySelector('style[data-kitwork-generated="transition"]')) {
        var style = document.createElement("style");
        style.setAttribute("data-kitwork-generated", "transition");
        clientOwned.add(style); // the page's own stylesheets are swapped on a move; this one stays
        style.textContent = transitionCSS;
        document.head.appendChild(style);
      }
      var root = document.documentElement;
      root.setAttribute(MOVING, name === "slide" && !forward ? "slide back" : name);
      var token = {};
      currentMove = token;
      // A second move started during this one's animation skips this transition, whose finished then
      // settles: only the move that still owns the attribute clears it.
      var done = function () {
        if (currentMove !== token) return;
        currentMove = null;
        root.removeAttribute(MOVING);
      };
      // The swap runs exactly once. A browser that never draws the frame the transition waits for (a
      // throttled tab, a headless one) would otherwise hold the next page back forever: after a
      // second the swap happens without the animation.
      var swapped = false, transition = null, guard = 0;
      var once = function () {
        if (swapped) return;
        swapped = true;
        clearTimeout(guard);
        update();
      };
      try {
        transition = document.startViewTransition(once);
        transition.finished.then(done, done);
        // A transition the browser skips (a hidden tab, a second move) rejects ready: the swap still
        // runs through once, so the rejection is expected and must not surface as an uncaught error.
        if (transition.ready) transition.ready.catch(function () { });
      } catch (e) {
        done();
        once();
        return;
      }
      guard = setTimeout(function () {
        if (swapped) return;
        try { transition.skipTransition(); } catch (e) { }
        done();
        once();
      }, 1000);
    }

    // swap returns a promise that settles once the page on screen has changed (or was left alone): the
    // change may wait for a component file the next page needs, or for a View Transition's update. A
    // move that a newer one has replaced by then (current() false) changes nothing.
    function swap(html, url, push, scrollY, responseLayout, forward, current) {
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
      if (!cur || !next) { location.assign(url); return Promise.resolve(); } // nothing safe to swap → real navigation

      document.dispatchEvent(new CustomEvent("kitwork:before-swap", { detail: { url: url } }));
      var stylesheets = stageStylesheets(doc);
      var waits = mergeHead(doc).concat(stylesheets.waits);
      var moving = transitionOf(doc);
      var nextTransition = doc.documentElement ? doc.documentElement.getAttribute("data-kitwork-transition") : null;
      var nextAppearance = doc.documentElement ? doc.documentElement.getAttribute(APPEARANCE) : null;
      var settle;
      var settled = new Promise(function (resolve) { settle = resolve; });
      if (waits.length) Promise.all(waits).then(change);
      else change();
      return settled;

      function change() {
        if (!current()) { settle(); return; } // a newer move took over while a component file loaded
        if (moving) moveWith(moving, forward, finish);
        else finish();
      }

      function finish() {
        if (!current()) { settle(); return; } // a newer move took over during the transition's wait
        if (doc.title) document.title = doc.title;
        swapIcons(doc);
        swapThemeColor(doc);
        swapStylesheets(stylesheets);
        swapGenerated(doc);
        swapBackdrop(doc, cur);
        // The page now on screen carries its own transition, as a whole load of it would.
        if (nextTransition) document.documentElement.setAttribute("data-kitwork-transition", nextTransition);
        else document.documentElement.removeAttribute("data-kitwork-transition");
        carryAppearance(nextAppearance);
        kit.morph(cur, next);
        if (!sameLayout && doc.body) {
          document.body.className = doc.body.className;
          document.body.setAttribute("data-kitwork-layout", layoutKey(doc));
        }
        runScripts(cur);

        if (push) history.pushState({ kitwork: true, i: ++historyIndex }, "", url);
        markCurrent();

        var hash = ""; try { hash = new URL(url, location.href).hash; } catch (e) { }
        var anchor = null;
        if (hash) { try { anchor = document.getElementById(decodeURIComponent(hash.slice(1))); } catch (e) { } }
        instant(function () {
          if (anchor) anchor.scrollIntoView();
          else window.scrollTo(0, scrollY || 0);
        });

        var t = region(document);
        if (t) { t.setAttribute("tabindex", "-1"); try { t.focus({ preventScroll: true }); } catch (e) { } }
        // A move without a reload does not tell a screen reader the page changed: the reader component
        // (on every page that moves this way) says the new title, as Drive reports a move to kit.progress.
        if (kit.reader) kit.reader.say(document.title);
        document.dispatchEvent(new CustomEvent("kitwork:load", { detail: { url: url } }));
        settle();
      }
    }

    // One move at a time. A newer click aborts the older fetch, but the older chain still settles
    // afterwards (an abort rejects asynchronously, and a page fetched ahead was never given a signal), so
    // every step asks whether its move is still the current one before touching anything shared: the
    // watchdog, the swap, the loading class, the bar, inflight. Otherwise the older move ended the
    // newer one's bar, cleared its watchdog, and could swap its stale page in after the newer one.
    // forward is the direction of a slide: a link goes forward; history goes the way its index says.
    function visit(url, push, scrollY, forward) {
      if (inflight) inflight.abort();
      clearTimeout(watchdog);
      var ctrl = new AbortController();
      inflight = ctrl;
      var current = function () { return inflight === ctrl; };
      document.documentElement.classList.add("kitwork-loading");
      progress(true);
      document.dispatchEvent(new CustomEvent("kitwork:before-visit", { detail: { url: url } }));
      var timer = watchdog = setTimeout(function () {
        if (current()) { ctrl.abort(); location.assign(url); }
      }, 3000);

      var p = prefetch[url] || fetchPage(url, ctrl.signal);
      delete prefetch[url];
      p.then(function (res) {
        if (!current()) return;
        clearTimeout(timer);
        var finalUrl = (res && res.url) || url;
        var h = ""; try { h = new URL(url, location.href).hash; } catch (e) { }
        if (h) { try { if (!new URL(finalUrl, location.href).hash) finalUrl += h; } catch (e) { } }
        return swap(res.html, finalUrl, push, scrollY, res.layout, forward === undefined ? push : forward, current);
      })
        .catch(function (err) {
          clearTimeout(timer);
          if (current() && (!err || err.name !== "AbortError")) location.assign(url);
        })
        .then(function () {
          if (!current()) return;
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
      history.replaceState({ kitwork: true, y: window.scrollY, i: historyIndex }, "", location.href);
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
      history.replaceState({ kitwork: true, y: window.scrollY, i: historyIndex }, "", location.href);
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
    // Each entry Drive pushes carries its index, so Back and Forward know which way they went: a slide
    // moves back on Back and forward on Forward. An entry with no index (made before Drive ran) is back.
    listen(window, "popstate", function (e) {
      var to = e.state && typeof e.state.i === "number" ? e.state.i : null;
      var forward = to !== null && to > historyIndex;
      if (to !== null) historyIndex = to;
      visit(location.href, false, (e.state && e.state.y) || 0, forward);
    });

    cleanup(function () {
      if (inflight) inflight.abort();
      clearTimeout(watchdog);
      clearTimeout(hoverTimer);
      clearTimeout(barTimer);
      if (barShown && kit.progress) kit.progress.finish(); // a move cut short must not leave the bar running
      kit.hydrate = false;
    });

    drive.visit = visit;
    drive.prefetch = doPrefetch;
    if ("scrollRestoration" in history) history.scrollRestoration = "manual";
    history.replaceState({ kitwork: true, i: historyIndex }, "", location.href);
  }

  var drive = { start: start };
  kit.drive = drive;
  kit.module("drive", drive);
  kit.onStart(start);
})(window, document);
