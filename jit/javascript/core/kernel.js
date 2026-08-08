// ============================================================================
// Kitwork Client Runtime — core/kernel.js (Reference Kernel, Spec 0.7)
// ============================================================================
// Single-file reference kernel per README §20 ("MAY giữ một reference kernel
// file để review semantics"). Implements the sovereign reactive core:
//   §4 seven parser modes · §5 no-eval expression language + safety
//   §6 contexts + lexical read/write ownership · §7 scope/SSR seed
//   §8 component instance/alias/ref/lifecycle · §9–§13 directives + scheduler
//   §12 events/modifiers · §14 promise observer + error pipeline · §19 codes
//
// CORNERSTONE (§5.3/§18): NO eval, NO new Function — expressions are lexed →
// parsed (per mode) → private cached AST → walked. Dangerous member paths,
// global fallback, evaluation budget and call depth are all guarded.
//
// Deferred to later milestones (§24 M4–M6, §25): kit.task, kit.request, Drive
// morph/persist, teleport/transition, full IME edge cases, capability loading.
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};
  if (kit._kernelInitialized) return;
  kit._kernelInitialized = true;
  kit.version = "0.7.0-draft";

  var doc = typeof document !== "undefined" ? document : null;

  // ---- per-element state behind a private Symbol (leak-free; dies with node)
  var STATE = (typeof Symbol === "function") ? Symbol("kit") : "__kitState";
  function state(el) { return el[STATE] || (el[STATE] = {}); }
  function peek(el) { return el[STATE] || null; }

  // ---- registries -----------------------------------------------------------
  var blueprints = {};       // component name -> blueprint
  var services = {};         // service name -> { impl, grants:{member:1} }
  var astCache = {};         // (mode|source) -> AST
  var timers = {};
  var dirtyApps = null;      // Set of app-root elements to render
  var scheduled = false;
  var rendering = false;

  // ---- diagnostics (§19) ----------------------------------------------------
  function diag(code, detail) {
    if (typeof console !== "undefined" && console.warn) console.warn("[kit] " + code, detail || "");
  }
  kit.onError = function (err, ctx) {
    ctx = ctx || {};
    var handled = false;
    if (ctx.component && typeof ctx.component.error === "function") {
      try { handled = ctx.component.error(err, ctx) === true; } catch (e) { err = e; }
    }
    if (!handled && typeof console !== "undefined" && console.error) console.error("[kit:error]", err, ctx);
    if (doc && doc.dispatchEvent) {
      try { doc.dispatchEvent(new CustomEvent("kitwork:error", { detail: { error: err, context: ctx } })); } catch (_) {}
    }
  };

  // ==========================================================================
  // 1. EXPRESSION ENGINE — Zero-Eval (Lexer → Parser → AST → Walker)  §5
  // ==========================================================================
  // Prototype-less lookup maps: an inherited Object.prototype member (constructor,
  // toString, __proto__…) MUST NOT read as a truthy hit and reject a legitimate name.
  function wordSet(str) { var o = Object.create(null); str.split(" ").forEach(function (k) { if (k) o[k] = 1; }); return o; }
  var BLOCKED = wordSet("constructor prototype __proto__ __defineGetter__ __defineSetter__ __lookupGetter__ " +
    "__lookupSetter__ ownerDocument defaultView contentWindow window globalThis top parent self");
  function blocked(k) {
    if (typeof k !== "string") return false;
    if (k === "__proto__") return true;              // literal { __proto__: } is proto-setter, not a key
    return BLOCKED[k] === 1;
  }
  var FORBIDDEN = wordSet("var let const function class return if else for while do switch case new delete void typeof " +
    "instanceof in await yield throw try catch finally import export");

  function KitParseError(msg) { this.code = "KIT_PARSE_INVALID_TOKEN"; this.message = msg; }

  function lex(src) {
    var toks = [], i = 0, n = src.length;
    function idStart(c) { return (c >= "A" && c <= "Z") || (c >= "a" && c <= "z") || c === "_" || c === "$"; }
    function idPart(c) { return idStart(c) || (c >= "0" && c <= "9"); }
    function digit(c) { return c >= "0" && c <= "9"; }
    function scanTemplate() {
      var quasis = [], exprs = [], cur = ""; i++; // skip `
      while (i < n) {
        var c = src.charAt(i);
        if (c === "`") { i++; quasis.push(cur); return { t: "tpl", quasis: quasis, exprs: exprs }; }
        if (c === "\\") { cur += src.charAt(i + 1); i += 2; continue; }
        if (c === "$" && src.charAt(i + 1) === "{") {
          quasis.push(cur); cur = ""; i += 2;
          var depth = 1, sub = "";
          while (i < n && depth > 0) {
            var d = src.charAt(i);
            if (d === "{") depth++;
            else if (d === "}") { depth--; if (depth === 0) { i++; break; } }
            sub += d; i++;
          }
          exprs.push(sub);
          continue;
        }
        cur += c; i++;
      }
      throw new KitParseError("unterminated template literal");
    }
    while (i < n) {
      var c = src.charAt(i);
      if (c === " " || c === "\t" || c === "\n" || c === "\r") { i++; continue; }
      if (c === "`") { toks.push(scanTemplate()); continue; }
      if (digit(c) || (c === "." && digit(src.charAt(i + 1)))) {
        var num = ""; while (i < n && (digit(src.charAt(i)) || src.charAt(i) === ".")) num += src.charAt(i++);
        toks.push({ t: "lit", v: parseFloat(num) }); continue;
      }
      if (c === '"' || c === "'") {
        var q = c, s = ""; i++;
        while (i < n && src.charAt(i) !== q) {
          if (src.charAt(i) === "\\") { s += src.charAt(i + 1); i += 2; }
          else s += src.charAt(i++);
        }
        if (i >= n) throw new KitParseError("unterminated string");
        i++; toks.push({ t: "lit", v: s }); continue;
      }
      if (idStart(c)) {
        var id = ""; while (i < n && idPart(src.charAt(i))) id += src.charAt(i++);
        if (FORBIDDEN[id]) throw new KitParseError("forbidden keyword in expression: " + id);
        if (id === "true") toks.push({ t: "lit", v: true });
        else if (id === "false") toks.push({ t: "lit", v: false });
        else if (id === "null") toks.push({ t: "lit", v: null });
        else if (id === "undefined") toks.push({ t: "lit", v: undefined });
        else toks.push({ t: "id", v: id });
        continue;
      }
      // multi-char operators (reject forbidden mutators + loose equality)
      var three = src.substr(i, 3), two = src.substr(i, 2);
      if (three === "===" || three === "!==") { toks.push({ t: "op", v: three }); i += 3; continue; }
      if (two === "?.") { toks.push({ t: "op", v: "?." }); i += 2; continue; }
      if (two === "??") { toks.push({ t: "op", v: "??" }); i += 2; continue; }
      if (two === "&&" || two === "||" || two === "<=" || two === ">=") { toks.push({ t: "op", v: two }); i += 2; continue; }
      if (two === "=>") throw new KitParseError("arrow functions are not allowed in markup");
      if (two === "++" || two === "--") throw new KitParseError("increment/decrement not allowed");
      if (two === "==" || two === "!=") throw new KitParseError("loose equality not supported; use === / !==");
      if (two === "+=" || two === "-=" || two === "*=" || two === "/=") throw new KitParseError("compound assignment not allowed");
      if ("+-*/%<>!?:.,()[]{}=".indexOf(c) !== -1) { toks.push({ t: "op", v: c }); i++; continue; }
      throw new KitParseError("unexpected character '" + c + "'");
    }
    toks.push({ t: "eof", v: "" });
    return toks;
  }

  function parseTokens(toks, opts) {
    opts = opts || {};
    var pos = 0;
    function cur() { return toks[pos]; }
    function is(v) { return toks[pos].t === "op" && toks[pos].v === v; }
    function eat(v) { if (is(v)) { pos++; return true; } return false; }

    function expr() { return assign(); }
    function assign() {
      var left = coalesce();
      if (is("=")) {
        if (!opts.allowAssign) throw new KitParseError("assignment not allowed in this directive");
        pos++;
        return { t: "assign", target: left, value: assign() };
      }
      return left;
    }
    function coalesce() {
      var l = ternary();
      while (is("??")) { pos++; l = { t: "coalesce", l: l, r: ternary() }; }
      return l;
    }
    function ternary() {
      var c = or();
      if (is("?")) { pos++; var a = assign(); eat(":"); var b = assign(); return { t: "cond", c: c, a: a, b: b }; }
      return c;
    }
    function or() { var l = and(); while (is("||")) { pos++; l = { t: "logic", op: "||", l: l, r: and() }; } return l; }
    function and() { var l = eq(); while (is("&&")) { pos++; l = { t: "logic", op: "&&", l: l, r: eq() }; } return l; }
    function eq() { var l = rel(); while (is("===") || is("!==")) { var o = cur().v; pos++; l = { t: "bin", op: o, l: l, r: rel() }; } return l; }
    function rel() { var l = add(); while (is("<") || is(">") || is("<=") || is(">=")) { var o = cur().v; pos++; l = { t: "bin", op: o, l: l, r: add() }; } return l; }
    function add() { var l = mul(); while (is("+") || is("-")) { var o = cur().v; pos++; l = { t: "bin", op: o, l: l, r: mul() }; } return l; }
    function mul() { var l = unary(); while (is("*") || is("/") || is("%")) { var o = cur().v; pos++; l = { t: "bin", op: o, l: l, r: unary() }; } return l; }
    function unary() { if (is("!") || is("-") || is("+")) { var o = cur().v; pos++; return { t: "unary", op: o, arg: unary() }; } return postfix(); }
    function postfix() {
      var node = primary();
      while (true) {
        if (is(".") || is("?.")) { pos++; var nm = toks[pos++].v; node = { t: "member", obj: node, name: nm, computed: false }; }
        else if (is("[")) { pos++; var ix = expr(); eat("]"); node = { t: "member", obj: node, prop: ix, computed: true }; }
        else if (is("(")) {
          pos++; var args = [];
          if (!is(")")) { args.push(assign()); while (eat(",")) args.push(assign()); }
          eat(")"); node = { t: "call", callee: node, args: args };
        } else break;
      }
      return node;
    }
    function primary() {
      var tk = cur();
      if (tk.t === "lit") { pos++; return { t: "lit", v: tk.v }; }
      if (tk.t === "tpl") { pos++; var ex = []; for (var e = 0; e < tk.exprs.length; e++) ex.push(compile(tk.exprs[e], "binding")); return { t: "tpl", quasis: tk.quasis, exprs: ex }; }
      if (tk.t === "id") { pos++; return { t: "id", name: tk.v }; }
      if (is("(")) { pos++; var ex2 = expr(); eat(")"); return ex2; }
      if (is("[")) { pos++; var items = []; if (!is("]")) { items.push(assign()); while (eat(",")) items.push(assign()); } eat("]"); return { t: "arr", items: items }; }
      if (is("{")) {
        pos++; var pairs = [];
        if (!is("}")) {
          do {
            var k = toks[pos++]; var key = k.v; eat(":");
            pairs.push({ key: String(key), val: assign() });
          } while (eat(","));
        }
        eat("}"); return { t: "obj", pairs: pairs };
      }
      throw new KitParseError("unexpected token near '" + (tk.v === undefined ? tk.t : tk.v) + "'");
    }
    var ast = expr();
    if (cur().t !== "eof") throw new KitParseError("unexpected trailing input");
    return ast;
  }

  // compile source under a mode; caches; returns AST (or an error-carrying lit node)
  function compile(src, mode) {
    if (typeof src !== "string") return { t: "lit", v: undefined };
    var key = (mode || "binding") + " " + src;
    if (astCache[key]) return astCache[key];
    var ast;
    try {
      ast = parseTokens(lex(src), { allowAssign: mode === "action" || mode === "path" });
    } catch (e) {
      diag(e && e.code || "KIT_PARSE_INVALID_TOKEN", (e && e.message) + " — in: " + src);
      ast = { t: "err" };
    }
    astCache[key] = ast;
    return ast;
  }

  // ---- evaluator (budget + call depth + blocked keys) -----------------------
  var MAX_NODES = 20000, MAX_DEPTH = 64;
  function evalTop(ast, res) {
    var b = { nodes: 0, depth: 0 };
    try { return ev(ast, res, b); }
    catch (e) { if (e && (e.code === "KIT_EVALUATION_BUDGET" || e.code === "KIT_CALL_DEPTH")) diag(e.code, e.message); return undefined; }
  }
  function ev(node, res, b) {
    if (!node) return undefined;
    if (++b.nodes > MAX_NODES) throw { code: "KIT_EVALUATION_BUDGET", message: "evaluation budget exceeded" };
    switch (node.t) {
      case "lit": return node.v;
      case "err": return undefined;
      case "id": return res.get(node.name);
      case "tpl": {
        var s = "";
        for (var i = 0; i < node.quasis.length; i++) {
          s += node.quasis[i];
          if (i < node.exprs.length) { var v = ev(node.exprs[i], res, b); s += (v == null ? "" : String(v)); }
        }
        return s;
      }
      case "arr": { var a = []; for (var j = 0; j < node.items.length; j++) a.push(ev(node.items[j], res, b)); return a; }
      case "obj": { var o = {}; for (var k = 0; k < node.pairs.length; k++) o[node.pairs[k].key] = ev(node.pairs[k].val, res, b); return o; }
      case "unary": { var u = ev(node.arg, res, b); return node.op === "!" ? !u : node.op === "-" ? -u : +u; }
      case "logic": { var lv = ev(node.l, res, b); if (node.op === "&&") return lv ? ev(node.r, res, b) : lv; return lv ? lv : ev(node.r, res, b); }
      case "coalesce": { var cv = ev(node.l, res, b); return (cv === null || cv === undefined) ? ev(node.r, res, b) : cv; }
      case "bin": return binOp(node.op, ev(node.l, res, b), ev(node.r, res, b));
      case "cond": return ev(node.c, res, b) ? ev(node.a, res, b) : ev(node.b, res, b);
      case "member": {
        var obj = ev(node.obj, res, b);
        var key = node.computed ? ev(node.prop, res, b) : node.name;
        if (obj == null || blocked(key)) return undefined;
        return obj[key];
      }
      case "call": {
        if (++b.depth > MAX_DEPTH) throw { code: "KIT_CALL_DEPTH", message: "call depth exceeded" };
        var out;
        if (node.callee.t === "member") {
          var self = ev(node.callee.obj, res, b);
          var mk = node.callee.computed ? ev(node.callee.prop, res, b) : node.callee.name;
          if (self == null || blocked(mk) || typeof self[mk] !== "function") { b.depth--; return undefined; }
          out = self[mk].apply(self, evArgs(node.args, res, b));
        } else {
          var fn = ev(node.callee, res, b);
          if (typeof fn !== "function") { b.depth--; return undefined; }
          out = fn.apply(undefined, evArgs(node.args, res, b));
        }
        b.depth--; return out;
      }
      case "assign": {
        var val = ev(node.value, res, b);
        if (node.target.t === "id") res.set(node.target.name, val);
        else if (node.target.t === "member") {
          var to = ev(node.target.obj, res, b);
          var tk = node.target.computed ? ev(node.target.prop, res, b) : node.target.name;
          if (to != null && !blocked(tk)) to[tk] = val; else diag("KIT_MODEL_NOT_WRITABLE", tk);
        }
        return val;
      }
    }
    return undefined;
  }
  function evArgs(nodes, res, b) { var out = []; for (var i = 0; i < nodes.length; i++) out.push(ev(nodes[i], res, b)); return out; }
  function binOp(op, l, r) {
    switch (op) {
      case "+": return l + r; case "-": return l - r; case "*": return l * r; case "/": return l / r; case "%": return l % r;
      case "<": return l < r; case ">": return l > r; case "<=": return l <= r; case ">=": return l >= r;
      case "===": return l === r; case "!==": return l !== r;
    }
    return undefined;
  }

  // ==========================================================================
  // 2. NAMED MAP / CLASS parsing — top-level split (§4.1 rule 3)
  // ==========================================================================
  function splitTop(str, sep) {
    var out = [], depth = 0, q = null, tpl = 0, cur = "";
    for (var i = 0; i < str.length; i++) {
      var c = str.charAt(i);
      if (q) { cur += c; if (c === q && str.charAt(i - 1) !== "\\") q = null; continue; }
      if (tpl) { cur += c; if (c === "`" && str.charAt(i - 1) !== "\\") tpl = 0; continue; }
      if (c === '"' || c === "'") { q = c; cur += c; continue; }
      if (c === "`") { tpl = 1; cur += c; continue; }
      if (c === "(" || c === "[" || c === "{") { depth++; cur += c; continue; }
      if (c === ")" || c === "]" || c === "}") { depth--; cur += c; continue; }
      if (c === sep && depth === 0) { out.push(cur); cur = ""; continue; }
      cur += c;
    }
    if (cur.trim() !== "") out.push(cur);
    return out;
  }
  // parse "key: expr; key: expr;" honoring top-level only; returns [{key, expr}]
  function parseMap(str) {
    var entries = [], stmts = splitTop(str, ";");
    for (var s = 0; s < stmts.length; s++) {
      var stmt = stmts[s].trim(); if (!stmt) continue;
      var depth = 0, q = null, tpl = 0, ci = -1;
      for (var j = 0; j < stmt.length; j++) {
        var c = stmt.charAt(j);
        if (q) { if (c === q && stmt.charAt(j - 1) !== "\\") q = null; continue; }
        if (tpl) { if (c === "`") tpl = 0; continue; }
        if (c === '"' || c === "'") { q = c; continue; }
        if (c === "`") { tpl = 1; continue; }
        if (c === "(" || c === "[" || c === "{") depth++;
        else if (c === ")" || c === "]" || c === "}") depth--;
        else if (c === ":" && depth === 0) { ci = j; break; }
      }
      if (ci === -1) { diag("KIT_PARSE_INVALID_MAP", stmt); continue; }
      var key = stmt.substring(0, ci).trim(), expr = stmt.substring(ci + 1).trim();
      if ((key.charAt(0) === "'" && key.charAt(key.length - 1) === "'") ||
        (key.charAt(0) === '"' && key.charAt(key.length - 1) === '"')) key = key.substring(1, key.length - 1);
      entries.push({ key: key, expr: expr });
    }
    return entries;
  }
  // §4.3: Class Map iff first token is a static class key AND a top-level ':'
  // precedes any top-level '?'. Otherwise Class Value Expression.
  function isClassMap(str) {
    var depth = 0, q = null, tpl = 0;
    for (var i = 0; i < str.length; i++) {
      var c = str.charAt(i);
      if (q) { if (c === q && str.charAt(i - 1) !== "\\") q = null; continue; }
      if (tpl) { if (c === "`") tpl = 0; continue; }
      if (c === '"' || c === "'") { q = c; continue; }
      if (c === "`") { tpl = 1; continue; }
      if (c === "(" || c === "[" || c === "{") depth++;
      else if (c === ")" || c === "]" || c === "}") depth--;
      else if (depth === 0 && c === "?") return false;
      else if (depth === 0 && c === ":") return true;
    }
    return false;
  }

  // ==========================================================================
  // 3. SCOPE, CONTEXTS, OWNERSHIP  §6/§7
  // ==========================================================================
  function appRootFor(el) {
    var cur = el;
    while (cur && cur.nodeType === 1) { if (cur.getAttribute && cur.getAttribute("data-kit-app") != null) return cur; cur = cur.parentNode; }
    return doc ? doc.documentElement : null;
  }
  function appScope(appEl) { var st = appEl ? state(appEl) : null; if (st && !st.scope) st.scope = {}; return st ? st.scope : {}; }
  function appAliases(appEl) { var st = appEl ? state(appEl) : null; if (st && !st.aliases) st.aliases = {}; return st ? st.aliases : {}; }
  function hostFor(el) { var cur = el; while (cur && cur.nodeType === 1) { if (cur.getAttribute && cur.getAttribute("data-kit-component") != null) return cur; cur = cur.parentNode; } return null; }
  function parentInstance(host) { if (!host || !host.parentNode) return null; var ph = hostFor(host.parentNode); return ph ? (peek(ph) && peek(ph).instance) || null : null; }

  function collectScopes(el) {
    var arr = [], cur = el;
    while (cur && cur.nodeType === 1) {
      var st = peek(cur);
      if (st) {
        if (st.loop) arr.push(st.loop);
        if (st.scope && !st.instance) arr.push(st.scope);
        if (st.instance) { if (st.scope) arr.push(st.scope); arr.push(st.instance); break; } // component boundary
      }
      if (cur.getAttribute && cur.getAttribute("data-kit-app") != null) break;
      cur = cur.parentNode;
    }
    arr.push(appScope(appRootFor(el)));
    return arr;
  }

  // curated kit surface for authored expressions (§17.1 default-deny)
  var kitPublic = (typeof Proxy === "function") ? new Proxy({}, {
    get: function (_, name) {
      if (typeof name !== "string" || blocked(name)) return undefined;
      var sv = services[name];
      if (!sv) return undefined;
      var view = sv.view || (sv.view = buildGrantView(sv));
      return view;
    },
    set: function () { return false; }, has: function (_, n) { return !!services[n]; }
  }) : services;
  function buildGrantView(sv) {
    var v = {};
    for (var m in sv.grants) if (typeof sv.impl[m] === "function") (function (mm) { v[mm] = function () { return sv.impl[mm].apply(sv.impl, arguments); }; })(m);
      else if (Object.prototype.hasOwnProperty.call(sv.grants, m)) Object.defineProperty(v, m, { get: function () { return sv.impl[m]; }, enumerable: true });
    return v;
  }

  var RESERVED = wordSet("$element $host $event $refs $component $parent $error kit");
  function makeResolver(scopes, ctx, aliases) {
    return {
      get: function (name) {
        if (name === "kit") return kitPublic;
        if (ctx && Object.prototype.hasOwnProperty.call(ctx, name)) return ctx[name];
        if (name.charAt(0) === "$" && aliases && aliases[name] !== undefined) return aliases[name];
        for (var i = 0; i < scopes.length; i++) { if (scopes[i] && name in scopes[i]) return scopes[i][name]; }
        return undefined; // §6.3: no global fallback, undefined not 0
      },
      set: function (name, value) {
        if (RESERVED[name] || (aliases && aliases[name] !== undefined)) { diag("KIT_MODEL_NOT_WRITABLE", name); return; }
        for (var i = 0; i < scopes.length; i++) { if (scopes[i] && name in scopes[i]) { scopes[i][name] = value; return; } }
        (scopes.length > 1 ? scopes[0] : appScopeFallback())[name] = value; // §6.4 create in nearest local
      }
    };
  }
  function appScopeFallback() { return doc ? appScope(doc.documentElement) : {}; }
  function contextFor(el, extra) {
    extra = extra || {};
    var host = hostFor(el);
    return {
      $element: el, $host: host,
      $event: extra.event || undefined,
      $refs: host ? ((peek(host) && peek(host).refs) || {}) : {},
      $component: host ? (peek(host) && peek(host).instance) || null : null,
      $parent: parentInstance(host),
      $error: extra.error
    };
  }
  function resolverFor(el, extra) {
    return makeResolver(collectScopes(el), contextFor(el, extra), appAliases(appRootFor(el)));
  }

  // ---- component instance (clone/seed) §7.2/§7.3 ----------------------------
  function cloneVal(v) { if (v === null || typeof v !== "object") return v; try { return JSON.parse(JSON.stringify(v)); } catch (e) { return v; } }
  function instantiate(el, name) {
    var def = blueprints[name];
    if (!def) { diag("KIT_COMPONENT_NOT_FOUND", name); return null; }
    var inst = {};
    var names = Object.getOwnPropertyNames(def);
    for (var i = 0; i < names.length; i++) {
      var k = names[i], d = Object.getOwnPropertyDescriptor(def, k);
      if (d.get || d.set) Object.defineProperty(inst, k, d);
      else if (typeof d.value === "function") inst[k] = d.value.bind(inst);
      else inst[k] = cloneVal(d.value);
    }
    var st = state(el);
    st.instance = inst; st.refs = st.refs || {};
    // runtime metadata (non-enumerable, read-only) §7.3
    var host = el;
    def_ro(inst, "$host", host); def_ro(inst, "$refs", st.refs);
    def_ro(inst, "$parent", parentInstance(host)); def_ro(inst, "$app", appRootFor(host));
    // host scope keys override blueprint defaults §7.2
    if (st.scope) for (var hk in st.scope) if (typeof inst[hk] !== "function") inst[hk] = st.scope[hk];
    return inst;
  }
  function def_ro(o, k, v) { try { Object.defineProperty(o, k, { value: v, enumerable: false, writable: false, configurable: true }); } catch (e) { o[k] = v; } }

  // register alias
  function registerAlias(el) {
    var alias = el.getAttribute("data-kit-as"); if (!alias) return;
    if (!/^\$[A-Za-z][A-Za-z0-9_]*$/.test(alias)) { diag("KIT_PARSE_INVALID_TOKEN", alias); return; }
    var aliases = appAliases(appRootFor(el));
    var st = peek(el); var inst = st && st.instance;
    if (!inst) return;
    if (aliases[alias] && aliases[alias] !== inst) { diag("KIT_DUPLICATE_ALIAS", alias); return; }
    aliases[alias] = inst; st.alias = alias;
  }

  // ==========================================================================
  // 4. DIRECTIVE RENDER  §9/§10/§13.2
  // ==========================================================================
  var HTML_BOOL = wordSet("disabled checked selected readonly required multiple hidden open autofocus autoplay controls loop muted novalidate formnovalidate itemscope default reversed");
  var BIND_DENY = wordSet("class style srcdoc");
  var URL_ATTRS = wordSet("href src action formaction poster xlink:href");

  function applyBindAttr(el, key, val) {
    if (key.indexOf("on") === 0 || BIND_DENY[key] || key.indexOf("data-kit") === 0) { diag("KIT_BIND_UNSAFE_ATTRIBUTE", key); return; }
    if (URL_ATTRS[key] && typeof val === "string") { var low = val.replace(/[\s -]/g, "").toLowerCase(); if (low.indexOf("javascript:") === 0 || low.indexOf("vbscript:") === 0) { diag("KIT_BIND_UNSAFE_ATTRIBUTE", key); return; } }
    var isData = key.indexOf("data-") === 0, isAria = key.indexOf("aria-") === 0;
    if (val === null || val === undefined) { el.removeAttribute(key); return; }
    if (val === false) { if (isData || isAria) el.setAttribute(key, "false"); else el.removeAttribute(key); return; }
    if (val === true) { if (isData || isAria) el.setAttribute(key, "true"); else if (HTML_BOOL[key]) el.setAttribute(key, ""); else el.setAttribute(key, "true"); return; }
    el.setAttribute(key, String(val));
  }

  function classSet(el, res) {
    var src = el.getAttribute("data-kit-class");
    var desired = {};
    if (isClassMap(src)) {
      var entries = parseMap(src);
      for (var i = 0; i < entries.length; i++) {
        if (evalTop(compile(entries[i].expr, "binding"), res)) {
          entries[i].key.split(/\s+/).forEach(function (t) { if (t) desired[t] = 1; });
        }
      }
    } else {
      collectClasses(evalTop(compile(src, "binding"), res), desired);
    }
    var st = state(el), prev = st.classes || {};
    for (var p in prev) if (!desired[p]) el.classList.remove(p);
    for (var d in desired) el.classList.add(d);
    st.classes = desired;
  }
  function collectClasses(r, out) {
    if (r == null || r === false) return;
    if (typeof r === "string") { r.split(/\s+/).forEach(function (t) { if (t) out[t] = 1; }); return; }
    if (r instanceof Array) { for (var i = 0; i < r.length; i++) collectClasses(r[i], out); return; }
    if (typeof r === "object") for (var k in r) if (r[k]) out[k] = 1;
  }

  // form model
  function readControl(el) {
    var type = (el.type || "").toLowerCase();
    if (el.tagName === "SELECT" && el.multiple) { var vs = []; for (var i = 0; i < el.options.length; i++) if (el.options[i].selected) vs.push(el.options[i].value); return vs; }
    if (type === "checkbox") return !!el.checked;
    if (type === "number" || type === "range") { var num = parseFloat(el.value); return el.value === "" || isNaN(num) ? null : num; }
    return el.value;
  }
  function writeControl(el, v) {
    var type = (el.type || "").toLowerCase();
    if (type === "checkbox") { el.checked = !!v; return; }
    if (type === "radio") { el.checked = (el.value === String(v)); return; }
    var s = v == null ? "" : String(v); if (el.value !== s) el.value = s;
  }
  function writeModel(el) {
    var ast = compile(el.getAttribute("data-kit-model"), "path");
    var res = resolverFor(el, {}), v = readControl(el);
    if (ast.t === "id") res.set(ast.name, v);
    else if (ast.t === "member") { var o = evalTop(ast.obj, res); var k = ast.computed ? evalTop(ast.prop, res) : ast.name; if (o != null && !blocked(k)) o[k] = v; else diag("KIT_MODEL_NOT_WRITABLE", el.getAttribute("data-kit-model")); }
    else diag("KIT_MODEL_NOT_WRITABLE", el.getAttribute("data-kit-model"));
    invalidate(el);
  }

  // structural: for
  function renderFor(tpl, res) {
    var spec = tpl.getAttribute("data-kit-for");
    var m = /^\s*(\$[A-Za-z_][\w]*)\s*(?:,\s*(\$[A-Za-z_][\w]*))?\s+of\s+([\s\S]+)$/.exec(spec);
    if (!m) { diag("KIT_PARSE_INVALID_TOKEN", spec); return; }
    var itemVar = m[1], idxVar = m[2] || "$index", items = evalTop(compile(m[3], "binding"), res);
    if (!(items instanceof Array)) items = [];
    var st = state(tpl);
    if (!st.forInit) { st.forInit = true; tpl.style.display = "none"; st.clones = []; st.keys = []; }
    var keyExpr = tpl.getAttribute("data-kit-key");
    var clones = st.clones, oldKeys = st.keys, newKeys = [];
    // simple keyed-by-value/index reconcile (move-preserving via key match)
    var byKey = {};
    for (var o = 0; o < oldKeys.length; o++) byKey[oldKeys[o]] = clones[o];
    var next = [], anchor = tpl;
    for (var i = 0; i < items.length; i++) {
      var loop = {}; loop[itemVar] = items[i]; loop[idxVar] = i;
      var key = keyExpr ? evalTop(compile(keyExpr, "binding"), makeResolver([loop].concat(collectScopes(tpl)), contextFor(tpl, {}), appAliases(appRootFor(tpl)))) : ("#" + i);
      key = "" + key;
      if (newKeys.indexOf(key) !== -1) { diag("KIT_DUPLICATE_KEY", key); return; }
      newKeys.push(key);
      var clone = byKey[key];
      if (!clone) { clone = tpl.cloneNode(true); clone.removeAttribute("data-kit-for"); clone.removeAttribute("data-kit-key"); clone.style.display = ""; state(clone).isClone = true; }
      else { delete byKey[key]; }
      state(clone).loop = loop;
      if (clone.previousSibling !== anchor) tpl.parentNode.insertBefore(clone, anchor.nextSibling);
      anchor = clone;
      next.push(clone);
    }
    for (var g in byKey) if (byKey[g] && byKey[g].parentNode) { cleanupTree(byKey[g]); byKey[g].parentNode.removeChild(byKey[g]); }
    st.clones = next; st.keys = newKeys;
    for (var r = 0; r < next.length; r++) evaluateNode(next[r]);
  }

  function evaluateNode(el) {
    if (!el || el.nodeType !== 1) return;

    // component instance
    var compName = el.getAttribute("data-kit-component");
    if (compName != null && !(peek(el) && peek(el).instance)) {
      // seed host scope first (§7.2), then instance
      seedScope(el);
      if (blueprints[compName]) { instantiate(el, compName); registerAlias(el); if (peek(el).instance && typeof peek(el).instance.mount === "function") queueMount(el); }
    } else {
      seedScope(el);
    }

    // for (template)
    var forAttr = el.getAttribute("data-kit-for");
    if (forAttr != null && !(peek(el) && peek(el).isClone)) { renderFor(el, resolverFor(el, {})); return; }

    var res = resolverFor(el, {});

    // if (structural — v1: hidden + skip subtree; full unmount is Milestone 3)
    var ifAttr = el.getAttribute("data-kit-if");
    if (ifAttr != null) { var on = !!evalTop(compile(ifAttr, "binding"), res); el.hidden = !on; if (!on) return; }

    var attrs = el.attributes;
    if (attrs) {
      for (var a = 0; a < attrs.length; a++) {
        var name = attrs[a].name, val = attrs[a].value;
        if (name.indexOf("data-kit-") !== 0) continue;
        var dir = name.substring(9);
        if (dir === "text") {
          var tv = evalTop(compile(val, "binding"), res);
          if (tv && typeof tv.then === "function") diag("KIT_ASYNC_BINDING", "data-kit-text");
          else if (tv !== null && tv !== undefined && (typeof tv === "object")) diag("KIT_TEXT_NON_SCALAR", "");
          else { var s = (tv == null) ? "" : String(tv); if (el.textContent !== s) el.textContent = s; }
        } else if (dir === "show") { el.hidden = !evalTop(compile(val, "binding"), res); }
        else if (dir === "class") { classSet(el, res); }
        else if (dir === "style") { var sp = parseMap(val); for (var si = 0; si < sp.length; si++) { var cv = evalTop(compile(sp[si].expr, "binding"), res); if (cv == null || cv === false) el.style.removeProperty(sp[si].key); else el.style.setProperty(sp[si].key, String(cv)); } }
        else if (dir === "bind") { var bp = parseMap(val); for (var bi = 0; bi < bp.length; bi++) applyBindAttr(el, bp[bi].key, evalTop(compile(bp[bi].expr, "binding"), res)); }
        else if (dir === "model") { writeControl(el, evalTop(compile(val, "path"), res)); }
      }
    }

    // recurse — but do NOT cross into a nested component's internals here (§13.2);
    // nested components hydrate on their own pass. For v1 we recurse uniformly
    // (each component seeds once); refined boundary rendering is Milestone 3.
    var child = el.firstElementChild;
    while (child) { evaluateNode(child); child = child.nextElementSibling; }
  }

  function seedScope(el) {
    var raw = el.getAttribute("data-kit-scope"); if (raw == null) return;
    var st = state(el); if (st.scopeInit) return;
    st.scopeInit = true; st.scope = st.scope || {};
    var entries = parseMap(raw);
    // sequential seed: later entries see earlier ones + parent context (§7.1)
    var res = makeResolver([st.scope].concat(collectScopes(el.parentNode || el)), contextFor(el, {}), appAliases(appRootFor(el)));
    for (var i = 0; i < entries.length; i++) {
      var k = entries[i].key;
      if (k.charAt(0) === "$" || k === "kit") { diag("KIT_SCOPE_KEY_RESERVED", k); continue; }
      st.scope[k] = evalTop(compile(entries[i].expr, "binding"), res);
    }
  }

  function scanRefs(root) {
    var refs = (root || doc).querySelectorAll("[data-kit-ref]");
    for (var i = 0; i < refs.length; i++) {
      var el = refs[i], nm = el.getAttribute("data-kit-ref"), host = hostFor(el);
      if (nm && host && peek(host)) { var reg = peek(host).refs || (peek(host).refs = {}); reg[nm] = el; }
    }
  }

  function cleanupTree(el) {
    if (!el || el.nodeType !== 1) return;
    var child = el.firstElementChild; while (child) { cleanupTree(child); child = child.nextElementSibling; }
    var st = peek(el); if (!st) return;
    if (st.instance) {
      try { if (typeof st.instance.unmount === "function") st.instance.unmount(); } catch (e) { kit.onError(e, { component: st.instance, element: el }); }
      if (st.alias) { var aliases = appAliases(appRootFor(el)); if (aliases[st.alias] === st.instance) delete aliases[st.alias]; }
    }
    el[STATE] = undefined;
  }

  // ==========================================================================
  // 5. EVENTS + MODIFIERS + PROMISE OBSERVER  §12/§14
  // ==========================================================================
  var pending = {};
  function runAction(evt, el, program, mods) {
    if (mods.indexOf("enter") !== -1 && evt.key !== "Enter") return;
    if (mods.indexOf("escape") !== -1 && evt.key !== "Escape") return;
    if (mods.indexOf("outside") !== -1) { if (el.contains(evt.target)) return; var fm = peek(el); if (fm && fm.freshMount) return; }
    if (mods.indexOf("prevent") !== -1) evt.preventDefault();
    if (mods.indexOf("stop") !== -1) evt.stopPropagation();

    if (mods.indexOf("once") !== -1) { var os = state(el); if (os.once && os.once[program]) return; (os.once = os.once || {})[program] = true; }

    var key = state(el).eid || (state(el).eid = "e" + Math.random().toString(36).slice(2));
    var run = function () {
      var res = resolverFor(el, { event: evt });
      var stmts = splitTop(program, ";"), last;
      for (var i = 0; i < stmts.length; i++) { var s = stmts[i].trim(); if (s) last = evalTop(compile(s, "action"), res); }
      if (last && typeof last.then === "function") {
        pending[key] = (pending[key] || 0) + 1; el.setAttribute("data-busy", "true"); el.setAttribute("aria-busy", "true");
        var settle = function () { pending[key] = Math.max(0, (pending[key] || 1) - 1); if (pending[key] === 0) { el.removeAttribute("data-busy"); el.removeAttribute("aria-busy"); } invalidate(el); };
        last.then(settle, function (err) { settle(); if (!(err && err.name === "AbortError")) kit.onError(err, { element: el }); });
      } else invalidate(el);
    };
    var deb = 0, thr = 0;
    for (var i = 0; i < mods.length; i++) { var mm = mods[i]; if (mm.indexOf("debounce(") === 0) deb = parseInt(mm.slice(9, -1), 10) || 300; else if (mm.indexOf("throttle(") === 0) thr = parseInt(mm.slice(9, -1), 10) || 300; }
    if (deb > 0) { if (timers[key]) clearTimeout(timers[key]); timers[key] = setTimeout(run, deb); }
    else if (thr > 0) { var now = Date.now(); if (!timers["t" + key] || now - timers["t" + key] >= thr) { timers["t" + key] = now; run(); } }
    else run();
  }
  function delegate(type) {
    doc.addEventListener(type, function (evt) {
      if ((type === "input" || type === "change") && evt.target.getAttribute && evt.target.getAttribute("data-kit-model") != null) writeModel(evt.target);
      var el = evt.target;
      while (el && el !== doc) {
        var attrs = el.attributes;
        if (attrs) for (var a = 0; a < attrs.length; a++) {
          var an = attrs[a].name; if (an.indexOf("data-kit-") !== 0) continue;
          var dir = an.substring(9);
          if (dir === type || dir.indexOf(type + ":") === 0) runAction(evt, el, attrs[a].value, dir.split(":").slice(1));
        }
        el = el.parentNode;
      }
    }, true);
    // :document / :window / :outside listeners for this type
    doc.addEventListener(type, function (evt) {
      var sel = "[data-kit-" + type + "\\:outside],[data-kit-" + type + "\\:document],[data-kit-" + type + "\\:escape\\:document]";
      var nodes; try { nodes = doc.querySelectorAll(sel); } catch (e) { return; }
      for (var i = 0; i < nodes.length; i++) {
        var el = nodes[i], attrs = el.attributes;
        for (var a = 0; a < attrs.length; a++) {
          var an = attrs[a].name; if (an.indexOf("data-kit-" + type + ":") !== 0) continue;
          var mods = an.substring(9).split(":").slice(1);
          if (mods.indexOf("document") !== -1 || mods.indexOf("outside") !== -1) runAction(evt, el, attrs[a].value, mods);
        }
      }
    }, true);
  }

  // ==========================================================================
  // 6. SCHEDULER + BOOT  §13
  // ==========================================================================
  function invalidate(el) { dirtyApps = dirtyApps || {}; var app = appRootFor(el); if (app) { dirtyApps[""] = app; } schedule(); }
  function schedule() {
    if (scheduled) return; scheduled = true;
    var raf = (window.requestAnimationFrame) ? window.requestAnimationFrame.bind(window) : function (cb) { setTimeout(cb, 16); };
    // batch at least once per turn: microtask nudge + rAF paint (§13.1)
    var run = function () { scheduled = false; render(); };
    if (typeof queueMicrotask === "function") queueMicrotask(run); else raf(run);
  }
  function render() {
    if (!doc || !doc.body || rendering) return;
    rendering = true;
    try { scanRefs(doc.body); evaluateNode(doc.documentElement); flushMounts(); }
    finally { rendering = false; }
  }
  kit.render = render;

  var mountQueue = [];
  function queueMount(el) { mountQueue.push(el); }
  function flushMounts() {
    var q = mountQueue; mountQueue = [];
    for (var i = 0; i < q.length; i++) {
      var st = peek(q[i]); if (!st || !st.instance || st.mounted) continue; st.mounted = true;
      // fresh-mount marker so a mounting click can't self-close via :outside (§12.4)
      st.freshMount = true; (function (s) { var clear = function () { s.freshMount = false; }; (typeof queueMicrotask === "function") ? queueMicrotask(clear) : setTimeout(clear, 0); })(st);
      try { var c = st.instance.mount(); if (typeof c === "function") st.cleanup = c; } catch (e) { kit.onError(e, { component: st.instance, element: q[i] }); }
    }
  }

  function setupObserver() {
    if (typeof MutationObserver === "undefined" || !doc.body) return;
    new MutationObserver(function (muts) {
      if (rendering) return;
      for (var i = 0; i < muts.length; i++) if (muts[i].addedNodes && muts[i].addedNodes.length) { schedule(); return; }
    }).observe(doc.body, { childList: true, subtree: true });
  }

  // ==========================================================================
  // 7. PUBLIC API
  // ==========================================================================
  kit.component = function (name, def) { if (!name) return; if (!def) return blueprints[name]; blueprints[name] = def; schedule(); return def; };
  kit.service = function (name, impl, opts) {
    opts = opts || {}; var grants = {}; (opts.expression || []).forEach(function (m) { grants[m] = 1; });
    services[name] = { impl: impl, grants: grants }; kit[name] = impl; return impl;
  };
  // test/introspection hooks (no DOM) — used by conformance fixtures
  kit._compile = compile;
  kit._evaluate = function (src, scopeObj, extra) {
    var res = makeResolver([scopeObj || {}], { $element: null, $host: null, $event: (extra && extra.event), $refs: {}, $component: (extra && extra.component) || null, $parent: null }, {});
    return evalTop(compile(src, (extra && extra.mode) || "action"), res);
  };
  kit.boot = function () { render(); };

  if (doc) {
    ["click", "dblclick", "keydown", "keyup", "submit", "input", "change"].forEach(delegate);
    if (doc.readyState === "loading") doc.addEventListener("DOMContentLoaded", function () { setupObserver(); render(); });
    else setTimeout(function () { setupObserver(); render(); }, 0);
  }

})(typeof window !== "undefined" ? window : globalThis);
