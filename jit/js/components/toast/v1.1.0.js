/* toast component — short notices stacked at an edge of the screen (17/10/2026,
 * docs/components/toast.md).
 *
 * Kitwork UI. The author draws the stack and ONE notice, in markup; the engine (ui/toast.go) writes
 * the rest:
 *
 *   <box component="toast" as="$toast" place="bottom-end" gap="2">
 *     <card for="notice of notices; key: notice.id" flex="row" gap="3" padding="3">
 *       <icon show="notice.tone == 'danger'" value="alert-circle" color="danger" />
 *       <box gap="1">
 *         <text show="notice.title" weight="bold">{{ notice.title }}</text>
 *         <text>{{ notice.message }}</text>
 *         <button show="notice.action.click" click="act(notice.id)">{{ notice.action.label }}</button>
 *         <link show="notice.action.link" value="{{ notice.action.link }}">{{ notice.action.label }}</link>
 *       </box>
 *       <button click="dismiss(notice.id)"><icon value="x" alt="Đóng thông báo" /></button>
 *     </card>
 *   </box>
 *
 *   <div class="flex-column gap-2 toast toast-bottom-end" data-kitwork-component="toast: $toast"
 *        role="region" aria-label="Thông báo (F8)" tabindex="-1" data-kitwork-scope="place: 'bottom-end'; limit: 3">
 *     <div class="card …" data-kitwork-for="notice of notices; key: notice.id" data-kitwork-bind="data-leaving: notice.leaving">…</div>
 *     <span class="reader" role="status"></span>
 *     <span class="reader" role="alert"></span>
 *   </div>
 *
 * The kernel's for makes one row of the blueprint for each notice and keeps it by its key, so a notice
 * that moves keeps its node (and the focus in it). The page calls the toast through its alias:
 * $toast.success('Đã lưu.'), $toast.show('Đã xoá.', { title: 'Ghi chú', action: { label: 'Hoàn tác',
 * click: () => $notes.restore() } }), $toast.loading('Đang lưu…', { id: 'save' }) then
 * $toast.update('save', 'Đã lưu.', { tone: 'success' }).
 *
 *   notices   what is on screen, in the order the eye sees it, top to bottom: { id, message, title,
 *             tone, loading, action, count, leaving }
 *   waiting   how many older notices wait off screen (more than limit at once); their time is held
 *   paused    true while time stands still: the pointer on a notice, the keyboard in the stack, the
 *             page hidden, the window in the background
 *   place     the edge or corner (the engine hands it in); limit how many show at once (1 to 10)
 *
 *   show(message, options) → id       info / success / warning / danger(message, options) → id
 *   loading(message, options) → id   stays until updated or dismissed
 *   update(id, message, options) → true when the notice is still there (shown or waiting); never creates
 *   dismiss(id) → true when found     clear()      act(id): the notice's action.click, then dismiss
 *
 *   options: tone, title, duration ("4s", "800ms", 0 = stays), action ({ label, click } or
 *   { label, link }), id (a name that starts with a letter: letters, digits, - and _; a number is the
 *   toast's own, 1, 2, 3…, and one given would meet it), loading (true)
 *
 * Time: success 4s, info 5s, warning 8s, danger and loading stay until closed; with an action at least
 * 8s; never less than 60ms a character of title and message, up to 15s; the call's duration wins.
 * Paused, a notice keeps the time it has left and goes on with at least 2s. The same tone, title and
 * message shown again while the first is still there is no second notice: the first becomes the
 * newest, counts one more, starts its time again and is said again.
 *
 * Said once, in the words of the notice, through the two regions the engine writes last in the host
 * (aria-atomic="false": each says the line added to it, alone): the polite one for info, success,
 * warning and loading, the assertive one for danger only. A line stays a second, and the lines of
 * before go when one is added: a reader that reaches the stack hears each notice in the notice. Never
 * takes the focus: F8 reaches the stack (and pauses it) when the page can give it the focus (not
 * behind a modal dialog), Escape closes the notice that has the focus, and closing the notice that
 * has the focus hands it to the one beside it, else back where it was; that focus stays when the
 * kernel moves the row once the notice closed has gone (a node moved loses it: settle gives it back).
 * A link in a notice closes it when followed. A swipe across a notice on a touch screen closes it.
 * Drive: a notice whose action calls into the page it was shown on closes when the page is swapped.
 *
 * It writes no attribute on the page's markup: role, aria-*, tabindex and data-leaving are the
 * engine's bindings. Two things only: a sentence in a region (a <span> of its own, gone after 1s),
 * and the translate of a notice while a finger drags it, given back when it lets go. What it cannot
 * use of a call from data is one console line each, in the compiler's words.
 *
 * A template page (frozen, written before Kitwork UI: a data-kit-component="toast" host) keeps the
 * contract of v1.0.0 exactly: visible, message, show(message) — one message, three seconds, a second
 * show starts the three seconds again, "Notification" when empty — and hide(). No stack, no pause on
 * hover (its host wraps the very button that says it), no F8, no regions (the page writes its own
 * role="status").
 */
