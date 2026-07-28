// Kitwork hydrate kernel. Composed into /kit.js with bridge, morph, capability, compat and Drive
// modules; jit/js prepends that same composed runtime before verb modules. Expressions, verbs,
// model, validation and live updates all ride one window.kitwork root, one registry, one delegated
// event system and one DOM observer.
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

  if (kitwork.runtime && kitwork.runtime.loaded) return;
  var runtimeMeta = kitwork.runtime && typeof kitwork.runtime === "object" ? kitwork.runtime : {};
  runtimeMeta.name = "kitwork";
  runtimeMeta.version = "2.0.0";
  runtimeMeta.engine = kitwork.platform || "web";
  runtimeMeta.development = !!runtimeMeta.development;
  runtimeMeta.loaded = true;
  runtimeMeta.booted = false;
  runtimeMeta.info = function () {
    return {
      name: this.name,
      version: this.version,
      engine: this.engine,
      development: this.development
    };
  };
  kitwork.runtime = runtimeMeta;

  var modules = Object.create(null);
  var startHooks = [];
  kitwork.modules = modules;
  kitwork.module = function (name, value) {
    if (arguments.length === 1) return modules[name];
    modules[name] = value;
    return value;
  };
  kitwork.has = function (name) {
    return Object.prototype.hasOwnProperty.call(modules, name);
  };
  kitwork.onStart = function (callback) {
    if (typeof callback !== "function") return function () { };
    startHooks.push(callback);
    if (runtimeMeta.booted) callback();
    return function () {
      var index = startHooks.indexOf(callback);
      if (index >= 0) startHooks.splice(index, 1);
    };
  };

  var globalCleanups = [];
  function cleanup(callback) {
    if (typeof callback === "function") globalCleanups.push(callback);
    return callback;
  }
  function listen(target, eventName, handler, options) {
    target.addEventListener(eventName, handler, options);
    cleanup(function () { target.removeEventListener(eventName, handler, options); });
    return handler;
  }
  kitwork.cleanup = cleanup;

  // ---- expressions: source → IR (same grammar as engine/jit/hydrate/compile.go) ----
  var PREC = { "||": 1, "&&": 2, "==": 3, "!=": 3, ">": 4, "<": 4, ">=": 4, "<=": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6 };

  function lex(s) {
    var out = [], i = 0, n = s.length;
    while (i < n) {
      var c = s[i];
      if (c === " " || c === "\t" || c === "\n" || c === "\r") { i++; continue; }
      if ((c >= "0" && c <= "9") || (c === "." && i + 1 < n && s[i + 1] >= "0" && s[i + 1] <= "9")) {
        var j = i, dot = false;
        while (j < n) {
          if (s[j] >= "0" && s[j] <= "9") { j++; continue; }
          if (s[j] === "." && !dot) { dot = true; j++; continue; }
          break;
        }
        out.push({ t: "num", v: s.slice(i, j) }); i = j; continue;
      }
      if (c === "'" || c === '"') {
        var q = c, k = i + 1; while (k < n && s[k] !== q) k++;
        if (k >= n) throw new Error("hydrate: unterminated string");
        out.push({ t: "str", v: s.slice(i + 1, k) }); i = k + 1; continue;
      }
      if (/[A-Za-z_$]/.test(c)) {
        var m = i; while (m < n && /[A-Za-z0-9_$]/.test(s[m])) m++;
        out.push({ t: "id", v: s.slice(i, m) }); i = m; continue;
      }
      var two = s.slice(i, i + 2);
      if (two === "==" || two === "!=" || two === ">=" || two === "<=" || two === "&&" || two === "||" || two === "=>") { out.push({ t: "op", v: two }); i += 2; continue; }
      if ("+-*/%<>!?:().,={}[];".indexOf(c) >= 0) { out.push({ t: "op", v: c }); i++; continue; }
      throw new Error("hydrate: unexpected character '" + c + "'");
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
  // The client and Go walkers share the same 10k-node budget. callDepth is an additional guard
  // against exhausting the browser stack during recursive component or lambda calls.
  var evalDepth = 0;
  var evalRemaining = 0;
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
    if (evalDepth === 0) evalRemaining = 10000;
    evalRemaining--;
    if (evalRemaining < 0) throw new Error("hydrate: evaluation budget exceeded");
    evalDepth++;
    try {
      return walk(x, s);
    } finally {
      evalDepth--;
    }
  }
  function walk(x, s) {
    if (!(x instanceof Array) || x.length === 0) return x;
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
      if (callDepth >= 64) throw new Error("hydrate: call depth exceeded");
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
    var l = run(x[1], s);
    if (op === "&&") return l ? run(x[2], s) : l;
    if (op === "||") return l ? l : run(x[2], s);
    var r = run(x[2], s);
    switch (op) {
      case "+": return l + r; case "-": return l - r; case "*": return l * r; case "/": return l / r; case "%": return l % r;
      case ">": return l > r; case "<": return l < r; case ">=": return l >= r; case "<=": return l <= r;
      case "==": return l == r; case "!=": return l != r;
    }
    throw new Error("hydrate: unknown op '" + op + "'");
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
      if (k in aliases) return aliases[k]; // $sidebar / $theme … → the named instance's scope
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
  function boundaryScope(b) {
    var st = state(b);
    var craw = b.getAttribute("data-kitwork-component") || b.getAttribute("data-kit-component");
    if (craw) {
      var tag = parseComponentTag(craw);
      var cname = tag.name;
      var alias = tag.alias || b.getAttribute("data-alias") || "";
      // Component registration (kitwork.component) can run AFTER the first render — so seed lazily,
      // the first time the blueprint is available, and never re-seed once done (keeps mutations).
      if (!st.scope) st.scope = {};
      if (alias) aliases[alias] = st.scope; // global handle → this instance's scope
      if (!st.seeded) {
        if (blueprints[cname]) {
          seedComponent(st.scope, blueprints[cname]);
          st.seeded = true;
          runInit(b);
        } else {
          var loader = kitwork.module("componentLoader");
          if (loader) loader.load(cname);
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
        if (k in aliases) return aliases[k]; // $sidebar / $theme … → the named instance's scope
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
        if (k in aliases) return aliases[k]; // $sidebar / $theme … → the named instance's scope
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

  // Named component handles: data-kit-component="sidebar=$sidebar" registers `$sidebar` → that
  // instance's scope, so ANY expression reaches it ($sidebar.cycle()), even from outside its DOM
  // subtree (the scattered-controls case). No alias = purely lexical (bare cycle() = nearest scope).
  var aliases = {};
  // $theme is a boot-registered GLOBAL handle: theme is a global capability, not a DOM-scoped instance,
  // so it needs no per-page boundary — data-kit-click="$theme.toggle()" works everywhere. It delegates
  // to the one theme source of truth (kitwork.theme setter). Coherent with $sidebar etc. (all in aliases);
  // NOT stored on kitwork.theme, which is the theme STRING ("light"/"dark").
  aliases["$theme"] = { toggle: function () { kitwork.toggleTheme(); } };
  // parseComponentTag splits `name@version=$alias` (all but name optional) → { name, version, alias }.
  function parseComponentTag(raw) {
    var name = raw, version = "", alias = "", i;
    if ((i = name.indexOf("=")) >= 0) { alias = name.slice(i + 1).trim(); name = name.slice(0, i); }
    if ((i = name.indexOf("@")) >= 0) { version = name.slice(i + 1).trim(); name = name.slice(0, i); }
    return { name: name.trim(), version: version, alias: alias };
  }

  var activeComponents = {};
  function rebuildActiveComponents() {
    var next = {};
    document.querySelectorAll("[data-kitwork-component],[data-kit-component]").forEach(function (el) {
      var craw = el.getAttribute("data-kitwork-component") || el.getAttribute("data-kit-component");
      if (!craw) return;
      var cname = parseComponentTag(craw).name;
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

  // classNames flattens a class expression's VALUE into a list of names. String → split on spaces;
  // array → each item, recursively; object → the keys whose value is truthy. false/null/"" drop out,
  // so `active ? 'ring' : ''` adds nothing on the false branch.
  function classNames(v, out) {
    if (v == null || v === false || v === true) return out;
    if (typeof v === "string") {
      var parts = v.split(/\s+/);
      for (var i = 0; i < parts.length; i++) if (parts[i]) out.push(parts[i]);
      return out;
    }
    if (Array.isArray(v)) {
      for (var j = 0; j < v.length; j++) classNames(v[j], out);
      return out;
    }
    if (typeof v === "object") {
      for (var k in v) if (Object.prototype.hasOwnProperty.call(v, k) && v[k]) classNames(k, out);
      return out;
    }
    return out;
  }

  function render() {
    rebuildActiveComponents();
    document.querySelectorAll(selector("text")).forEach(function (el) { var x = directive(el, "text"); if (!x) return; var v = run(x, scopeFor(el)); el.textContent = v == null ? "" : v; });
    document.querySelectorAll(selector("show")).forEach(function (el) { var x = directive(el, "show"); if (!x) return; el.hidden = !run(x, scopeFor(el)); });
    // bind → attributes: the expression is an OBJECT (reusing the closed grammar), each key an attr.
    // { src: avatar, alt: name, disabled: n > 3 }. false/null removes the attr; true sets it empty;
    // else the value. So <img data-kit-bind="{ src: avatar }"> tracks a scope key with no new syntax.
    document.querySelectorAll(selector("bind")).forEach(function (el) {
      var x = directive(el, "bind"); if (!x) return;
      var obj = run(x, scopeFor(el));
      if (!obj || typeof obj !== "object") return;
      for (var k in obj) {
        if (!Object.prototype.hasOwnProperty.call(obj, k)) continue;
        var v = obj[k];
        if (v === false || v == null) el.removeAttribute(k);
        else if (v === true) el.setAttribute(k, "");
        else if (String(el.getAttribute(k)) !== String(v)) el.setAttribute(k, v);
      }
    });
    // class → toggle classes from an expression. Every shape the grammar allows is accepted, so
    // nobody has to remember a special syntax: a string ('card ring'), an object whose truthy keys
    // win ({ 'is-open': open }), a ternary between names, an array mixing all three, nested freely.
    // The names must be written out in full — the CSS JIT reads them off this same expression to
    // decide what to emit, and cannot see a name built with '+'.
    document.querySelectorAll(selector("class")).forEach(function (el) {
      var x = directive(el, "class"); if (!x) return;
      var want = classNames(run(x, scopeFor(el)), []);
      // Remove only what THIS directive added last pass, never the static class attribute: an
      // element is normally `class="card" data-kit-class="{ ring: focused }"` and blowing away
      // "card" on the first toggle would strip the page's own styling.
      var prev = el.__kitClass || [];
      for (var i = 0; i < prev.length; i++) if (want.indexOf(prev[i]) < 0) el.classList.remove(prev[i]);
      for (var j = 0; j < want.length; j++) el.classList.add(want[j]);
      el.__kitClass = want;
    });
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
  var rememberStoragePrefix = "kitwork:remember:";
  function getStorageItem(k) {
    try {
      var value = localStorage.getItem(rememberStoragePrefix + k);
      return value === null ? localStorage.getItem(k) : value;
    } catch (e) {
      return memCache[k] !== undefined ? memCache[k] : null;
    }
  }
  function setStorageItem(k, v) {
    try { localStorage.setItem(rememberStoragePrefix + k, v); } catch (e) { memCache[k] = v; }
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

  function loadRemembered() {
    document.querySelectorAll(REMEMBER).forEach(function (el) {
      parseKeys(el.getAttribute("data-kitwork-remember") || el.getAttribute("data-kit-remember")).forEach(registerRememberedKey);
    });
  }

  // ---- behaviors (verbs): ONE registry; jit/js modules register into it ----
  // Per-element runtime state lives behind a private Symbol. Resources registered on the state are
  // explicitly released when morph or another DOM owner removes the node.
  var behaviors = {};
  var stateKey = Symbol("kitwork");
  function state(element) {
    return element[stateKey] || (element[stateKey] = {});
  }
  function onCleanup(element, callback) {
    var store = state(element);
    (store.cleanups || (store.cleanups = [])).push(callback);
    return callback;
  }
  function cleanupElement(element) {
    var store = element && element[stateKey];
    if (!store) return;
    if (store.visibilityObserver) {
      store.visibilityObserver.disconnect();
      store.visibilityObserver = null;
    }
    if (store.apiController) {
      store.apiController.abort();
      store.apiController = null;
    }
    (store.cleanups || []).splice(0).forEach(function (callback) {
      try { callback(); } catch (_) { }
    });
  }
  function cleanupTree(node) {
    if (!node || node.nodeType !== 1) return;
    cleanupElement(node);
    node.querySelectorAll("*").forEach(cleanupElement);
  }
  kitwork.onCleanup = onCleanup;
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

  // ---- ONE set of delegated listeners for everything ----
  listen(document, "click", function (e) {
    var ex = e.target.closest && e.target.closest(selector("click"));
    if (ex) { var x = directive(ex, "click"); if (x) { run(x, elementScope(ex)); render(); } }
    var act = e.target.closest && e.target.closest(ACTION);
    if (act) fire(act, e);
  });
  // data-kit-drag: a native window drag region (a custom title bar). Primary-button press hands the
  // drag to the OS via $app.window('drag'); double-click maximizes — standard title-bar behaviour.
  // A no-op on the web (bridge absent). Buttons inside can opt out with data-kit-no-drag.
  var DRAG = "[data-kitwork-drag],[data-kit-drag]";
  listen(document, "mousedown", function (e) {
    if (e.button !== 0) return;
    if (e.target.closest && e.target.closest("[data-kit-no-drag],[data-kitwork-no-drag]")) return;
    if (e.target.closest && e.target.closest(DRAG)) kitwork.window("drag");
  });
  listen(document, "dblclick", function (e) {
    if (e.target.closest && e.target.closest("[data-kit-no-drag],[data-kitwork-no-drag]")) return;
    if (e.target.closest && e.target.closest(DRAG)) kitwork.window("maximize");
  });
  listen(document, "input", function (e) {
    var el = e.target.closest && e.target.closest(MODEL);
    if (!el) return;
    scopeFor(el)[modelKey(el)] = modelValue(el);
    render();
  });
  // Submit: the validate gate runs FIRST — an invalid form neither submits nor fires its verb;
  // the server re-checks the SAME rule for truth either way. A valid form then fires its verb.
  listen(document, "submit", function (e) {
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
  listen(document, "kitwork:load", bindVisible);

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
      var controller = typeof AbortController !== "undefined" ? new AbortController() : null;
      st.apiController = controller;
      fetch(url, {
        credentials: "same-origin",
        headers: { "Accept": "application/json" },
        signal: controller ? controller.signal : undefined
      })
        .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); })
        .then(function (data) {
          st.apiController = null;
          if (!el.isConnected) return;
          var s = boundaryScope(el);
          if (data && typeof data === "object" && !(data instanceof Array)) {
            for (var k in data) { if (Object.prototype.hasOwnProperty.call(data, k)) s[k] = data[k]; }
          } else { s.data = data; }
          st.apiState = "done";
          el.setAttribute("data-state", "ready");
          render();
        })
        .catch(function (e) {
          st.apiController = null;
          if ((e && e.name === "AbortError") || !el.isConnected) return;
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
  var domObserver = new MutationObserver(function (records) {
    records.forEach(function (record) {
      record.removedNodes.forEach(cleanupTree);
    });
    if (livePending) return;
    livePending = true;
    setTimeout(function () { livePending = false; syncLive(); syncApi(); }, 0);
  });
  domObserver.observe(document.documentElement, { childList: true, subtree: true });
  cleanup(function () { domObserver.disconnect(); });

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
  kitwork.destroy = function () {
    Object.keys(streams).forEach(function (url) {
      streams[url].es.close();
      delete streams[url];
    });
    while (globalCleanups.length) {
      try { globalCleanups.pop()(); } catch (_) { }
    }
    cleanupTree(document.documentElement);
    kitwork.hydrate = false;
    kitwork.runtime.booted = false;
    kitwork.runtime.loaded = false;
  };

  function initAppConfig() {
    var appEl = document.querySelector("[data-kitwork-app],[data-kit-app],[data-kitwork-hydrate],[data-kit-hydrate]");
    if (appEl) {
      // data-kit-progress="#0af" — the navigation bar's colour, declared in markup where a reader
      // can see it. It is published as a CSS variable rather than written onto the bar, so the same
      // value also works when set in a stylesheet, and so a site can scope it per section.
      var progressColor = appEl.getAttribute("data-kitwork-progress") || appEl.getAttribute("data-kit-progress") || "";
      if (progressColor) document.documentElement.style.setProperty("--kitwork-progress", progressColor);
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

  function boot() {
    if (runtimeMeta.booted) return kitwork;
    runtimeMeta.booted = true;
    initAppConfig();
    loadRemembered();
    seedModels();
    render();
    syncLive();
    syncApi();
    bindVisible();
    startHooks.slice().forEach(function (start) {
      try { start(); } catch (error) {
        if (runtimeMeta.development && window.console) {
          console.error("kitwork: module start failed", error);
        }
      }
    });
    document.dispatchEvent(new CustomEvent("kitwork:ready", {
      detail: { runtime: runtimeMeta.info(), modules: Object.keys(modules) }
    }));
    return kitwork;
  }
  kitwork.start = boot;

  Object.defineProperty(kitwork, "internal", {
    value: {
      cleanup: cleanup,
      listen: listen,
      cleanupTree: cleanupTree,
      state: state,
      target: target
    },
    configurable: true,
    enumerable: false
  });
  kitwork.module("kernel", {
    start: boot,
    compile: kitwork.compile,
    run: kitwork.run,
    render: render
  });
  // Another tab changed a remembered value → adopt it and re-render (live cross-tab sync).
  listen(window, "storage", function (e) {
    var key = e.key && e.key.indexOf(rememberStoragePrefix) === 0 ?
      e.key.slice(rememberStoragePrefix.length) : e.key;
    if (key && remembered[key]) {
      scheduleRender();
    }
  });
  // After every swap: seed any new inputs, re-render expressions, reconcile live streams
  // (bindVisible re-binds through its own kitwork:load listener above).
  listen(document, "kitwork:load", function () { loadRemembered(); seedModels(); render(); syncLive(); syncApi(); });
})();
