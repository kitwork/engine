// call.js — the browser side of router.view's bridge (28/10/2026, docs/KITWORK_BRIDGE.md).
//
// A page whose view binds a job or a var runs in the kernel as one component: the engine writes what the
// page reads as the kernel's attributes (ui/runtime.go) and the page's data once, as JSON
// (<script type="application/json" data-kitwork-page>, ui/render.go). This file puts that data in the
// page's scope, and each job there as a function the markup calls by name: remove(todo.id).
//
// A call is a POST to the page's own URL naming the job (Kitwork-Call), its body { args, vars, seq }. The
// answer is the data the job changed, never the page (work/view_call.go):
//
//   { "seq": 4, "patch": { "left": 1 }, "lists": { "todos": { "key": "id", "set": [ … ], "remove": [45] } } }
//
// patch is written into the scope name by name, a list by row, and the kernel paints what reads them.
// Calls go one at a time, in the order they were made: the next leaves when the one before has answered,
// so an answer never lands on a page newer than the one it was computed from. While a call waits or is
// out, job.pending is true, read in a row of a for for the call made from that row; job.error is the
// failed call's { code, message }, null once a call of the job is made again. A call that fails changes
// nothing, and what the person did to the element that made it (a box ticked) goes back to the data.
(function () {
  "use strict";
  var kit = window.kitwork || window.kit;
  if (!kit || !kit.scope || window.__kitworkCall) return;
  window.__kitworkCall = true;

  var page = null;   // { jobs, vars } of the page drawn now, null on a page with none
  var epoch = 0;     // the page drawn now: an answer to a page Drive has left is dropped
  var seq = 0;       // the number of the last call sent
  var queue = [];    // calls waiting for the one in flight
  var flying = null; // the call in flight
  var track = {};    // job → { pending, error, rows: { row → { pending, error } } }

  function scope() { return kit.scope.$; }
  function paint() { if (typeof kit.render === "function") kit.render(); }
  function acting() { return typeof kit.element === "function" ? kit.element() : null; }

  // the row of a for an element stands in, "" outside every row: the list's id and the row's key
  function rowOf(el) {
    var row = el && el.closest ? el.closest("[data-kitwork-item]") : null;
    return row ? row.getAttribute("data-kitwork-item") + ":" + row.getAttribute("data-kitwork-key") : "";
  }

  // job.pending and job.error, read where the kernel paints: in a row, that row's call; outside, any
  function read(name, field) {
    var t = track[name];
    if (!t) return field === "pending" ? false : null;
    var row = rowOf(acting());
    var at = row ? t.rows[row] : t;
    if (!at) return field === "pending" ? false : null;
    return field === "pending" ? at.pending > 0 : at.error;
  }

  function mark(call, on, error) {
    var t = track[call.name] || (track[call.name] = { pending: 0, error: null, rows: {} });
    var at = call.row ? (t.rows[call.row] || (t.rows[call.row] = { pending: 0, error: null })) : null;
    var step = on ? 1 : -1;
    t.pending += step;
    if (at) at.pending += step;
    if (on) {
      t.error = null;
      if (at) at.error = null;
    } else if (error) {
      t.error = error;
      if (at) at.error = error;
    }
  }

  function job(name) {
    var fn = function () {
      var el = acting();
      var call = { name: name, args: Array.prototype.slice.call(arguments), row: rowOf(el), el: el, epoch: epoch };
      mark(call, true);
      queue.push(call);
      pump();
    };
    Object.defineProperty(fn, "pending", { get: function () { return read(name, "pending"); } });
    Object.defineProperty(fn, "error", { get: function () { return read(name, "error"); } });
    return fn;
  }

  // the page's data and jobs, from what the server wrote with the page
  function seed() {
    var el = document.querySelector("[data-kitwork-page]");
    page = null;
    if (!el) return;
    var data;
    try { data = JSON.parse(el.textContent || "{}"); } catch (e) { return; }
    var s = scope();
    page = { jobs: data.jobs || [], vars: data.vars || [] };
    var state = data.state || {};
    Object.keys(state).forEach(function (k) { s[k] = state[k]; });
    page.jobs.forEach(function (name) { s[name] = job(name); });
  }

  function pump() {
    if (flying || !queue.length) return;
    var call = flying = queue.shift();
    if (call.epoch !== epoch) { flying = null; pump(); return; }
    call.seq = ++seq;
    var vars = {};
    if (page) page.vars.forEach(function (k) { vars[k] = scope()[k]; });
    send(call, { args: call.args, vars: vars, seq: call.seq }).then(function (answer) {
      settle(call, answer);
    }, function (error) {
      settle(call, { error: { code: 0, message: String(error && error.message || error) } });
    }).then(function () {
      flying = null;
      pump();
    });
  }

  // the transport: the page's own URL, the job's name in Kitwork-Call
  function send(call, body) {
    return fetch(location.pathname + location.search, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "Accept": "application/json", "Kitwork-Call": call.name },
      body: JSON.stringify(body)
    }).then(function (response) {
      if (response.redirected) return { redirect: response.url }; // a guard sent the page away
      var type = response.headers.get("Content-Type") || "";
      if (type.indexOf("application/json") < 0) return { reload: true }; // the folder answered a page
      return response.json().then(function (data) {
        if (!response.ok && !data.error) data.error = { code: response.status, message: "" };
        return data;
      });
    });
  }

  function settle(call, answer) {
    answer = answer || {};
    if (call.epoch !== epoch) return; // the page it was made on is gone
    if (answer.redirect) { location.href = answer.redirect; return; }
    if (answer.reload) { location.reload(); return; }
    if (answer.error) {
      mark(call, false, { code: answer.error.code, message: answer.error.message || "" });
      if (typeof kit.forget === "function") kit.forget(call.el && call.el.closest && call.el.closest("[data-kitwork-item]") || call.el);
      if (window.console) console.error("kitwork: việc " + call.name + " không thành", answer.error);
      paint();
      return;
    }
    var s = scope();
    var patch = answer.patch || {}, lists = answer.lists || {};
    Object.keys(patch).forEach(function (k) { s[k] = patch[k]; });
    Object.keys(lists).forEach(function (k) { s[k] = applyList(s[k], lists[k]); });
    mark(call, false, null);
    paint();
  }

  // a keyed list's change by row: the rows removed, the rows set (changed in place, or new at the end), and
  // the order when the server sent one
  function applyList(list, change) {
    var path = String(change.key || "").split(".");
    function keyOf(row) {
      var v = row;
      for (var i = 0; i < path.length; i++) v = v == null ? undefined : v[path[i]];
      return String(v);
    }
    var removed = {}, set = {}, seen = {}, out = [];
    (change.remove || []).forEach(function (k) { removed[String(k)] = true; });
    (change.set || []).forEach(function (row) { set[keyOf(row)] = row; });
    (list instanceof Array ? list : []).forEach(function (row) {
      var k = keyOf(row);
      if (removed[k] || seen[k]) return;
      seen[k] = true;
      out.push(k in set ? set[k] : row);
    });
    (change.set || []).forEach(function (row) {
      var k = keyOf(row);
      if (!seen[k]) { seen[k] = true; out.push(row); }
    });
    if (change.order instanceof Array) {
      var by = {}, ordered = [];
      out.forEach(function (row) { by[keyOf(row)] = row; });
      change.order.forEach(function (k) {
        k = String(k);
        if (by[k]) { ordered.push(by[k]); delete by[k]; }
      });
      out.forEach(function (row) { if (by[keyOf(row)]) ordered.push(row); });
      out = ordered;
    }
    return out;
  }

  // Drive leaves the page: its calls are dropped; the page it brings is seeded from its own data
  document.addEventListener("kitwork:before-swap", function () {
    epoch++;
    queue = [];
    track = {};
  });
  document.addEventListener("kitwork:load", function () {
    seed();
    paint();
  });

  seed();
})();
