/* countdown component — time that runs out, counted from the moment play() starts it
 * (Kitwork UI's <countdown>). The author draws the time left, once, and the buttons:
 *
 *   <countdown as="$tea" duration="5m" ready="play()" finish="$toast.success('Trà đã sẵn sàng')" flex="row" gap="3">
 *     <text>Còn <time>{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time></text>
 *     <button click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
 *     <button click="stop()">Dừng</button>
 *   </countdown>
 *
 * The engine writes the host, the box the tag builds, as data-kitwork-component="countdown: $tea" with
 * the duration in milliseconds, data-kitwork-scope="duration: 300000", and gives the <time> role="timer"
 * and a datetime bound to the parts ('PT' + hours + 'H' + minutes + 'M' + seconds + 'S'). This file
 * keeps the state those read, and writes only the time's segments (below).
 *
 *   duration   how long it counts, in milliseconds: the tag's, until markup sets the time; read-only
 *   remaining  what is left, rounded up to a whole second (a multiple of 1000), 0 exactly at the end;
 *              all of duration on the server's first paint and until play(); read-only
 *   hours, minutes, seconds  the parts of remaining (0-24, 0-59, 0-59), and the one way markup sets
 *              the time (20/10/2026): written like a var, minutes = 3, seconds++, a field two-way on one
 *   playing    true while it counts; false on the server's first paint; read-only
 *   play()     after pause() it goes on from what is left; never started, after stop() or after the
 *              end, from all of duration; while playing, nothing; while a segment is edited, it holds
 *              and plays once the edit is over
 *   pause()    while playing it holds, what is left kept to the millisecond; otherwise nothing, and
 *              nothing at a run whose time has run out before its timer said so (the clock ends it)
 *   toggle()   pause() while playing, play() otherwise
 *   stop()     from anything: not playing, all of duration left
 *   finish     (event) the time ran out while playing: once a run, sent on the host once the end is
 *              painted, carrying nothing. Never on stop(), and never from an instance removed
 *
 * None of the methods sends an event. Without JavaScript the server's full time stays on the screen;
 * a reload starts over, from the tag's duration.
 *
 * THE TIME SET BY MARKUP. A part written is a whole number in its range, clamped (a non-number or a
 * negative is the least, 75 seconds is 59, never carried to minutes): its segment's range when the
 * <time> has one (the engine writes it, an author's min and max only narrowing it), else 0-24, 0-59,
 * 0-59; the total is the time, at most 24 hours (24 hours and any minute is 24:00:00). Not playing
 * (never started, paused, stopped, finished), the total is the new full time: remaining and duration,
 * what play() starts from and stop() returns to; a pause is dropped. Playing, the total is what is
 * left, counted on from the write (0 stops the run there, with no finish: a write sends no event);
 * duration stays. A write at a run whose time ran out before its timer said so changes nothing, as
 * pause(). play() of a total of 0 does nothing.
 *
 * THE SEGMENTS (20/10/2026, evening). The fields the author writes in the <time>, in a clock's order
 * (<input value="{{ minutes }}" alt="Phút" />:<input value="{{ seconds }}" alt="Giây" />), are the
 * clock: the engine marks each with data-part, its range (aria-valuemin, aria-valuemax) and the
 * placeholder 00, binds none of them, and this file owns them: it writes the part as it ticks, in two
 * digits (05, 02 hours), with aria-valuenow and aria-valuetext (5 phút, 5 minutes).
 * THE BAR (20/10/2026, late evening: "đồng hồ cần nhận con trỏ"). reboot.css draws the <time> as one
 * long field, and it is typed as one: the caret goes where a click puts it (nothing is selected on
 * focus; a press on the bar beside the segments puts it in the nearest one), and runs through the bar:
 * ArrowRight at the end of a segment goes on to the start of the next, ArrowLeft at the start back to the
 * end of the one before, Home and End to the bar's ends, ":" to the start of the next segment. Only
 * digits go in, at the caret, no leading zero asked for (5 shows 5 while it is typed); a digit typed at
 * the end of a segment of two flows into the next one, and inside a segment of two it takes the place
 * of the digit after the caret. Backspace at the start of a segment, nothing selected, deletes the last
 * digit of the one before, Delete at its end the first of the next. ArrowUp and ArrowDown step the part
 * by one within its range, no wrap. A part is never above its max (65 seconds typed is 59, and so is 6
 * then 5 typed at the start of 00: the next digit goes on with what was typed, not with the 59 shown), an
 * empty segment is 0; over 24 hours in all the time is 24:00:00, the other parts kept as written while
 * the edit lasts (a typo of 25 for 2 hours, taken back, loses no minute); Ctrl (Alt, Cmd) with Backspace
 * or Delete takes the segment's digits before or after the caret, as a word; a time pasted (1:30) sets the
 * whole time, read from the right; a composition (an input method) is read once it commits, at the caret
 * it began at; the digits and the colon of a full-width input method (３, ：) are taken as typed. The
 * segment the focus is in shows what was typed; every other one, and all of them once the edit ends, two
 * digits. A segment focused, clicked or
 * typed in begins an edit: playing, the countdown pauses there, held to the millisecond, and while a
 * segment is edited the clock never writes it, and a play() holds as it would; Enter, or the focus
 * leaving the <time>, ends the edit (Enter takes the focus off the segment, so a focused spinbutton is
 * never one whose value changes every second) and plays on from the time edited if it played when the
 * edit began (the edit was of what is left: duration stays); Escape puts back the time it had then, and
 * plays on the same way. After Enter or Escape the next Tab goes on past the bar (Shift+Tab before it),
 * never into its next segment. The focus taken by a press of the mouse plays on once the press and its
 * click are over (or a drag of it), and not when that click called a method (Dừng stops, Chạy plays
 * once). The window going away (another tab) leaves the edit as it is. A segment left changed sends
 * change, once, and so does, once the edit is over, every segment whose value is not the one its last
 * change carried (Escape putting back one that sent it, a part the 24 hours or a pasted time changed).
 * Not playing, an edit sets the full time, as any write does.
 *
 * THE CLOCK. play() notes the time on two clocks, the wall's (Date.now) and the page's own
 * (performance.now), and what was left. What has passed since is the larger of the two counts: a
 * device asleep counts (the wall clock moved while the page's may not have), a wall clock turned back
 * never adds time (the page's did not go back). Each tick is a timer aimed at the next whole second of
 * what is left, the moment the second shown changes, and state is written only then: every write
 * repaints. A hidden page keeps counting, with nothing drawn: its tick chain is dropped and one timer
 * is set to the end (a browser may hold it back); coming back in sight, or from the history cache
 * (pageshow), it reads the clocks again, and a run whose end has passed finishes then, once. Hover,
 * focus elsewhere and less motion change nothing.
 */