var TOAST = Symbol("kit:toast");

// what a notice stays for, by tone, when the call names no duration (0: until closed)
var LASTS = { info: 5000, success: 4000, warning: 8000, danger: 0 };
var WITH_ACTION = 8000; // a keyboard needs time to reach the button (F8, then Tab)
var PER_CHARACTER = 60; // about reading speed
var LONGEST_READ = 15000;
var LEAST_LEFT = 2000; // what a notice keeps when its time goes on again
var LEAVING = 150; // the notice fades out this long before it goes
var BEAT = 100; // a region speaks a change: the sentence lands this long after the notice
var SPOKEN = 1000; // how long a sentence stays in its region: said by then, and not heard twice after
var SWIPE_SHARE = 0.4; // of the notice's width, a drag that closes it
var SWIPE_SPEED = 0.5; // px/ms, a flick that closes it
var TEMPLATE_PAGE = 3000; // v1.0.0: one message, three seconds
var EDGES = { "bottom-end": true, "bottom": true, "bottom-start": true, "top-end": true, "top": true, "top-start": true };
var NAME = /^[A-Za-z][A-Za-z0-9_-]*$/; // an author's id; the toast's own are numbers
var DURATION = /^([0-9]+(?:\.[0-9]+)?)(ms|s)$/;

var WORDS = {
  vi: { warning: "Cảnh báo: ", danger: "Lỗi: ", reach: function (label) { return "Nhấn F8 để tới “" + label + "”."; } },
  en: { warning: "Warning: ", danger: "Error: ", reach: function (label) { return "Press F8 to reach “" + label + "”."; } }
};

