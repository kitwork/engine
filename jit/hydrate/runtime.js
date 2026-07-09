// kitwork kernel — THE one client runtime. Served at /kit.js, and prepended by jit/js as the
// core of every verb bundle, so expressions, verbs, model, validate and live all ride ONE root
// (window.kitwork), ONE registry, ONE set of delegated listeners, ONE observer.
//
// Boot-guarded: safe under double inclusion and under Kitwork Drive re-running head scripts.
// PREFIX = ORIGIN (strict, for expression directives): authors write data-kit-* SOURCE — this
// kernel carries a tiny parser for it (same grammar the Go side compiles for ctx.validate) plus the
// IR walker; data-kitwork-* on a directive is ENGINE-emitted precompiled IR (JSON). No eval, no
// new Function, ever.
//
// Leak-free by architecture: nodes carry no listeners and no closures (everything is delegated),
// per-element state hides behind a Symbol and dies with the node, SSE streams are deduped by URL
// and auto-closed when their last subscriber leaves the DOM.
(function () {
  "use strict";
  var kitwork = (window.kitwork = window.kitwork || {});

  // Component auto-loading is OPT-IN: set kitwork.cdnComponents to a base URL (e.g. an npm CDN like
  // jsdelivr) BEFORE the kernel boots, and an unregistered data-kitwork-component is fetched from
  // <base>/<name>/<version|name>.js. Default OFF — a Kitwork-engine page delivers components itself
  // and never silently phones home, and the "no dependencies" promise holds until you pick a CDN.
  kitwork.cdnComponents = kitwork.cdnComponents || "";

  var bridge = window.kitworkBridge || null;
  kitwork.bridge = bridge;
  kitwork.platform = bridge && bridge.platform ? bridge.platform : "web";
  kitwork.isNative = !!bridge;

  if (kitwork.runtime) return;
  kitwork.runtime = "v.1.0.0";

  // The one theme action — markup calls it as $app.toggleTheme() (data-kit-click). It flips the
  // `theme` property defined below, whose setter is the SINGLE place that toggles the .dark class
  // and persists localStorage["theme"]. So the pre-paint (jit/theme) and every caller read/write
  // one source of truth and can never drift.
  kitwork.toggleTheme = function () {
    kitwork.theme = kitwork.theme === "light" ? "dark" : "light";
  };

  // ---- expressions: source → IR (same grammar as engine/jit/hydrate/compile.go) ----
  var PREC = { "||": 1, "&&": 2, "==": 3, "!=": 3, ">": 4, "<": 4, ">=": 4, "<=": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6 };

  function lex(s) {
    var out = [], i = 0, n = s.length;
    while (i < n) {
      var c = s[i];
      if (c === " " || c === "\t" || c === "\n" || c === "\r") { i++; continue; }
      if ((c >= "0" && c <= "9") || (c === "." && i + 1 < n && s[i + 1] >= "0" && s[i + 1] <= "9")) {
        var j = i; while (j < n && ((s[j] >= "0" && s[j] <= "9") || s[j] === ".")) j++;
        out.push({ t: "num", v: s.slice(i, j) }); i = j; continue;
      }
      if (c === "'" || c === '"') {
        var q = c, k = i + 1; while (k < n && s[k] !== q) k++;
        out.push({ t: "str", v: s.slice(i + 1, k) }); i = k + 1; continue;
      }
      if (/[A-Za-z_$]/.test(c)) {
        var m = i; while (m < n && /[A-Za-z0-9_$]/.test(s[m])) m++;
        out.push({ t: "id", v: s.slice(i, m) }); i = m; continue;
      }
      var two = s.slice(i, i + 2);
      if (two === "==" || two === "!=" || two === ">=" || two === "<=" || two === "&&" || two === "||" || two === "=>") { out.push({ t: "op", v: two }); i += 2; continue; }
      if ("+-*/%<>!?:().,={}[];".indexOf(c) >= 0) out.push({ t: "op", v: c });
      i++;
    }
    out.push({ t: "eof", v: "" });
    return out;
  }

  function parse(toks) {
    var pos = 0;
    function peek() { return toks[pos]; }
    function next() { return toks[pos++]; }
    function eat(v) { if (peek().v !== v) throw new Error("hydrate: expected " + v); next(); }
    function assign() {
      var left = ternary();
      if (peek().v === "=") {
        next(); var val = assign();
        if (left instanceof Array && left[0] === "$" && left[1] !== "$") return ["=", left[1], val];
        if (left instanceof Array && left[0] === "." && left[1] instanceof Array && left[1][0] === "$" && left[1][1] === "$") return ["=$", left[2], val];
        throw new Error("hydrate: bad assignment");
      }
      return left;
    }
    function ternary() {
      var c = binary(0);
      if (peek().v === "?") { next(); var a = assign(); eat(":"); var b = assign(); return ["?", c, a, b]; }
      return c;
    }
    function binary(min) {
      var left = unary();
      for (; ;) {
        var t = peek();
        if (t.t !== "op" || !(t.v in PREC) || PREC[t.v] < min) break;
        var op = next().v;
        left = [op, left, binary(PREC[op] + 1)];
      }
      return left;
    }
    function unary() {
      var v = peek().v;
      if (v === "!" || v === "-") { next(); return ["u" + v, unary()]; }
      return postfix();
    }
    function callArgs() {
      var args = [];
      if (peek().v !== ")") { args.push(assign()); while (peek().v === ",") { next(); args.push(assign()); } }
      eat(")");
      return args;
    }
    function postfix() {
      var e = primary();
      for (; ;) {
        if (peek().v === ".") {
          next(); var name = next().v;
          if (peek().v === "(") { next(); e = ["()", e, name, callArgs()]; }
          else e = [".", e, name];
          continue;
        }
        if (peek().v === "(") { next(); e = ["call", e, callArgs()]; continue; }
        break;
      }
      return e;
    }
    function tryArrowParams() {
      var save = pos;
      next(); // (
      var params = [];
      if (peek().v === ")") { next(); }
      else {
        for (; ;) {
          if (peek().t !== "id") { pos = save; return null; }
          params.push(next().v);
          if (peek().v === ",") { next(); continue; }
          break;
        }
        if (peek().v !== ")") { pos = save; return null; }
        next();
      }
      if (peek().v !== "=>") { pos = save; return null; }
      next();
      return params;
    }
    function primary() {
      var t = peek();
      if (t.t === "num") { next(); return ["#", parseFloat(t.v)]; }
      if (t.t === "str") { next(); return ["#", t.v]; }
      if (t.t === "id") {
        next();
        if (t.v === "true") return ["#", true];
        if (t.v === "false") return ["#", false];
        if (t.v === "null") return ["#", null];
        return ["$", t.v];
      }
      if (t.v === "(") {
        var params = tryArrowParams();
        if (params) return ["=>", params, assign()];
        next(); var e = assign(); eat(")"); return e;
      }
      if (t.v === "{") {
        next();
        var pairs = [];
        while (peek().v !== "}") {
          var kt = next();
          if (kt.t !== "id" && kt.t !== "str") throw new Error("hydrate: bad object key " + kt.v);
          eat(":");
          pairs.push([kt.v, assign()]);
          if (peek().v === ",") { next(); continue; } // objects allow a trailing comma
          break;
        }
        eat("}");
        return ["{}", pairs];
      }
      if (t.v === "[") {
        next();
        var items = [];
        if (peek().v !== "]") {
          for (; ;) {
            items.push(assign());
            if (peek().v === ",") {
              next();
              if (peek().v === "]") throw new Error("hydrate: arrays reject a trailing comma");
              continue;
            }
            break;
          }
        }
        eat("]");
        return ["[]", items];
      }
      throw new Error("hydrate: unexpected " + t.v);
    }
    // entry: a sequence `a = 1; b = 2` (trailing ; fine); a single expression stays unwrapped.
    var node = assign();
    if (peek().v === ";") {
      var exprs = [";", node];
      while (peek().v === ";") {
        next();
        if (peek().t === "eof") break;
        exprs.push(assign());
      }
      node = exprs.length === 2 ? node : exprs;
    }
    if (peek().t !== "eof") throw new Error("hydrate: trailing tokens");
    return node;
  }

  // ---- run: walk one IR node against the scope ----
  // callDepth is the client twin of the server walker's op budget: lambdas cannot loop, so the
  // only runaway is recursion — cut it off instead of blowing the stack.
  var callDepth = 0;
  // blockedKey seals the ONLY member names that can reach code execution — `.constructor` leads to
  // Function (i.e. eval), and __proto__/prototype enable prototype pollution. Denying them in every
  // read / call / write makes "no eval" true BY CONSTRUCTION (not merely blocked by CSP), which is
  // the precondition for running client-sent expressions (capsules) safely.
  function blockedKey(k) {
    return k === "constructor" || k === "__proto__" || k === "prototype" ||
      k === "ownerDocument" || k === "defaultView" || k === "contentWindow" ||
      k === "window" || k === "parent" || k === "top" || k === "self" || k === "globalThis";
  }
  function run(x, s) {
    var op = x[0];
    if (op === "#") return x[1];
    if (op === "$") return s[x[1]];
    if (op === "=") { if (blockedKey(x[1])) return undefined; var v = run(x[2], s); s[x[1]] = v; return v; }
    if (op === "=$") { if (blockedKey(x[1])) return undefined; var vp = run(x[2], s); s["$"][x[1]] = vp; return vp; }
    if (op === "{}") {
      var obj = {};
      for (var oi = 0; oi < x[1].length; oi++) obj[x[1][oi][0]] = run(x[1][oi][1], s);
      return obj;
    }
    if (op === "[]") { return x[1].map(function (y) { return run(y, s); }); }
    if (op === "=>") { return { __kitLambda: true, params: x[1], body: x[2] }; }
    if (op === ";") {
      var sv;
      for (var si = 1; si < x.length; si++) sv = run(x[si], s);
      return sv;
    }
    if (op === "call") {
      var fn = run(x[1], s);
      var fargs = x[2].map(function (y) { return run(y, s); });
      if (callDepth > 64) throw new Error("hydrate: call depth exceeded");
      if (fn && fn.__kitLambda) {
        callDepth++;
        try {
          if (!fn.params.length) return run(fn.body, s);
          // Params overlay the calling scope; writes to NON-param keys flow back out (lexical).
          var local = {};
          for (var pi = 0; pi < fn.params.length; pi++) local[fn.params[pi]] = fargs[pi];
          var overlay = new Proxy(local, {
            get: function (t, k) { return k in t ? t[k] : s[k]; },
            set: function (t, k, v2) { if (k in t) t[k] = v2; else s[k] = v2; return true; }
          });
          return run(fn.body, overlay);
        } finally { callDepth--; }
      }
      // A registered component method — real JS the developer wrote — called with `this` = the
      // component scope, so `this.count` reads/writes its state. Still no eval: it's a function
      // reference, not a compiled string. (Server-side there are no registered methods, so its
      // walker returns nil here — the divergence is intentional and safe.)
      if (typeof fn === "function") {
        callDepth++;
        try { return fn.apply(s, fargs); } finally { callDepth--; }
      }
      return undefined;
    }
    if (op === "?") return run(x[1], s) ? run(x[2], s) : run(x[3], s);
    if (op === ".") { var o = run(x[1], s); return (o == null || blockedKey(x[2])) ? undefined : o[x[2]]; }
    if (op === "()") { var oo = run(x[1], s); if (oo == null || blockedKey(x[2])) return undefined; var a = x[3].map(function (y) { return run(y, s); }); return typeof oo[x[2]] === "function" ? oo[x[2]].apply(oo, a) : undefined; }
    if (op === "u!") return !run(x[1], s);
    if (op === "u-") return -run(x[1], s);
    var l = run(x[1], s), r = run(x[2], s);
    switch (op) {
      case "+": return l + r; case "-": return l - r; case "*": return l * r; case "/": return l / r; case "%": return l % r;
      case ">": return l > r; case "<": return l < r; case ">=": return l >= r; case "<=": return l <= r;
      case "==": return l == r; case "!=": return l != r; case "&&": return l && r; case "||": return l || r;
    }
  }

  // ---- directives: the PREFIX carries the encoding (strict origin convention) ----
  // data-kit-<name>     = AUTHOR-written source → parsed by the tiny parser here.
  // data-kitwork-<name> = ENGINE-emitted, precompiled IR (JSON) → JSON.parse, no parsing.
  // (The old long-prefix source alias and the suffixed IR attribute are gone: two names,
  // two meanings, told apart by prefix alone.)
  var cache = {};
  function directive(el, name) {
    var raw = el.getAttribute("data-kitwork-" + name);
    if (raw) {
      if (!(raw in cache)) { try { cache[raw] = JSON.parse(raw); } catch (e) { cache[raw] = null; } }
      return cache[raw];
    }
    raw = el.getAttribute("data-kit-" + name);
    if (!raw) return null;
    var key = "$" + raw;
    if (!(key in cache)) { try { cache[key] = parse(lex(raw)); } catch (e) { cache[key] = null; } }
    return cache[key];
  }
  function selector(name) {
    return "[data-kitwork-" + name + "],[data-kit-" + name + "]";
  }

  var MODEL = "[data-kitwork-model],[data-kit-model]";
  function modelKey(el) { return el.getAttribute("data-kitwork-model") || el.getAttribute("data-kit-model"); }
  function modelValue(el) { return el.type === "number" ? (parseFloat(el.value) || 0) : (el.value || ""); }

  var raw = {};

  var scope = new Proxy(raw, {
    get: function (t, k) {
      if (k === "$") return t;
      if (k === "$app") return kitwork;
      return k in t ? t[k] : 0;
    },
    set: function (t, k, v) {
      t[k] = v;
      return true;
    }
  });

  // ---- scopes: data-kit-scope="<name>" marks a component boundary ----
  // Lexical, closure-like resolution: reads fall through ancestor scopes up to the page scope;
  // writes go to the scope that OWNS the key, else the nearest one — "private shadows shared".
  // `$` addresses the page scope explicitly ($.total = $.total + 1) — the same $ the server's
  // template language uses for its root data. Scope objects live in the node's Symbol state,
  // so they die with their node; two sibling scopes never see each other.
  // A component boundary is any of these. data-kitwork-component names a REGISTERED blueprint
  // (see kitwork.component); data-kit-scope carries an inline name/init/blueprint.
  var SCOPE = "[data-kitwork-scope],[data-kit-scope],[data-kitwork-component],[data-kit-component],[data-kitwork-api],[data-kit-api]";

  // The component registry: kitwork.component("counter", { count: 0, inc() {…} }). A blueprint is a
  // plain JS object — state values + methods. Methods are real functions (called with this = the
  // component scope); state is deep-cloned per instance so two boundaries never share it.
  // (Named `blueprints` to stay clear of kitwork.components, which is the verb back-compat surface.)
  var blueprints = {};
  function cloneState(v) {
    if (v === null || typeof v !== "object") return v;
    try { return JSON.parse(JSON.stringify(v)); } catch (e) { return v; }
  }
  function seedComponent(target, def) {
    for (var k in def) {
      if (!Object.prototype.hasOwnProperty.call(def, k)) continue;
      target[k] = (typeof def[k] === "function") ? def[k] : cloneState(def[k]);
    }
  }

  // boundaryScope initializes a boundary's local state ONCE, from the attribute's shape:
  //   data-kitwork-component="counter"        → a REGISTERED blueprint (state + real JS methods)
  //   data-kit-scope="counter"                → a NAME (label; local state)
  //   data-kit-scope="count = 5; open = true" → an INIT program (runs once; writes stay local)
  //   data-kit-scope="{ count: 5, inc: () => count = count + 1 }" → an INLINE blueprint (IR methods)
  // Inline blueprints/init are the same compiled grammar as everything else — parsed, never eval'd —
  // and being markup they are visible to the server (verify + future PreRender).
  // ---- IndexedDB persistence for dynamic CDN components (opt-in via data-kit-persist="true") ----
  var DB_NAME = "kitwork";
  var STORE_NAME = "blueprints";
  var dbPromise = null;
  function getDB() {
    if (dbPromise) return dbPromise;
    dbPromise = new Promise(function (resolve, reject) {
      if (!window.indexedDB) { reject(new Error("IndexedDB not supported")); return; }
      var req = indexedDB.open(DB_NAME, 1);
      req.onupgradeneeded = function (e) {
        var db = e.target.result;
        if (!db.objectStoreNames.contains(STORE_NAME)) {
          db.createObjectStore(STORE_NAME);
        }
      };
      req.onsuccess = function (e) { resolve(e.target.result); };
      req.onerror = function (e) { reject(e.target.error); };
    });
    return dbPromise;
  }
  function dbGet(key) {
    return getDB().then(function (db) {
      return new Promise(function (resolve, reject) {
        var tx = db.transaction(STORE_NAME, "readonly");
        var store = tx.objectStore(STORE_NAME);
        var req = store.get(key);
        req.onsuccess = function (e) { resolve(e.target.result); };
        req.onerror = function (e) { reject(e.target.error); };
      });
    });
  }
  function dbSet(key, val) {
    return getDB().then(function (db) {
      return new Promise(function (resolve, reject) {
        var tx = db.transaction(STORE_NAME, "readwrite");
        var store = tx.objectStore(STORE_NAME);
        var req = store.put(val, key);
        req.onsuccess = function () { resolve(); };
        req.onerror = function (e) { reject(e.target.error); };
      });
    });
  }

  function injectScriptCode(code, cname) {
    try {
      var blob = new Blob([code], { type: "application/javascript" });
      var url = URL.createObjectURL(blob);
      var s = document.createElement("script");
      s.src = url;
      s.async = true;
      s.onload = function () {
        URL.revokeObjectURL(url);
        scheduleRender();
      };
      s.onerror = function () {
        URL.revokeObjectURL(url);
        console.error("kitjs: failed to execute stored component '" + cname + "'");
      };
      document.head.appendChild(s);
    } catch (e) {
      console.error("kitjs: failed to create blob for '" + cname + "'", e);
    }
  }

  function fetchCodeAndStore(url, cname) {
    fetch(url)
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.text();
      })
      .then(function (code) {
        dbSet(cname, code).catch(function (err) {
          console.warn("kitjs: failed to store component '" + cname + "' to IndexedDB", err);
        });
        injectScriptCode(code, cname);
      })
      .catch(function (err) {
        console.error("kitjs: failed to fetch component '" + cname + "' from " + url, err);
      });
  }

  var loadingComponents = {};
  function loadComponentFromCDN(cname) {
    var base = kitwork.cdnComponents;
    if (!base || loadingComponents[cname]) return;          // opt-in only — off by default
    // Validate name[@version] so a hostile attribute can never build an unexpected URL.
    var m = /^([a-z][a-z0-9-]*)(?:@([v0-9.]+))?$/.exec(cname);
    if (!m) { console.error("kitjs: invalid component name '" + cname + "'"); return; }
    loadingComponents[cname] = true;
    var url = base.replace(/\/+$/, "") + "/" + m[1] + "/" + (m[2] || m[1]) + ".js";

    if (kitwork.useIndexed && window.indexedDB) {
      dbGet(cname).then(function (cachedCode) {
        if (cachedCode) {
          injectScriptCode(cachedCode, cname);
        } else {
          fetchCodeAndStore(url, cname);
        }
      }).catch(function () {
        fetchCodeAndStore(url, cname);
      });
    } else {
      var s = document.createElement("script");
      s.src = url;
      s.async = true;
      s.onload = function () { scheduleRender(); };
      s.onerror = function () { console.error("kitjs: failed to load component '" + cname + "' from " + url); };
      document.head.appendChild(s);
    }
  }

  function boundaryScope(b) {
    var st = state(b);
    var cname = b.getAttribute("data-kitwork-component") || b.getAttribute("data-kit-component");
    if (cname) {
      // Component registration (kitwork.component) can run AFTER the first render — so seed lazily,
      // the first time the blueprint is available, and never re-seed once done (keeps mutations).
      if (!st.scope) st.scope = {};
      if (!st.seeded) {
        if (blueprints[cname]) {
          seedComponent(st.scope, blueprints[cname]);
          st.seeded = true;
          runInit(b);
        } else {
          loadComponentFromCDN(cname);
        }
      }
      return st.scope;
    }
    if (st.scope) return st.scope;
    st.scope = {};
    var v = (b.getAttribute("data-kitwork-scope") || b.getAttribute("data-kit-scope") || "").trim();
    if (!v) return st.scope;
    try {
      var parent = b.parentElement ? scopeFor(b.parentElement) : scope;
      if (v.charAt(0) === "{") {
        var o = run(parse(lex(v)), parent);
        if (o && typeof o === "object") { for (var k in o) st.scope[k] = o[k]; }
        runInit(b);
      } else if (v.indexOf("=") >= 0) {
        // init: reads fall through to ancestors, writes ALWAYS land in this boundary.
        var target = st.scope;
        var initProxy = new Proxy(target, {
          get: function (t, kk) { if (kk === "$") return raw; return kk in t ? t[kk] : parent[kk]; },
          set: function (t, kk, vv) { t[kk] = vv; return true; }
        });
        run(parse(lex(v)), initProxy);
      }
    } catch (e) { }
    return st.scope;
  }
  // runInit calls a boundary's init() ONCE, right after it is seeded — the mount lifecycle hook.
  // A registered component's init is real JS (this = the scope); an inline blueprint's is an IR
  // lambda. Set the guard BEFORE calling so a re-entrant scopeFor never loops.
  function runInit(b) {
    var st = state(b);
    if (st.inited) return;
    st.inited = true;
    var fn = st.scope && st.scope.init;
    if (!fn) return;
    try {
      var proxy = scopeFor(b);
      if (typeof fn === "function") fn.apply(proxy, []);
      else if (fn.__kitLambda) run(fn, proxy);
    } catch (e) { }
  }
  function chainFor(el) {
    var objs = [];
    var b = el && el.closest ? el.closest(SCOPE) : null;
    while (b) {
      objs.push(boundaryScope(b));
      b = b.parentElement ? b.parentElement.closest(SCOPE) : null;
    }
    objs.push(raw);
    return objs;
  }
  // elementScope wraps a scope with the acting element's DOM handles — the escape hatch for the
  // rare imperative need (focus, scroll, integrate a widget, toggle an attribute on a child). It is
  // NOT a prototype mutation: `$el`/`$root` are variables in the expression context that resolve to
  // native elements, so `$el.querySelector('input').focus()` executes exactly as it reads. `$root`
  // is the component boundary (nearest data-kit-scope, else <html>), so a query stays inside what
  // the component owns. Reads and method calls only — value/attribute CHANGES belong to bindings
  // (data-kit-model, state→CSS), not to reaching in and poking the DOM.
  function elementScope(el) {
    var base = scopeFor(el);
    return new Proxy(base, {
      get: function (t, k) {
        if (k === "$el") return el;
        if (k === "$root") return (el.closest && el.closest(SCOPE)) || document.documentElement;
        if (k === "$app") return kitwork;
        return base[k];
      },
      set: function (t, k, v) { base[k] = v; return true; }
    });
  }

  function scopeFor(el) {
    var b = el && el.closest ? el.closest(SCOPE) : null;
    if (!b) return scope;
    var st = state(b);
    if (st.scopeProxy) return st.scopeProxy;
    st.scopeProxy = new Proxy(boundaryScope(b), {
      get: function (t, k) {
        if (k === "$") return raw;
        if (k === "$app") return kitwork;
        var objs = chainFor(b);
        for (var i = 0; i < objs.length; i++) { if (k in objs[i]) return objs[i][k]; }
        return 0;
      },
      set: function (t, k, v) {
        var objs = chainFor(b);
        for (var i = 0; i < objs.length; i++) {
          if (k in objs[i]) {
            objs[i][k] = v;
            return true;
          }
        }
        objs[0][k] = v;
        return true;
      }
    });
    return st.scopeProxy;
  }

  // Seed scope keys from the inputs present in the DOM — at boot AND after every swap. Seeding at
  // boot (not at script parse) matters: an inline bundle executes in <head> before the body exists,
  // and a morphed-in page may bring new data-kit-model inputs whose server-rendered value must win.
  // A key is seeded into the input's NEAREST scope, only when no scope in its chain owns it yet.
  function seedModels() {
    document.querySelectorAll(MODEL).forEach(function (el) {
      var k = modelKey(el);
      var objs = chainFor(el), found = false;
      for (var i = 0; i < objs.length; i++) { if (k in objs[i]) { found = true; break; } }
      if (!found) objs[0][k] = modelValue(el);
    });
  }

  var activeComponents = {};
  function rebuildActiveComponents() {
    var next = {};
    document.querySelectorAll("[data-kitwork-component],[data-kit-component]").forEach(function (el) {
      var cname = el.getAttribute("data-kitwork-component") || el.getAttribute("data-kit-component");
      if (!cname) return;
      var st = state(el);
      if (st.seeded) {
        var inst = scopeFor(el);
        (next[cname] = next[cname] || []).push(inst);
      }
    });

    var helpers = { "action": true, "actions": true, "target": true, "state": true, "fire": true };
    for (var k in activeComponents) {
      if (!helpers[k] && !(k in next)) {
        delete activeComponents[k];
      }
    }
    for (var k in next) {
      if (next[k].length === 1) {
        activeComponents[k] = next[k][0];
      } else {
        activeComponents[k] = next[k];
      }
    }
  }

  function render() {
    rebuildActiveComponents();
    document.querySelectorAll(selector("text")).forEach(function (el) { var x = directive(el, "text"); if (!x) return; var v = run(x, scopeFor(el)); el.textContent = v == null ? "" : v; });
    document.querySelectorAll(selector("show")).forEach(function (el) { var x = directive(el, "show"); if (!x) return; el.hidden = !run(x, scopeFor(el)); });
    // validate → state→CSS: the element carries data-state="valid|invalid"; styling is CSS's job.
    document.querySelectorAll(selector("validate")).forEach(function (el) { var x = directive(el, "validate"); if (!x) return; el.setAttribute("data-state", run(x, scopeFor(el)) ? "valid" : "invalid"); });
    document.querySelectorAll(MODEL).forEach(function (el) { var k = modelKey(el), s = scopeFor(el); if (String(s[k]) !== el.value) el.value = s[k]; });
  }

  // ---- remember: persist chosen page-scope ($) keys across reloads ----
  // Declared in markup — data-kit-remember="theme, locale, cart" (comma / space / [brackets] all
  // accepted; put it on the root, or spread across elements) — or kitwork.remember("theme"). Those
  // $ keys mirror to localStorage and sync across tabs; everything else in $ stays ephemeral.
  // Client-only (localStorage) — the SERVER sees the DECLARATION but not the value.
  var REMEMBER = "[data-kitwork-remember],[data-kit-remember]";
  var remembered = {};
  var memCache = {};
  function getStorageItem(k) {
    try { return localStorage.getItem(k); } catch (e) { return memCache[k] !== undefined ? memCache[k] : null; }
  }
  function setStorageItem(k, v) {
    try { localStorage.setItem(k, v); } catch (e) { memCache[k] = v; }
  }
  function parseKeys(v) {
    return (v || "").trim().replace(/^\[/, "").replace(/\]$/, "").split(/[\s,]+/).filter(Boolean);
  }
  function registerRememberedKey(k) {
    if (remembered[k]) return;
    remembered[k] = true;
    var localVal = getStorageItem(k);
    var defaultVal = raw[k];
    Object.defineProperty(raw, k, {
      get: function () {
        var val = getStorageItem(k);
        if (val === null) return undefined;
        try { return JSON.parse(val); } catch (e) { return val; }
      },
      set: function (v) {
        var s = typeof v === "object" ? JSON.stringify(v) : String(v);
        if (getStorageItem(k) !== s) {
          setStorageItem(k, s);
          scheduleRender();
        }
      },
      configurable: true,
      enumerable: true
    });
    if (localVal === null && defaultVal !== undefined) {
      raw[k] = defaultVal;
    }
  }

  Object.defineProperty(kitwork, "theme", {
    get() {
      let theme = localStorage.getItem("theme");

      if (!theme) {
        theme = document.documentElement.classList.contains("dark")
          ? "dark"
          : "light";
      }

      return theme;
    },

    set(v) {
      const dark = v === "dark";

      document.documentElement.classList.toggle("dark", dark);

      try {
        localStorage.setItem("theme", dark ? "dark" : "light");
      } catch (e) { }
    },

    configurable: true,
    enumerable: true
  });

  function loadRemembered() {
    document.querySelectorAll(REMEMBER).forEach(function (el) {
      parseKeys(el.getAttribute("data-kitwork-remember") || el.getAttribute("data-kit-remember")).forEach(registerRememberedKey);
    });
  }

  // ---- behaviors (verbs): ONE registry; jit/js modules register into it ----
  // Per-element runtime state lives behind a PRIVATE Symbol, so it can never collide with a host
  // page's own element properties; it dies with the node — nothing to clean up.
  var behaviors = {};
  var stateKey = Symbol("kitwork");
  function state(element) {
    return element[stateKey] || (element[stateKey] = {});
  }
  // data-kitwork-target = "#id"/selector → element; defaults to the actor itself.
  function target(el) {
    var sel = el.getAttribute("data-kitwork-target") || el.getAttribute("data-kit-target");
    return sel ? document.querySelector(sel) : el;
  }
  var ACTION = "[data-kitwork-action],[data-kit-action]";
  function fire(el, e) {
    var fn = behaviors[el.getAttribute("data-kitwork-action") || el.getAttribute("data-kit-action")];
    if (fn) fn(el, e);
  }
  kitwork.behavior = function (name, fn) { behaviors[name] = fn; return kitwork; };
  // Register a reusable stateful component blueprint. Activate it with data-kitwork-component="name".
  // Distinct from behavior() (a stateless verb): a component has state + methods + a scope boundary.
  // Registering (re)renders on the next tick, so components registered after boot still paint.
  var renderScheduled = false;
  function scheduleRender() {
    if (renderScheduled) return;
    renderScheduled = true;
    (typeof queueMicrotask === "function" ? queueMicrotask : function (f) { setTimeout(f, 0); })(function () {
      renderScheduled = false;
      render();
    });
  }
  kitwork.component = function (name, def) { blueprints[name] = def; scheduleRender(); return kitwork; };
  // Programmatic form of data-kit-remember: mark $ keys as persisted across reloads.
  kitwork.remember = function () {
    for (var i = 0; i < arguments.length; i++) registerRememberedKey(arguments[i]);
    scheduleRender();
    return kitwork;
  };

  kitwork.action = function (name, fn) { behaviors[name] = fn; return kitwork; };
  kitwork.behavior = kitwork.action;
  kitwork.target = target;
  kitwork.state = state;
  kitwork.fire = fire;
  kitwork.components = activeComponents;
  kitwork.blueprints = blueprints;
  kitwork.actions = behaviors;

  // Back-compat surface for existing verb modules and pages (kitwork.components.action(...)).
  Object.defineProperty(activeComponents, "action", { value: function (name, fn) { behaviors[name] = fn; return this; }, configurable: true, writable: true, enumerable: false });
  Object.defineProperty(activeComponents, "actions", { value: behaviors, configurable: true, writable: true, enumerable: false });
  Object.defineProperty(activeComponents, "target", { value: target, configurable: true, writable: true, enumerable: false });
  Object.defineProperty(activeComponents, "state", { value: state, configurable: true, writable: true, enumerable: false });
  Object.defineProperty(activeComponents, "fire", { value: fire, configurable: true, writable: true, enumerable: false });

  // ---- ONE set of delegated listeners for everything ----
  document.addEventListener("click", function (e) {
    var ex = e.target.closest && e.target.closest(selector("click"));
    if (ex) { var x = directive(ex, "click"); if (x) { run(x, elementScope(ex)); render(); } }
    var act = e.target.closest && e.target.closest(ACTION);
    if (act) fire(act, e);
  });
  document.addEventListener("input", function (e) {
    var el = e.target.closest && e.target.closest(MODEL);
    if (!el) return;
    scopeFor(el)[modelKey(el)] = modelValue(el);
    render();
  });
  // Submit: the validate gate runs FIRST — an invalid form neither submits nor fires its verb;
  // the server re-checks the SAME rule for truth either way. A valid form then fires its verb.
  document.addEventListener("submit", function (e) {
    var f = e.target;
    if (f.matches && (f.matches('[data-state="invalid"]') || f.querySelector('[data-state="invalid"]'))) {
      e.preventDefault();
      return;
    }
    if (f.getAttribute && (f.getAttribute("data-kitwork-action") || f.getAttribute("data-kit-action"))) fire(f, e);
  }, true);

  // Auto-trigger: [data-kitwork-trigger="visible"] fires its action when scrolled into view (lazy
  // load / infinite scroll). Re-evaluated on every kitwork:load (after navigation or an append).
  function bindVisible() {
    if (!("IntersectionObserver" in window)) return;
    document.querySelectorAll('[data-kitwork-trigger="visible"],[data-kit-trigger="visible"]').forEach(function (el) {
      var store = state(el);
      if (store.visibilityObserver) store.visibilityObserver.disconnect();
      var observer = new IntersectionObserver(function (entries) {
        if (entries[0].isIntersecting) fire(el, null);
      }, { rootMargin: "300px" });
      store.visibilityObserver = observer;
      observer.observe(el);
    });
  }
  document.addEventListener("kitwork:load", bindVisible);

  // ---- live: data-kit-live="<sse-url>" — the server PUSHES JSON scope patches over SSE ----
  // Still ONE EventSource per URL (deduped), but the patch is delivered to each subscriber's NEAREST
  // scope: a live region on a boundary (data-kit-api / -scope / -component) patches THAT scope, so
  // api (fetch initial) + live (keep fresh) pair on one element; a bare live region patches the page.
  // A payload that parses as a JSON object is merged and re-rendered; anything else is ignored.
  var LIVE = "[data-kitwork-live],[data-kit-live]";
  var streams = {};
  function liveTarget(el) {
    var b = el.closest ? el.closest(SCOPE) : null;
    return b ? boundaryScope(b) : raw;
  }
  function syncLive() {
    if (!window.EventSource) return;
    var want = {};
    document.querySelectorAll(LIVE).forEach(function (el) {
      var u = el.getAttribute("data-kitwork-live") || el.getAttribute("data-kit-live");
      if (u) (want[u] = want[u] || []).push(el);
    });
    Object.keys(want).forEach(function (u) {
      if (streams[u]) { streams[u].els = want[u]; return; } // refresh subscribers (e.g. after morph)
      var rec = streams[u] = { es: null, els: want[u] };
      rec.es = new EventSource(u);
      rec.es.onmessage = function (e) {
        var patch = null;
        try { patch = JSON.parse(e.data); } catch (err) { patch = null; }
        if (patch && typeof patch === "object" && !(patch instanceof Array)) {
          rec.els.forEach(function (el) {
            var target = liveTarget(el);
            Object.keys(patch).forEach(function (k) { target[k] = patch[k]; });
          });
          render();
        }
      };
    });
    Object.keys(streams).forEach(function (u) {
      if (!want[u]) { streams[u].es.close(); delete streams[u]; }
    });
  }
  // ---- api: data-kit-api="/url" — seed a boundary's scope from a JSON fetch (once, at mount) ----
  // The element is a boundary (see SCOPE), so the response fills ITS scope: an object is merged key
  // by key; anything else lands under `data`. Lifecycle is state→CSS on the element itself —
  // data-state="loading" → "ready"/"error" — plus `error` in scope for the message. Fetch-once per
  // element (tracked in Symbol state); same-origin credentials. This is data-source tier A: the URL
  // is an endpoint the server owns, so it is safe by construction (subject to CORS).
  var API = "[data-kitwork-api],[data-kit-api]";
  function syncApi() {
    if (!window.fetch) return;
    document.querySelectorAll(API).forEach(function (el) {
      var st = state(el);
      if (st.apiState) return; // idle only — already loading/done/error
      var url = el.getAttribute("data-kitwork-api") || el.getAttribute("data-kit-api");
      if (!url) return;
      st.apiState = "loading";
      el.setAttribute("data-state", "loading");
      fetch(url, { credentials: "same-origin", headers: { "Accept": "application/json" } })
        .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); })
        .then(function (data) {
          var s = boundaryScope(el);
          if (data && typeof data === "object" && !(data instanceof Array)) {
            for (var k in data) { if (Object.prototype.hasOwnProperty.call(data, k)) s[k] = data[k]; }
          } else { s.data = data; }
          st.apiState = "done";
          el.setAttribute("data-state", "ready");
          render();
        })
        .catch(function (e) {
          st.apiState = "error";
          el.setAttribute("data-state", "error");
          boundaryScope(el).error = String((e && e.message) || e);
          render();
        });
    });
  }

  // ONE observer for the whole kernel: DOM is the manifest — live regions arriving or leaving
  // (morph, SPA swaps) re-reconcile subscriptions on the next tick.
  var livePending = false;
  new MutationObserver(function () {
    if (livePending) return;
    livePending = true;
    setTimeout(function () { livePending = false; syncLive(); syncApi(); }, 0);
  }).observe(document.documentElement, { childList: true, subtree: true });

  // ---- morph: kernel primitive — make an existing DOM node match a new one, preserving
  // focus, cursor, scroll and input state (nodes are PATCHED, never recreated).
  function morph(fromNode, toNode) {
    if (fromNode.nodeType !== toNode.nodeType) { fromNode.replaceWith(toNode.cloneNode(true)); return; }
    if (fromNode.nodeType === 3) {
      if (fromNode.nodeValue !== toNode.nodeValue) fromNode.nodeValue = toNode.nodeValue;
      return;
    }
    if (fromNode.nodeType === 1) {
      if (fromNode.tagName !== toNode.tagName) { fromNode.replaceWith(toNode.cloneNode(true)); return; }
      var fromAttrs = fromNode.attributes, toAttrs = toNode.attributes, i;
      for (i = fromAttrs.length - 1; i >= 0; i--) {
        if (!toNode.hasAttribute(fromAttrs[i].name)) fromNode.removeAttribute(fromAttrs[i].name);
      }
      for (i = 0; i < toAttrs.length; i++) {
        if (fromNode.getAttribute(toAttrs[i].name) !== toAttrs[i].value) fromNode.setAttribute(toAttrs[i].name, toAttrs[i].value);
      }
      if (fromNode.tagName === "INPUT" || fromNode.tagName === "TEXTAREA") {
        if (fromNode.value !== toNode.value) fromNode.value = toNode.value;
        if (toNode.hasAttribute("checked") !== fromNode.checked) fromNode.checked = toNode.checked;
      } else if (fromNode.tagName === "SELECT") {
        if (fromNode.value !== toNode.value) fromNode.value = toNode.value;
      }
      // Kernel-owned overlay nodes (progress bar, announcer, toasts — marked data-kitwork-ui) are
      // CLIENT truth: the fetched HTML never contains them, so they are invisible to matching and
      // exempt from removal. Without this, an app rooted at <html> loses the progress bar on the
      // first swap (the bar died with the morph, so it "sometimes shows, sometimes doesn't").
      function kernelUI(n) { return n.nodeType === 1 && n.hasAttribute("data-kitwork-ui"); }
      var fromChildren = Array.prototype.slice.call(fromNode.childNodes).filter(function (n) { return !kernelUI(n); });
      var toChildren = Array.prototype.slice.call(toNode.childNodes);

      function getKey(n) {
        return n.nodeType === 1 ? (n.getAttribute("data-kitwork-key") || n.getAttribute("data-kit-key") || n.getAttribute("data-key")) : null;
      }

      var fromKeys = {};
      for (var j = 0; j < fromChildren.length; j++) {
        var key = getKey(fromChildren[j]);
        if (key) fromKeys[key] = fromChildren[j];
      }

      var activeIndex = 0;
      for (var j = 0; j < toChildren.length; j++) {
        var tChild = toChildren[j];
        var tKey = getKey(tChild);
        var fChild = null;

        if (tKey && fromKeys[tKey]) {
          fChild = fromKeys[tKey];
          if (fromNode.childNodes[activeIndex] !== fChild) {
            fromNode.insertBefore(fChild, fromNode.childNodes[activeIndex]);
          }
          var idx = fromChildren.indexOf(fChild);
          if (idx >= 0) fromChildren.splice(idx, 1);
        } else {
          for (var k = 0; k < fromChildren.length; k++) {
            var cand = fromChildren[k];
            if (!getKey(cand) && cand.nodeType === tChild.nodeType && (cand.nodeType !== 1 || cand.tagName === tChild.tagName)) {
              fChild = cand;
              if (fromNode.childNodes[activeIndex] !== fChild) {
                fromNode.insertBefore(fChild, fromNode.childNodes[activeIndex]);
              }
              fromChildren.splice(k, 1);
              break;
            }
          }
        }

        if (fChild) {
          morph(fChild, tChild);
        } else {
          var newCloned = tChild.cloneNode(true);
          fromNode.insertBefore(newCloned, fromNode.childNodes[activeIndex]);
        }
        activeIndex++;
      }

      // Tail cleanup: drop leftovers the new page doesn't have — but never a kernel overlay.
      for (var r = fromNode.childNodes.length - 1; r >= activeIndex; r--) {
        var leftover = fromNode.childNodes[r];
        if (!kernelUI(leftover)) leftover.remove();
      }
    }
  }

  // ---- drive: SPA navigation, absorbed into the kernel ----
  // Ported from the proven standalone hydrate.js. Activates at boot ONLY when the page declares a
  // hydrate region ([data-kitwork-hydrate] / [data-kit-hydrate]) AND no drive is already running:
  // kitwork.hydrate is the two-way lock — the legacy standalone file sets the same flag, so old
  // pages keep their file, new pages use the kernel, and the two never double-drive.
  // Contract unchanged: intercept same-origin links + GET forms, fetch with X-Kitwork-Hydrate,
  // swap the region (fallback <main>), morph, mergeHead swaps data-kitwork-jit blocks, history +
  // scroll owned, hover prefetch, entry-page back-to-top trap. Everything delegated.
  function initDrive() {
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
    var inSite = false, trapped = false;  // entry-page back-to-top trap state
    document.querySelectorAll("script[src]").forEach(function (s) { loadedSrc.add(s.src); });

    // Self-contained top progress bar — no markup needed in the shell. data-kitwork-ui marks it as
    // a kernel overlay so morph leaves it alone; the isConnected re-append is belt-and-braces for
    // any other code that clears <body>.
    var bar = document.createElement("div");
    bar.setAttribute("data-kitwork-ui", "progress");
    bar.style.cssText = "position:fixed;top:0;left:0;height:2px;width:0;background:#f82244;" +
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
      morph(cur, next);
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
    document.addEventListener("click", function (e) {
      var a = e.target.closest && e.target.closest("a[href]");
      if (!a || !drivable(a, e)) return;
      var dest; try { dest = new URL(a.href, location.href); } catch (x) { return; }
      e.preventDefault();
      if (dest.pathname === location.pathname && dest.search === location.search && !dest.hash) {
        instant(function () { window.scrollTo(0, 0); });
        return;
      }
      inSite = true; trapped = false;
      history.replaceState({ kitwork: true, y: window.scrollY }, "", location.href);
      visit(a.href, true, 0);
    });

    // GET form submits — POST and other methods fall through (the kernel's validate gate, which
    // runs in the capture phase, has already stopped invalid forms: defaultPrevented is respected).
    document.addEventListener("submit", function (e) {
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
      inSite = true; trapped = false;
      history.replaceState({ kitwork: true, y: window.scrollY }, "", location.href);
      visit(u.href, true, 0);
    });

    // Prefetch on hover (delayed so quick fly-overs don't fetch).
    var hoverTimer;
    document.addEventListener("mouseover", function (e) {
      var a = e.target.closest && e.target.closest("a[href]");
      if (!a || !drivable(a, null) || prefetch[a.href]) return;
      var href = a.href;
      clearTimeout(hoverTimer);
      hoverTimer = setTimeout(function () { doPrefetch(href); }, 65);
    });

    // Entry-page back-to-top trap + scroll restore.
    window.addEventListener("scroll", function () {
      if (inSite || trapped || window.scrollY <= 8) return;
      history.pushState({ kitwork: true, trap: true }, "", location.href);
      trapped = true;
    }, { passive: true });

    window.addEventListener("popstate", function (e) {
      if (trapped && !(e.state && e.state.trap)) {
        trapped = false;
        if (window.scrollY > 0) { instant(function () { window.scrollTo(0, 0); }); return; }
        history.back();
        return;
      }
      visit(location.href, false, (e.state && e.state.y) || 0);
    });

    if ("scrollRestoration" in history) history.scrollRestoration = "manual";
    history.replaceState({ kitwork: true }, "", location.href);
  }

  // ---- exports + boot ----
  // compile(src) → IR array — the SAME compiler the server runs, exposed so tools (a playground,
  // a debugger) can show the bytecode a source expression becomes. No eval; pure data out.
  // run(ir[, scope]) walks an IR tree — the walker itself, for tools and tests.
  kitwork.compile = function (src) { return parse(lex(src)); };
  kitwork.run = function (ir, s) { return run(ir, s || scope); };
  kitwork.scope = scope;
  kitwork.scopeFor = scopeFor;
  kitwork.render = render;
  kitwork.streams = streams;
  kitwork.sync = syncLive;
  kitwork.syncApi = syncApi;
  kitwork.morph = morph;
  kitwork.set = function (k, v) { scope[k] = v; render(); };
  kitwork.fetchWithRetry = function (url, options, retries, delay) {
    var rCount = retries !== undefined ? retries : 2;
    var rDelay = delay !== undefined ? delay : 1000;
    return fetch(url, options).catch(function (err) {
      if (err && err.name === "AbortError") throw err;
      if (rCount <= 0) throw err;
      return new Promise(function (resolve) {
        setTimeout(resolve, rDelay);
      }).then(function () {
        return kitwork.fetchWithRetry(url, options, rCount - 1, rDelay * 2);
      });
    });
  };
  // Back-compat alias: earlier hydrate demos and docs used window.hydrate.
  window.hydrate = kitwork;

  function initAppConfig() {
    var appEl = document.querySelector("[data-kitwork-app],[data-kit-app],[data-kitwork-hydrate],[data-kit-hydrate]");
    if (appEl) {
      var appVal = appEl.getAttribute("data-kitwork-app") || appEl.getAttribute("data-kit-app") || "";
      var mode = "runtime";
      var version = "latest";
      if (appVal) {
        var parts = appVal.split("@");
        if (parts.length === 2) {
          mode = parts[0];
          version = parts[1];
        } else if (parts.length === 1 && parts[0]) {
          var val = parts[0];
          if (val.charAt(0) === "v" || (val.charAt(0) >= "0" && val.charAt(0) <= "9")) {
            version = val;
          } else {
            mode = val;
          }
        }
      }
      kitwork.mode = mode;
      kitwork.version = version;
      kitwork.useIndexed = appEl.getAttribute("data-kitwork-indexed") === "true" || appEl.getAttribute("data-kit-indexed") === "true";
    }
  }

  function boot() { initAppConfig(); loadRemembered(); seedModels(); render(); syncLive(); syncApi(); bindVisible(); initDrive(); }
  // Another tab changed a remembered value → adopt it and re-render (live cross-tab sync).
  window.addEventListener("storage", function (e) {
    if (remembered[e.key]) {
      scheduleRender();
    }
  });
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
  // After every swap: seed any new inputs, re-render expressions, reconcile live streams
  // (bindVisible re-binds through its own kitwork:load listener above).
  document.addEventListener("kitwork:load", function () { loadRemembered(); seedModels(); render(); syncLive(); syncApi(); });
})();