var COUNTDOWN = Symbol("kit:countdown");

// MOST is the most each part holds; UNITS a part in Vietnamese words (in English it is the part's own
// name, one less s for one)
var MOST = { hours: 24, minutes: 59, seconds: 59 };
var UNITS = { hours: "giờ", minutes: "phút", seconds: "giây" };

// whole is a duration the host gave, in whole milliseconds, or 0 (nothing to count)
function whole(value) {
  var ms = Math.round(Number(value));
  return isFinite(ms) && ms > 0 ? ms : 0;
}

// left is what is left now, to the millisecond: while playing, what was left when the run went on
// less the time since, the larger of the two clocks' counts
function left(own) {
  if (!own.since) return own.from;
  var passed = Math.max(Date.now() - own.since.wall, performance.now() - own.since.page);
  return Math.max(0, own.from - passed);
}

// segments are the time's fields the engine marked, the host's own (a countdown nested in it keeps its
// own); segment is the one of a part, or the one an event came from
function segments(own) {
  return own.context.owned("[data-part]");
}
function segment(own, name) {
  return segments(own).filter(function (el) { return el === name || el.getAttribute("data-part") === name; })[0];
}

// clamp is a part as written, a whole number in its segment's range, else the part's: a non-number is
// the least
function clamp(own, name, value) {
  var el = segment(own, name), low = el ? +el.getAttribute("aria-valuemin") : 0;
  var high = el ? +el.getAttribute("aria-valuemax") : MOST[name], n = Math.floor(Number(value));
  return n > low ? Math.min(n, high) : low;
}