var toastDef = {
  notices: [],
  waiting: 0,
  paused: false,
  place: "bottom-end",
  limit: 3,
  // a template page's (v1.0.0)
  visible: false,
  message: "",

  show: function (message, options) {
    var own = ownOf(this);
    if (own.templatePage) return templateShow(own, message);
    return say(own, message, options, "");
  },
  info: function (message, options) { return say(ownOf(this), message, options, "info"); },
  success: function (message, options) { return say(ownOf(this), message, options, "success"); },
  warning: function (message, options) { return say(ownOf(this), message, options, "warning"); },
  danger: function (message, options) { return say(ownOf(this), message, options, "danger"); },
  loading: function (message, options) {
    var given = plain(options) ? options : {};
    var copy = {};
    for (var key in given) if (Object.prototype.hasOwnProperty.call(given, key)) copy[key] = given[key];
    copy.loading = true;
    return say(ownOf(this), message, copy, "");
  },

  update: function (id, message, options) {
    var own = ownOf(this);
    var record = find(own, id);
    if (!record) return false;
    change(own, record, message, plain(options) ? options : {}, false);
    return true;
  },

  dismiss: function (id) {
    var own = ownOf(this);
    var record = find(own, id);
    if (!record) return false;
    close(own, record, true);
    return true;
  },

  clear: function () {
    var own = ownOf(this);
    var inside = own.host && own.host.contains(document.activeElement);
    own.all.slice().forEach(function (record) { close(own, record, false); });
    if (inside) back(own);
  },

  // act runs the notice's action, then closes it. The action runs once the click has finished: a lambda
  // called from inside an expression runs in the calling scope (the notice's row), called from outside
  // any in the scope that wrote it — the button that said the notice, where $files means what it meant.
  act: function (id) {
    var own = ownOf(this);
    var record = find(own, id);
    if (!record) return;
    var action = record.notice.action;
    close(own, record, true);
    if (!action || typeof action.click !== "function") return;
    Promise.resolve().then(function () {
      try { action.click(); } catch (problem) { complain("click của action gặp lỗi", problem); }
    });
  },

  // a template page's: close it before its three seconds
  hide: function () {
    var own = ownOf(this);
    if (!own.templatePage) { this.clear(); return; }
    clearTimeout(own.hideTimer);
    own.scope.visible = false;
  },

  init: function (context) {
    var own = ownOf(this);
    own.scope = this;
    own.context = context;
    own.host = context.host;
    own.templatePage = context.host.hasAttribute("data-kit-component");
    context.cleanup(function () {
      own.gone = true;
      clearTimeout(own.hideTimer);
      own.all.concat(own.leaving).forEach(function (record) { clearTimeout(record.timer); clearTimeout(record.going); });
      own.spoken.forEach(clearTimeout);
    });
    if (own.templatePage) return;

    var host = context.host;
    own.top = String(this.place).indexOf("top") === 0;
    if (!EDGES[String(this.place)]) complain("place của toast là một mép hay góc màn hình: bottom-end, bottom, bottom-start, top-end, top, top-start; không phải " + this.place);
    var limit = Math.floor(Number(this.limit));
    own.limit = limit >= 1 && limit <= 10 ? limit : 3;
    own.motion = window.matchMedia ? window.matchMedia("(prefers-reduced-motion: reduce)") : { matches: false };

    // time stands still while someone reads: the pointer on a notice, the keyboard in the stack, the
    // page hidden, the window behind another
    var reason = function (name, on) {
      if (!!own.reasons[name] === on) return;
      own.reasons[name] = on;
      clock(own);
    };
    context.listen(host, "pointerover", function (event) { if (rowOf(own, event.target)) reason("pointer", true); });
    context.listen(host, "pointerout", function (event) { if (!rowOf(own, event.relatedTarget)) reason("pointer", false); });
    // the keyboard only: a mouse that pressed a button leaves its focus behind, and the pointer pauses
    // already while it is there
    context.listen(host, "focusin", function (event) {
      if (!host.contains(event.relatedTarget) && event.relatedTarget !== host) own.before = event.relatedTarget || own.before;
      if (keyboard(event.target)) reason("focus", true);
    });
    context.listen(host, "focusout", function (event) {
      if (!host.contains(event.relatedTarget)) reason("focus", false);
    });
    context.listen(document, "visibilitychange", function () { reason("hidden", document.hidden); });
    context.listen(window, "blur", function () { reason("away", true); });
    context.listen(window, "focus", function () { reason("away", false); });
    own.reasons.hidden = !!document.hidden;

    // F8 reaches the stack from anywhere, while it holds a notice; F8 again (or Escape on the stack)
    // goes back. Escape on a notice closes it.
    context.listen(document, "keydown", function (event) {
      if (event.key !== "F8" || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || event.defaultPrevented) return;
      if (host.contains(document.activeElement)) {
        event.preventDefault();
        back(own);
        return;
      }
      if (!own.all.length) return; // nothing to reach: F8 is the browser's
      var before = document.activeElement;
      try { host.focus({ preventScroll: true }); } catch (problem) { host.focus(); }
      // the page could not give the stack the focus (a modal dialog holds it): F8 stays the browser's,
      // and the time does not stand still for a focus that never came
      if (document.activeElement !== host) return;
      event.preventDefault();
      own.before = before;
      reason("focus", true);
    });
    context.listen(host, "keydown", function (event) {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      var row = rowOf(own, event.target);
      if (event.target === host) {
        event.preventDefault();
        back(own);
      } else if (row) {
        var record = recordOf(own, row);
        if (record) {
          event.preventDefault();
          close(own, record, true);
        }
      }
    });

    // a link in a notice goes somewhere: the notice has said what it had to
    context.listen(host, "click", function (event) {
      var link = event.target && event.target.closest ? event.target.closest("a[href]") : null;
      var record = link && host.contains(link) ? recordOf(own, rowOf(own, link)) : null;
      if (record) close(own, record, false);
    });

    swipes(own, context);

    // Drive: what a notice's button does belongs to the page that said it
    context.listen(document, "kitwork:before-swap", function () {
      own.all.slice().forEach(function (record) {
        if (record.notice.action && typeof record.notice.action.click === "function") close(own, record, false);
      });
    });
  }
};

