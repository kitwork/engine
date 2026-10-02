// Kitwork hydrate kernel — the whole client runtime. Composed into /kit.js with bridge, the
// platform modules, morph and Drive; jit/js appends the components a page named.
// Expressions, boundaries, components, structure and events all ride one
// window.kit root (window.kitwork is a deprecated alias to the SAME object), one registry, one
// delegated event system and one DOM observer.
//
// Boot-guarded: safe under double inclusion and under Kitwork Drive re-running head scripts.
// PREFIX = ORIGIN (strict, for expression directives): authors write data-kit-* SOURCE — this
// kernel carries a tiny parser for it (the same grammar the Go side compiles) plus the
// IR walker; data-kitwork-* on a directive is ENGINE-emitted precompiled IR (JSON). No eval, no
// new Function, ever.
//
// Leak-free by architecture: nodes carry no listeners and no closures (everything is delegated),
// per-element state hides behind a Symbol and dies with the node, SSE streams are deduped by URL
// and auto-closed when their last subscriber leaves the DOM.
(function () {
  "use strict";
  // `kit` is the canonical author root going forward: authors write data-kit-*, load /kit.js, and
  // reach the runtime through window.kit. `window.kitwork` remains a DEPRECATED alias pointing at the
  // SAME object, so every existing component file and module keeps working during the migration —
  // it is removed only after every caller uses window.kit. (An older page that only seeded
  // window.kitwork is adopted, so a cached bundle never double-initialises.)
  var kit = (window.kit = window.kit || window.kitwork || {});
  var kitwork = (window.kitwork = kit);

  // Two facts that belong to the RUNTIME, not to the native bridge, even though the bridge is what
  // makes them interesting. They live here so a page that ships no bridge at all still has them:
  // web.js throws KitworkError from theme.set(), and runtimeMeta reads platform two lines below.
  if (typeof kit.KitworkError !== "function") {
    kit.KitworkError = function (message, code, moduleName, actionName, details) {
      var err = new Error(message || "Kitwork execution error");
      err.name = "KitworkError";
      err.code = code || "INTERNAL_ERROR";
      err.module = moduleName || "system";
      err.action = actionName || "unknown";
      err.details = details || {};
      return err;
    };
  }
  if (!Object.getOwnPropertyDescriptor(kit, "platform")) {
    Object.defineProperty(kit, "platform", {
      get: function () {
        var bridge = kit.bridge;
        return bridge && typeof bridge.call === "function" ? (bridge.platform || "native") : "web";
      },
      configurable: true,
      enumerable: true
    });
    Object.defineProperty(kit, "isNative", {
      get: function () { return !!(kit.bridge && typeof kit.bridge.call === "function"); },
      configurable: true,
      enumerable: true
    });
  }

  if (kit.runtime && kit.runtime.loaded) return;
  var runtimeMeta = kit.runtime && typeof kit.runtime === "object" ? kit.runtime : {};
  runtimeMeta.name = "kitwork";
  runtimeMeta.version = "2.0.0";
  runtimeMeta.engine = kit.platform || "web";
  runtimeMeta.development = !!runtimeMeta.development;
  runtimeMeta.loaded = true;
  runtimeMeta.booted = false;
  runtimeMeta.info = function () {
    return {
      name: this.name,
      version: this.version,
      engine: kit.platform || this.engine,
      development: this.development
    };
  };
  kit.runtime = runtimeMeta;

  var modules = Object.create(null);
  var startHooks = [];
  kit.modules = modules;
  kit.module = function (name, value) {
    if (arguments.length === 1) return modules[name];
    modules[name] = value;
    return value;
  };
  kit.has = function (name) {
    return Object.prototype.hasOwnProperty.call(modules, name);
  };

  // Public platform capabilities are services, separate from the runtime's control API. Trusted
  // JavaScript receives the concrete object at kit.<name>; authored expressions receive only the
  // exact members explicitly granted at registration time.
  var services = Object.create(null);
  var expressionServiceMembers = Object.create(null);
  var expressionServiceSurfaces = Object.create(null);
  var reservedServiceNames = Object.create(null);
  ("service bridge runtime internal module modules has onStart cleanup onCleanup " +
    "compile run scope scopeFor render set fetchWithRetry destroy start component " +
    "components blueprints state morph hydrate " +
    "platform isNative mode version").split(" ").forEach(function (name) {
      reservedServiceNames[name] = true;
    });

  function validServiceName(name) {
    return /^[A-Za-z][A-Za-z0-9]*$/.test(name) &&
      (!blockedKey(name) || name === "window") &&
      !Object.prototype.hasOwnProperty.call(reservedServiceNames, name);
  }

  function expressionServiceSurface(name) {
    if (Object.prototype.hasOwnProperty.call(expressionServiceSurfaces, name)) {
      return expressionServiceSurfaces[name];
    }
    var surface = new Proxy(Object.create(null), {
      get: function (_, member) {
        var grants = expressionServiceMembers[name];
        if (!grants || !Object.prototype.hasOwnProperty.call(grants, member)) return undefined;
        var service = services[name];
        if (service == null) return undefined;
        var value = service[member];
        return typeof value === "function" ? value.bind(service) : value;
      },
      has: function (_, member) {
        var grants = expressionServiceMembers[name];
        return !!grants && Object.prototype.hasOwnProperty.call(grants, member);
      },
      ownKeys: function () {
        var grants = expressionServiceMembers[name];
        return grants ? Object.keys(grants) : [];
      },
      getOwnPropertyDescriptor: function (_, member) {
        var grants = expressionServiceMembers[name];
        if (!grants || !Object.prototype.hasOwnProperty.call(grants, member)) return undefined;
        return { configurable: true, enumerable: true };
      },
      set: function () { return false; },
      defineProperty: function () { return false; },
      deleteProperty: function () { return false; },
      setPrototypeOf: function () { return false; },
      getPrototypeOf: function () { return null; }
    });
    expressionServiceSurfaces[name] = surface;
    return surface;
  }

  kit.service = function (name, value, options) {
    name = String(name || "");
    if (arguments.length === 1) {
      return Object.prototype.hasOwnProperty.call(services, name) ? services[name] : undefined;
    }
    if (!validServiceName(name)) throw new Error("kit: invalid or reserved service name '" + name + "'");

    services[name] = value;
    kit[name] = value;

    var grants = Object.create(null);
    var members = options && options.expression;
    if (Array.isArray(members)) {
      members.forEach(function (member) {
        member = String(member || "");
        if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(member) && !blockedKey(member)) {
          grants[member] = true;
        }
      });
    }
    if (Object.keys(grants).length) expressionServiceMembers[name] = grants;
    else delete expressionServiceMembers[name];
    return value;
  };

  kit.onStart = function (callback) {
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
  kit.cleanup = cleanup;

  // ---- expressions: source → IR (same grammar as engine/jit/hydrate/compile.go) ----
  var PREC = { "||": 1, "&&": 2, "==": 3, "!=": 3, "===": 3, "!==": 3, ">": 4, "<": 4, ">=": 4, "<=": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6 };

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
        // Escapes as JavaScript reads them (the Go twin's lex says why they came): \n \t \r \b \f
        // \v \0, \uXXXX, and any other escaped character stands for itself — \' \" \\ included.
        var q = c, k = i + 1, text = "", closed = false;
        while (k < n) {
          var ch = s[k];
          if (ch === q) { closed = true; break; }
          if (ch !== "\\" || k + 1 >= n) { text += ch; k++; continue; }
          var e = s[++k];
          if (e === "u" && /^[0-9a-fA-F]{4}$/.test(s.slice(k + 1, k + 5))) { text += String.fromCharCode(parseInt(s.slice(k + 1, k + 5), 16)); k += 5; continue; }
          text += ({ n: "\n", t: "\t", r: "\r", b: "\b", f: "\f", v: "\v", "0": "\0" })[e] || e;
          k++;
        }
        if (!closed) throw new Error("hydrate: unterminated string");
        out.push({ t: "str", v: text }); i = k + 1; continue;
      }
      if (/[A-Za-z_$]/.test(c)) {
        var m = i; while (m < n && /[A-Za-z0-9_$]/.test(s[m])) m++;
        out.push({ t: "id", v: s.slice(i, m) }); i = m; continue;
      }
      var two = s.slice(i, i + 2);
      var three = s.slice(i, i + 3); // longest match first: `===` must not lex as `==` + `=`
      if (three === "===" || three === "!==") { out.push({ t: "op", v: three }); i += 3; continue; }
      if (two === "==" || two === "!=" || two === ">=" || two === "<=" || two === "&&" || two === "||" || two === "=>" || two === "++" || two === "--") { out.push({ t: "op", v: two }); i += 2; continue; }
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
      if (v === "++" || v === "--") { next(); return update(v, unary(), true); }
      if (v === "!" || v === "-") { next(); return ["u" + v, unary()]; }
      return postfix();
    }
    // ++/-- take the targets `=` takes — a name, or $.name — and nothing else: obj.count++ is out
    // for the same reason obj.count = v is. A name is its own op rather than sugar for
    // count = count + 1 because the value is coerced to a NUMBER first: "5"++ is 6, where the
    // assignment would have concatenated it into "51".
    function update(op, target, prefix) {
      if (target instanceof Array && target[0] === "$" && target[1] !== "$") return [op, target[1], prefix];
      if (target instanceof Array && target[0] === "." && target[1] instanceof Array && target[1][0] === "$" && target[1][1] === "$") return [op + "$", target[2], prefix];
      throw new Error("hydrate: " + op + " needs a name — count" + op + " or $.count" + op);
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
        // a[b]: the key is any expression — items[1], items[i], map[key]. Same read as a.name once
        // the key is known; a blocked name reads as undefined, as it does after a dot.
        if (peek().v === "[") { next(); var key = assign(); eat("]"); e = ["idx", e, key]; continue; }
        break;
      }
      // count++ binds once to the left-hand side just read: count++++ is an error, as in JavaScript.
      if (peek().v === "++" || peek().v === "--") return update(next().v, e, false);
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
  // The expression globals — the fourteen the design named (ideaship-extension §4) and the spec
  // listed as fact while the kernel had none: Math.ceil(7 / 4) read as undefined and said nothing.
  // A global comes LAST: a name the page declares wins, as a local does in JavaScript. There is no
  // `new` in the grammar, so Date gives now/parse/UTC and never a Date object; `Object` is only
  // { keys }, so defineProperty and its kind stay out of reach. `.constructor` and `.prototype` are
  // already refused on every member and index read, which is what keeps Function unreachable.
  var expressionGlobals = Object.create(null);
  expressionGlobals.Math = Math;
  expressionGlobals.Date = Date;
  expressionGlobals.JSON = JSON;
  expressionGlobals.Number = Number;
  expressionGlobals.String = String;
  expressionGlobals.Boolean = Boolean;
  expressionGlobals.Array = Array;
  expressionGlobals.Object = Object.freeze({ keys: Object.keys });
  expressionGlobals.parseInt = parseInt;
  expressionGlobals.parseFloat = parseFloat;
  expressionGlobals.encodeURIComponent = encodeURIComponent;
  expressionGlobals.decodeURIComponent = decodeURIComponent;
  expressionGlobals.isNaN = isNaN;
  expressionGlobals.isFinite = isFinite;
  function globalOrZero(k) {
    return Object.prototype.hasOwnProperty.call(expressionGlobals, k) ? expressionGlobals[k] : 0;
  }
  // invokeLambda runs a lambda VALUE. Params overlay the calling scope; writes to NON-param keys
  // flow back out (lexical). It is the one way a lambda runs: a bare inc() and a callback inside
  // filter() go through here alike, so the two cannot drift apart.
  function invokeLambda(fn, args, s) {
    if (callDepth >= 64) throw new Error("hydrate: call depth exceeded");
    callDepth++;
    try {
      if (!fn.params.length) return run(fn.body, s);
      var local = {};
      for (var pi = 0; pi < fn.params.length; pi++) local[fn.params[pi]] = args[pi];
      var overlay = new Proxy(local, {
        get: function (t, k) { return k in t ? t[k] : s[k]; },
        set: function (t, k, v2) { if (k in t) t[k] = v2; else s[k] = v2; return true; }
      });
      return run(fn.body, overlay);
    } finally { callDepth--; }
  }
  // callbacks turns every lambda VALUE in an argument list into a real function before a native
  // method sees it. A lambda is a compiled tree, not a function, so items.filter((x) => x.done)
  // used to throw "object is not a function" — the grammar parsed it and the runtime could not
  // run it. The function it becomes only walks that tree again, in the caller's scope; it runs
  // inside the same run(), so every step still counts against the expression's one budget.
  function callbacks(args, s) {
    for (var ci = 0; ci < args.length; ci++) {
      if (args[ci] && args[ci].__kitLambda) {
        args[ci] = (function (fn) {
          return function () { return invokeLambda(fn, Array.prototype.slice.call(arguments), s); };
        })(args[ci]);
      }
    }
    return args;
  }
  var evalDepth = 0;
  var evalRemaining = 0;
  var callDepth = 0;
  // blockedKey seals the ONLY member names that can reach code execution — `.constructor` leads to
  // Function (i.e. eval), and __proto__/prototype enable prototype pollution. Denying them in every
  // read / call / write makes "no eval" true BY CONSTRUCTION (not merely blocked by CSP), which is
  // the precondition for running client-sent expressions (capsules) safely.
  function blockedKey(k) {
    return k === "constructor" || k === "__proto__" || k === "prototype" ||
      k === "__defineGetter__" || k === "__defineSetter__" ||
      k === "__lookupGetter__" || k === "__lookupSetter__" ||
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
    if (op === "++" || op === "--" || op === "++$" || op === "--$") {
      if (blockedKey(x[1])) return undefined;
      var home = op.length === 3 ? s["$"] : s; // the page-scope form writes where $.name = v does
      var was = Number(home[x[1]]);
      var now = op.charAt(0) === "+" ? was + 1 : was - 1;
      home[x[1]] = now;
      return x[2] ? now : was;
    }
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
      if (fn && fn.__kitLambda) return invokeLambda(fn, fargs, s);
      // A registered component method — real JS the developer wrote — called with `this` = the
      // component scope, so `this.count` reads/writes its state. Still no eval: it's a function
      // reference, not a compiled string. (Server-side there are no registered methods, so its
      // walker returns nil here — the divergence is intentional and safe.)
      if (typeof fn === "function") {
        callDepth++;
        try { return fn.apply(s, callbacks(fargs, s)); } finally { callDepth--; }
      }
      return undefined;
    }
    if (op === "?") return run(x[1], s) ? run(x[2], s) : run(x[3], s);
    if (op === "idx") {
      var io = run(x[1], s), ik = run(x[2], s);
      if (io == null || ik == null) return undefined;
      var ikey = typeof ik === "number" ? ik : String(ik);
      if (typeof ikey === "string" && blockedKey(ikey)) return undefined;
      return io[ikey];
    }
    if (op === ".") {
      var o = run(x[1], s);
      var publicService = x[2] === "window" && o === publicKitSurface &&
        Object.prototype.hasOwnProperty.call(expressionServiceMembers, x[2]);
      return (o == null || (blockedKey(x[2]) && !publicService)) ? undefined : o[x[2]];
    }
    if (op === "()") {
      var oo = run(x[1], s);
      var publicMethod = x[2] === "window" && oo === publicKitSurface &&
        Object.prototype.hasOwnProperty.call(expressionServiceMembers, x[2]);
      if (oo == null || (blockedKey(x[2]) && !publicMethod)) return undefined;
      var a = x[3].map(function (y) { return run(y, s); });
      return typeof oo[x[2]] === "function" ? oo[x[2]].apply(oo, callbacks(a, s)) : undefined;
    }
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
      case "===": return l === r; case "!==": return l !== r;
    }
    throw new Error("hydrate: unknown op '" + op + "'");
  }

  // ---- directives: data-kit-<name> is the one authored form ----
  // The kernel reads source and parses it here; it does not decode a precompiled IR any more —
  // the engine never emitted one, and the data-kitwork-* prefix is reserved for what the engine
  // does put on the wire (the jit marker, the root anchor, the jitjs verbs, kernel-owned overlays).
  var cache = {};
  function directive(el, name) {
    var raw = el.getAttribute("data-kit-" + name);
    if (!raw) return null;
    var key = "$" + raw;
    if (!(key in cache)) { try { cache[key] = parse(lex(raw)); } catch (e) { cache[key] = null; } }
    return cache[key];
  }
  function selector(name) {
    return "[data-kit-" + name + "]";
  }

  // The binding groups, shared in spirit with the component runtime's dom.js: same names, same writes.
  var REFLECTED_BOOLEAN = { disabled: "disabled", required: "required", readonly: "readOnly", multiple: "multiple", hidden: "hidden", open: "open" };
  var LIVE_PROPERTY = { checked: "checked", selected: "selected", value: "value", indeterminate: "indeterminate" };
  var LIVE_BOOLEAN = { checked: true, selected: true, indeterminate: true };
  var SVG_CASE = { viewbox: "viewBox", preserveaspectratio: "preserveAspectRatio", gradientunits: "gradientUnits", gradienttransform: "gradientTransform", patternunits: "patternUnits", markerwidth: "markerWidth", markerheight: "markerHeight", refx: "refX", refy: "refY", textlength: "textLength", stddeviation: "stdDeviation" };
  function writeAttribute(el, name, v) {
    if (el.namespaceURI === "http://www.w3.org/2000/svg" && SVG_CASE[name]) name = SVG_CASE[name];
    var aria = name.indexOf("aria-") === 0;
    if (v == null || v === false && !aria) { if (el.hasAttribute(name)) el.removeAttribute(name); return; }
    var text = v === true && !aria ? "" : String(v);
    if (el.getAttribute(name) !== text) el.setAttribute(name, text);
  }
  function writeBinding(el, name, v) {
    if (/^on/.test(name) || /^data-kit/.test(name) || name === "style" || name === "srcdoc") return;
    var reflected = REFLECTED_BOOLEAN[name];
    if (reflected) {
      var on = !!v;
      if (el[reflected] !== on) el[reflected] = on;
      if (el.hasAttribute(name) !== on) el.toggleAttribute(name, on);
      return;
    }
    var live = LIVE_PROPERTY[name];
    if (live) {
      var next = LIVE_BOOLEAN[name] ? !!v : v == null ? "" : v;
      if (el[live] !== next) el[live] = next;
      return;
    }
    if (name.indexOf("-") < 0 && name in el && typeof el[name] !== "function" && typeof el[name] !== "object") {
      var plain = v == null ? "" : v;
      if (el[name] !== plain) el[name] = plain;
      return;
    }
    writeAttribute(el, name, v);
  }

  var MODEL = "[data-kit-model]";
  function modelKey(el) { return el.getAttribute("data-kit-model"); }
  // number AND range are numeric inputs — coerce to a float so arithmetic (n + step) adds, not
  // string-concatenates. Every other input type stays a string.
  function modelValue(el) { return (el.type === "number" || el.type === "range") ? (parseFloat(el.value) || 0) : (el.value || ""); }

  var raw = {};

  // A write to any scope OUTSIDE a pass — a component method finishing in a .then, a timer, a
  // service callback — schedules one coalesced repaint, so `copied` set two seconds later paints
  // without the component knowing the kernel exists. Inside a pass (render itself, an event
  // handler, a model write) the pass paints synchronously and nothing is scheduled.
  var painting = 0;
  function wrote() { if (!painting) scheduleRender(); }
  function pass(fn) {
    painting++;
    try { return fn(); } finally { painting--; }
  }

  var scope = new Proxy(raw, {
    get: function (t, k) {
      if (k === "$") return t;
      if (k in aliases) return aliases[k]; // kit / $app / $sidebar … → a public surface or component handle
      return k in t ? t[k] : globalOrZero(k);
    },
    set: function (t, k, v) {
      t[k] = v;
      wrote();
      return true;
    }
  });

  // ---- scopes: data-kit-scope="<name>" marks a component boundary ----
  // Lexical, closure-like resolution: reads fall through ancestor scopes up to the page scope;
  // writes go to the scope that OWNS the key, else the nearest one — "private shadows shared".
  // `$` addresses the page scope explicitly ($.total = $.total + 1) — the same $ the server's
  // template language uses for its root data. Scope objects live in the node's Symbol state,
  // so they die with their node; two sibling scopes never see each other.
  // A component boundary is any of these. data-kit-component names a REGISTERED blueprint
  // (see kit.component); data-kit-scope carries an inline name/init/blueprint.
  var SCOPE = "[data-kit-scope],[data-kit-component],[data-kit-item]";

  // The component registry: kit.component("counter", { count: 0, inc() {…} }). A blueprint is a
  // plain JS object — state values + methods. Methods are real functions (called with this = the
  // component scope); state is deep-cloned per instance so two boundaries never share it.
  // (Named `blueprints` to stay clear of kit.components, which is the registry of live surface.)
  var blueprints = {};
  function cloneState(v) {
    if (v === null || typeof v !== "object") return v;
    try { return JSON.parse(JSON.stringify(v)); } catch (e) { return v; }
  }
  // Copy a blueprint onto a boundary's scope. A GETTER is copied as a getter, not called once and
  // frozen: `get visibleTodos() { return this.todos.filter(...) }` is how a component writes a
  // computed value, and reading it through the scope proxy is what makes it repaint when todos
  // change. Plain values are cloned so two instances never share one array.
  function seedComponent(target, def) {
    for (var k in def) {
      if (!Object.prototype.hasOwnProperty.call(def, k)) continue;
      var descriptor = Object.getOwnPropertyDescriptor(def, k);
      if (descriptor && (descriptor.get || descriptor.set)) {
        Object.defineProperty(target, k, {
          get: descriptor.get,
          set: descriptor.set,
          enumerable: true,
          configurable: true
        });
        continue;
      }
      target[k] = (typeof def[k] === "function") ? def[k] : cloneState(def[k]);
    }
  }

  // boundaryScope initializes a boundary's local state ONCE, from the attribute's shape:
  //   data-kit-component="counter"            → a REGISTERED blueprint (state + real JS methods)
  //   data-kit-scope="{ count: 5, inc: () => count = count + 1 }" → an INLINE blueprint (IR methods)
  //   data-kit-scope="count: 5, open: true"   → the SAME literal, braces optional (ideaship-final §6)
  // One literal, one parser: the old NAME form ("counter") and INIT form ("count = 5; open = true")
  // are gone (B8, 22/09) — an empty attribute is an empty boundary, anything else must parse as
  // the literal or the boundary reports it. Inline blueprints are the same compiled grammar as
  // everything else — parsed, never eval'd — and being markup they are visible to the server.
  function boundaryScope(b) {
    var st = state(b);
    var craw = b.getAttribute("data-kit-component");
    if (craw) {
      var tag = parseComponentTag(craw);
      var cname = tag.name;
      var alias = b.getAttribute("data-kit-alias") || "";
      // Component registration (kit.component) can run AFTER the first render — so seed lazily,
      // the first time the blueprint is available, and never re-seed once done (keeps mutations).
      if (!st.scope) st.scope = {};
      registerAlias(alias, st.scope, b);
      if (!st.seeded) {
        if (blueprints[cname]) {
          seedComponent(st.scope, blueprints[cname]);
          st.seeded = true;
          runInit(b);
        } else {
          // Nothing registered under this name. The engine ships exactly the components a page
          // names, so this is a typo or a missing _components/<name>.js. Say it ONCE per host:
          // every later pass would otherwise repeat it for as long as the page is open.
          if (!st.reportedMissing) {
            st.reportedMissing = true;
            reportError(new Error("hydrate: no component registered as \"" + cname + "\""), b, "data-kit-component");
          }
        }
      }
      return st.scope;
    }
    if (st.scope) return st.scope;
    st.scope = {};
    var v = (b.getAttribute("data-kit-scope") || "").trim();
    if (!v) return st.scope;
    try {
      var parent = b.parentElement ? scopeFor(b.parentElement) : scope;
      var o = run(parse(lex(v.charAt(0) === "{" ? v : "{" + v + "}")), parent);
      if (o && typeof o === "object") { for (var k in o) st.scope[k] = o[k]; }
      runInit(b);
    } catch (e) {
      reportError(new Error("hydrate: data-kit-scope must be an object literal, braces optional (" + (e && e.message || e) + ")"), b, "data-kit-scope");
    }
    return st.scope;
  }
  // ---- init(context): the component's hands on its own DOM (ideaship-final §6, B9 one kernel) ----
  // Nine keys — the seven the component runtime handed out, so a ported body needs no rewrite, then
  // require and emit (below):
  //   host                — the element carrying data-kit-component / data-kit-scope
  //   owned(selector)     — matches under the host, the host itself included, a nested host's
  //                         subtree excluded (its elements are its own)
  //   element(name)       — the first data-kit-element="name" the host owns; elements(name) = all
  //   listen(target, type, fn, options) — an event listener released with the host
  //   cleanup(fn)         — runs when morph removes the host (this = the scope); the returned
  //                         cancel runs it now instead
  //   afterRender(fn)     — runs ONCE after the next paint, outside the pass — so a state write
  //                         inside it schedules its own repaint (no lost update)
  //   require(name, ...attributes) — the elements it cannot work without; a miss is reported
  //   emit(name, detail)  — an event the page hears with data-kit-event:<name>
  // Every resource lives on the host's state, so cleanupTree releases it when the host leaves.
  var HOST = "[data-kit-component],[data-kit-scope]";
  var afterRenders = [];
  function initContext(b) {
    function owned(selector) {
      if (typeof selector !== "string") throw new TypeError("hydrate: context.owned(selector) expects a string");
      var out = [];
      if (b.matches && b.matches(selector)) out.push(b);
      b.querySelectorAll(selector).forEach(function (el) {
        if (!el.matches(HOST) && el.parentElement && el.parentElement.closest(HOST) === b) out.push(el);
      });
      return out;
    }
    function elements(name) {
      if (typeof name !== "string" || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) throw new TypeError("hydrate: context.elements(name) expects an identifier");
      return owned('[data-kit-element="' + name + '"]');
    }
    function element(name) { return elements(name)[0] || null; }
    function cleanup(fn) {
      if (typeof fn !== "function") throw new TypeError("hydrate: context.cleanup(fn) expects a function");
      var active = true;
      var release = function () {
        if (!active) return;
        active = false;
        try { fn.call(scopeFor(b)); } catch (error) { reportError(error, b, "cleanup"); }
      };
      onCleanup(b, release);
      return release;
    }
    function listen(target, type, fn, options) {
      if (!target || typeof target.addEventListener !== "function") throw new TypeError("hydrate: context.listen(target, type, fn, options) expects an EventTarget");
      if (typeof type !== "string" || typeof fn !== "function") throw new TypeError("hydrate: context.listen(target, type, fn, options) expects a string and a function");
      var capture = typeof options === "boolean" ? options : !!(options && options.capture);
      target.addEventListener(type, fn, options);
      return cleanup(function () { target.removeEventListener(type, fn, capture); });
    }
    function afterRender(fn) {
      if (typeof fn !== "function") throw new TypeError("hydrate: context.afterRender(fn) expects a function");
      var entry = { host: b, run: fn, active: true };
      afterRenders.push(entry);
      return function () { entry.active = false; };
    }
    // require(name, ...attributes) — what the component cannot work without, said in its own code:
    // context.require("item", "data-id"). Checked after the first paint, so an element a template
    // renders and an attribute a binding writes both count. A miss is reported ONCE through the error
    // channel, naming the component — a tree with no data-id used to ignore the keyboard in silence
    // (02/10). Returns the elements as they stand now.
    function requireElements(name) {
      var attributes = Array.prototype.slice.call(arguments, 1);
      var now = elements(name); // a bad name throws here, where the author wrote it
      afterRender(function () {
        var found = elements(name);
        var part = 'data-kit-element="' + name + '"', problem = found.length ? "" : "needs " + part + " inside it, and has none";
        attributes.forEach(function (attribute) {
          var missing = found.filter(function (el) { return !el.hasAttribute(attribute); }).length;
          if (missing && !problem) problem = "needs " + attribute + " on every " + part + "; " + missing + " of " + found.length + " have none";
        });
        if (problem) reportError(new Error("kitwork: " + b.getAttribute("data-kit-component") + " " + problem), b, "data-kit-component");
      });
      return now;
    }
    // emit(name, detail) — tell the page something happened: a CustomEvent on the host that bubbles,
    // heard with data-kit-event:<name>="…" and read as $event.detail. A DOM event's name is refused,
    // so data-kit-event:click always means the DOM's click. Emitted during a paint (from init, say),
    // it waits for the paint to end: a handler must not run inside the pass that is drawing the
    // page, and the paint is also where the document learns to listen for a new name.
    function emit(name, detail) {
      if (!EVENT_NAME.test(name) || DOM_EVENT_NAMES[name]) throw new TypeError('hydrate: context.emit("' + name + '") needs a lower-case name of its own, not a DOM event\'s');
      var send = function () {
        ensureListener(name);
        b.dispatchEvent(new CustomEvent(name, { bubbles: true, detail: detail }));
      };
      if (painting > 0) afterRender(send);
      else send();
    }
    return Object.freeze({ host: b, owned: owned, element: element, elements: elements, listen: listen, cleanup: cleanup, afterRender: afterRender, require: requireElements, emit: emit });
  }
  // flushAfterRender runs after a paint has ended (render → pass → paint, then this): one-shot,
  // in registration order, only for hosts still in the document; a host that morph removed
  // between registration and paint is skipped, its entry dropped.
  function flushAfterRender() {
    if (!afterRenders.length) return;
    var entries = afterRenders.splice(0);
    entries.forEach(function (entry) {
      if (!entry.active) return;
      entry.active = false;
      if (!entry.host.isConnected) return;
      try { entry.run.call(scopeFor(entry.host)); } catch (error) { reportError(error, entry.host, "afterRender"); }
    });
  }
  // runInit calls a boundary's init() ONCE, right after it is seeded — the mount lifecycle hook.
  // A registered component's init is real JS (this = the scope, one argument: the context above);
  // an inline blueprint's is an IR lambda. Set the guard BEFORE calling so a re-entrant scopeFor
  // never loops. An init that throws reaches the error boundary like any directive (directive
  // "init"), and the component stays mounted with whatever state it seeded.
  function runInit(b) {
    var st = state(b);
    if (st.inited) return;
    st.inited = true;
    var fn = st.scope && st.scope.init;
    if (!fn) return;
    try {
      var proxy = scopeFor(b);
      var result;
      if (typeof fn === "function") result = fn.call(proxy, initContext(b));
      else if (fn.__kitLambda) result = run(fn, proxy);
      observeEffect(result, proxy);
    } catch (e) { reportError(e, b, "init"); }
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
  // NOT a prototype mutation: they are variables in the expression context that resolve to native
  // objects, so `$this.querySelector('input').focus()` executes exactly as it reads. The system
  // variables of ideaship-final §3: `$this` is the element that owns the directive; `$host` is the
  // boundary element — nearest data-kit-scope/component, else <html> — so a query stays inside what
  // the component owns; `$event` is the native DOM event of the handler that is running;
  // `$element` the boundary's named elements. `$el` and `$root`, the old spellings, resolve to
  // nothing any more (B1, 22/09); their names stay reserved so no alias can take them.
  // Reads and method calls only — value/attribute CHANGES belong to bindings (data-kit-model,
  // state→CSS), not to reaching in and poking the DOM.
  function elementScope(el, event, errorContext) {
    var base = scopeFor(el);
    return new Proxy(base, {
      get: function (t, k) {
        if (k === "$this") return el;
        if (k === "$host") return (el.closest && el.closest(SCOPE)) || document.documentElement;
        if (k === "$event") return event || null;
        if (k === "$error") return errorContext || null;
        if (k === "$element") return namedElements(el);
        if (k in aliases) return aliases[k]; // kit / $app / $sidebar … → a public surface or component handle
        return base[k];
      },
      set: function (t, k, v) { base[k] = v; return true; }
    });
  }
  // namedElements is `$element` for an expression acting on `el` (ideaship-final §6: data-kit-element names a
  // DOM element, data-kit-alias names an instance). The registry belongs to the acting boundary —
  // the nearest data-kit-scope/component/item, else the page — and holds only the refs that
  // boundary owns: not one inside a nested boundary, not one outside. The first element with a
  // name wins; a missing name is nullish, so `$element.search?.focus()` is the guarded spelling.
  // Looked up on read, since the elements are whatever the DOM holds at that moment.
  function namedElements(el) {
    var boundary = (el.closest && el.closest(SCOPE)) || null;
    return new Proxy(Object.create(null), {
      get: function (t, name) {
        if (typeof name !== "string" || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) return undefined;
        var candidates = (boundary || document).querySelectorAll('[data-kit-element="' + name + '"]');
        for (var i = 0; i < candidates.length; i++) {
          if (candidates[i].closest(SCOPE) === boundary) return candidates[i];
        }
        return undefined;
      },
      set: function () { return false; }
    });
  }

  // ---- data-kit-style:<property>="expr" — one property per attribute (ideaship-final §2.3, B7) ----
  // The same shape as bind and seed: the target is in the attribute NAME, the value is one
  // expression. A component with a continuous value — a bar's width, a popover's offset, a
  // carousel's transform — says it in markup instead of writing element.style from JavaScript,
  // which is the whole point: the server sees the expression and the CSS JIT can read the names.
  //
  // A nullish, false or empty result RESTORES what the author wrote in the style attribute, so a
  // binding that stops applying never leaves a value behind. The baseline is captured once, the
  // first time this element's property is written.
  //
  // What a value may not contain: control characters, `;{}\@`, comments, !important, and the
  // functions that can fetch or execute (url, image-set, src, expression, attr), plus the
  // javascript:/vbscript:/data:text/html spellings. `var()` IS allowed — a Kitwork site's colours
  // are custom properties, so blocking it would make the directive useless here.
  var STYLE_BLOCKED = { "css-text": 1, csstext: 1, behavior: 1, "-moz-binding": 1 };
  function styleName(name) {
    if (name.indexOf("--") === 0) {
      return /^--[A-Za-z_][A-Za-z0-9_-]*$/.test(name) && !/^--(?:kit|kitwork)-/i.test(name) ? name : "";
    }
    return /^-?[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(name) && !STYLE_BLOCKED[name] ? name : "";
  }
  function unsafeStyleValue(text) {
    if (/[\u0000-\u001f\u007f-\u009f]/.test(text) || /[;{}\\@]/.test(text) ||
      text.indexOf("/*") >= 0 || text.indexOf("*/") >= 0 || /!\s*important\b/i.test(text) ||
      /(^|[^A-Za-z0-9_-])(url|image-set|-webkit-image-set|src|expression|attr)\s*\(/i.test(text)) return true;
    var compact = text.replace(/\s+/g, "").toLowerCase();
    return compact.indexOf("javascript:") >= 0 || compact.indexOf("vbscript:") >= 0 ||
      compact.indexOf("data:text/html") >= 0;
  }
  function writeStyle(el, property, value) {
    var store = state(el);
    var baseline = store.styleBaseline || (store.styleBaseline = {});
    if (!(property in baseline)) {
      baseline[property] = { value: el.style.getPropertyValue(property), priority: el.style.getPropertyPriority(property) };
    }
    if (value == null || value === false || value === "") {
      var was = baseline[property];
      if (was.value !== "") el.style.setProperty(property, was.value, was.priority);
      else el.style.removeProperty(property);
      return;
    }
    var text = typeof value === "number" ? (isFinite(value) ? String(value) : "") : String(value);
    if (!text || unsafeStyleValue(text)) throw new Error('hydrate: unsafe value for data-kit-style:' + property);
    el.style.setProperty(property, text, "");
  }

  function scopeFor(el) {
    // A comment node (a data-kit-for / data-kit-if anchor) has no closest() — resolve through its
    // parent element, else the region's local/component scope is lost and reads fall to the PAGE
    // scope (which is why a for/if over a component-scoped array or condition would render nothing).
    var node = el;
    if (node && !node.closest) node = node.parentElement || node.parentNode;
    var b = node && node.closest ? node.closest(SCOPE) : null;
    if (!b) return scope;
    var st = state(b);
    if (st.scopeProxy) return st.scopeProxy;
    st.scopeProxy = new Proxy(boundaryScope(b), {
      get: function (t, k) {
        if (k === "$") return raw;
        if (k in aliases) return aliases[k]; // kit / $app / $sidebar … → a public surface or component handle
        var objs = chainFor(b);
        for (var i = 0; i < objs.length; i++) { if (k in objs[i]) return objs[i][k]; }
        return globalOrZero(k);
      },
      set: function (t, k, v) {
        var objs = chainFor(b);
        for (var i = 0; i < objs.length; i++) {
          if (k in objs[i]) {
            objs[i][k] = v;
            wrote();
            return true;
          }
        }
        objs[0][k] = v;
        wrote();
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

  // Named component handles: data-kit-component="sidebar" data-kit-alias="$sidebar" registers
  // `$sidebar` → that instance's scope, so ANY expression reaches it ($sidebar.cycle()), even from
  // outside its DOM subtree (the scattered-controls case). No alias = purely lexical (bare cycle() =
  // nearest scope). data-kit-alias is the one spelling (ideaship-final §6): the alias names the
  // component INSTANCE; data-kit-element will name an element.
  var aliases = Object.create(null);
  var reservedAliases = Object.create(null);
  ["$", "$this", "$el", "$host", "$root", "$event", "$element", "$error"].forEach(function (name) { reservedAliases[name] = true; });
  // aliasOwners remembers which boundary each alias names, so a handle resolves to that boundary's
  // SCOPE PROXY rather than to its raw state object.
  //
  // This is the difference between $menu.hide() working and half-working. A method invoked from a
  // directive receives the proxy as `this`, and the proxy's set trap calls wrote() — so a write
  // from a timer the method started still repaints. A method invoked through a handle used to
  // receive the raw object: the write landed, nothing was told, and the DOM kept the old answer
  // until something else happened to repaint. Inside a pass nobody noticed, because the pass
  // repaints anyway; outside one — a timer, a promise, another component — it simply did not work.
  var aliasOwners = Object.create(null);
  function registerAlias(alias, target, owner) {
    alias = String(alias || "").trim();
    var store = owner ? state(owner) : null;
    var previous = store && store.componentAlias;
    if (previous && previous !== alias && aliasOwners[previous] === owner) {
      delete aliases[previous];
      delete aliasOwners[previous];
    }
    if (store) store.componentAlias = "";
    if (!/^\$[A-Za-z][A-Za-z0-9_]*$/.test(alias) || reservedAliases[alias]) return false;
    if (owner) {
      aliasOwners[alias] = owner;
      Object.defineProperty(aliases, alias, {
        get: function () { return scopeFor(owner); },
        configurable: true,
        enumerable: true
      });
    } else {
      aliases[alias] = target;
    }
    if (store) store.componentAlias = alias;
    return true;
  }
  // `kit` is the platform/runtime service surface. Trusted JavaScript uses window.kit directly;
  // authored markup resolves the identifier `kit` through this curated, read-only view so it cannot
  // reach runtime control internals or the raw native transport. A service becomes callable from
  // expressions only by an explicit grant here.
  //
  // `$app` is deliberately NOT installed by the kernel. It is an ordinary component alias owned by
  // the application:
  //   kit.component("app", definition)
  //   <html data-kit-component="app" data-kit-alias="$app">
  // This keeps application state/lifecycle separate from shared platform services.
  var publicKitIntrinsics = Object.create(null);
  ("platform isNative mode version").split(" ").forEach(function (name) {
    publicKitIntrinsics[name] = true;
  });
  function expressionService(name) {
    if (!Object.prototype.hasOwnProperty.call(expressionServiceMembers, name)) return undefined;
    return expressionServiceSurface(name);
  }
  var publicKitSurface = new Proxy(Object.create(null), {
    get: function (_, name) {
      if (Object.prototype.hasOwnProperty.call(publicKitIntrinsics, name)) return kit[name];
      return expressionService(name);
    },
    has: function (_, name) {
      return Object.prototype.hasOwnProperty.call(publicKitIntrinsics, name) ||
        Object.prototype.hasOwnProperty.call(expressionServiceMembers, name);
    },
    ownKeys: function () {
      return Object.keys(publicKitIntrinsics).concat(Object.keys(expressionServiceMembers));
    },
    getOwnPropertyDescriptor: function (_, name) {
      if (!Object.prototype.hasOwnProperty.call(publicKitIntrinsics, name) &&
        !Object.prototype.hasOwnProperty.call(expressionServiceMembers, name)) return undefined;
      return { configurable: true, enumerable: true };
    },
    set: function () { return false; },
    defineProperty: function () { return false; },
    deleteProperty: function () { return false; },
    setPrototypeOf: function () { return false; },
    getPrototypeOf: function () { return null; }
  });
  aliases["kit"] = publicKitSurface;
  // parseComponentTag splits `name@version` (version optional) → { name, version }. The alias is
  // its own attribute, data-kit-alias, never a tail on the name.
  function parseComponentTag(raw) {
    var name = raw, version = "", i;
    if ((i = name.indexOf("@")) >= 0) { version = name.slice(i + 1).trim(); name = name.slice(0, i); }
    return { name: name.trim(), version: version };
  }

  var activeComponents = {};
  // Every host MOUNTS on the pass — seeded from its blueprint and init(context) run — whether or
  // not a directive inside it ever asks for its scope. An init-only component (a favicon fallback,
  // a live counter that owns its DOM) has nothing to bind, and still has to start.
  function rebuildActiveComponents() {
    var next = {};
    document.querySelectorAll("[data-kit-component]").forEach(function (el) {
      var craw = el.getAttribute("data-kit-component");
      if (!craw) return;
      var cname = parseComponentTag(craw).name;
      boundaryScope(el);
      if (state(el).seeded) {
        var inst = scopeFor(el);
        (next[cname] = next[cname] || []).push(inst);
      }
    });

    for (var k in activeComponents) {
      if (!(k in next)) {
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

  // ---- data-kit-for: client list rendering ----
  // The one capability the kernel lacked. It owns STRUCTURE — which item nodes exist, in what order,
  // matched by data-kit-key — and hands CONTENT to the ordinary text/bind/class pass by giving each
  // materialised item a per-item scope in its Symbol state (item / index), which chainFor then reads
  // through to the enclosing component scope. It reuses the SAME keyed-identity idea as morph rather
  // than diffing: a keyed node that survives is MOVED, never rebuilt, so focus/cursor/input on a row
  // are preserved across re-renders. No IR runs the list logic — only the tiny author expression that
  // names the array (`items`) and the key (`item.id`) is walked, exactly like any other attribute.
  var FOR = "[data-kit-for]";
  var forRegistry = [];
  var forSerial = 0;
  function parseFor(raw) {
    var m = /^\s*([$A-Za-z_][\w$]*)\s*(?:,\s*([$A-Za-z_][\w$]*)\s*)?\s+of\s+([\s\S]+)$/.exec(raw || "");
    if (!m) return null;
    var list;
    try { list = parse(lex(m[3])); } catch (e) { return null; }
    return { item: m[1], index: m[2] || "", list: list, keySrc: "" };
  }
  // First sight of a data-kit-for element: capture it as a template, replace it with a comment anchor.
  // The anchor is where materialised rows are inserted; the source element itself never renders.
  function collectFor() {
    document.querySelectorAll(FOR).forEach(function (el) {
      var parent = el.parentNode;
      if (!parent) return;
      var spec = parseFor(el.getAttribute("data-kit-for"));
      if (!spec) { el.removeAttribute("data-kit-for"); return; }
      spec.keySrc = el.getAttribute("data-kit-key") || "";
      var template = el.cloneNode(true);
      template.removeAttribute("data-kit-for");
      // The list's place in the document is the marker pair of ideaship-final §7 — the same shape
      // a server-rendered list arrives in — and every row carries the list's id in data-kit-item.
      var id = "f" + (++forSerial);
      var anchor = document.createComment("kit-for:start id=" + id);
      var end = document.createComment("kit-for:end");
      parent.insertBefore(anchor, el);
      parent.insertBefore(end, el);
      parent.removeChild(el);
      forRegistry.push({ id: id, anchor: anchor, end: end, template: template, spec: spec, keyIR: spec.keySrc ? parse(lex(spec.keySrc)) : null });
    });
  }
  function renderFor() {
    collectFor();
    for (var r = 0; r < forRegistry.length; r++) {
      var reg = forRegistry[r];
      var parent = reg.anchor.parentNode;
      if (!parent) continue; // anchor left the DOM (a swap removed the region) — nothing to render
      var arr = run(reg.spec.list, scopeFor(reg.anchor));
      if (!(arr instanceof Array)) arr = [];

      // current rows: the data-kit-item siblings between the start and end markers.
      var current = [], curByKey = {}, n = reg.anchor.nextSibling;
      while (n && n !== reg.end) {
        if (n.nodeType === 1 && n.getAttribute("data-kit-item") === reg.id) {
          curByKey[n.getAttribute("data-kit-key")] = n;
          current.push(n);
        }
        n = n.nextSibling;
      }

      var insertAfter = reg.anchor, used = {}, created = false;
      for (var i = 0; i < arr.length; i++) {
        // The row's scope is an overlay (§7, rule 2): the item and index under their authored
        // names plus count / first / last / even / odd — the item object itself is never touched.
        var itemScope = { count: arr.length, first: i === 0, last: i === arr.length - 1, even: i % 2 === 0, odd: i % 2 === 1 };
        itemScope[reg.spec.item] = arr[i];
        if (reg.spec.index) itemScope[reg.spec.index] = i;
        var key = reg.keyIR ? String(run(reg.keyIR, itemScope)) : String(i);
        used[key] = true;

        var node = curByKey[key];
        if (!node) {
          node = reg.template.cloneNode(true);
          node.setAttribute("data-kit-item", reg.id);
          node.setAttribute("data-kit-key", key);
          created = true;
        }
        var st = state(node);
        st.scope = itemScope; // (re)bind the row to its current item; content is filled by render()
        st.seeded = true;
        if (insertAfter.nextSibling !== node) parent.insertBefore(node, insertAfter.nextSibling);
        insertAfter = node;
      }

      for (var d = 0; d < current.length; d++) {
        if (!used[current[d].getAttribute("data-kit-key")]) {
          cleanupTree(current[d]);
          current[d].remove();
        }
      }
      // A row made in this pass may carry a data-kit-for of its own (weeks of days, groups of
      // items). Collect it NOW: the loop re-reads forRegistry.length, so the inner list fills in
      // this same pass. Collected only at the start, a new row stayed empty until something else
      // happened to repaint — the calendar drew September's title over a grid with no days in it.
      if (created) collectFor();
    }
  }

  // A subtree mounted DURING a click (a modal opened by that very click) must not be closed by the
  // same click's :outside check — the opening click is, by definition, outside a panel that did
  // not exist yet. Fresh mounts are remembered until the next microtask, i.e. until the whole click
  // dispatch (every delegated listener) has finished; the NEXT outside click closes normally.
  var freshMounts = [];
  function markFresh(node) {
    if (freshMounts.indexOf(node) < 0) freshMounts.push(node);
    if (freshMounts.length === 1) {
      (typeof queueMicrotask === "function" ? queueMicrotask : function (f) { setTimeout(f, 0); })(function () { freshMounts.length = 0; });
    }
  }
  function isFresh(el) {
    for (var i = 0; i < freshMounts.length; i++) {
      var f = freshMounts[i];
      if (f === el || (f.contains && f.contains(el))) return true;
    }
    return false;
  }

  // ---- data-kit-if: conditional mount / unmount ----
  // The sibling of data-kit-for, on the same machinery — anchor comment, captured template,
  // cleanupTree on removal. The difference from data-kit-show is the whole point: `show` keeps the
  // DOM and only toggles `hidden`, so a hidden subtree's bindings and effects keep running; `if`
  // MOUNTS and UNMOUNTS, so an absent branch does nothing at all. That is what a modal, an editing
  // panel or a lazy region needs — not a hidden node quietly holding an SSE stream open.
  var IF = "[data-kit-if]";
  var ifRegistry = [];
  function collectIf() {
    document.querySelectorAll(IF).forEach(function (el) {
      var parent = el.parentNode;
      if (!parent) return;
      var cond = directive(el, "if");
      if (!cond) { el.removeAttribute("data-kit-if"); return; }
      var template = el.cloneNode(true);
      template.removeAttribute("data-kit-if");
      var anchor = document.createComment("kit-if");
      parent.insertBefore(anchor, el);
      parent.removeChild(el);
      ifRegistry.push({ anchor: anchor, template: template, cond: cond, mounted: null });
    });
  }
  function renderIf() {
    collectIf();
    for (var r = 0; r < ifRegistry.length; r++) {
      var reg = ifRegistry[r];
      var parent = reg.anchor.parentNode;
      if (!parent) continue;
      var show = !!run(reg.cond, scopeFor(reg.anchor));
      if (show && !reg.mounted) {
        // Mount a fresh clone right after the anchor; the ordinary binding pass (below) fills it in
        // this same render, because renderIf runs before the text/show/bind queries.
        reg.mounted = reg.template.cloneNode(true);
        parent.insertBefore(reg.mounted, reg.anchor.nextSibling);
        markFresh(reg.mounted); // don't let the click that opened it also close it via data-kit-click:outside
      } else if (!show && reg.mounted) {
        cleanupTree(reg.mounted); // release the subtree's listeners/observers/streams before removal
        reg.mounted.remove();
        reg.mounted = null;
      }
    }
  }

  // ---- data-kit-seed: DOM → state, once — the mirror of data-kit-bind (chốt 21/09) ----
  // The server already rendered the value; seed hands it to state instead of the author writing it
  // a second time into a scope literal. data-kit-seed="key" reads the element's text (on a
  // <script type="application/json">, its JSON); data-kit-seed:<name>="key" reads the property or
  // attribute <name> by the three bind groups, in reverse. The target is a state key, a dotted
  // path, or list[] — one entry per element in document order, the list rebuilt whenever a member
  // that has not been seeded yet appears. The write goes where an assignment would go: the scope
  // in the chain that owns the key, else the nearest one. Once per element; attributes stay
  // strings (no type guessing beyond a boolean property or a numeric input).
  var SEED_TARGET = /^([A-Za-z_][A-Za-z0-9_]*)((?:\.[A-Za-z_][A-Za-z0-9_]*)*)(\[\])?$/;
  function seedsOf(el) {
    if (el.__kitSeeds) return el.__kitSeeds;
    var list = [];
    if (el.getAttributeNames) {
      el.getAttributeNames().forEach(function (name) {
        if (name === "data-kit-seed") list.push({ attr: name, name: "" });
        else if (name.indexOf("data-kit-seed:") === 0) list.push({ attr: name, name: name.slice(14) });
      });
    }
    el.__kitSeeds = list;
    return list;
  }
  function checkSeedData(v, seen) {
    if (v === null || typeof v !== "object") return v;
    if (seen.indexOf(v) >= 0) throw new Error("hydrate: circular seed data");
    seen.push(v);
    for (var k in v) {
      if (!Object.prototype.hasOwnProperty.call(v, k)) continue;
      if (blockedKey(k)) throw new Error("hydrate: blocked seed key '" + k + "'");
      checkSeedData(v[k], seen);
    }
    return v;
  }
  function readSeed(el, name) {
    if (!name) {
      var tag = String(el.tagName || "").toLowerCase();
      var type = String(el.type || (el.getAttribute && el.getAttribute("type")) || "").toLowerCase();
      if (tag === "script" && type === "application/json") return checkSeedData(JSON.parse(el.textContent), []);
      return String(el.textContent || "").trim();
    }
    if (/^on/.test(name) || /^data-kit/.test(name) || name === "style" || name === "srcdoc" || name === "innerhtml" || name === "outerhtml") {
      throw new Error("hydrate: unsafe seed source '" + name + "'");
    }
    var reflected = REFLECTED_BOOLEAN[name];
    if (reflected) return reflected in el ? !!el[reflected] : el.hasAttribute(name);
    var live = LIVE_PROPERTY[name];
    if (live) {
      if (LIVE_BOOLEAN[name]) return live in el ? !!el[live] : el.hasAttribute(name);
      var kind = String(el.type || (el.getAttribute && el.getAttribute("type")) || "").toLowerCase();
      if (kind === "number" || kind === "range") {
        if (el.value === "" || el.value == null) return null;
        var n = Number(el.value);
        return isFinite(n) ? n : null;
      }
      return el[live] == null ? (el.getAttribute(name) || "") : String(el[live]);
    }
    if (name.indexOf("-") < 0 && name in el && typeof el[name] !== "function" && typeof el[name] !== "object") return el[name];
    return el.hasAttribute(name) ? el.getAttribute(name) : null;
  }
  function seedWrite(el, path, value) {
    var s = scopeFor(el);
    if (path.length === 1) { s[path[0]] = value; return; }
    var holder = s[path[0]];
    if (!holder || typeof holder !== "object" || holder instanceof Array) { holder = {}; s[path[0]] = holder; }
    for (var i = 1; i < path.length - 1; i++) {
      var next = holder[path[i]];
      if (!next || typeof next !== "object" || next instanceof Array) { next = {}; holder[path[i]] = next; }
      holder = next;
    }
    holder[path[path.length - 1]] = value;
  }
  function seedElements() {
    var lists = {}, rebuild = {};
    document.querySelectorAll("*").forEach(function (el) {
      var seeds = seedsOf(el);
      if (!seeds.length) return;
      var done = el.__kitSeeded || (el.__kitSeeded = {});
      seeds.forEach(function (sd) {
        var m = SEED_TARGET.exec(el.getAttribute(sd.attr) || "");
        if (!m) { if (!done[sd.attr]) { done[sd.attr] = true; reportError(new Error("hydrate: data-kit-seed target must be a key, a dotted path, or list[]"), el, sd.attr); } return; }
        var path = [m[1]].concat(m[2] ? m[2].slice(1).split(".") : []);
        for (var i = 0; i < path.length; i++) if (blockedKey(path[i])) return;
        if (m[3]) {
          var key = path.join(".");
          if (!done[sd.attr]) rebuild[key] = true;
          (lists[key] || (lists[key] = [])).push({ el: el, sd: sd, path: path });
          return;
        }
        if (done[sd.attr]) return;
        done[sd.attr] = true;
        try { seedWrite(el, path, readSeed(el, sd.name)); } catch (e) { reportError(e, el, sd.attr); }
      });
    });
    for (var key in rebuild) {
      var members = lists[key], values = [];
      for (var j = 0; j < members.length; j++) {
        var member = members[j];
        member.el.__kitSeeded[member.sd.attr] = true;
        try { values.push(readSeed(member.el, member.sd.name)); } catch (e) { reportError(e, member.el, member.sd.attr); }
      }
      try { seedWrite(members[0].el, members[0].path, values); } catch (e) { reportError(e, members[0].el, members[0].sd.attr); }
    }
  }

  function render() { pass(paint); flushAfterRender(); }
  function paint() {
    seedElements();
    seedModels();
    rebuildActiveComponents();
    renderFor();
    renderIf();
    document.querySelectorAll(selector("text")).forEach(function (el) { var x = directive(el, "text"); if (!x) return; guarded(el, "data-kit-text", function () { var v = run(x, scopeFor(el)); el.textContent = v == null ? "" : v; }); });
    document.querySelectorAll(selector("show")).forEach(function (el) { var x = directive(el, "show"); if (!x) return; guarded(el, "data-kit-show", function () { el.hidden = !run(x, scopeFor(el)); }); });
    // bind → data-kit-bind:<name>="expr": the target is in the attribute name, the value is one
    // expression (ideaship-final §5). Three groups by name: a reflected boolean (disabled, hidden,
    // open…) lands on the property AND the attribute; a live property (checked, value…) on the
    // property only, so a form reset still returns to the authored attribute; anything else — aria-*,
    // data-*, a name the element has no property for — on the attribute, aria spelled "true"/"false".
    // A name carries no selector, so the pass walks the document and keeps each element's bound
    // names on the element after the first look.
    document.querySelectorAll("*").forEach(function (el) {
      // data-kit-event:<name> listens for a name the short list does not cover: make sure the
      // document hears it before the first one fires.
      var handlers = handlersOf(el);
      for (var h = 0; h < handlers.length; h++) ensureListener(handlers[h].type);
      var names = el.__kitBound;
      if (!names) {
        names = [];
        el.getAttributeNames().forEach(function (n) { if (n.indexOf("data-kit-bind:") === 0) names.push(n); });
        el.__kitBound = names;
      }
      for (var i = 0; i < names.length; i++) {
        var raw = el.getAttribute(names[i]); if (!raw) continue;
        var key = "$" + raw;
        if (!(key in cache)) { try { cache[key] = parse(lex(raw)); } catch (e) { cache[key] = null; } }
        if (!cache[key]) continue;
        (function (name, program) {
          guarded(el, name, function () { writeBinding(el, name.slice(14), run(program, scopeFor(el))); });
        })(names[i], cache[key]);
      }
      var styles = el.__kitStyled;
      if (!styles) {
        styles = [];
        el.getAttributeNames().forEach(function (n) { if (n.indexOf("data-kit-style:") === 0) styles.push(n); });
        el.__kitStyled = styles;
      }
      for (var s = 0; s < styles.length; s++) {
        var source = el.getAttribute(styles[s]); if (!source) continue;
        var property = styleName(styles[s].slice(15));
        if (!property) continue; // render.go reported it; the kernel simply does not write it
        var programKey = "$" + source;
        if (!(programKey in cache)) { try { cache[programKey] = parse(lex(source)); } catch (e) { cache[programKey] = null; } }
        if (!cache[programKey]) continue;
        (function (attribute, name, program) {
          guarded(el, attribute, function () { writeStyle(el, name, run(program, scopeFor(el))); });
        })(styles[s], property, cache[programKey]);
      }
    });
    // class → toggle classes from an expression. Every shape the grammar allows is accepted, so
    // nobody has to remember a special syntax: a string ('card ring'), an object whose truthy keys
    // win ({ 'is-open': open }), a ternary between names, an array mixing all three, nested freely.
    // The names must be written out in full — the CSS JIT reads them off this same expression to
    // decide what to emit, and cannot see a name built with '+'.
    document.querySelectorAll(selector("class")).forEach(function (el) {
      var x = directive(el, "class"); if (!x) return;
      var want = guarded(el, "data-kit-class", function () { return classNames(run(x, scopeFor(el)), []); });
      if (!want) return;
      // Remove only what THIS directive added last pass, never the static class attribute: an
      // element is normally `class="card" data-kit-class="{ ring: focused }"` and blowing away
      // "card" on the first toggle would strip the page's own styling.
      var prev = el.__kitClass || [];
      for (var i = 0; i < prev.length; i++) if (want.indexOf(prev[i]) < 0) el.classList.remove(prev[i]);
      for (var j = 0; j < want.length; j++) el.classList.add(want[j]);
      el.__kitClass = want;
    });
    document.querySelectorAll(MODEL).forEach(function (el) {
      // A field whose debounce is still pending holds what was typed and the scope does not have it
      // yet — on purpose. Syncing it here would drag it back to the old value mid-word, whenever
      // anything else happened to paint.
      if (state(el).debounceTimer) return;
      var k = modelKey(el), s = scopeFor(el);
      if (String(s[k]) !== el.value) el.value = s[k];
    });
  }

  // ---- per-element runtime state ----
  // Lives behind a private Symbol. Resources registered on the state are explicitly released when
  // morph or another DOM owner removes the node. (The verb registry that used to sit here —
  // data-kit-action, data-kit-target, the fire() dispatcher — is gone: behaviour is a component or an
  // expression, transport is Drive. 22/09.)
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
    // Ownership is the test, not identity: reading aliases[name] now resolves the boundary's scope
    // proxy, which is never the raw scope object this store holds.
    if (store.componentAlias && aliasOwners[store.componentAlias] === element) {
      delete aliases[store.componentAlias];
      delete aliasOwners[store.componentAlias];
      store.componentAlias = "";
    }
    if (store.apiController) {
      store.apiController.abort();
      store.apiController = null;
    }
    if (store.debounceTimer) {
      clearTimeout(store.debounceTimer);
      store.debounceTimer = null;
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
  kit.onCleanup = onCleanup;
  // Register a reusable stateful component blueprint. Activate it with data-kit-component="name".
  // A component has state + methods + a scope boundary. Registering (re)renders on the next tick,
  // so components registered after boot still paint.
  var renderScheduled = false;
  function scheduleRender() {
    if (renderScheduled) return;
    renderScheduled = true;
    (typeof queueMicrotask === "function" ? queueMicrotask : function (f) { setTimeout(f, 0); })(function () {
      renderScheduled = false;
      render();
    });
  }
  //
  // Not before boot. Components arrive as files of their own, one after another, ahead of
  // DOMContentLoaded — and a render scheduled by the first would paint while the rest had not run,
  // reporting every one of them missing. Boot's first pass sees them all; after boot, a component
  // that arrives later (a file Drive added) still schedules its own render.
  kit.component = function (name, def) {
    blueprints[name] = def;
    if (runtimeMeta.booted) scheduleRender();
    return kitwork;
  };
  kit.state = state;
  kit.components = activeComponents;
  kit.blueprints = blueprints;

  function observeEffect(result, current) {
    if (result && typeof result.then === "function") {
      result.then(function () {
        scheduleRender();
      }, function (error) {
        current.error = error && error.message ? error.message : String(error);
        scheduleRender();
      });
    }
    return result;
  }
  function runEffect(expression, element, event, errorContext) {
    var current = elementScope(element, event, errorContext);
    return observeEffect(run(expression, current), current);
  }
  // ---- data-kit-error: the error boundary (ideaship-final §2) ----
  // A directive that fails — an action or a binding — hands its error to the nearest ancestor
  // carrying data-kit-error, whose expression runs with `$error` (cause, message, directive,
  // element). The error does not travel past that boundary; with no boundary above, or while a
  // boundary is already handling one, it reaches the console. guarded() wraps every place a
  // directive's expression runs, so one failing element never aborts the pass for the others.
  var handlingError = false;
  function reportError(error, el, directive) {
    var boundary = el && el.closest ? el.closest("[data-kit-error]") : null;
    if (boundary && !handlingError) {
      var x = programOf(boundary, "data-kit-error");
      if (x) {
        handlingError = true;
        try {
          runEffect(x, boundary, null, { cause: error, message: String(error && error.message || error), directive: directive || "", element: el });
          return;
        } catch (failure) {
          if (typeof console !== "undefined" && console.error) console.error(failure);
        } finally {
          handlingError = false;
        }
      }
    }
    if (typeof console !== "undefined" && console.error) console.error(error);
  }
  function guarded(el, directive, fn) {
    try { return fn(); } catch (error) { reportError(error, el, directive); }
  }

  // data-kit-debounce="300": coalesce a burst of a data-kit-model input's writes into one, ms after it goes quiet.
  // The pending timer lives in the element's state so cleanupTree cancels it when the actor unmounts.
  function debounceMs(el) {
    var raw = el.getAttribute("data-kit-debounce");
    var n = raw ? parseInt(raw, 10) : 0;
    return n > 0 ? n : 0;
  }
  function debounced(el, fn) {
    var ms = debounceMs(el);
    if (!ms) { fn(); return; }
    var st = state(el);
    if (st.debounceTimer) clearTimeout(st.debounceTimer);
    st.debounceTimer = setTimeout(function () { st.debounceTimer = null; fn(); }, ms);
  }
  // ---- the event family: data-kit-<event>[:modifier...]="expr" (ideaship-final §2–§4) ----
  // Dispatch is delegated at the document, one listener per event type. A modifier rides in the
  // attribute NAME, so an element's handlers are read off getAttributeNames() once and kept on the
  // element, the way the bind pass keeps its names. The author may write modifiers in any order; the
  // runtime always runs the fixed pipeline of §4:
  //   target (:window :document — listen regardless of where the event lands)
  //   → filter (:outside :escape :enter :self — a failed filter STOPS here, the event is not swallowed)
  //   → :prevent → :stop → timing (:debounce(n) :throttle(n)) → :once → run the expression.
  // data-kit-debounce stays a companion of data-kit-model only (an input coalescing its own
  // writes); for an event the delay is the :debounce(n) modifier.
  var EVENT_TYPES = ["click", "dblclick", "submit", "input", "change", "keydown", "keyup", "pointerdown", "pointerup", "focusin", "focusout"];
  // data-kit-event:<name>[:modifier…] is the long form of the family (02/10): any event by name — one
  // a component emits (data-kit-event:seen), or a DOM event outside the short list
  // (data-kit-event:paste, data-kit-event:scroll:window). data-kit-click and the rest stay its
  // shorthand. A name is lower case, as an attribute name always is once the browser has parsed it.
  var EVENT_HEAD = "data-kit-event:";
  var EVENT_NAME = /^[a-z][a-z0-9-]*$/;
  // The names a component may not emit under: the DOM events a page listens for, so each spelling
  // has one meaning. (Not every DOM event: the kernel's size budget pays for the likely ones.)
  var DOM_EVENT_NAMES = {};
  (EVENT_TYPES.join(" ") + " focus blur scroll wheel resize load error close toggle select paste copy cut contextmenu drop").split(" ").forEach(function (name) {
    DOM_EVENT_NAMES[name] = true;
  });
  var KEY_FILTER = { escape: function (e) { return e.key === "Escape" || e.key === "Esc" || e.keyCode === 27; }, enter: function (e) { return e.key === "Enter" || e.keyCode === 13; } };
  // ---- key combinations: data-kit-keydown:mod+k="open()" (ideaship-final §4) ----
  // `mod` is Control on Windows and Linux, Command on macOS — EXACTLY one of the two, so
  // Ctrl+Cmd+K is not the shortcut. Every modifier the author did NOT name must be off, which is
  // what makes a combination predictable: mod+k does not also fire on mod+shift+k, so a page can
  // give those two different meanings.
  //
  // A repeat (a held key), an IME composition and the legacy keyCode 229 are refused: they are the
  // browser assembling text, not a person pressing a shortcut. The key itself is compared
  // case-insensitively against event.key, so `mod+k` matches whatever the layout produces for K.
  var COMBO_FLAGS = { ctrl: "ctrlKey", meta: "metaKey", shift: "shiftKey", alt: "altKey" };
  function parseCombo(source) {
    var parts = source.split("+");
    var combo = { mod: false, ctrl: false, meta: false, shift: false, alt: false, key: "" };
    for (var i = 0; i < parts.length; i++) {
      var part = parts[i];
      if (part === "mod") combo.mod = true;
      else if (part in COMBO_FLAGS) combo[part] = true;
      else if (combo.key || !part) return null; // one key, and it must be there
      else combo.key = part;
    }
    return combo.key ? combo : null;
  }
  function comboMatches(combo, e) {
    if (!e || e.repeat || e.isComposing || e.keyCode === 229) return false;
    if (combo.mod) {
      if (!!e.ctrlKey === !!e.metaKey) return false; // exactly one of them
    } else {
      if (!!e.ctrlKey !== combo.ctrl || !!e.metaKey !== combo.meta) return false;
    }
    if (!!e.shiftKey !== combo.shift || !!e.altKey !== combo.alt) return false;
    var key = String(e.key || "").toLowerCase();
    if (combo.key === "escape") return key === "escape" || key === "esc" || e.keyCode === 27;
    if (combo.key === "space") return key === " " || key === "spacebar" || e.keyCode === 32;
    return key === combo.key;
  }

  var OUTSIDE_TYPES = { click: true, dblclick: true, pointerdown: true, pointerup: true, focusin: true };
  function parseHandler(attr, type, head) {
    var h = { attr: attr, type: type, target: "self", outside: false, self: false, key: "", combo: null, prevent: false, stop: false, debounce: 0, throttle: 0, once: false, valid: true };
    var mods = attr.slice((head || "data-kit-" + type).length).split(":").slice(1);
    for (var i = 0; i < mods.length; i++) {
      var m = mods[i], timing = /^(debounce|throttle)\(([0-9]+)\)$/.exec(m);
      if (m === "window" || m === "document") h.target = m;
      else if (m === "outside") h.outside = true;
      else if (m === "self") h.self = true;
      else if (m === "escape" || m === "enter") h.key = m;
      else if (m.indexOf("+") > 0 && parseCombo(m)) h.combo = parseCombo(m);
      else if (m === "prevent") h.prevent = true;
      else if (m === "stop") h.stop = true;
      else if (m === "once") h.once = true;
      else if (timing) h[timing[1]] = parseInt(timing[2], 10);
      else h.valid = false;
    }
    // A modifier that cannot apply to this event disables the handler rather than misfiring: the
    // server's verify pass already named the mistake.
    if ((h.key || h.combo) && type !== "keydown" && type !== "keyup") h.valid = false;
    if (h.key && h.combo) h.valid = false;
    if (h.outside && !OUTSIDE_TYPES[type]) h.valid = false;
    if (h.self && (h.outside || h.target !== "self")) h.valid = false;
    if (h.debounce && h.throttle) h.valid = false;
    return h;
  }
  function handlersOf(el) {
    if (el.__kitEvents) return el.__kitEvents;
    var list = [];
    if (el.getAttributeNames) {
      el.getAttributeNames().forEach(function (name) {
        if (name.indexOf("data-kit-") !== 0) return;
        if (name.indexOf(EVENT_HEAD) === 0) {
          var type = name.slice(EVENT_HEAD.length).split(":")[0];
          if (EVENT_NAME.test(type)) list.push(parseHandler(name, type, EVENT_HEAD + type));
          return;
        }
        for (var i = 0; i < EVENT_TYPES.length; i++) {
          var head = "data-kit-" + EVENT_TYPES[i];
          if (name === head || name.indexOf(head + ":") === 0) { list.push(parseHandler(name, EVENT_TYPES[i])); return; }
        }
      });
    }
    el.__kitEvents = list;
    return list;
  }
  function programOf(el, attr) {
    var raw = el.getAttribute(attr);
    if (!raw) return null;
    var key = "$" + raw;
    if (!(key in cache)) { try { cache[key] = parse(lex(raw)); } catch (e) { cache[key] = null; } }
    return cache[key];
  }
  // pipeline runs one handler for one event, filter first; returns true when :stop ends the walk.
  function pipeline(el, h, e) {
    if (!h.valid) return false;
    var st = state(el);
    if (st.once && st.once[h.attr]) return false;
    if (h.key && !KEY_FILTER[h.key](e)) return false;
    if (h.combo && !comboMatches(h.combo, e)) return false;
    if (h.self && e.target !== el) return false;
    if (h.prevent && e.preventDefault) e.preventDefault();
    if (h.stop && e.stopPropagation) e.stopPropagation();
    var program = programOf(el, h.attr);
    if (!program) return h.stop;
    var execute = function () {
      if (h.once) { (st.once || (st.once = {}))[h.attr] = true; }
      pass(function () { guarded(el, h.attr, function () { runEffect(program, el, e); }); });
      render();
    };
    if (h.debounce) {
      var timers = st.timers || (st.timers = {});
      if (timers[h.attr]) clearTimeout(timers[h.attr]);
      timers[h.attr] = setTimeout(function () { timers[h.attr] = null; execute(); }, h.debounce);
    } else if (h.throttle) {
      var last = st.lastRun || (st.lastRun = {}), now = Date.now();
      if (last[h.attr] && now - last[h.attr] < h.throttle) return h.stop;
      last[h.attr] = now;
      execute();
    } else {
      execute();
    }
    return h.stop;
  }
  // One delegated listener per event type, the short list at once and any other name the first time
  // a paint finds an element that listens for it (ensureListener, called from paint).
  var listening = {};
  function ensureListener(type) {
    if (listening[type]) return;
    listening[type] = true;
    listen(document, type, function (e) {
      // Direct handlers: the element that owns the attribute is the target or an ancestor of it.
      var el = e.target;
      while (el && el !== document) {
        var hs = handlersOf(el), stopped = false;
        for (var i = 0; i < hs.length; i++) {
          var h = hs[i];
          if (h.type !== type || h.target !== "self" || h.outside) continue;
          if (pipeline(el, h, e)) stopped = true;
        }
        if (stopped) break;
        el = el.parentElement;
      }
      // Listeners elsewhere: :window/:document run wherever the event landed; :outside runs when it
      // landed anywhere but inside the element — and not on the element the very same event mounted.
      document.querySelectorAll("*").forEach(function (owner) {
        var hs = handlersOf(owner);
        if (!hs.length) return;
        for (var i = 0; i < hs.length; i++) {
          var h = hs[i];
          if (h.type !== type) continue;
          if (h.outside) {
            if (owner === e.target || (owner.contains && owner.contains(e.target))) continue;
            if (isFresh(owner)) continue;
          } else if (h.target === "self") continue;
          pipeline(owner, h, e);
        }
      });
    });
  }
  EVENT_TYPES.forEach(ensureListener);
  // data-kit-drag="true" marks a region the OS may drag the window by — a title bar the page draws
  // itself, because app.desktop({ chrome: "html" }) opens a window with no frame of its own.
  // Primary-button press hands the drag to the OS through the private kit.window transport;
  // double-click maximises, which is what a title bar does everywhere.
  //
  // A control inside it opts out with data-kit-drag="false", so pressing a button is a press and
  // not the start of a drag. ONE attribute, two values: the old pair (data-kit-drag beside a
  // separate data-kit-no-drag) made the opt-out look like a different feature.
  //
  // All of it is a no-op on the web, where there is no bridge and no window to move.
  var DRAG = '[data-kit-drag="true"]';
  var NO_DRAG = '[data-kit-drag="false"]';
  function runWindowCommand(command) {
    try {
      var result = kit.window[command]();
      if (result && typeof result.catch === "function") result.catch(function () { });
    } catch (_) { }
  }
  function draggableTarget(e) {
    if (!e.target || !e.target.closest) return false;
    if (e.target.closest(NO_DRAG)) return false;
    return !!e.target.closest(DRAG);
  }
  listen(document, "mousedown", function (e) {
    if (e.button !== 0) return;
    if (draggableTarget(e)) runWindowCommand("drag");
  });
  listen(document, "dblclick", function (e) {
    if (!draggableTarget(e)) return;
    // A double-click TOGGLES: the second one has to put the window back, which is what every other
    // title bar does. Asking the shell is the only way to know which way to go; if it cannot answer,
    // maximising is the safer guess than doing nothing.
    var answer;
    try { answer = kit.window.isMaximized(); } catch (_) { answer = null; }
    Promise.resolve(answer).then(
      function (maximized) { runWindowCommand(maximized === true ? "restore" : "maximize"); },
      function () { runWindowCommand("maximize"); }
    );
  });
  // CAPTURE, so the model writes before any data-kit-input handler runs. Both listen on the
  // document; in bubble order the handler went first, its paint synced every model field FROM the
  // scope — which did not have the new value yet — and wiped what was typed before the model could
  // read it. A search box that also reset its page (data-kit-input="page = 1") lost every
  // keystroke. The value lands first now, so the handler and its paint see it.
  listen(document, "input", function (e) {
    var el = e.target.closest && e.target.closest(MODEL);
    if (!el) return;
    // data-kit-debounce on a model input delays the scope write + render until typing settles — the
    // final value is read inside the timer, so a search box syncs once, not once per keystroke.
    debounced(el, function () {
      pass(function () { scopeFor(el)[modelKey(el)] = modelValue(el); });
      render();
    });
  }, true);
  // Submit gate: a form holding data-state="invalid" does not submit. The kernel only READS that
  // state — the page decides who writes it (the server rendering the attribute, a component, or the
  // browser's own constraint validation reflected onto it). data-kit-validate, the directive that
  // used to write it from an expression, is gone (22/09): 0 sites wrote one, and required/pattern/
  // type do the same work natively. The server still re-checks for truth with ctx.validate, which
  // is the half nothing else can replace.
  listen(document, "submit", function (e) {
    var f = e.target;
    if (f.matches && (f.matches('[data-state="invalid"]') || f.querySelector('[data-state="invalid"]'))) {
      e.preventDefault();
    }
  }, true);

  // (data-kit-api, data-kit-live and data-kit-remember — a JSON fetch into a boundary, SSE patches,
  // page keys kept in localStorage — were optional modules plugged into hooks here. Nothing used
  // them, and they went on 02/10 with their hooks. A site that keeps something in the browser does
  // it in a component of its own.)

  // ONE observer for the whole kernel: DOM is the manifest — a removed subtree is cleaned up at once.
  //
  // REMOVED means no longer on the page. A node that was MOVED — taken out and put back somewhere
  // else, which is how morph reorders a page — appears in removedNodes too, and this callback runs a
  // tick later, after the component inside it has already been initialised in its new place. Cleaning
  // it up then took the component's own listeners while its state stayed seeded, so it never
  // initialised again: after a Drive swap the tree and the tabs on kitwork.io's tree page stopped
  // answering the keyboard (01/10). Only a node that is not connected any more is cleaned up.
  var domObserver = new MutationObserver(function (records) {
    records.forEach(function (record) {
      record.removedNodes.forEach(function (node) {
        if (!node.isConnected) cleanupTree(node);
      });
    });
  });
  domObserver.observe(document.documentElement, { childList: true, subtree: true });
  cleanup(function () { domObserver.disconnect(); });

  // ---- exports + boot ----
  // compile(src) → IR array — the SAME compiler the server runs, exposed so tools (a playground,
  // a debugger) can show the bytecode a source expression becomes. No eval; pure data out.
  // run(ir[, scope]) walks an IR tree — the walker itself, for tools and tests.
  function publicScope(s) {
    if (!s || s === scope) return scope;
    return new Proxy(s, {
      get: function (target, key) {
        if (key === "$") return target;
        return key in target ? target[key] : globalOrZero(key);
      },
      set: function (target, key, value) {
        target[key] = value;
        return true;
      }
    });
  }
  kit.compile = function (src) { return parse(lex(src)); };
  kit.run = function (ir, s) { return run(ir, publicScope(s)); };
  kit.scope = scope;
  kit.scopeFor = scopeFor;
  kit.render = render;
  kit.set = function (k, v) { pass(function () { scope[k] = v; }); render(); };
  kit.fetchWithRetry = function (url, options, retries, delay) {
    var rCount = retries !== undefined ? retries : 2;
    var rDelay = delay !== undefined ? delay : 1000;
    return fetch(url, options).catch(function (err) {
      if (err && err.name === "AbortError") throw err;
      if (rCount <= 0) throw err;
      return new Promise(function (resolve) {
        setTimeout(resolve, rDelay);
      }).then(function () {
        return kit.fetchWithRetry(url, options, rCount - 1, rDelay * 2);
      });
    });
  };
  kit.destroy = function () {
    while (globalCleanups.length) {
      try { globalCleanups.pop()(); } catch (_) { }
    }
    cleanupTree(document.documentElement);
    kit.hydrate = false;
    kit.runtime.booted = false;
    kit.runtime.loaded = false;
  };

  function initAppConfig() {
    // Two settings a page may declare, each read from the element that carries it — there is no root
    // marker any more, so nothing has to be hung off one.
    //
    // data-kit-progress="#0af" is the navigation bar's colour, published as a CSS variable rather
    // than written onto the bar, so the same value also works when set in a stylesheet.
    var painted = document.querySelector("[data-kit-progress],[data-kitwork-progress]");
    if (painted) {
      var colour = painted.getAttribute("data-kit-progress") || painted.getAttribute("data-kitwork-progress") || "";
      if (colour) document.documentElement.style.setProperty("--kitwork-progress", colour);
    }
  }

  function boot() {
    if (runtimeMeta.booted) return kitwork;
    runtimeMeta.booted = true;
    initAppConfig();
    seedModels();
    render();
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
  kit.start = boot;

  Object.defineProperty(kitwork, "internal", {
    value: {
      cleanup: cleanup,
      listen: listen,
      cleanupTree: cleanupTree,
      state: state
    },
    configurable: true,
    enumerable: false
  });
  kit.module("kernel", {
    start: boot,
    compile: kit.compile,
    run: kit.run,
    render: render
  });
  // After every swap: seed any new inputs, re-render expressions.
  listen(document, "kitwork:load", function () { seedModels(); render(); });
})();