// write shows a part in its segment, in two digits (05, 00, 02 hours), and says it, by the page's
// language. What a person types shows as typed (put writes it after the paint its write makes), and
// the segment shows two digits again when the focus leaves it.
function write(own, el) {
  var name = el.getAttribute("data-part"), n = own[name], text = ("0" + n).slice(-2), lang = (el.closest("[lang]") || {}).lang || "vi";
  // written by its key: a field's value is no name of the countdown's (the server reads the names off
  // this source, every `.name =` among them)
  if (el.value !== text) el["value"] = text;
  el.setAttribute("aria-valuenow", n);
  el.setAttribute("aria-valuetext", n + " " + (/^vi/.test(lang) ? UNITS[name] : n === 1 ? name.slice(0, -1) : name));
}

// paint writes every segment. While one is edited the clock is held (begin pauses it, play() waits), so
// what writes the one being edited is typing, a write of a part, or the end of a run that ran out
// before its timer said so (which shows 00 there too, never a time that is over).
function paint(own) {
  segments(own).forEach(function (el) { write(own, el); });
}

// show writes remaining and its parts, only when the second shown changes. The parts live in the
// private slot, read through their accessors; the write of remaining is the one that repaints. A time
// the clock (or Escape, or a method) shows is no longer the one an edit wrote: wrote goes.
function show(scope, ms) {
  var own = scope[COUNTDOWN], remaining = Math.ceil(ms / 1000) * 1000;
  if (scope.remaining === remaining) return;
  own.wrote = null;
  own.hours = Math.floor(remaining / 3600000);
  own.minutes = Math.floor(remaining / 60000) % 60;
  own.seconds = (remaining / 1000) % 60;
  paint(own);
  scope.remaining = remaining;
}

// edit is a part written, clamped: the total of the parts, at most 24 hours (24:00:00 over it, the
// parts an edit wrote kept in wrote, see part), is the time. Not playing,
// and not an edit of a run in a segment (onward), the new full time (remaining, duration, a pause
// dropped); playing, what is left, counted on from now. A total of 0 while playing stops the run there
// and sends nothing: finish is the clock's, and no write, like no method, sends an event; play() then
// runs all of duration again.
function edit(scope) {
  var own = scope[COUNTDOWN], s = own.hours * 3600 + own.minutes * 60 + own.seconds;
  if (s > 86400) {
    own.hours = 24;
    own.minutes = own.seconds = 0;
    s = 86400;
  }
  own.from = s * 1000;
  paint(own);
  scope.remaining = own.from;
  if (own.since) {
    own.since = s ? { wall: Date.now(), page: performance.now() } : null;
    schedule(scope);
    if (!s) scope.playing = false;
  } else if (!own.onward) scope.duration = own.from;
}

// part is a part's accessor, called on the instance's state object (the kernel's scope writes through
// to it): a read is what was last shown or written, a write sets the time, through the scope init was
// given, so a timer the write starts repaints. Before init: 0, and nothing set. At a run whose time has
// run out before its timer said so, a write changes nothing, as pause() there: the run is over, and its
// end is the clock's to send. In an edit the parts are kept as written (wrote), the total over 24 hours
// or not, and each write starts from them: 25 typed for 2 hours shows 24:00:00, and taken back to 2 it
// is 2:05:00 again, the 5 minutes never lost to a passing keystroke.
function part(scope, name, value) {
  var own = scope[COUNTDOWN], wrote;
  if (!own) return 0;
  if (arguments.length < 3) return own[name];
  if (own.since && left(own) <= 0) return;
  if (wrote = own.wrote) own.hours = wrote[0], own.minutes = wrote[1], own.seconds = wrote[2];
  own[name] = clamp(own, name, value);
  if (own.edited) own.wrote = [own.hours, own.minutes, own.seconds];
  edit(own.scope);
}