// ownOf is what an instance keeps for itself, unseen by markup (a Symbol is never a name)
function ownOf(scope) {
  return scope[TOAST] || (scope[TOAST] = { all: [], leaving: [], serial: 0, order: 0, reasons: {}, spoken: [], limit: 3, scope: scope });
}

function plain(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

// complain says, in one console line, what a call from data wrote that the toast cannot use (the
// compiler says the same of a call written out); the notice goes on without it.
function complain(what, problem) {
  if (!window.console || !console.warn) return;
  if (problem) console.warn("kitwork toast: " + what, problem);
  else console.warn("kitwork toast: " + what);
}

function keyboard(element) {
  try { return !!element && element.matches(":focus-visible"); } catch (problem) { return true; }
}

// templateShow is v1.0.0's show: one message, three seconds, a second show starts them again.
function templateShow(own, message) {
  var scope = own.scope;
  scope.message = message || "Notification";
  scope.visible = true;
  clearTimeout(own.hideTimer);
  own.hideTimer = setTimeout(function () { scope.visible = false; }, TEMPLATE_PAGE);
}

// read turns what a call wrote into a notice's parts, saying what it could not use.
function read(options, fixed) {
  var out = {};
  var tone = fixed || options.tone;
  if (tone === undefined || tone === null || tone === "") tone = "info";
  if (!Object.prototype.hasOwnProperty.call(LASTS, tone)) {
    complain("tone của thông báo là info, success, warning, danger, không phải " + tone);
    tone = "info";
  }
  out.tone = tone;
  if ("title" in options) out.title = options.title == null ? "" : String(options.title);
  if ("action" in options) out.action = action(options.action);
  if ("duration" in options) out.duration = duration(options.duration);
  out.loading = options.loading === true;
  return out;
}

// action is what a notice's button does: a function to run, or a page to go to; anything else is dropped
function action(given) {
  if (given === null || given === undefined) return null;
  if (!plain(given) || typeof given.label !== "string" || !given.label.trim()) {
    complain("action cần label: chữ trên nút ({ label: 'Hoàn tác', click: () => … }); thông báo hiện không có nút");
    return null;
  }
  var hasClick = typeof given.click === "function", hasLink = typeof given.link === "string" && given.link !== "";
  if (hasClick && hasLink) {
    complain("action có cả click và link: một nút làm một việc; thông báo hiện không có nút");
    return null;
  }
  if (!hasClick && !hasLink) {
    complain("action cần click (một hàm) hay link (một trang); thông báo hiện không có nút");
    return null;
  }
  if (hasLink && !safeLink(given.link)) {
    complain("link của action chỉ đi tới đường dẫn trong site (/…, ./…, #…), http:, https:, mailto: hoặc tel:, không phải " + given.link);
    return null;
  }
  return hasClick ? { label: given.label, click: given.click } : { label: given.label, link: given.link };
}

// safeLink is the rule of a link (ui/render.go, safeLink): a page of the site, http:, https:, mailto: or
// tel:, never javascript:, data: or any other scheme — the kernel writes a bound href as it is.
function safeLink(link) {
  var value = String(link).replace(/[\t\n\r]/g, "").replace(/^[\u0000- ]+/, "");
  var scheme = /^([A-Za-z][A-Za-z0-9+.-]*):/.exec(value);
  var name = scheme ? scheme[1].toLowerCase() : "";
  return name === "" || name === "http" || name === "https" || name === "mailto" || name === "tel";
}

// duration reads "4s", "800ms", "1.5s" or 0 (stays) into milliseconds; undefined when it cannot
function duration(value) {
  if (value === 0 || value === "0") return 0;
  var m = typeof value === "string" ? DURATION.exec(value.trim()) : null;
  if (!m) {
    complain("duration là một thời lượng có đơn vị ('4s', '800ms') hay 0 (ở lại tới khi đóng), không phải " + value);
    return undefined;
  }
  var ms = Number(m[1]) * (m[2] === "s" ? 1000 : 1);
  return isFinite(ms) ? ms : undefined;
}

// lasts is how long a notice stays: what the call said, else its tone's time, longer with an action
// and for a long sentence; 0 stays until closed.
function lasts(notice, written) {
  if (written !== undefined) return written;
  if (notice.loading) return 0;
  var base = LASTS[notice.tone];
  if (!base) return 0;
  if (notice.action) base = Math.max(base, WITH_ACTION);
  return Math.max(base, Math.min(LONGEST_READ, (notice.title.length + notice.message.length) * PER_CHARACTER));
}

// say shows a notice, or brings back the one already saying the same thing.
function say(own, message, options, fixed) {
  if (own.gone) return null;
  options = plain(options) ? options : {};
  var text = message === null || message === undefined ? "" : String(message);
  if (!text.trim()) {
    complain("thông báo cần một câu; câu rỗng không hiện gì");
    return null;
  }
  var parts = read(options, fixed);
  var id = options.id;
  if (id !== undefined && id !== null) {
    if (typeof id === "number") {
      complain("id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _): số là của toast (1, 2, 3…), không phải " + id);
      id = undefined;
    } else if (!(typeof id === "string" && NAME.test(id))) {
      complain("id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _), không phải " + id);
      id = undefined;
    }
  }
  if (id !== undefined && id !== null) {
    var named = find(own, id);
    if (named) {
      // the notice of that id is said again in place: what it says now is all of this call
      if (!("title" in options)) parts.title = "";
      if (!("action" in options)) parts.action = null;
      change(own, named, text, options, true, parts);
      return named.notice.id;
    }
  } else {
    var title = parts.title || "";
    for (var i = 0; i < own.all.length; i++) {
      var same = own.all[i];
      if (same.notice.tone === parts.tone && same.notice.title === title && same.notice.message === text && !same.notice.loading && !parts.loading) {
        same.notice.count++;
        same.order = ++own.order; // the newest again: nearest the edge
        own.all.splice(i, 1);
        own.all.push(same);
        restart(same, lasts(same.notice, same.written));
        publish(own, true);
        speak(own, same.notice);
        return same.notice.id;
      }
    }
    id = ++own.serial;
  }
  var notice = {
    id: id, message: text, title: parts.title || "", tone: parts.tone, loading: parts.loading,
    action: parts.action || null, count: 1, leaving: false
  };
  var record = { notice: notice, order: ++own.order, written: parts.duration, remaining: 0, timer: 0, started: 0, shown: false, held: false };
  record.remaining = lasts(notice, parts.duration);
  own.all.push(record);
  publish(own, true);
  speak(own, notice);
  return id;
}

// change says a notice again with what a call wrote: update keeps what it does not name, a show with
// that notice's id names it all (parts already read).
function change(own, record, message, options, whole, parts) {
  var notice = record.notice;
  if (!parts) parts = read(options, options.tone ? "" : notice.tone);
  if (message !== undefined && message !== null && String(message).trim()) notice.message = String(message);
  notice.tone = parts.tone;
  if ("title" in parts) notice.title = parts.title;
  if ("action" in parts) notice.action = parts.action;
  notice.loading = parts.loading;
  notice.count = 1;
  record.written = whole || "duration" in parts ? parts.duration : undefined;
  restart(record, lasts(notice, record.written));
  publish(own);
  speak(own, notice);
}

function restart(record, ms) {
  clearTimeout(record.timer);
  record.timer = 0;
  record.held = false;
  record.remaining = ms;
}

function find(own, id) {
  for (var i = 0; i < own.all.length; i++) {
    if (own.all[i].notice.id === id || String(own.all[i].notice.id) === String(id)) return own.all[i];
  }
  return null;
}

// publish hands the markup what is on screen: the newest limit notices and those leaving, in the order
// the eye sees them (newest nearest the edge), and how many wait. Then the clocks follow, and settle
// once the kernel has painted it (reveal: a notice was said, so the stack shows its newest).
function publish(own, reveal) {
  if (own.gone) return;
  var had = document.activeElement, holding = !!own.reasons.focus;
  var shown = own.all.slice(Math.max(0, own.all.length - own.limit));
  own.all.forEach(function (record) { record.shown = shown.indexOf(record) >= 0; });
  var drawn = shown.concat(own.leaving).sort(function (a, b) { return a.order - b.order; });
  if (own.top) drawn.reverse();
  own.scope.notices = drawn.map(function (record) { return record.notice; });
  own.scope.waiting = own.all.length - shown.length;
  clock(own);
  // the kernel's paint is a microtask the write above queued (or one before it), or ran already (a
  // write inside an event's pass paints when the pass ends): this one runs after it
  Promise.resolve().then(function () { settle(own, had, holding, reveal); });
}

// settle runs once the kernel has painted what publish handed it.
//   - The focus: when a notice before the one that has the focus goes (or the order changes), the
//     kernel's for moves the rows that stay, and a node moved loses the focus (to the page). It is
//     given back, with the pause it held, to a notice still there (never one closed and fading out).
//   - The pointer: the browser says nothing (no pointerout) when the notice under a pointer that does
//     not move goes, so the pause is read again from what is under it now.
//   - A bottom stack taller than the screen scrolls inside: its newest is at its end.
function settle(own, had, holding, reveal) {
  if (own.gone) return;
  var host = own.host, now = document.activeElement;
  if (had && had.isConnected && recordOf(own, rowOf(own, had)) && now !== had && (!now || now === document.body)) {
    try { had.focus({ preventScroll: true }); } catch (problem) { had.focus(); }
    if (holding && document.activeElement === had && !own.reasons.focus) {
      own.reasons.focus = true;
      clock(own);
    }
  }
  if (own.reasons.pointer && !host.querySelector(":scope > [data-kit-key]:hover")) {
    own.reasons.pointer = false;
    clock(own);
  }
  if (reveal) host.scrollTop = own.top ? 0 : host.scrollHeight;
}

// clock runs the time of every notice on screen while nothing pauses the stack, and holds the rest.
function clock(own) {
  var paused = !!(own.reasons.pointer || own.reasons.focus || own.reasons.hidden || own.reasons.away);
  if (own.scope.paused !== paused) own.scope.paused = paused;
  var now = Date.now();
  own.all.forEach(function (record) {
    var run = record.shown && !paused && record.remaining > 0;
    if (run && !record.timer) {
      if (record.held) record.remaining = Math.max(record.remaining, LEAST_LEFT);
      record.held = false;
      record.started = now;
      record.timer = setTimeout(function () {
        record.timer = 0;
        close(own, record, false);
      }, record.remaining);
    } else if (!run && record.timer) {
      clearTimeout(record.timer);
      record.timer = 0;
      record.remaining = Math.max(0, record.remaining - (now - record.started)) || 1;
      record.held = true;
    }
  });
}

// close takes a notice off: it fades out (LEAVING), and a notice waiting comes back at once. When the
// notice held the focus (and moved says the person closed it), the focus goes to the notice beside it,
// else back where it was before the stack.
function close(own, record, moved) {
  var at = own.all.indexOf(record);
  if (at < 0) return;
  own.all.splice(at, 1);
  clearTimeout(record.timer);
  record.timer = 0;
  var row = rowFor(own, record.notice.id);
  var focused = row && row.contains(document.activeElement);
  if (focused) {
    var next = neighbour(own, row);
    if (next) focusIn(next);
    else if (moved) back(own);
  }
  if (!record.shown || (own.motion && own.motion.matches) || own.host.classList.contains("transition-none")) {
    publish(own); // a notice waiting off screen, less motion asked for, or transition="none": it goes at once
    return;
  }
  record.notice.leaving = true;
  own.leaving.push(record);
  record.going = setTimeout(function () {
    var i = own.leaving.indexOf(record);
    if (i >= 0) own.leaving.splice(i, 1);
    publish(own);
  }, LEAVING);
  publish(own);
}

// back hands the focus back to where it was before the stack, else to the top of the page.
function back(own) {
  own.reasons.focus = false;
  var before = own.before;
  own.before = null;
  if (before && before.isConnected && before !== document.body && typeof before.focus === "function" && !own.host.contains(before)) {
    try { before.focus({ preventScroll: true }); } catch (problem) { before.focus(); }
  } else if (document.activeElement && own.host.contains(document.activeElement) && document.activeElement.blur) {
    document.activeElement.blur();
  }
  clock(own);
}

// rows are the notices the kernel drew: the host's children it made from the blueprint, by key
function rows(own) {
  return Array.prototype.filter.call(own.host.children, function (child) { return child.hasAttribute("data-kit-key"); });
}

function rowFor(own, id) {
  var key = String(id);
  var found = rows(own).filter(function (row) { return row.getAttribute("data-kit-key") === key; });
  return found[0] || null;
}

function rowOf(own, element) {
  if (!element || !element.closest || !own.host.contains(element)) return null;
  var row = element.closest("[data-kit-key]");
  return row && row.parentElement === own.host ? row : null;
}

function recordOf(own, row) {
  if (!row) return null;
  return find(own, row.getAttribute("data-kit-key"));
}

// neighbour is the notice after row on screen that is not leaving, else the one before it
function neighbour(own, row) {
  var list = rows(own).filter(function (other) {
    var record = recordOf(own, other);
    return other === row || (record && !record.notice.leaving);
  });
  var at = list.indexOf(row);
  return list[at + 1] || list[at - 1] || null;
}

function focusIn(row) {
  var target = row.querySelector('button:not([hidden]):not([disabled]), a[href]:not([hidden]), [tabindex]:not([tabindex="-1"])');
  if (!target) return;
  try { target.focus({ preventScroll: true }); } catch (problem) { target.focus(); }
}

// speak says a notice once, in its own words: the assertive region for danger, the polite one for the
// rest. A region (aria-atomic="false") says what is added to it, so each sentence is a line of its own,
// added a beat after the notice; the lines of before, said already, go then (two of one moment stay,
// both said), and each goes a second after: a reader that reaches the stack hears the notice once.
function speak(own, notice) {
  var region = null;
  var role = notice.tone === "danger" ? "alert" : "status";
  Array.prototype.forEach.call(own.host.children, function (child) {
    if (child.classList.contains("reader") && child.getAttribute("role") === role) region = child;
  });
  if (!region) return;
  var marked = own.host.closest("[lang]");
  var language = marked ? String(marked.getAttribute("lang")).toLowerCase() : "";
  var words = language === "" || language.indexOf("vi") === 0 ? WORDS.vi : WORDS.en;
  var sentence = notice.title ? join(notice.title, notice.message) : notice.message;
  if (notice.tone === "warning" || notice.tone === "danger") sentence = words[notice.tone] + sentence;
  if (notice.action) sentence = join(sentence, words.reach(notice.action.label));
  later(own, function () {
    var now = Date.now();
    Array.prototype.slice.call(region.children).forEach(function (before) {
      if (!(now - before[TOAST] < BEAT)) before.remove();
    });
    var line = document.createElement("span");
    line.textContent = sentence;
    line[TOAST] = now; // when it was said (a property, never an attribute)
    region.appendChild(line);
    later(own, function () { line.remove(); }, SPOKEN);
  }, BEAT);
}

// later runs fn after ms unless the host has gone first (its timers go with it)
function later(own, fn, ms) {
  var timer = setTimeout(function () {
    var at = own.spoken.indexOf(timer);
    if (at >= 0) own.spoken.splice(at, 1);
    if (!own.gone) fn();
  }, ms);
  own.spoken.push(timer);
}

// join writes two sentences as one: a full stop between them unless the first already ends one
function join(first, second) {
  return /[.!?…:]$/.test(first.trim()) ? first.trim() + " " + second : first.trim() + ". " + second;
}

// swipes: a finger (or a pen) dragged across a notice moves it with the finger; let go past 40% of its
// width, or flicked, it closes; else it goes back. Up and down stays the page's scroll (touch-action).
// With less motion asked for, it does not follow the finger: past the line it closes.
function swipes(own, context) {
  var drag = null;
  context.listen(own.host, "pointerdown", function (event) {
    if (event.pointerType !== "touch" && event.pointerType !== "pen") return;
    var row = rowOf(own, event.target);
    if (!row || !recordOf(own, row)) return;
    drag = { row: row, id: event.pointerId, x: event.clientX, y: event.clientY, dx: 0, time: Date.now(), on: false };
  });
  context.listen(own.host, "pointermove", function (event) {
    if (!drag || event.pointerId !== drag.id) return;
    var dx = event.clientX - drag.x, dy = event.clientY - drag.y;
    if (!drag.on) {
      if (Math.abs(dy) > 8 && Math.abs(dy) > Math.abs(dx)) { drag = null; return; }
      if (Math.abs(dx) < 8) return;
      drag.on = true;
      try { drag.row.setPointerCapture(drag.id); } catch (problem) { /* nothing to hold */ }
    }
    drag.dx = dx;
    if (!(own.motion && own.motion.matches)) {
      drag.row.style.transition = "none"; // with the finger, not the notice's 200ms behind it
      drag.row.style.translate = dx + "px";
    }
  });
  var end = function (event) {
    if (!drag || event.pointerId !== drag.id) return;
    var done = drag;
    drag = null;
    done.row.style.transition = "";
    if (!done.on) {
      done.row.style.translate = "";
      return;
    }
    var width = done.row.getBoundingClientRect().width || 1;
    var speed = Math.abs(done.dx) / Math.max(1, Date.now() - done.time);
    var record = recordOf(own, done.row);
    if (record && (Math.abs(done.dx) > width * SWIPE_SHARE || speed > SWIPE_SPEED)) {
      // it goes on the way the finger sent it, fading as it leaves (the inline translate wins over the
      // leaving rule's way back to the edge)
      if (!(own.motion && own.motion.matches)) done.row.style.translate = (done.dx < 0 ? -width : width) + "px";
      close(own, record, false);
    } else {
      done.row.style.translate = ""; // back to its place, at the notice's own pace
    }
  };
  context.listen(own.host, "pointerup", end);
  context.listen(own.host, "pointercancel", end);
}

window.kit.component("toast", toastDef);
