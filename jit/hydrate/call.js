// call.js — the browser side of router.view's bridge (28/10/2026, docs/KITWORK_BRIDGE.md).
//
// A page whose view binds a job or a var runs in the browser as one component, the instance "page". The server
// writes the page's data once, as JSON (<script type="application/json" data-kitwork-page>, ui/page_block.go),
// each instance's data under its place, the page's under "page"; this file puts the page's in the page's state,
// and each job there as a function the markup calls by name: remove(todo.id).
//
// Most such pages have a program (ui/page_program.go, /_kitwork/page.<hash>.js): each element whose values
// follow the data carries one mark, data-kitwork-mark="n", and the program says what mark n writes, from which
// expression and which names of the page it reads. A name that changes runs again only what reads it; a
// list changes by row; a row whose item is the same object is left alone. A page with no program (one that
// holds a component of Kitwork) is painted by the kernel from its data-kitwork-* attributes, as before.
//
// A call is a POST to the page's own URL naming the job (?kitwork-call=remove), its body { args, vars, seq }. The
// answer is the data the job changed, never the page (work/view_call.go):
//
//   { "seq": 4, "patch": { "left": 1 }, "lists": { "todos": { "key": "id", "set": [ … ], "remove": [45] } } }
//
// patch is written into the state name by name, a list by row. Calls go one at a time, in the order they
// were made: the next leaves when the one before has answered, so an answer never lands on a page newer
// than the one it was computed from. A call made as a field is typed in waits for a pause in the typing, and
// the next keystroke's call to the same job takes its place: a word typed in a search is one call, not one a
// letter; it takes the place, too, of one typed before that still waits for the call out. While a call waits
// or is out, job.pending is true, read in a row of a
// list for the call made from that row; job.error is the failed call's { code, message }, null once a call
// of the job is made again. A call that fails changes nothing, and what the person did to the element that
// made it (a box ticked) goes back to the data.
//
// A component of the site whose jobs go through the bridge (ui/component_live.go) is registered with the
// kernel from the same block: the values its source writes out (bridge.defaults), and each job a function that
// calls shop-cart.checkout on the instance it runs in. That call names the place the instance
// is called from (data-kitwork-at on its host) and carries the instance's vars in scope, and the revisions of
// its data the tab holds in scopeRevs, as the page's in revs (the block brings them by place); its answer holds
// the data of that instance the job changed, under the same place, written into the instance, and the new
// revisions; a name the tab held at another revision than the job ran on comes whole (a search's results, read
// through the query the field set here):
//
//   { "seq": 5, "components": { "c1": { "patch": { "items": [], "done": true }, "revs": { "total": "9c0e…" } } } }
(function () {
  "use strict";
  var kitwork = window.kitwork;
  if (!kitwork || !kitwork.scope || window.__kitworkCall) return;
  window.__kitworkCall = true;

  var page = null;   // { jobs, vars } of the page drawn now, null on a page with none
  var view = null;   // the program running the page drawn now, null on a page the kernel paints
  var epoch = 0;     // the page drawn now: an answer to a page Drive has left is dropped
  var seq = 0;       // the number of the last call sent
  var queue = [];    // calls waiting for the one in flight
  var typed = [];    // calls made as a field is typed in, waiting for a pause in the typing
  var handling = null; // the event whose handler the program runs now
  var flying = null; // the call in flight
  var track = {};    // job → { pending, error, rows: { row → { pending, error } } }
  var current = null; // the element whose binding or event the program runs now
  var revs = {};      // the revision of each name of the page's data it holds, sent with each call
  var scopeRevs = {}; // the same of each instance of a site component, by place: sent with a call of its jobs
  var stamp = 0;      // the moment the server read the data the page holds: no data read before it is taken
  var told = 0;       // the newest stamp the server told while the page listens, not asked about yet

  function acting() { return current || (typeof kitwork.element === "function" ? kitwork.element() : null); }
  // the event a call is made in, while it is dispatched: the program's, or the kernel's on a page it paints
  function dispatching() { return handling || (typeof kitwork.event === "function" ? kitwork.event() : null); }
  function get(name) { return view ? view.state[name] : kitwork.scope.$[name]; }
  function put(name, value) {
    if (view) view.set(name, value);
    else kitwork.scope.$[name] = value;
  }
  function paint() {
    if (view) view.flush();
    else if (typeof kitwork.render === "function") kitwork.render();
  }

  // the row of a list an element stands in, "" outside every row: the list's id and the row's key
  function rowOf(el) {
    var row = el && el.closest ? el.closest("[data-kitwork-item]") : null;
    return row ? row.getAttribute("data-kitwork-item") + ":" + row.getAttribute("data-kitwork-key") : "";
  }

  // job.pending and job.error, read where they are painted: in a row, that row's call; outside, any
  function read(name, field) {
    var t = track[name];
    if (!t) return field === "pending" ? false : null;
    var row = rowOf(acting());
    var at = row ? t.rows[row] : t;
    if (!at) return field === "pending" ? false : null;
    return field === "pending" ? at.pending > 0 : at.error;
  }

  function mark(call, on, error) {
    var key = call.at ? call.name + "@" + call.at : call.name;
    var t = track[key] || (track[key] = { pending: 0, error: null, rows: {} });
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
    if (view) view.touch(call.name, call.row);
  }

  function job(name) {
    var fn = function () {
      var el = acting();
      make({ name: name, args: Array.prototype.slice.call(arguments), row: rowOf(el), el: el, epoch: epoch });
    };
    Object.defineProperty(fn, "pending", { get: function () { return read(name, "pending"); } });
    Object.defineProperty(fn, "error", { get: function () { return read(name, "error"); } });
    return fn;
  }

  // the host of the component instance a scope belongs to, null outside every one
  function hostOf(self) {
    try { return kitwork.context(self).host; } catch (e) { return null; }
  }
  // the place the component host around an element is called from, "" outside every one
  function atOf(el) {
    var host = el && el.closest ? el.closest("[data-kitwork-at]") : null;
    return host ? host.getAttribute("data-kitwork-at") : "";
  }
  function instanceOf(host) {
    var st = host && typeof kitwork.state === "function" ? kitwork.state(host) : null;
    return st && st.scope || null;
  }

  // a job of a site component: called on the instance whose scope it runs in; .pending and .error are that
  // instance's, read where the kernel paints
  function componentJob(component, name, vars) {
    var wire = component + "." + name;
    var fn = function () {
      var host = hostOf(this), at = host ? host.getAttribute("data-kitwork-at") : "";
      if (!at) {
        if (window.console) console.error("kitwork: việc " + wire + " gọi ngoài một instance của " + component);
        return;
      }
      var el = acting();
      make({ name: wire, at: at, host: host, vars: vars, args: Array.prototype.slice.call(arguments), row: rowOf(el), el: el, epoch: epoch });
    };
    Object.defineProperty(fn, "pending", { get: function () { return read(wire + "@" + atOf(acting()), "pending"); } });
    Object.defineProperty(fn, "error", { get: function () { return read(wire + "@" + atOf(acting()), "error"); } });
    return fn;
  }

  // the page's data block (ui/page_block.go): one, for the page and every component of the site it holds that the
  // browser runs, each instance's data under its place
  function block() {
    var el = document.querySelector("[data-kitwork-page]");
    if (!el) return null;
    try { return JSON.parse(el.textContent || "{}"); } catch (e) { return null; }
  }

  // the site components the page holds that the browser runs, registered with the kernel: each with the values
  // its source writes out and its jobs; the revisions of each instance's data, by place
  function seedComponents() {
    scopeRevs = {};
    var data = block();
    if (!data) return;
    var bridge = data.bridge || {}, defaults = bridge.defaults || {}, jobs = data.jobs || {}, vars = data.vars || {};
    Object.keys(data.components || {}).forEach(function (at) {
      if (bridge.revs && bridge.revs[at]) scopeRevs[at] = bridge.revs[at];
    });
    Object.keys(defaults).forEach(function (component) {
      var def = {}, state = defaults[component] || {};
      Object.keys(state).forEach(function (k) { def[k] = state[k]; });
      (jobs[component] || []).forEach(function (name) { def[name] = componentJob(component, name, vars[component] || []); });
      kitwork.component(component, def);
    });
  }

  // the page's data and jobs, the instance "page" of the block, and its program when it has one
  function seed() {
    var data = block();
    page = null;
    view = null;
    if (!data || !data.page) return;
    var bridge = data.bridge || {};
    page = { jobs: (data.jobs || {}).page || [], vars: (data.vars || {}).page || [], program: bridge.program || "", tickets: bridge.tickets || [] };
    ticketsAt = Date.now();
    revs = (bridge.revs || {}).page || {};
    stamp = bridge.stamp || 0;
    var state = {};
    Object.keys(data.page).forEach(function (k) { state[k] = data.page[k]; });
    page.jobs.forEach(function (name) { state[name] = job(name); });
    var program = page.program && kitwork.pages && kitwork.pages[page.program];
    if (program) {
      view = makeView(program, state, page.jobs);
      return;
    }
    if (page.program) return; // its program has not arrived yet: kitwork.pageReady seeds again when it does
    var s = kitwork.scope.$;
    Object.keys(state).forEach(function (k) { s[k] = state[k]; });
  }
  // the program running the page drawn now, for a test and the console
  kitwork.page = { view: function () { return view; } };
  // a page's program arrived after this file ran: the page drawn now may be its
  kitwork.pageReady = function (id) {
    if (!view && page && page.program === id) {
      seed();
      paint();
    }
  };

  // A call made as a field is typed in (in its input event) waits TYPING_PAUSE for the typing to stop: the call
  // the next keystroke makes in the same field, to the same job, takes its place. A call made any other way takes
  // those waiting with it, them first, so calls still leave in the order they were made: typing then pressing a
  // button sends what was typed, then the button's. A box ticked or a choice made fires input too, and is not
  // typing: each such call goes as it is made.
  var TYPING_PAUSE = 200;
  function make(call) {
    mark(call, true);
    var e = dispatching();
    if (e && e.type === "input" && typedIn(e.target)) {
      call.field = e.target;
      typed = typed.filter(function (waiting) {
        if (!sameField(waiting, call)) return true;
        clearTimeout(waiting.timer);
        mark(waiting, false, null);
        return false;
      });
      call.typed = true;
      call.timer = setTimeout(function () { release(call); }, TYPING_PAUSE);
      typed.push(call);
      return;
    }
    release(typed[typed.length - 1]);
    queue.push(call);
    pump();
  }
  // a call waiting for a pause in the typing goes, and those made before it, in order. It takes the place of a
  // call typed in the same field, to the same job, that waits in the queue for the call out (a slow search):
  // what was typed last stands for what was typed before, and is the one that goes next.
  function release(call) {
    var at = typed.indexOf(call);
    if (at < 0) return;
    typed.splice(0, at + 1).forEach(function (waiting) {
      clearTimeout(waiting.timer);
      queue = queue.filter(function (queued) {
        if (!queued.typed || !sameField(queued, waiting)) return true;
        mark(queued, false, null);
        return false;
      });
      queue.push(waiting);
    });
    pump();
  }
  function sameField(a, b) { return a.name === b.name && (a.at || "") === (b.at || "") && a.field === b.field; }
  // a field one types in: a textarea, an element one edits, an input but a box, a choice, a file or a button
  var NOT_TYPED = { checkbox: true, radio: true, file: true, color: true, button: true, submit: true, reset: true, image: true };
  function typedIn(el) {
    if (!el || el.nodeType !== 1) return false;
    if (el.isContentEditable || el.tagName === "TEXTAREA") return true;
    return el.tagName === "INPUT" && !NOT_TYPED[String(el.type || "text").toLowerCase()];
  }

  function pump() {
    if (flying || !queue.length) return;
    var call = flying = queue.shift();
    if (call.epoch !== epoch) { flying = null; pump(); return; }
    if (call.refresh) {
      // a refresh asks for all that changed till now, what the server told included; one asked for only
      // because the server told is left when the page already holds data as new
      if (call.told && told <= stamp) { told = 0; flying = null; pump(); return; }
      told = 0;
    }
    call.seq = ++seq;
    var vars = {};
    if (page) page.vars.forEach(function (k) { vars[k] = get(k); });
    // a refresh runs the page with the vars the tab holds, as a job does: data a var reads (a search's query)
    // comes as the tab shows it
    var body = call.refresh ? { vars: vars, seq: call.seq, revs: revs } : { args: call.args, vars: vars, seq: call.seq, revs: revs };
    // the keys its tickets are for, while they are new: the answer brings tickets only when they are not those
    if (tickets().length && Date.now() - ticketsAt < 5 * 60 * 1000) body.marks = marksOf(tickets());
    if (call.at) {
      // the instance's vars, read as the call leaves; its inputs never go: the server reads them where it stands
      var own = {}, instance = instanceOf(call.host) || {};
      call.vars.forEach(function (k) { own[k] = instance[k]; });
      body.at = call.at;
      body.scope = own;
      if (scopeRevs[call.at]) body.scopeRevs = scopeRevs[call.at];
    }
    send(call, body).then(function (answer) {
      settle(call, answer);
    }, function (error) {
      settle(call, { error: { code: 0, message: String(error && error.message || error) } });
    }).then(function () {
      flying = null;
      pump();
    }, function (error) {
      flying = null;
      if (window.console) console.error("kitwork: áp câu trả lời của " + (call.refresh ? "lần lấy lại" : "việc " + call.name) + " không thành", error);
      pump();
    });
  }

  // the transport: the page's own URL naming the job (kitwork-call) or the ask for what changed
  // (kitwork-refresh), and no header of Kitwork's own: the shell of a native app passes a request's method,
  // URL and body as a browser does, and not always its headers
  function send(call, body) {
    var named = call.refresh ? "kitwork-refresh=1" : "kitwork-call=" + encodeURIComponent(call.name);
    return fetch(location.pathname + (location.search ? location.search + "&" : "?") + named, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
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
    if (answer.error && call.refresh) {
      if (window.console) console.error("kitwork: lấy lại dữ liệu của trang không thành", answer.error);
      return;
    }
    if (answer.error) {
      mark(call, false, { code: answer.error.code, message: answer.error.message || "" });
      var back = call.el && call.el.closest && call.el.closest("[data-kitwork-item]") || call.el;
      if (view) view.forget(back);
      else if (typeof kitwork.forget === "function") kitwork.forget(back);
      if (window.console) console.error("kitwork: việc " + call.name + " không thành", answer.error);
      paint();
      return;
    }
    var patch = answer.patch || {}, lists = answer.lists || {}, held = answer.revs || {};
    if (answer.stamp && answer.stamp <= stamp) {
      // another tab told this page data read after this answer's: the data it holds is newer, and of the
      // answer only the page's own vars are taken
      var own = {};
      if (page) page.vars.forEach(function (k) { if (Object.prototype.hasOwnProperty.call(patch, k)) own[k] = patch[k]; });
      patch = own;
      lists = {};
      held = {};
    } else if (answer.stamp) {
      stamp = answer.stamp;
      // the tickets of what the run of this answer read: the channel holds them in place of the page's
      if (answer.tickets && page) {
        page.tickets = answer.tickets;
        ticketsAt = Date.now();
      }
    }
    // a name this page held at another revision than the job ran on comes whole; every name that changed
    // brings its new revision
    Object.keys(held).forEach(function (k) {
      if (held[k]) revs[k] = held[k];
      else delete revs[k];
    });
    Object.keys(patch).forEach(function (k) { put(k, patch[k]); });
    Object.keys(lists).forEach(function (k) { put(k, applyList(get(k), lists[k])); });
    var mine = call.at && answer.components ? answer.components[call.at] : null, instance = mine ? instanceOf(call.host) : null;
    if (instance) Object.keys(mine.patch || {}).forEach(function (k) { instance[k] = mine.patch[k]; });
    if (mine && mine.revs) {
      var kept = scopeRevs[call.at] || (scopeRevs[call.at] = {});
      Object.keys(mine.revs).forEach(function (k) {
        if (mine.revs[k]) kept[k] = mine.revs[k];
        else delete kept[k];
      });
    }
    if (!call.refresh) mark(call, false, null);
    tell(held);
    paint();
    hold();
  }

  // ---- the page kept the same across the tabs of the browser and the moments it is not seen ----

  // the data this page holds, told to the other tabs of the browser on the same page when an answer changed
  // it, with the moment the server read it: a tab takes it, whole, only when the data it holds was read
  // before, so a message or an answer that arrives late never takes a tab back. A tab sees what another did
  // without a request.
  var channel = typeof BroadcastChannel === "function" ? new BroadcastChannel("kitwork:page") : null;
  function here() { return location.pathname + location.search; }
  function tell(held) {
    if (!channel || !page || !stamp || !Object.keys(held || {}).length) return;
    var values = {};
    Object.keys(revs).forEach(function (k) { values[k] = get(k); });
    try { channel.postMessage({ page: here(), stamp: stamp, values: values, revs: revs }); } catch (e) { /* a value that does not clone */ }
  }
  if (channel) {
    channel.onmessage = function (e) {
      var m = e && e.data;
      if (!m || !page || m.page !== here() || !m.revs || !(m.stamp > stamp)) return;
      stamp = m.stamp;
      var changed = false;
      Object.keys(m.revs).forEach(function (k) {
        if (revs[k] === m.revs[k]) return; // this tab holds it already
        put(k, m.values[k]);
        revs[k] = m.revs[k];
        changed = true;
      });
      Object.keys(revs).forEach(function (k) {
        if (Object.prototype.hasOwnProperty.call(m.revs, k)) return;
        put(k, null); // a name the page's data no longer has
        delete revs[k];
        changed = true;
      });
      if (changed) paint();
    };
  }

  // a tab seen again asks for what changed since its revisions while it was not seen: another device, another
  // person, a tab of another browser; one request, through the page's guards, answered with only what changed
  var lastRefresh = 0;
  function refresh() {
    if (!page || Date.now() - lastRefresh < 500) return;
    if ((flying && flying.refresh) || queue.some(function (c) { return c.refresh; })) return;
    lastRefresh = Date.now();
    queue.push({ refresh: true, name: "", args: [], row: "", el: null, epoch: epoch });
    pump();
  }
  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "visible") {
      refresh();
      listen();
    } else {
      unlisten();
    }
  });
  window.addEventListener("focus", refresh);

  // ---- the tab told when the data it shows changes, while it is seen ----

  // a tab seen holds one channel, GET /_kitwork/changes (work/view_changes.go), kept across the pages it moves
  // to: a stream that carries only the stamp of a change. The channel holds the tickets of the page shown now,
  // one for each key of data its run read, handed to it by POST; a stamp newer than the data the tab holds
  // asks for what changed, through the queue of its calls, with its cookies and past the guards. Tickets run
  // out: told to renew, the tab asks again, and the answer brings new ones. A hidden tab closes its channel and
  // asks when seen again: a browser holds few connections to one host.
  var changes = null, connection = "", holding = "", ticketsAt = 0;
  function tickets() { return (page && page.tickets) || []; }
  function marksOf(list) { return list.map(function (t) { return t.split(".")[0]; }).sort().join(" "); }
  function want(at) {
    if (!tickets().length || !(at > stamp)) return;
    if (at > told) told = at;
    if (queue.some(function (c) { return c.refresh; })) return; // the refresh waiting asks for it
    queue.push({ refresh: true, told: true, name: "", args: [], row: "", el: null, epoch: epoch });
    pump();
  }
  function listen() {
    if (changes || !tickets().length || typeof EventSource !== "function" || document.visibilityState === "hidden") return;
    var source = changes = new EventSource("/_kitwork/changes");
    source.addEventListener("connection", function (e) {
      connection = e.data;
      holding = "";
      hold();
    });
    source.addEventListener("renew", function () { refresh(); });
    source.onmessage = function (e) { want(Number(e.data)); };
  }
  function unlisten() {
    if (changes) changes.close();
    changes = null;
    connection = holding = "";
  }
  // hands the channel the tickets of the page shown now, when they are not what it holds: the answer is the last
  // change told of any of them, maybe one the page has not seen
  function hold() {
    var now = tickets().join(" ");
    if (!connection || now === holding) return;
    holding = now;
    var at = connection;
    fetch("/_kitwork/changes?connection=" + encodeURIComponent(at), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ tickets: tickets() })
    }).then(function (response) {
      if (response.status === 404 && at === connection) {
        // the channel was closed under the tab (the site was reloaded): it opens another
        unlisten();
        listen();
        return null;
      }
      return response.ok ? response.json() : null;
    }).then(function (answer) {
      if (answer && answer.last) want(answer.last);
    }, function () { /* the channel tells again on the next change */ });
  }

  // a keyed list's change by row: the rows removed, the rows set (changed in place, or new at the end), and
  // the order when the server sent one; a row nothing changed stays the same object
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

  // ---- the program: what each mark writes, run again only when a name it reads changes ----

  var POSITION = { count: true, first: true, last: true, even: true, odd: true };

  // names reports whether an expression reads any of the names given
  function readsAny(ir, names) {
    if (!(ir instanceof Array) || !ir.length) return false;
    if (ir[0] === "$") return !!names[ir[1]];
    if (ir[0] === "#") return false;
    for (var i = 1; i < ir.length; i++) if (readsAny(ir[i], names)) return true;
    return false;
  }

  function makeView(program, state, jobs) {
    var groups = program.g || [], lists = program.l || [];
    var isJob = {};
    jobs.forEach(function (name) { isJob[name] = true; });
    var v = { state: state, runs: 0 };
    var dirty = {}, touched = [], dirtyRows = [];
    var pageEls = {};       // mark → the element of a mark outside every row
    var pageReaders = {};   // name → marks outside every row that read it
    var rowReaders = {};    // name → marks in rows that read it
    var listByName = {};    // name of the page → list it draws
    var listById = {};      // the list's id on the page (s1) → list
    var events = {};        // event type → true

    groups.forEach(function (g, n) {
      var into = g.l < 0 ? pageReaders : rowReaders;
      [g.b || [], g.c || []].forEach(function (pairs) {
        pairs.forEach(function (p) {
          (p.r || []).forEach(function (name) { (into[name] || (into[name] = [])).indexOf(n) < 0 && into[name].push(n); });
        });
      });
      (g.e || []).forEach(function (p) { events[p.n.split(".")[0]] = true; });
    });

    document.querySelectorAll("[data-kitwork-mark]").forEach(function (el) {
      var n = +el.getAttribute("data-kitwork-mark"), g = groups[n];
      if (g && g.l < 0) pageEls[n] = el;
    });

    lists.forEach(function (spec, i) {
      var template = document.querySelector('[data-kitwork-template="' + i + '"]');
      if (!template) return;
      var start = template.previousSibling, close = template.nextSibling;
      while (close && !(close.nodeType === 8 && close.nodeValue === "kitwork-for-end")) close = close.nextSibling;
      var opened = start && start.nodeType === 8 && /^kitwork-for-start id=(\S+)$/.exec(start.nodeValue);
      if (!opened || !close) return;
      template.parentNode.removeChild(template);
      template.removeAttribute("data-kitwork-template");
      template.removeAttribute("hidden");
      var list = { i: i, spec: spec, id: opened[1], start: start, end: close, template: template, rows: {}, order: [] };
      list.name = spec.a instanceof Array && spec.a[0] === "$" ? spec.a[1] : null;
      var position = {};
      position[spec.i] = false;
      if (spec.x) position[spec.x] = true;
      Object.keys(POSITION).forEach(function (k) { position[k] = true; });
      list.positional = groups.some(function (g) {
        return g.l === i && [g.b || [], g.c || []].some(function (pairs) {
          return pairs.some(function (p) { return readsAny(p.x, position); });
        });
      });
      var items = state[list.name] instanceof Array ? state[list.name] : [];
      var byKey = {};
      items.forEach(function (item, at) { byKey[keyOf(list, item, at)] = item; });
      for (var node = start.nextSibling; node && node !== close; node = node.nextSibling) {
        if (node.nodeType !== 1 || node.getAttribute("data-kitwork-item") !== list.id) continue;
        var key = node.getAttribute("data-kitwork-key");
        var row = { el: node, key: key, item: byKey[key], index: list.order.length, marks: marksOf(node) };
        list.rows[key] = row;
        list.order.push(key);
      }
      listById[list.id] = list;
      if (list.name) listByName[list.name] = list;
    });

    // a row's key, kept per item object: a list of a thousand rows that changes by one row reads one key
    var keys = typeof WeakMap === "function" ? new WeakMap() : null;
    function keyOf(list, item, at) {
      if (!list.spec.k) return String(at);
      var cached = keys && item !== null && typeof item === "object" && !list.spec.x;
      if (cached && keys.has(item)) return keys.get(item);
      var s = {};
      s[list.spec.i] = item;
      if (list.spec.x) s[list.spec.x] = at;
      var key = String(kitwork.run(list.spec.k, s));
      if (cached) keys.set(item, key);
      return key;
    }
    function marksOf(rowEl) {
      var marks = {};
      if (rowEl.hasAttribute("data-kitwork-mark")) (marks[rowEl.getAttribute("data-kitwork-mark")] = []).push(rowEl);
      rowEl.querySelectorAll("[data-kitwork-mark]").forEach(function (el) {
        var n = el.getAttribute("data-kitwork-mark");
        (marks[n] || (marks[n] = [])).push(el);
      });
      return marks;
    }
    function rowScope(list, row) {
      var s = {};
      for (var k in state) s[k] = state[k];
      var count = list.order.length;
      s[list.spec.i] = row.item;
      if (list.spec.x) s[list.spec.x] = row.index;
      s.count = count;
      s.first = row.index === 0;
      s.last = row.index === count - 1;
      s.even = row.index % 2 === 0;
      s.odd = row.index % 2 === 1;
      return s;
    }
    function scopeOf(el) {
      var rowEl = el && el.closest ? el.closest("[data-kitwork-item]") : null;
      var list = rowEl && listById[rowEl.getAttribute("data-kitwork-item")];
      var row = list && list.rows[rowEl.getAttribute("data-kitwork-key")];
      return row ? rowScope(list, row) : state;
    }

    // run what mark n writes on one element, in a scope: every value, or only those that read a name of only
    function reads(p, only) {
      if (!only) return true;
      for (var i = 0; p.r && i < p.r.length; i++) if (only[p.r[i]]) return true;
      return false;
    }
    function runGroup(n, el, scope, only) {
      var g = groups[n];
      if (!g) return;
      current = el;
      try {
        (g.b || []).forEach(function (p) {
          if (!reads(p, only)) return;
          v.runs++;
          var value = kitwork.run(p.x, scope);
          if (p.n === "text") kitwork.text(el, value);
          else if (p.n === "show") el.hidden = !value;
          else if ((p.n === "value" || p.n === "checked") && twoWay(el, p)) model(el, p.n, value);
          else kitwork.write(el, p.n, value);
        });
        (g.c || []).forEach(function (p) {
          if (!reads(p, only)) return;
          v.runs++;
          el.classList.toggle(p.n, !!kitwork.run(p.x, scope));
        });
      } catch (error) {
        if (window.console) console.error("kitwork:", error, el);
      } finally {
        current = null;
      }
    }
    function twoWay(el, p) {
      return p.x instanceof Array && p.x[0] === "$" && /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName);
    }
    function model(el, name, value) {
      if (name === "checked") { if (el.checked !== !!value) el.checked = !!value; }
      else if (String(value == null ? "" : value) !== el.value) el.value = value == null ? "" : value;
    }
    function runRow(list, row) {
      var scope = rowScope(list, row);
      Object.keys(row.marks).forEach(function (n) {
        row.marks[n].forEach(function (el) { runGroup(+n, el, scope); });
      });
    }

    // a list against its rows: rows by key, a row whose item is another object runs again, a new row is
    // made from the template, a row whose key is gone leaves, the rows stand in the list's order
    function reconcile(list) {
      var items = state[list.name] instanceof Array ? state[list.name] : [];
      var parent = list.start.parentNode;
      if (!parent) return;
      var keep = {}, order = [], after = list.start;
      items.forEach(function (item, at) {
        var key = keyOf(list, item, at);
        if (keep[key]) return;
        keep[key] = true;
        order.push(key);
        var row = list.rows[key];
        if (!row) {
          var el = list.template.cloneNode(true);
          el.setAttribute("data-kitwork-item", list.id);
          el.setAttribute("data-kitwork-key", key);
          row = list.rows[key] = { el: el, key: key, item: item, index: at, marks: marksOf(el) };
          dirtyRows.push([list, row]);
        } else if (row.item !== item) {
          row.item = item;
          dirtyRows.push([list, row]);
        } else if (row.index !== at && list.positional) {
          dirtyRows.push([list, row]);
        }
        row.index = at;
        if (after.nextSibling !== row.el) parent.insertBefore(row.el, after.nextSibling);
        after = row.el;
      });
      list.order.forEach(function (key) {
        if (!keep[key]) {
          var gone = list.rows[key];
          if (gone.el.parentNode) gone.el.parentNode.removeChild(gone.el);
          delete list.rows[key];
        }
      });
      if (list.positional && order.length !== list.order.length) {
        order.forEach(function (key) { dirtyRows.push([list, list.rows[key]]); });
      }
      list.order = order;
    }

    v.set = function (name, value) {
      state[name] = value;
      dirty[name] = true;
    };
    // a job's call began or ended: what reads the job outside every row, and in the row it was made from
    v.touch = function (name, row) {
      touched.push([name, row]);
    };
    v.forget = function (el) {
      if (!el || el.nodeType !== 1) return;
      if (typeof kitwork.forget === "function") kitwork.forget(el);
      var all = [el];
      el.querySelectorAll("[data-kitwork-mark]").forEach(function (x) { all.push(x); });
      all.forEach(function (x) {
        if (x.hasAttribute("data-kitwork-mark")) runGroup(+x.getAttribute("data-kitwork-mark"), x, scopeOf(x));
      });
    };
    v.flush = function () {
      var names = Object.keys(dirty);
      dirty = {};
      var jobs = touched;
      touched = [];
      names.forEach(function (name) { if (listByName[name]) reconcile(listByName[name]); });
      var rows = dirtyRows;
      dirtyRows = [];
      var ran = {};
      rows.forEach(function (pair) {
        var id = pair[0].id + ":" + pair[1].key;
        if (ran[id] || !pair[0].rows[pair[1].key]) return;
        ran[id] = true;
        runRow(pair[0], pair[1]);
      });
      var pageMarks = {};
      names.concat(jobs.map(function (t) { return t[0]; })).forEach(function (name) {
        (pageReaders[name] || []).forEach(function (n) { (pageMarks[n] || (pageMarks[n] = {}))[name] = true; });
      });
      Object.keys(pageMarks).forEach(function (n) { if (pageEls[n]) runGroup(+n, pageEls[n], state, pageMarks[n]); });
      // a name of the page read in rows: every row, but a job: the row its call was made from
      names.forEach(function (name) {
        if (!rowReaders[name] || isJob[name]) return;
        Object.keys(listById).forEach(function (id) {
          var list = listById[id];
          list.order.forEach(function (key) {
            if (ran[id + ":" + key]) return;
            var row = list.rows[key], scope = rowScope(list, row), only = {};
            only[name] = true;
            rowReaders[name].forEach(function (n) {
              (row.marks[n] || []).forEach(function (el) { runGroup(n, el, scope, only); });
            });
          });
        });
      });
      jobs.forEach(function (t) {
        var name = t[0], at = t[1];
        if (!rowReaders[name] || !at) return;
        var cut = at.indexOf(":"), list = listById[at.slice(0, cut)];
        var row = list && list.rows[at.slice(cut + 1)];
        if (!row) return;
        var scope = rowScope(list, row), only = {};
        only[name] = true;
        rowReaders[name].forEach(function (n) {
          (row.marks[n] || []).forEach(function (el) { runGroup(n, el, scope, only); });
        });
      });
    };

    // events: one listener per type for the page, the handlers of the marks from the target up
    Object.keys(events).forEach(function (type) {
      if (listening[type]) return;
      listening[type] = true;
      document.addEventListener(type, function (e) {
        if (!view) return;
        view.handle(type, e);
      });
    });
    v.handle = function (type, e) {
      for (var el = e.target; el && el.nodeType === 1; el = el.parentElement) {
        if (!el.hasAttribute("data-kitwork-mark")) continue;
        var g = groups[+el.getAttribute("data-kitwork-mark")];
        if (!g || !g.e) continue;
        var stop = false;
        for (var i = 0; i < g.e.length; i++) {
          var p = g.e[i], parts = p.n.split(".");
          if (parts[0] !== type) continue;
          if (parts.indexOf("prevent") > 0 && e.preventDefault) e.preventDefault();
          if (parts.indexOf("stop") > 0) { stop = true; if (e.stopPropagation) e.stopPropagation(); }
          current = el;
          handling = e;
          try { kitwork.run(p.x, scopeOf(el)); }
          catch (error) { if (window.console) console.error("kitwork:", error, el); }
          finally { current = null; handling = null; }
        }
        if (stop) break;
      }
      v.flush();
    };
    // a field that holds a var: what is typed is the var, and what reads it runs again
    v.input = function (e) {
      var el = e.target;
      if (!el || !el.hasAttribute || !el.hasAttribute("data-kitwork-mark")) return;
      var g = groups[+el.getAttribute("data-kitwork-mark")];
      (g && g.b || []).forEach(function (p) {
        if ((p.n === "value" || p.n === "checked") && twoWay(el, p)) {
          var value = p.n === "checked" ? !!el.checked : (el.type === "number" || el.type === "range") ? (parseFloat(el.value) || 0) : (el.value || "");
          v.set(p.x[1], value);
        }
      });
      v.flush();
    };
    return v;
  }
  var listening = {};
  document.addEventListener("input", function (e) { if (view) view.input(e); });

  // Drive leaves the page: its calls are dropped; the page it brings is seeded from its own data
  document.addEventListener("kitwork:before-swap", function () {
    epoch++;
    told = 0;
    typed.forEach(function (waiting) { clearTimeout(waiting.timer); });
    typed = [];
    queue = [];
    track = {};
    view = null;
  });
  // the components the new page holds are registered before the kernel paints it (its own listener is on the
  // document; one on the window in the capture phase runs first), so it never looks for one in vain
  window.addEventListener("kitwork:load", seedComponents, true);
  document.addEventListener("kitwork:load", function () {
    seed();
    paint();
    // the channel stays: it holds the new page's tickets, or closes for a page that has none
    if (!tickets().length) unlisten();
    listen();
    hold();
  });

  seedComponents();
  seed();
  listen();
})();