// schedule drops the clock's timer and, while playing, reads the clocks and sets the next one: on a
// page in sight at the next whole second of what is left, on a hidden page at the end
function schedule(scope) {
  var own = scope[COUNTDOWN];
  clearTimeout(own.timer);
  own.timer = 0;
  if (!own.since || own.gone) return;
  var ms = left(own);
  if (ms <= 0) return finish(scope);
  show(scope, ms);
  own.timer = setTimeout(function () {
    own.timer = 0;
    schedule(scope);
  }, document.hidden ? ms : (ms % 1000) || 1000);
}

// finish ends a run whose time ran out: nothing left, not playing, then the event, after the paint:
// emitted straight after the writes it would reach the page before they are drawn (a finish="…" that
// reads minutes would see the second before). A host removed meanwhile says nothing.
function finish(scope) {
  var own = scope[COUNTDOWN];
  clearTimeout(own.timer);
  own.timer = 0;
  own.since = null;
  own.from = 0;
  show(scope, 0);
  scope.playing = false;
  own.context.afterRender(function () {
    if (!own.gone) own.context.emit("finish");
  });
}

// begin is an edit in a segment: the first one focused (a click, Tab) or typed in holds the countdown
// where it is if it plays (onward: Enter or leaving plays on), and keeps the time it had for Escape and
// what each segment shows for change; another segment of the same bar goes on with it. Nothing is
// selected: the caret stays where the click put it. An edit begun while the one before waits to play
// on (the mouse still down) carries it on.
function begin(own, el) {
  if (!own.edited) {
    if (own.since) {
      own.scope.pause();
      own.onward = !own.since; // not held: the run is over, and its end is the clock's
    }
    own.before = [own.from, own.scope.duration];
    own.prior = segments(own).map(function (seg) { return seg.value; });
  }
  own.edited = el;
}

// tell sends change from a segment whose value is not the one its last change carried, or, none sent
// in this edit, the one it began with: a segment left changed (as a text field's change), and, the
// edit over, one Escape put back after its change, or one the 24 hours or a pasted time changed
function tell(own, el) {
  var i = segments(own).indexOf(el);
  if (own.prior && el.value !== own.prior[i]) {
    own.prior[i] = el.value;
    el.dispatchEvent(new Event("change", { bubbles: true }));
  }
}

// done ends an edit: every segment shows the time again, in two digits, each changed one says so, and it
// plays on from the time edited if it played when the edit began (a total of 0 left: the run stops
// there, as any write of 0 stops it). The focus taken by the mouse (or a finger) plays on only once the
// press is over and its click has run: a click on Chạy, on Dừng or on +1 phút is what the person asked,
// and nothing moves under it.
function done(own) {
  if (!own.edited) return;
  own.edited = own.wrote = own.typed = null;
  paint(own);
  segments(own).forEach(function (el) { tell(own, el); });
  if (!own.press) resume(own);
}

// rejoin puts the segments back in the order of Tab, as they were before Enter or Escape took them out
// (away: the tabindex each had), once the focus goes anywhere or the mouse is pressed
function rejoin(own) {
  var away = own.away;
  own.away = null;
  if (away) segments(own).forEach(function (seg, i) { away[i] == null ? seg.removeAttribute("tabindex") : seg.setAttribute("tabindex", away[i]); });
}

// resume plays on after an edit, unless a method came first (play, pause, toggle, stop end the wait)
function resume(own) {
  if (own.edited || !own.onward) return;
  own.onward = false;
  if (own.from) own.scope.play();
}

// beside is the segment before a segment (by -1) or after it (1), if the bar has one
function beside(own, el, by) {
  var all = segments(own);
  return all[all.indexOf(el) + by];
}

// caret puts the caret in a segment, the focus with it, at a place (-1: its end), and gives the segment
function caret(el, at) {
  el.focus();
  if (at < 0) at = el.value.length;
  el.setSelectionRange(at, at);
  return el;
}

