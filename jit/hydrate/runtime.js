// hydrate runtime — the client half of hydrate. Served once (cached) at /jithydrate by the SAME
// engine that owns the Go compiler, so the two ends version together by construction.
// Authors write data-kit-*/data-kitwork-* SOURCE attributes and the page ships them UNCHANGED
// (readable DOM). This runtime carries a tiny parser for that source — the same grammar the Go
// side compiles for ctx.validate — plus the IR walker. It also reads data-kitwork-*-ir when
// present (optional precompiled mode); no eval, no new Function, ever.
(function () {
  "use strict";

  // ---- compile: source expression → IR (same grammar as engine/jit/hydrate/compile.go) ----
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
      if (two === "==" || two === "!=" || two === ">=" || two === "<=" || two === "&&" || two === "||") { out.push({ t: "op", v: two }); i += 2; continue; }
      if ("+-*/%<>!?:().,=".indexOf(c) >= 0) out.push({ t: "op", v: c });
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
        if (!(left instanceof Array) || left[0] !== "$") throw new Error("hydrate: bad assignment");
        return ["=", left[1], val];
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
    function postfix() {
      var e = primary();
      while (peek().v === ".") {
        next(); var name = next().v;
        if (peek().v === "(") {
          next(); var args = [];
          if (peek().v !== ")") { args.push(assign()); while (peek().v === ",") { next(); args.push(assign()); } }
          eat(")");
          e = ["()", e, name, args];
        } else e = [".", e, name];
      }
      return e;
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
      if (t.v === "(") { next(); var e = assign(); eat(")"); return e; }
      throw new Error("hydrate: unexpected " + t.v);
    }
    var node = assign();
    if (peek().t !== "eof") throw new Error("hydrate: trailing tokens");
    return node;
  }

  // ---- run: walk one IR node against the scope ----
  function run(x, s) {
    var op = x[0];
    if (op === "#") return x[1];
    if (op === "$") return s[x[1]];
    if (op === "=") { var v = run(x[2], s); s[x[1]] = v; return v; }
    if (op === "?") return run(x[1], s) ? run(x[2], s) : run(x[3], s);
    if (op === ".") { var o = run(x[1], s); return o == null ? undefined : o[x[2]]; }
    if (op === "()") { var oo = run(x[1], s), a = x[3].map(function (y) { return run(y, s); }); return oo != null && typeof oo[x[2]] === "function" ? oo[x[2]].apply(oo, a) : undefined; }
    if (op === "u!") return !run(x[1], s);
    if (op === "u-") return -run(x[1], s);
    var l = run(x[1], s), r = run(x[2], s);
    switch (op) {
      case "+": return l + r; case "-": return l - r; case "*": return l * r; case "/": return l / r; case "%": return l % r;
      case ">": return l > r; case "<": return l < r; case ">=": return l >= r; case "<=": return l <= r;
      case "==": return l == r; case "!=": return l != r; case "&&": return l && r; case "||": return l || r;
    }
  }

  // ---- directives: source attr (default) or precompiled -ir attr (optional mode) ----
  var cache = {};
  function directive(el, name) {
    var raw = el.getAttribute("data-kitwork-" + name + "-ir");
    if (raw) {
      if (!(raw in cache)) { try { cache[raw] = JSON.parse(raw); } catch (e) { cache[raw] = null; } }
      return cache[raw];
    }
    raw = el.getAttribute("data-kitwork-" + name) || el.getAttribute("data-kit-" + name);
    if (!raw) return null;
    var key = "$" + raw;
    if (!(key in cache)) { try { cache[key] = parse(lex(raw)); } catch (e) { cache[key] = null; } }
    return cache[key];
  }
  function selector(name) {
    return "[data-kitwork-" + name + "],[data-kit-" + name + "],[data-kitwork-" + name + "-ir]";
  }

  var MODEL = "[data-kitwork-model],[data-kit-model]";
  function modelKey(el) { return el.getAttribute("data-kitwork-model") || el.getAttribute("data-kit-model"); }
  function modelValue(el) { return el.type === "number" ? (parseFloat(el.value) || 0) : (el.value || ""); }

  var raw = {};
  document.querySelectorAll(MODEL).forEach(function (el) {
    var k = modelKey(el);
    if (!(k in raw)) raw[k] = modelValue(el);
  });
  var scope = new Proxy(raw, { get: function (t, k) { return k in t ? t[k] : 0; }, set: function (t, k, v) { t[k] = v; return true; } });

  function render() {
    document.querySelectorAll(selector("text")).forEach(function (el) { var x = directive(el, "text"); if (!x) return; var v = run(x, scope); el.textContent = v == null ? "" : v; });
    document.querySelectorAll(selector("show")).forEach(function (el) { var x = directive(el, "show"); if (!x) return; el.hidden = !run(x, scope); });
    // validate → state→CSS: the element carries data-state="valid|invalid"; styling is CSS's job.
    document.querySelectorAll(selector("validate")).forEach(function (el) { var x = directive(el, "validate"); if (!x) return; el.setAttribute("data-state", run(x, scope) ? "valid" : "invalid"); });
    document.querySelectorAll(MODEL).forEach(function (el) { var k = modelKey(el); if (String(scope[k]) !== el.value) el.value = scope[k]; });
  }

  document.addEventListener("click", function (e) { var el = e.target.closest(selector("click")); if (!el) return; var x = directive(el, "click"); if (!x) return; run(x, scope); render(); });
  document.addEventListener("input", function (e) { var el = e.target.closest(MODEL); if (!el) return; scope[modelKey(el)] = modelValue(el); render(); });
  // An invalid form never submits from the client; the server re-checks the SAME rule for truth.
  document.addEventListener("submit", function (e) {
    var f = e.target;
    if (f.matches && (f.matches('[data-state="invalid"]') || f.querySelector('[data-state="invalid"]'))) e.preventDefault();
  }, true);

  // ---- live: data-kit-live="<sse-url>" — the server pushes JSON scope patches ----
  // Bookkeeping by architecture: ONE EventSource per URL no matter how many elements subscribe
  // (dedup), reference-checked against the DOM, auto-closed when the last subscriber leaves
  // (morph/SPA safe — nodes carry no listeners, so nothing to unbind, nothing leaks).
  // A payload that parses as a JSON object is merged into the scope and re-rendered; anything
  // else is ignored. Reconnects are native EventSource behavior.
  var LIVE = "[data-kitwork-live],[data-kit-live]";
  var streams = {};
  function syncLive() {
    if (!window.EventSource) return;
    var want = {};
    document.querySelectorAll(LIVE).forEach(function (el) {
      var u = el.getAttribute("data-kitwork-live") || el.getAttribute("data-kit-live");
      if (u) want[u] = true;
    });
    Object.keys(want).forEach(function (u) {
      if (streams[u]) return;
      var es = new EventSource(u);
      es.onmessage = function (e) {
        var patch = null;
        try { patch = JSON.parse(e.data); } catch (err) { patch = null; }
        if (patch && typeof patch === "object" && !(patch instanceof Array)) {
          Object.keys(patch).forEach(function (k) { scope[k] = patch[k]; });
          render();
        }
      };
      streams[u] = es;
    });
    Object.keys(streams).forEach(function (u) {
      if (!want[u]) { streams[u].close(); delete streams[u]; }
    });
  }
  // ONE observer for the whole runtime: DOM is the manifest — live regions arriving or leaving
  // (morph, SPA swaps) re-reconcile subscriptions on the next tick.
  var livePending = false;
  new MutationObserver(function () {
    if (livePending) return;
    livePending = true;
    setTimeout(function () { livePending = false; syncLive(); }, 0);
  }).observe(document.documentElement, { childList: true, subtree: true });

  // server (SSE/live) can write scope then re-render — realtime with the same model.
  window.hydrate = { scope: scope, render: render, streams: streams, sync: syncLive, set: function (k, v) { scope[k] = v; render(); } };
  render();
  syncLive();
})();