// put writes a segment's text as a person typed it, the caret at a place: its part is the number it
// reads, clamped like any write (an empty segment is 0); what was typed shows, unless it reads more than
// the part took (its max, the 24 hours): then the part, in as many digits as were typed (65 shows 59).
// typed keeps what was typed beside what it shows, and where the caret is: the next digit typed there
// goes on with the text typed, not with the part shown (6 then 5 at the start of 00 is 65, 59; on the 59
// shown it would be 55)
function put(own, el, text, at) {
  var name = el.getAttribute("data-part"), typed = text;
  part(own.scope, name, text);
  if (+text > own[name]) text = String(own[name]).padStart(Math.min(text.length, 2), "0");
  el["value"] = text;
  el.setSelectionRange(at = Math.min(at, text.length), at);
  own.typed = [el, typed, text, at];
}

// type puts text in at the caret as one field takes keys: a digit goes in over the selection, or, in a
// segment of two digits, in place of the one after the caret, or, at its end, at the start of the next
// segment; ":" moves the caret to the start of the next segment; anything else is refused. A segment
// showing less than was typed in it (typed), the caret where the typing left it, is typed on as typed.
function type(own, el, text) {
  for (var i = 0; i < text.length; i++) {
    var c = text[i], v = el.value, a = el.selectionStart, b = el.selectionEnd, next = beside(own, el, 1), t = own.typed;
    if (c === ":" && next) el = caret(next, 0);
    if (!/\d/.test(c)) continue;
    if (t && t[0] === el && t[2] === v && t[3] === a && a === b) v = t[1];
    if (a === b && v.length > 1) {
      if (a < v.length) b++;
      else if (!next) continue;
      else {
        v = (el = caret(next, 0)).value;
        a = 0;
        b = v.length > 1 ? 1 : 0;
      }
    }
    put(own, el, v.slice(0, a) + c + v.slice(b), a + 1);
  }
}

// erase deletes as Backspace (back) or Delete does in one field: the selection, else the digit before
// the caret (after it), or, a word (Ctrl, Alt, Cmd with it), every digit of the segment before the
// caret (after it); at the start of a segment the caret goes back into the one before and takes from
// its end, at its end on into the next and takes from its start
function erase(own, el, back, word) {
  var a = el.selectionStart, b = el.selectionEnd, other = beside(own, el, back ? -1 : 1);
  if (a === b) {
    if (back ? !a : a === el.value.length) {
      if (!other) return;
      a = b = (el = caret(other, back ? -1 : 0)).selectionStart;
    }
    if (back) a = word ? 0 : Math.max(0, a - 1);
    else b = word ? el.value.length : b + 1;
  }
  put(own, el, el.value.slice(0, a) + el.value.slice(b), a);
}

// step is ArrowUp, ArrowDown: the part one more or less, held in its range (no wrap), in as many digits
// as the segment held, the caret at its end
function step(own, el, by) {
  var name = el.getAttribute("data-part");
  put(own, el, String(clamp(own, name, own[name] + by)).padStart(Math.min(el.value.length, 2), "0"), 2);
}

// wide is text with the digits and the colon of a full-width input method (３, ：, what a Japanese or a
// Chinese one commits by default) as a keyboard types them: a field of digits takes them
function wide(text) {
  return String(text || "").replace(/[\uff10-\uff1a]/g, function (c) { return String.fromCharCode(c.charCodeAt(0) - 65248); });
}

// take is text put in a segment (a key's, a paste, a drop, what a composition commits, the composition
// first taken back to the text and the caret it began on): a time (1:30) sets the whole time, read from
// the right (its last part is the last segment, a part missing is 0, one the bar has no segment for
// makes it the most it shows); any other is typed
function take(own, el, text) {
  var ime = own.ime, all = segments(own), parts = (text = wide(text)).split(":").map(function (p) { return p.replace(/\D/g, ""); });
  var over = parts.length - all.length, most = parts.slice(0, over > 0 ? over : 0).some(Number);
  own.ime = null;
  if (ime) {
    el["value"] = ime[0];
    el.setSelectionRange(ime[1], ime[2]);
  }
  if (parts.length < 2 || !/\d/.test(text)) return type(own, el, text);
  all.forEach(function (seg, i) { part(own.scope, seg.getAttribute("data-part"), most ? Infinity : parts[i + over] || 0); });
  caret(el, -1);
}

var countdownDef = {
  duration: 0,
  remaining: 0,
  get hours() { return part(this, "hours"); },
  set hours(value) { part(this, "hours", value); },
  get minutes() { return part(this, "minutes"); },
  set minutes(value) { part(this, "minutes", value); },
  get seconds() { return part(this, "seconds"); },
  set seconds(value) { part(this, "seconds", value); },
  playing: false,

  play: function () {
    var own = this[COUNTDOWN], full = whole(this.duration);
    own.onward = false;
    if (own.since || own.gone || !full) return;
    if (own.from <= 0) own.from = full; // the end was reached: a new run
    show(this, own.from);
    // a segment being edited holds the run as it would hold one already playing: it plays on once the
    // edit is over, from the time edited
    if (own.edited) return void (own.onward = true);
    own.since = { wall: Date.now(), page: performance.now() };
    this.playing = true;
    schedule(this);
  },
  pause: function () {
    var own = this[COUNTDOWN];
    own.onward = false;
    if (!own.since) return;
    var ms = left(own);
    // the time ran out before its timer came: the run is over, and its end is the clock's to send (the
    // timer is due, or the page coming back in sight reads the clocks), never a method's
    if (ms <= 0) return;
    own.from = ms;
    own.since = null;
    schedule(this);
    show(this, ms);
    this.playing = false;
  },
  toggle: function () {
    if (this.playing) this.pause();
    else this.play();
  },
  stop: function () {
    var own = this[COUNTDOWN];
    own.since = own.onward = null;
    own.from = whole(this.duration);
    schedule(this);
    show(this, own.from);
    this.playing = false;
  },

  init: function (context) {
    var scope = this;
    // from is what is left when not playing, and what was left when the run went on while playing
    // (since: when, on each clock); scope is the kernel's scope, which a write of a part goes through;
    // edited is the segment an edit is in, onward whether it plays on once the edit is over, press whether
    // the mouse is down, ime the text and the caret a composition that waits to be read began on, prior
    // the values change is told against (one a segment), wrote the parts an edit wrote, typed the text
    // last typed in a segment, away the tabindex each segment had before Enter or Escape
    var own = scope[COUNTDOWN] = { context: context, scope: scope, from: whole(scope.duration), since: null, timer: 0, gone: false, hours: 0, minutes: 0, seconds: 0, edited: null };
    show(scope, own.from); // where the server's first paint starts: all of it
    // hidden: the tick chain goes and one timer is set to the end; in sight again, or back from the
    // history cache, the clocks are read again
    var again = function () { schedule(scope); };
    context.listen(document, "visibilitychange", again);
    context.listen(window, "pageshow", again);
    // the mouse (a finger's tap is a mouse press too) down anywhere: an edit its focus ends plays on
    // after its release, once the click the release makes has run (a timer: the click comes first). A
    // press that becomes a drag (a link, a picture) is let go with no mouseup: its dragend is the release.
    context.listen(document, "mousedown", function () {
      own.press = true;
      rejoin(own);
    }, true);
    var release = function () {
      own.press = false;
      if (own.onward) setTimeout(resume, 0, own);
    };
    context.listen(document, "mouseup", release, true);
    context.listen(document, "dragend", release, true);
    // the focus going anywhere after Enter or Escape: the segments are in the order of Tab again
    context.listen(document, "focusin", function () { rejoin(own); }, true);
    // the segments: each event read off the host, for the segment it came from
    var on = function (type, fn) {
      context.listen(context.host, type, function (e) {
        var el = segment(own, e.target);
        if (el) fn(e, el);
      });
    };
    on("focusin", function (e, el) { begin(own, el); });
    // a press on the bar beside its segments (its padding, a ":") puts the caret in the nearest one, at
    // its nearer end, as a field takes a press anywhere on it; the focus does not leave the bar
    context.listen(context.host, "mousedown", function (e) {
      var all = segments(own), el = all[0], bar = el && el.closest("time");
      if (!bar || !bar.contains(e.target) || segment(own, e.target)) return;
      e.preventDefault();
      all.forEach(function (seg) { if (seg.getBoundingClientRect().left < e.clientX) el = seg; });
      var box = el.getBoundingClientRect();
      caret(el, e.clientX < box.left + box.width / 2 ? 0 : -1);
    });
    // the focus leaving a segment: two digits again, change when its value is not what it was (as a
    // text field's), the edit over once it leaves the bar. The window going away (another tab, another
    // app) takes the focus off it but leaves it the page's, to give back: the edit goes on, held.
    on("focusout", function (e, el) {
      if (document.activeElement === el) return;
      write(own, el);
      tell(own, el);
      if (!segment(own, e.relatedTarget)) done(own);
    });
    // the browser's own change (sent after a composition it put in) is the one above, sent once
    on("change", function (e) {
      if (e.isTrusted) e.stopPropagation();
    });
    // the keys of one field across the bar: the arrows over a segment's edge go on into the next one,
    // Home and End to the bar's ends (with Shift they select, in the segment, as the browser does)
    on("keydown", function (e, el) {
      var key = e.key, all = segments(own), a = el.selectionStart, by = key === "ArrowRight" ? 1 : key === "ArrowLeft" ? -1 : 0;
      if (key === "ArrowUp" || key === "ArrowDown") {
        e.preventDefault();
        begin(own, el);
        step(own, el, key === "ArrowUp" ? 1 : -1);
      } else if (key === "Backspace" || key === "Delete") {
        e.preventDefault();
        begin(own, el);
        erase(own, el, key === "Backspace", e.ctrlKey || e.altKey || e.metaKey);
      } else if (by && !e.shiftKey && a === el.selectionEnd && a === (by > 0 ? el.value.length : 0) && beside(own, el, by)) {
        e.preventDefault();
        caret(beside(own, el, by), by > 0 ? 0 : -1);
      } else if (!e.shiftKey && (key === "Home" || key === "End")) {
        e.preventDefault();
        caret(key === "Home" ? all[0] : all[all.length - 1], key === "Home" ? 0 : -1);
      } else if (key === "Enter" || key === "Escape") {
        e.preventDefault();
        if (key === "Escape" && own.edited) {
          // back to the time the edit began with, to the millisecond
          own.from = own.before[0];
          scope.duration = own.before[1];
          show(scope, own.from);
        }
        // the browser goes on from the segment the focus left, and Tab would go into the next segment,
        // a new edit, held again: the segments are out of the order of Tab until the focus goes anywhere
        // (rejoin), so Tab goes on past the bar, Shift+Tab before it
        own.away = all.map(function (seg) {
          var was = seg.getAttribute("tabindex");
          seg.setAttribute("tabindex", -1);
          return was;
        });
        el.blur();
      }
    });
    // what the browser would put in: never in, read instead (a digit typed, a paste or a drop: take; a
    // deletion a phone's keyboard sends with no key named: erase). A composition (an input method, a
    // phone's keyboard) cannot be held back: what it shows as it composes stays, and what it commits is
    // read, once, at the caret it began at: as a cancelable insertText when the browser sends one, else
    // at its end. Read as it composes, each digit would go in again, and 3 then 5 would be 335.
    var composed = function (e) { return e.isComposing || e.inputType === "insertCompositionText"; };
    on("compositionstart", function (e, el) {
      begin(own, el);
      own.ime = [el.value, el.selectionStart, el.selectionEnd];
    });
    on("compositionend", function (e, el) {
      if (own.ime) take(own, el, e.data);
    });
    on("beforeinput", function (e, el) {
      if (composed(e)) return;
      e.preventDefault();
      begin(own, el);
      if (/^delete/.test(e.inputType)) erase(own, el, /Backward/.test(e.inputType), /Word|Line/.test(e.inputType));
      else take(own, el, e.data || (e.dataTransfer ? e.dataTransfer.getData("text") : ""));
    });
    // an input no beforeinput came before (a browser that sends none): its first two digits
    on("input", function (e, el) {
      if (!composed(e)) put(own, el, wide(el.value).replace(/\D/g, "").slice(0, 2), el.selectionStart);
    });
    context.cleanup(function () {
      own.gone = true;
      clearTimeout(own.timer);
    });
  }
};

window.kitwork.component("countdown", countdownDef);
