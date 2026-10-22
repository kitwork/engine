package js

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/minifier"
)

// The host the Kitwork UI engine writes for <countdown as="$tea" duration="5m">: the box, its name and
// alias, the duration in milliseconds, and the <time> bound to the parts.
const countdownHost = `<div data-kitwork-component="countdown: $tea" data-kitwork-scope="duration: 300000">` +
	`<time role="timer" data-kitwork-bind="text: minutes + ':' + seconds; datetime: 'PT' + hours + 'H' + minutes + 'M' + seconds + 'S'"></time>`

func TestCountdownIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<html><head></head><body>` + countdownHost + `</div></body></html>`)
	if !strings.Contains(out, `/_kitwork/components/countdown.`) {
		t.Fatalf("countdown was not delivered for markup that uses it:\n%s", out)
	}
	if src := ModulesJS([]string{"countdown"}); !strings.Contains(src, `component("countdown", `) || strings.Contains(src, `component("countdown@`) {
		t.Error("the file must register the bare name, and only it")
	}
}

// The server reads the countdown's names off its source: the contract's state and methods, so a page
// that reads them is told nothing (duration on the host is a parameter it has), and none of the words
// the contract refuses, so `$tea.left` or `paus()` is still named at render. The private slots are
// written on the Symbol's object, and their names are the ones a scan of `.x =` picks up: none may be a
// word a countdown author would reach for.
//
// CONTROL, against copies of v1.0.0.js served through -overlay: with `duration: 0,` taken out of the
// blueprint, the stray-parameter warning fired on data-kitwork-scope (the test failed); with the private
// slot `from` renamed `left`, "left" was read as a name and $tea.left went unreported (the test failed).
func TestCountdownAnswersItsNamesAndOnlyThose(t *testing.T) {
	names, ok := ComponentNames(nil)("countdown: $tea")
	if !ok {
		t.Fatal("countdown's names cannot be read, so nothing written against it is checked")
	}
	for _, want := range []string{"duration", "remaining", "hours", "minutes", "seconds", "playing", "play", "pause", "toggle", "stop"} {
		if !contains(names, want) {
			t.Errorf("%q missing from the countdown's names %v", want, names)
		}
	}
	for _, refused := range []string{"left", "time", "finish", "resume", "start", "reset", "restart", "cancel", "paused", "tick", "end", "interval", "format", "autoplay", "until"} {
		if contains(names, refused) {
			t.Errorf("%q is read as a countdown name (%v): a page writing it would hear nothing", refused, names)
		}
	}

	page := func(inside, outside string) string {
		return `<html><body>` + countdownHost + inside + `</div>` + outside + `</body></html>`
	}
	check := func(html string) string { return capturedCheck(t, html, ComponentNames(nil)) }
	if report := check(page(
		`<button data-kitwork-event="click: toggle()" data-kitwork-bind="text: playing ? 'Tạm dừng' : 'Chạy'"></button><button data-kitwork-event="click: stop()">Dừng</button>`,
		`<button data-kitwork-event="click: $tea.pause()">Tạm dừng</button><b data-kitwork-bind="text: $tea.remaining / 1000"></b>`)); report != "" {
		t.Errorf("the contract's own names, and duration on the host, should be told nothing, got:\n%s", report)
	}
	if report := check(page(``, `<b data-kitwork-bind="text: $tea.left"></b>`)); !strings.Contains(report, "$tea.left") {
		t.Errorf("$tea.left should be named, the countdown has no left, got:\n%s", report)
	}
	if report := check(page(`<button data-kitwork-event="click: paus()">x</button>`, ``)); !strings.Contains(report, `"paus"`) {
		t.Errorf("paus() inside the host should be named, got:\n%s", report)
	}
}

// The file is delivered minified in production, and the minifier returns its input unchanged when it
// does not parse: the countdown must come out shorter, still registering itself. It is a file of its
// own (the kernel's budget does not carry it), kept small anyway: a clock, four methods, one event, and
// the time's segments typed into as a native time field is (20/10/2026, evening: 1 KiB became 2 KiB,
// the segments' keys, focus and words taking 960 bytes of gzip, 1944 in all; the review that night
// made it 2263: an edit the mouse ends playing on after its click, the window going away, a
// composition read once, a pasted time and change on a segment took 319, so the bound was 2.5 KiB; the
// bar of 20/10/2026, late evening, made it 2730: the caret running through the segments, the arrows,
// Home and End across them, Backspace and Delete across them, a digit flowing on, a press on the bar,
// a composition read at its caret, less the native time field's typing, so the bound was 3 KiB; the
// review of the bar, 21/10/2026, made it 3059: the text typed kept past a clamp, the parts kept over
// 24 hours while an edit lasts, change from every segment changed once the edit is over, a full-width
// input method's digits, a word deleted, a drag let go, the order of Tab after Enter, 329 bytes, so
// the bound is 3.25 KiB).
//
// CONTROL: against a copy of v1.0.0.js (-overlay) with a stray `)` in show(), the minifier gave the
// source back unchanged, 6775 bytes in and out (the test failed).
func TestCountdownMinifiesAndStaysSmall(t *testing.T) {
	if minifier.JS("var a  =  1;") == "var a  =  1;" {
		t.Skip("the stdminify build serves readable source")
	}
	source := ComponentSource("countdown", "")
	if source == "" {
		t.Fatal("no countdown in the catalogue")
	}
	minified := minifier.JS(source)
	if len(minified) >= len(source) || !strings.Contains(minified, `component("countdown",`) {
		t.Fatalf("the minifier did not take the countdown (%d bytes in, %d out): it does not parse", len(source), len(minified))
	}
	var out bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	_, _ = writer.Write([]byte(minified))
	_ = writer.Close()
	t.Logf("countdown: %d bytes minified, %d bytes gzip", len(minified), out.Len())
	if out.Len() > 3328 {
		t.Errorf("the countdown is %d bytes gzip, over 3.25 KiB: a clock, four methods and the bar of segments should not need that", out.Len())
	}
}

// countdownClock is the clock the browser proofs drive by hand: Date.now and performance.now each
// read with a shift of their own (travel moves both, as time passing does; a wall clock set back or a
// device asleep moves one), and document.hidden answered by the page (look). Under the harness's
// virtual time a real wait costs its length of the budget, so long stretches are travelled, not waited.
const countdownClock = `
var wallShift = 0, pageShift = 0, wallNow = Date.now, pageNow = performance.now.bind(performance), hidden = false;
Date.now = function () { return wallNow() + wallShift; };
performance.now = function () { return pageNow() + pageShift; };
function travel(ms) { wallShift += ms; pageShift += ms; }
Object.defineProperty(document, "hidden", { configurable: true, get: function () { return hidden; } });
Object.defineProperty(document, "visibilityState", { configurable: true, get: function () { return hidden ? "hidden" : "visible"; } });
function look(isHidden) { hidden = isHidden; document.dispatchEvent(new Event("visibilitychange")); }
function painted() { return Promise.resolve().then(function () {}); }
// finish is heard first, at the window, before any handler on the page can change what is drawn
var finishes = [];
window.addEventListener("finish", function (event) {
  finishes.push({ id: event.target.id, shown: event.target.querySelector("time").textContent, detail: event.detail });
}, true);
function finished(id) { return finishes.filter(function (f) { return f.id === id; }).length; }
`

// The clock, step by step on one countdown of a minute: ticks on the whole second, pause keeping the
// millisecond left, play after stop and after the end starting over, the larger of the two clocks
// counting (a wall clock set back adds nothing, a device asleep counts), a hidden page dropping its
// ticks, finish once when the end has passed on coming back in sight or from the history cache, painted
// before it is sent, never on stop(); and the parts of an hour and a half.
//
// CONTROL, each against a copy of v1.0.0.js served through -overlay, the test failing at the step
// named: pause() keeping what is shown (rounded up to the second) instead of what is left still showed
// 58000 at 700 ms ("57000 at 650 ms"); left() counting Date.now alone gave 3657000 when the wall clock
// went back an hour; counting performance.now alone missed the sleep (57000, not 52000); stop() leaving
// from as it was showed 58000 ("stop()"); schedule() ticking on a hidden page wrote 58000 while hidden;
// finish() emitting straight after its writes sent the event with the last paint still drawn
// ("59000|0:0:59|playing"); with no pageshow listener the end went unfinished ("back from the history
// cache"); play() without its new run after the end stayed at 0 ("play() after the end starts over").
func TestBrowserCountdownClock(t *testing.T) {
	runBrowserProof(t, "countdown clock", nil, `
<div id="tea" data-kitwork-component="countdown: $tea" data-kitwork-scope="duration: 60000">
  <time data-kitwork-bind="text: remaining + '|' + hours + ':' + minutes + ':' + seconds + '|' + (playing ? 'playing' : 'held')"></time>
</div>
<button id="play" data-kitwork-event="click: $tea.play()">play</button>
<button id="pause" data-kitwork-event="click: $tea.pause()">pause</button>
<button id="toggle" data-kitwork-event="click: $tea.toggle()">toggle</button>
<button id="stop" data-kitwork-event="click: $tea.stop()">stop</button>
<div id="long" data-kitwork-component="countdown: $long" data-kitwork-scope="duration: 5400000">
  <time data-kitwork-bind="text: remaining + '|' + hours + ':' + minutes + ':' + seconds + '|' + (playing ? 'playing' : 'held')"></time>
  <button id="long-play" data-kitwork-event="click: play()">play</button>
</div>
<script>`+countdownClock+`
(async function () {
  await wait(80);
  var shown = function (id) { return document.querySelector("#" + id + " time").textContent; };
  var click = function (id) { document.getElementById(id).click(); };
  var expect = function (id, want, step) { assert(shown(id) === want, step + ": want " + want + ", got " + shown(id)); };

  expect("tea", "60000|0:1:0|held", "the start: all of it, not playing");
  expect("long", "5400000|1:30:0|held", "an hour and a half's parts");

  click("play");
  expect("tea", "60000|0:1:0|playing", "play()");
  await wait(2050);
  expect("tea", "58000|0:0:58|playing", "two ticks, on the whole seconds");

  // 2350 ms in, 57650 left: pause keeps the 650, and the next second comes 650 ms after play
  travel(300);
  click("pause");
  expect("tea", "58000|0:0:58|held", "pause()");
  travel(10000); look(false); await painted();
  expect("tea", "58000|0:0:58|held", "paused, the time passing changes nothing");
  click("play");
  await wait(600);
  expect("tea", "58000|0:0:58|playing", "600 ms after play, 57050 left");
  await wait(100);
  expect("tea", "57000|0:0:57|playing", "57000 at 650 ms, the millisecond kept");

  // the wall clock set back an hour adds nothing; asleep five seconds (the wall moved, the page did not) counts
  wallShift -= 3600000; look(false); await painted();
  expect("tea", "57000|0:0:57|playing", "a wall clock set back an hour");
  wallShift += 3600000 + 5000; look(false); await painted();
  expect("tea", "52000|0:0:52|playing", "a device asleep five seconds");

  click("stop");
  expect("tea", "60000|0:1:0|held", "stop()");
  click("play");
  travel(1500); look(false); await painted();
  expect("tea", "59000|0:0:59|playing", "play() after stop() starts over");

  // hidden: no tick is written, the time goes on
  look(true);
  await wait(1100);
  expect("tea", "59000|0:0:59|playing", "a hidden page writes nothing");
  travel(70000); look(false); await painted();
  expect("tea", "0|0:0:0|held", "in sight again past the end");
  assert(finished("tea") === 1, "finish once on coming back in sight, got " + finished("tea"));
  assert(finishes[0].shown === "0|0:0:0|held", "finish is sent once the end is drawn, it saw " + finishes[0].shown);
  assert(finishes[0].detail === null, "finish carries nothing, got " + JSON.stringify(finishes[0].detail));
  look(false);
  window.dispatchEvent(new PageTransitionEvent("pageshow", { persisted: true }));
  await wait(50);
  assert(finished("tea") === 1, "finish is once a run, got " + finished("tea"));

  click("play");
  expect("tea", "60000|0:1:0|playing", "play() after the end starts over");
  click("toggle");
  expect("tea", "60000|0:1:0|held", "toggle() while playing pauses");
  click("toggle");
  expect("tea", "60000|0:1:0|playing", "toggle() while held plays");

  // stop() never finishes, even when the end has passed before a timer could say so
  travel(70000);
  click("stop");
  look(false);
  window.dispatchEvent(new PageTransitionEvent("pageshow", { persisted: true }));
  await wait(50);
  expect("tea", "60000|0:1:0|held", "stop() past the end");
  assert(finished("tea") === 1, "stop() sends no finish, got " + finished("tea"));

  // back from the history cache past the end: finish
  click("play");
  travel(70000);
  window.dispatchEvent(new PageTransitionEvent("pageshow", { persisted: true }));
  await painted();
  expect("tea", "0|0:0:0|held", "back from the history cache past the end");
  assert(finished("tea") === 2, "pageshow finishes a run whose end has passed, got " + finished("tea"));

  click("long-play");
  travel(3723000); look(false); await painted();
  expect("long", "1677000|0:27:57|playing", "an hour, two minutes and three seconds later");
  assert(finished("long") === 0, "the long one has not ended");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// In real time, on the clock the browser keeps: ready="play()" starts a countdown as it comes alive
// (the first proof of ready on a component host), its timer reaches the end and finish="play()" runs it
// again, once a run; a countdown removed while it plays (its region's if turns false) stops for good,
// its timer released with it.
//
// CONTROL, against copies of v1.0.0.js served through -overlay: with the cleanup in init emptied, the
// removed countdown's scope (held from kit.components) went on counting and ended, "2000 then 0" (the
// test failed; the kernel drops the finish of a host no longer in the document, so the count alone
// could not tell); with finish() sending its event twice, the repeat counted 4 (the test failed).
func TestBrowserCountdownReadyFinishAndRemoval(t *testing.T) {
	runBrowserProof(t, "countdown ready and finish", nil, `
<div id="again" data-kitwork-component="countdown: $again" data-kitwork-scope="duration: 1000" data-kitwork-event="ready: play(); finish: play()">
  <time data-kitwork-bind="text: remaining + '|' + (playing ? 'playing' : 'held')"></time>
</div>
<div data-kitwork-scope="on: true">
  <button id="remove" data-kitwork-event="click: on = false">remove</button>
  <div data-kitwork-if="on">
    <div id="gone" data-kitwork-component="countdown: $gone" data-kitwork-scope="duration: 2000" data-kitwork-event="ready: play()">
      <time data-kitwork-bind="text: remaining + '|' + (playing ? 'playing' : 'held')"></time>
    </div>
  </div>
</div>
<script>
var finishes = [];
window.addEventListener("finish", function (event) {
  finishes.push({ id: event.target.id, shown: event.target.querySelector("time").textContent });
}, true);
function finished(id) { return finishes.filter(function (f) { return f.id === id; }).length; }
(async function () {
  await wait(80);
  var shown = function (id) { var time = document.querySelector("#" + id + " time"); return time ? time.textContent : "(removed)"; };
  assert(shown("again") === "1000|playing", "ready=\"play()\" starts it, got " + shown("again"));
  assert(shown("gone") === "2000|playing", "ready=\"play()\" starts the second too, got " + shown("gone"));
  var instances = window.kit.components.countdown;
  assert(instances && instances.length === 2, "two countdowns are alive");
  var held = instances[1];

  await wait(420);
  document.getElementById("remove").click();
  assert(shown("gone") === "(removed)", "the if took the countdown out");
  var left = held.remaining;

  await wait(1600);
  assert(finished("again") === 2, "two runs of a second ended in 2.1 s, once each, got " + finished("again"));
  assert(finishes[0].shown === "0|held", "finish is sent once the end is drawn, it saw " + finishes[0].shown);
  assert(shown("again") === "1000|playing", "finish=\"play()\" ran it again, got " + shown("again"));
  assert(finished("gone") === 0, "a countdown removed says nothing");
  assert(held.remaining === left && held.remaining === 2000, "a countdown removed stops counting: " + left + " then " + held.remaining);

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// No method sends an event, at the end either: pause() or toggle() at a run whose time has run out
// before its timer said so (a device asleep, a timer late) does nothing, and the clock ends the run as
// it comes, the end painted then finish, once; stop() there sends nothing (TestBrowserCountdownClock).
//
// CONTROL, against a copy of v1.0.0.js served through -overlay with pause() ending the run itself (as on
// 19/10/2026: `if (ms <= 0) return finish(this);`), the click sent finish ("pause() sends nothing, got
// 1"; the test failed).
func TestBrowserCountdownPauseAtTheEndLeavesItToTheClock(t *testing.T) {
	runBrowserProof(t, "countdown pause at the end", nil, `
<div id="tea" data-kitwork-component="countdown: $tea" data-kitwork-scope="duration: 60000">
  <time data-kitwork-bind="text: remaining + '|' + hours + ':' + minutes + ':' + seconds + '|' + (playing ? 'playing' : 'held')"></time>
</div>
<button id="play" data-kitwork-event="click: $tea.play()">play</button>
<button id="pause" data-kitwork-event="click: $tea.pause()">pause</button>
<button id="toggle" data-kitwork-event="click: $tea.toggle()">toggle</button>
<script>`+countdownClock+`
(async function () {
  await wait(80);
  var shown = function () { return document.querySelector("#tea time").textContent; };
  var click = function (id) { document.getElementById(id).click(); };

  var runs = 0;
  for (const method of ["pause", "toggle"]) {
    click("play");
    assert(shown() === "60000|0:1:0|playing", "play(), got " + shown());
    travel(70000); // past the end, before a timer could say so
    click(method);
    await painted();
    assert(finished("tea") === runs, method + "() sends nothing, got " + (finished("tea") - runs));
    assert(shown() === "60000|0:1:0|playing", method + "() at the end holds nothing back, got " + shown());
    await wait(1100);
    runs++;
    assert(finished("tea") === runs, "the clock ends the run once, got " + finished("tea"));
    assert(finishes[runs - 1].shown === "0|0:0:0|held", "finish is sent once the end is drawn, it saw " + finishes[runs - 1].shown);
    assert(shown() === "0|0:0:0|held", "the end, got " + shown());
  }

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// A countdown inside a countdown keeps its own: its private slot (behind its Symbol) is its instance's,
// not the outer one's (the kernel's scope wrote a Symbol to the first scope around that held it, so the
// inner's init took the outer's: the outer painted 2000 and ran the inner's clock); its finish is heard
// on its own host only (the event bubbles, and the outer's finish="stop()" ran at the inner's end); and
// one removed (its region's if turns false) leaves the countdown around it counting to its own end.
//
// CONTROL, each against a copy of jit/hydrate/kernel.js served through -overlay: with the scope's set
// writing a Symbol through the chain as any name, the outer showed "2000|playing" at the start ("the
// outer starts at its own 4000"; the test failed); with a component's event heard by every host around
// it of its kind (no self), the outer was stopped at the inner's end ("4000|held" at 2.5 s, "the
// inner's finish is not the outer's"; the test failed).
func TestBrowserCountdownNestedKeepsItsOwn(t *testing.T) {
	runBrowserProof(t, "countdown nested", nil, `
<div id="outer" data-kitwork-component="countdown: $outer" data-kitwork-scope="duration: 4000" data-kitwork-event="ready: play(); finish: stop()">
  <time data-kitwork-bind="text: remaining + '|' + (playing ? 'playing' : 'held')"></time>
  <div id="inner" data-kitwork-component="countdown: $inner" data-kitwork-scope="duration: 2000" data-kitwork-event="ready: play()">
    <time data-kitwork-bind="text: remaining + '|' + (playing ? 'playing' : 'held')"></time>
  </div>
</div>
<div id="around" data-kitwork-component="countdown: $around" data-kitwork-scope="duration: 3000" data-kitwork-event="ready: play()">
  <time data-kitwork-bind="text: remaining + '|' + (playing ? 'playing' : 'held')"></time>
  <div data-kitwork-scope="on: true">
    <button id="remove" data-kitwork-event="click: on = false">remove</button>
    <div data-kitwork-if="on">
      <div id="gone" data-kitwork-component="countdown: $gone" data-kitwork-scope="duration: 1000" data-kitwork-event="ready: play()">
        <time data-kitwork-bind="text: remaining + '|' + (playing ? 'playing' : 'held')"></time>
      </div>
    </div>
  </div>
</div>
<script>
var finishes = [];
window.addEventListener("finish", function (event) { finishes.push(event.target.id); }, true);
function finished(id) { return finishes.filter(function (f) { return f === id; }).length; }
(async function () {
  await wait(80);
  var shown = function (id) { var time = document.querySelector("#" + id + " time"); return time ? time.textContent : "(removed)"; };
  assert(shown("outer") === "4000|playing", "the outer starts at its own 4000, got " + shown("outer"));
  assert(shown("inner") === "2000|playing", "the inner starts at its own 2000, got " + shown("inner"));
  assert(shown("around") === "3000|playing" && shown("gone") === "1000|playing", "the second pair starts, got " + shown("around") + " and " + shown("gone"));

  await wait(300);
  document.getElementById("remove").click();
  assert(shown("gone") === "(removed)", "the if took the inner countdown out");

  await wait(2120); // 2.5 s in
  assert(finished("inner") === 1, "the inner ends once at its 2 s, got " + finished("inner"));
  assert(finished("outer") === 0, "the outer has not ended at 2.5 s");
  assert(shown("outer") === "2000|playing", "the inner's finish is not the outer's: it plays on, got " + shown("outer"));
  assert(shown("around") === "1000|playing", "a countdown removed inside leaves the one around it counting, got " + shown("around"));

  await wait(2000); // 4.5 s in
  assert(finished("outer") === 1 && shown("outer") === "4000|held", "the outer ends at its own 4 s and its finish=\"stop()\" runs, got " + finished("outer") + " and " + shown("outer"));
  assert(finished("around") === 1, "the countdown around the removed one ends at its 3 s, got " + finished("around"));
  assert(finished("gone") === 0, "the removed one says nothing");
  assert(finished("inner") === 1, "the inner ended once");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// The time set by markup (20/10/2026): hours, minutes and seconds are written like a var, by a field
// two-way on one (typed: the kernel's input listener writes it), by an assignment and by ++. Step by
// step on one countdown of five minutes, the clock driven by hand: a write while stopped is the new full
// time (remaining and duration), play() starts from it and stop() returns to it; one while paused drops
// the pause; one while playing is what is left, counted on from it, duration kept; each part is a whole
// number clamped in its range as it is written (the decision of 20/10/2026, evening: a value above the
// max is never shown or counted), 75 seconds 59, a non-number or a negative the least, never carried to
// the next part, and the field shows what the part is (the kernel's field follows the scope); the total
// is capped at 24 hours, 24:00:00 and no second more; play() of a total of 0 does nothing; the end of a
// time set that way finishes once, and play() after it runs that time again. The fields are readonly
// while it plays (readonly: playing).
//
// CONTROL, each against a copy of v1.0.0.js served through -overlay, the test failing at the step
// named (run 20/10/2026): edit() leaving duration as the tag wrote it ("a typed minute sets the time":
// duration 300000, not 120000); edit() while playing setting duration too ("while playing, duration is
// kept": 224000, not 165000); the parts as plain state with no accessors (the blueprint of 19/10/2026),
// a typed minute set no time ("a typed minute sets the time": 300000|0:2:0). Run that evening: part()
// keeping the value as written (no clamp, as that morning) kept 75 seconds ("75 seconds are 59":
// 75000|0:0:75|held|75000); clamp() letting a non-number through (NaN, not the least) made the total
// NaN ("a non-number is the least": NaN|0:1:NaN|held|NaN); edit() without the cap of 24 hours counted
// 24:01:08 ("30 hours are 24, and the minute and second with them go": 86468000|24:1:8).
func TestBrowserCountdownTimeSetByItsParts(t *testing.T) {
	runBrowserProof(t, "countdown time set by its parts", nil, `
<div id="tea" data-kitwork-component="countdown: $tea" data-kitwork-scope="duration: 300000">
  <input id="minutes" type="number" data-kitwork-bind="value: minutes; readonly: playing">
  <input id="seconds" type="number" data-kitwork-bind="value: seconds; readonly: playing">
  <input id="free" type="text" data-kitwork-bind="value: seconds">
  <time data-kitwork-bind="text: remaining + '|' + hours + ':' + minutes + ':' + seconds + '|' + (playing ? 'playing' : 'held') + '|' + duration"></time>
  <button id="three" data-kitwork-event="click: minutes = 3">3</button>
  <button id="more" data-kitwork-event="click: seconds++">+</button>
  <button id="long" data-kitwork-event="click: hours = 30">30</button>
  <button id="none" data-kitwork-event="click: hours = 0">0</button>
</div>
<button id="play" data-kitwork-event="click: $tea.play()">play</button>
<button id="pause" data-kitwork-event="click: $tea.pause()">pause</button>
<button id="stop" data-kitwork-event="click: $tea.stop()">stop</button>
<script>`+countdownClock+`
(async function () {
  await wait(80);
  var shown = function () { return document.querySelector("#tea time").textContent; };
  var click = function (id) { document.getElementById(id).click(); };
  var field = function (id) { return document.getElementById(id); };
  // type puts what a person types in a field: its value, then the input event the browser sends
  var type = function (id, text) { var el = field(id); el.value = text; el.dispatchEvent(new Event("input", { bubbles: true })); };
  var fields = function () { return field("minutes").value + ":" + field("seconds").value; };
  var expect = function (want, step) { assert(shown() === want, step + ": want " + want + ", got " + shown()); };

  expect("300000|0:5:0|held|300000", "the start: the tag's five minutes");
  assert(fields() === "5:0", "the fields start at the parts, got " + fields());

  type("minutes", "2");
  expect("120000|0:2:0|held|120000", "a typed minute sets the time");
  type("seconds", "30");
  expect("150000|0:2:30|held|150000", "a typed second too");
  assert(fields() === "2:30", "what was typed stays, got " + fields());
  click("play");
  expect("150000|0:2:30|playing|150000", "play() starts from the time set");
  assert(field("minutes").readOnly && field("seconds").readOnly, "the fields are readonly while it plays");
  travel(1500); look(false); await painted();
  expect("149000|0:2:29|playing|150000", "it counts from the time set");
  assert(fields() === "2:29", "the fields follow the count, got " + fields());
  click("stop");
  expect("150000|0:2:30|held|150000", "stop() returns to the time set");
  assert(!field("minutes").readOnly, "stopped, the fields are editable again");

  // paused, a write drops the pause: the new time replaces what was held
  click("play");
  travel(10300); look(false); await painted();
  click("pause");
  expect("140000|0:2:20|held|150000", "pause() holds 2:19.7");
  type("seconds", "45");
  expect("165000|0:2:45|held|165000", "a write while paused is the new full time");
  click("play");
  travel(1000); look(false); await painted();
  expect("164000|0:2:44|playing|165000", "play() after it starts from it, not from what was held");

  // playing (a write the readonly fields would not let a person make): what is left, counted on
  click("three");
  expect("224000|0:3:44|playing|165000", "while playing, duration is kept: 3:44 left");
  travel(1000); look(false); await painted();
  expect("223000|0:3:43|playing|165000", "the count goes on from what was written");
  click("stop");
  expect("165000|0:2:45|held|165000", "stop() returns to the full time, not to what was written while playing");

  // out of range: clamped as it is written, never above the max, never carried
  type("minutes", "0");
  type("seconds", "75");
  expect("59000|0:0:59|held|59000", "75 seconds are 59");
  assert(fields() === "0:59", "the field shows the part, never above its max, got " + fields());
  click("play");
  expect("59000|0:0:59|playing|59000", "play() counts from 59 seconds");
  click("stop");
  type("minutes", "75");
  expect("3599000|0:59:59|held|3599000", "75 minutes are 59");

  // a non-number and a negative are the least, the field showing it
  type("minutes", "1");
  type("free", "abc");
  expect("60000|0:1:0|held|60000", "a non-number is the least");
  assert(field("free").value === "0", "the text field shows the part, got " + field("free").value);
  type("free", "-5");
  expect("60000|0:1:0|held|60000", "a negative is the least");
  type("free", "7.9");
  expect("67000|0:1:7|held|67000", "a part is a whole number");
  click("more");
  expect("68000|0:1:8|held|68000", "seconds++ sets the time");

  // at most 24 hours
  click("long");
  expect("86400000|24:0:0|held|86400000", "30 hours are 24, and the minute and second with them go");
  click("more");
  expect("86400000|24:0:0|held|86400000", "at 24 hours a second more is no more");
  click("stop");
  expect("86400000|24:0:0|held|86400000", "stop() returns to 24 hours");

  // nothing to count: play() does nothing
  click("none");
  expect("0|0:0:0|held|0", "a total of 0");
  click("play");
  expect("0|0:0:0|held|0", "play() of a total of 0 does nothing");

  // the end of a time set by markup: finish once, and play() runs that time again
  type("seconds", "2");
  expect("2000|0:0:2|held|2000", "two seconds");
  click("play");
  travel(2500); look(false); await painted();
  expect("0|0:0:0|held|2000", "the end");
  assert(finished("tea") === 1, "finish once, got " + finished("tea"));
  assert(finishes[0].shown === "0|0:0:0|held|2000", "finish is sent once the end is drawn, it saw " + finishes[0].shown);
  click("play");
  expect("2000|0:0:2|playing|2000", "play() after the end runs the time set again");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// A write sends no event and never brings back a run that is over (the review of 20/10/2026), the clock
// driven by hand. A total of 0 written while playing (a field with no readonly, emptied to type another
// number) stops the run there and sends nothing, duration kept; no timer is left to end it later, and
// play() then runs all of duration again. A part written at a run whose time ran out before its timer
// said so (a device asleep, a timer late) changes nothing, as pause() there: the timer comes and ends the
// run, the end painted then finish, once.
//
// CONTROL, each against a copy of v1.0.0.js served through -overlay, the test failing at the step named
// (run 20/10/2026): edit() ending the run itself at a total of 0 (schedule() straight to finish(), as
// built earlier that day) sent finish from the write ("a total of 0 written while playing sends nothing,
// got 1"); part() writing at a run past its end (no check of left()) brought it back, 3:03 counted on
// from the write ("a write past the end changes nothing": 183000|0:3:3|playing|3000).
func TestBrowserCountdownWritesSendNothing(t *testing.T) {
	runBrowserProof(t, "countdown writes send nothing", nil, `
<div id="tea" data-kitwork-component="countdown: $tea" data-kitwork-scope="duration: 300000">
  <input id="minutes" type="number" data-kitwork-bind="value: minutes">
  <input id="seconds" type="number" data-kitwork-bind="value: seconds">
  <time data-kitwork-bind="text: remaining + '|' + hours + ':' + minutes + ':' + seconds + '|' + (playing ? 'playing' : 'held') + '|' + duration"></time>
  <button id="three" data-kitwork-event="click: minutes = 3">3</button>
</div>
<button id="play" data-kitwork-event="click: $tea.play()">play</button>
<button id="stop" data-kitwork-event="click: $tea.stop()">stop</button>
<script>`+countdownClock+`
(async function () {
  await wait(80);
  var shown = function () { return document.querySelector("#tea time").textContent; };
  var click = function (id) { document.getElementById(id).click(); };
  var type = function (id, text) { var el = document.getElementById(id); el.value = text; el.dispatchEvent(new Event("input", { bubbles: true })); };
  var expect = function (want, step) { assert(shown() === want, step + ": want " + want + ", got " + shown()); };

  type("minutes", "0");
  type("seconds", "3");
  expect("3000|0:0:3|held|3000", "three seconds set");
  click("play");
  travel(1200); look(false); await painted();
  expect("2000|0:0:2|playing|3000", "playing, 1.8 s left");

  // a total of 0 written while playing: the run stops there, nothing sent, duration kept
  type("seconds", "0");
  await painted();
  expect("0|0:0:0|held|3000", "a total of 0 written while playing stops the run");
  assert(finished("tea") === 0, "a total of 0 written while playing sends nothing, got " + finished("tea"));
  travel(5000); await wait(1100);
  assert(finished("tea") === 0, "no timer is left to end it later, got " + finished("tea"));
  expect("0|0:0:0|held|3000", "it stays stopped");
  click("play");
  expect("3000|0:0:3|playing|3000", "play() after it runs all of duration again");

  // a part written past the end, before a timer could say so: nothing changes, the clock ends the run
  travel(70000);
  click("three");
  await painted();
  expect("3000|0:0:3|playing|3000", "a write past the end changes nothing");
  assert(finished("tea") === 0, "the write sends nothing, got " + finished("tea"));
  await wait(1100);
  assert(finished("tea") === 1, "the clock ends the run once, got " + finished("tea"));
  assert(finishes[0].shown === "0|0:0:0|held|3000", "finish is sent once the end is drawn, it saw " + finishes[0].shown);
  expect("0|0:0:0|held|3000", "the end");
  click("three");
  expect("180000|0:3:0|held|180000", "after the end, a write sets the new full time");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// countdownSegment is a segment of the time as the Kitwork UI engine writes it (ui/countdown.go,
// countdownSegmentWords): a text field of digits, its part, its range, the placeholder 00 and the value
// it starts at, in two digits, and no binding: the countdown owns it.
func countdownSegment(id, part, high, now, text, said string) string {
	return `<input id="` + id + `" aria-label="` + part + `" role="spinbutton" inputmode="numeric" autocomplete="off" spellcheck="false" size="2" placeholder="00"` +
		` aria-valuemin="0" aria-valuemax="` + high + `" aria-valuenow="` + now + `" aria-valuetext="` + said + `" data-part="` + part + `" value="` + text + `">`
}

// countdownSegmentState is what a countdown's state reads, drawn by the kernel beside its segments.
const countdownSegmentState = `data-kitwork-bind="text: remaining + '|' + hours + ':' + minutes + ':' + seconds + '|' + (playing ? 'playing' : 'held') + '|' + duration"`

// countdownCaret are the helpers of the proofs of the bar: the focus put in a segment with the caret at
// a place (at), where the focus and the caret are (where: "s 1-1", "body"), what the segments show
// (shown), keys typed into what has the focus, each character a beforeinput as a keyboard sends it
// (keys), a key pressed (press, the event given back: a synthetic key does nothing of its own, so a key
// the countdown leaves alone is one it did not prevent).
const countdownCaret = `
  var el = function (id) { return document.getElementById(id); };
  var state = function (id) { return el(id || "state").textContent; };
  var shown = function () { return Array.prototype.map.call(arguments, function (id) { return el(id).value; }).join(":"); };
  var at = function (id, a, b) { el(id).focus(); el(id).setSelectionRange(a, b == null ? a : b); };
  var where = function () { var a = document.activeElement; return a === document.body ? "body" : a.id + " " + a.selectionStart + "-" + a.selectionEnd; };
  var keys = function (text) {
    for (const d of text) document.activeElement.dispatchEvent(new InputEvent("beforeinput", { inputType: "insertText", data: d, bubbles: true, cancelable: true }));
  };
  var press = function (name, extra) { return key(document.activeElement, name, extra); };
  var expect = function (got, want, step) { assert(got === want, step + ": want " + want + ", got " + got); };
`

// The time's segments as one bar (the decisions of 20/10/2026, evening and late evening: "count down
// tôi muốn nó là một thanh dài … placeholder là 00:00:00 … được điền vào như một input bình thường …
// không quá 60", then "đồng hồ cần nhận con trỏ"), the clock driven by hand: the fields in the <time>
// are the clock, written by the countdown in two digits (05:00, 02:00:00), and typed into as one
// field: the focus selects nothing, the caret stays where it was put; what is typed shows as typed, no
// leading zero asked for (5 is 5, an emptied segment shows its placeholder and is 0), and in two
// digits once the edit ends (05); ":" moves the caret to the next segment; a digit at the end of a full
// segment flows into the next one, inside a full one it takes the place of the digit after the caret,
// at the end of the bar it is refused, and so is anything not a digit; a part is never above its max
// (65 is 59, 75 minutes 59, 35 over a max of 30 is 30, 30 hours 24, and 24 hours take no minute);
// ArrowRight at the end of a segment and ArrowLeft at its start go on into the next or back into the
// one before, and inside a segment are left to the browser; Home and End go to the bar's ends;
// Backspace at the start of a segment takes the last digit of the one before, Delete at its end the
// first of the next; ArrowUp and ArrowDown step without wrap, in the digits the segment held; an input
// the browser could not be held back from is read for its digits. Not playing, an edit is the full
// time; Escape puts back the time the edit began with and takes the focus off. Playing, focusing a
// segment pauses it to the millisecond, the typed value is held, duration kept, Enter plays on from
// it, the segments and their aria-valuenow and aria-valuetext following the count; Escape puts back
// what was held, to the millisecond, and plays on; while a segment is edited the clock never writes it
// (a play() from elsewhere holds as the run it starts would, and plays once the edit is over); a total
// of 0 edited while it played stops the run there, no finish, and play() runs all of duration again.
// minutes++ and minutes-- write a part, clamped at 59; the words are the page's language (1 minute, 23
// hours).
//
// CONTROL, each against a copy of v1.0.0.js served through -overlay, the test failing at the step
// named (run 20/10/2026, late evening): begin() selecting the segment again (as before the bar) gave
// "m 0-2" ("the focus selects nothing"); put() writing the part in two digits over what was typed showed
// 08:00 ("what is typed shows as typed"); type() without the flow kept 12:00, the caret at m 2-2 ("a
// digit at the end of a full segment flows into the next"); the keydown without its arrows across the
// bar left the caret at m 2-2 ("ArrowRight at the end of a segment"); erase() without crossing into the
// segment before left 59:59, the caret at s 0-0 ("Backspace at the start of a segment"); put() showing
// what was typed whatever the part took showed 42:65 ("65 is 59"); begin() without pause() let it count
// under the fingers ("focusing a segment pauses it there": …|playing); the Escape branch without its
// restore kept what was typed, 06:03 ("Escape puts back the time the edit began with"); done() without
// its resume left it held ("Enter plays on from the typed value": …|held); edit() without the cap of
// 24 hours counted 24:5:00, 86700000 ("24 hours take no minute").
func TestBrowserCountdownSegments(t *testing.T) {
	runBrowserProof(t, "countdown segments", nil, `
<div id="tea" lang="vi" data-kitwork-component="countdown: $tea" data-kitwork-scope="duration: 300000">
  <time role="group" aria-label="Thời gian còn lại" data-kitwork-bind="datetime: 'PT' + hours + 'H' + minutes + 'M' + seconds + 'S'">`+
		countdownSegment("m", "minutes", "59", "5", "05", "5 phút")+`:`+countdownSegment("s", "seconds", "59", "0", "00", "0 giây")+`</time>
  <b id="state" `+countdownSegmentState+`></b>
  <button id="more" data-kitwork-event="click: minutes++">+1</button>
  <button id="less" data-kitwork-event="click: minutes--">-1</button>
</div>
<button id="play" data-kitwork-event="click: $tea.play()">play</button>
<button id="stop" data-kitwork-event="click: $tea.stop()">stop</button>
<div id="short" data-kitwork-component="countdown: $short" data-kitwork-scope="duration: 600000">
  <time>`+countdownSegment("n", "minutes", "30", "10", "10", "10 phút")+`:`+countdownSegment("ns", "seconds", "59", "0", "00", "0 giây")+`</time>
</div>
<div id="day" lang="en" data-kitwork-component="countdown: $day" data-kitwork-scope="duration: 7200000">
  <time>`+countdownSegment("h", "hours", "24", "2", "02", "2 hours")+`:`+countdownSegment("dm", "minutes", "59", "0", "00", "0 minutes")+`:`+
		countdownSegment("ds", "seconds", "59", "0", "00", "0 seconds")+`</time>
  <b id="day-state" `+countdownSegmentState+`></b>
</div>
<script>`+countdownClock+`
(async function () {
  await wait(80);`+countdownCaret+`
  expect(shown("m", "s") + " " + state(), "05:00 300000|0:5:0|held|300000", "the start, as the server wrote it");
  expect(shown("h", "dm", "ds"), "02:00:00", "hours in two digits too");

  // the focus selects nothing: the caret stays where it was put
  el("m").setSelectionRange(1, 1);
  el("m").focus();
  expect(where(), "m 1-1", "the focus selects nothing");

  // what is typed shows as typed; an emptied segment is 0; two digits once the edit ends
  at("m", 0, 2);
  keys("8");
  await painted();
  expect(shown("m", "s") + " " + where() + " " + state(), "8:00 m 1-1 480000|0:8:0|held|480000", "what is typed shows as typed");
  press("Backspace");
  await painted();
  expect("[" + shown("m") + "] " + where() + " " + state(), "[] m 0-0 0|0:0:0|held|0", "an emptied segment is 0, its placeholder shown");
  keys("5");
  await painted();
  expect(shown("m", "s") + " " + state(), "5:00 300000|0:5:0|held|300000", "5 stays 5 while it is typed");
  press("Enter");
  await painted();
  expect(shown("m", "s") + " " + where(), "05:00 body", "and is 05 once Enter ends the edit");

  // ":" moves on; a digit at the end of a full segment flows into the next, inside one it takes the
  // place of the digit after the caret, at the end of the bar it is refused, and so is a letter
  at("m", 2);
  keys(":");
  expect(where(), "s 0-0", "\":\" moves the caret to the start of the next segment");
  at("m", 0, 2);
  keys("12");
  expect(shown("m", "s") + " " + where(), "12:00 m 2-2", "two digits typed");
  keys("3");
  expect(shown("m", "s") + " " + where(), "12:30 s 1-1", "a digit at the end of a full segment flows into the next");
  keys("45");
  await painted();
  expect(shown("m", "s") + " " + where() + " " + state(), "12:34 s 2-2 754000|0:12:34|held|754000", "inside a full segment a digit takes the place of the next; at the end of the bar it is refused");
  keys("x:");
  expect(shown("m", "s") + " " + where(), "12:34 s 2-2", "a letter is refused, and \":\" in the last segment goes nowhere");
  at("m", 0);
  keys("4");
  expect(shown("m", "s") + " " + where(), "42:34 m 1-1", "the first digit of a full segment, replaced");

  // never above the max
  at("s", 0, 2);
  keys("6");
  keys("5");
  await painted();
  expect(shown("m", "s") + " " + where() + " " + state(), "42:59 s 2-2 2579000|0:42:59|held|2579000", "65 is 59");
  at("m", 0, 2);
  keys("75");
  expect(shown("m", "s") + " " + where(), "59:59 m 2-2", "75 minutes are 59: without hours the time stops at 59:59");

  // the arrows across the bar, Home and End
  press("ArrowRight");
  expect(where(), "s 0-0", "ArrowRight at the end of a segment goes on to the start of the next");
  press("ArrowLeft");
  expect(where(), "m 2-2", "ArrowLeft at the start of a segment goes back to the end of the one before");
  at("m", 1);
  var left = press("ArrowRight");
  expect(where() + " " + left.defaultPrevented, "m 1-1 false", "inside a segment the arrows are the browser's");
  at("s", 2);
  left = press("ArrowRight");
  expect(where() + " " + left.defaultPrevented, "s 2-2 false", "ArrowRight at the end of the bar goes nowhere");
  press("Home");
  expect(where(), "m 0-0", "Home goes to the bar's start");
  press("End");
  expect(where(), "s 2-2", "End goes to the bar's end");

  // Backspace and Delete across the bar
  at("s", 0);
  press("Backspace");
  await painted();
  expect(shown("m", "s") + " " + where() + " " + state(), "5:59 m 1-1 359000|0:5:59|held|359000", "Backspace at the start of a segment takes the last digit of the one before");
  press("Delete");
  await painted();
  expect(shown("m", "s") + " " + where() + " " + state(), "05:9 s 0-0 309000|0:5:9|held|309000", "Delete at the end of a segment takes the first digit of the next");
  at("s", 1);
  press("Backspace");
  expect("[" + shown("s") + "] " + where(), "[] s 0-0", "Backspace inside a segment, as in any field");

  // ArrowUp and ArrowDown step without wrap, in the digits the segment held
  press("ArrowDown");
  expect("[" + shown("s") + "]", "[0]", "ArrowDown at the least: no wrap");
  press("ArrowUp"); press("ArrowUp");
  expect(shown("s") + " " + where(), "2 s 1-1", "ArrowUp steps by one, in the digits the segment held");
  at("s", 0, 2);
  keys("59");
  press("ArrowUp");
  expect(shown("s"), "59", "ArrowUp at the max: no wrap");
  at("m", 0);
  press("ArrowUp");
  expect(shown("m"), "06", "a segment of two digits stays two digits");

  // an input that could not be held back, then Escape
  at("s", 0, 2);
  el("s").value = "3y"; el("s").dispatchEvent(new Event("input", { bubbles: true }));
  await painted();
  expect(shown("s") + " " + state(), "3 363000|0:6:3|held|363000", "an input that could not be held back is read for its digits");
  press("Escape");
  await painted();
  expect(shown("m", "s") + " " + where() + " " + state(), "05:00 body 300000|0:5:0|held|300000", "Escape puts back the time the edit began with, and takes the focus off");

  // playing: focusing a segment pauses it to the millisecond, Enter plays on from what was typed
  el("play").click();
  travel(2350); look(false); await painted();
  expect(state(), "298000|0:4:58|playing|300000", "2.35 s in");
  el("s").focus();
  await painted();
  expect(state() + " " + shown("m", "s"), "298000|0:4:58|held|300000 04:58", "focusing a segment pauses it there");
  travel(10000); look(false); await painted();
  expect(state(), "298000|0:4:58|held|300000", "held while it is edited");
  at("s", 0, 2);
  keys("3");
  await painted();
  expect(state() + " " + shown("m", "s"), "243000|0:4:3|held|300000 04:3", "the typed value, held, duration kept");
  press("Enter");
  await painted();
  expect(state() + " " + shown("m", "s") + " " + where(), "243000|0:4:3|playing|300000 04:03 body", "Enter plays on from the typed value");
  travel(1000); look(false); await painted();
  expect(state() + " " + shown("m", "s") + " " + el("s").getAttribute("aria-valuenow") + " " + el("s").getAttribute("aria-valuetext"),
    "242000|0:4:2|playing|300000 04:02 2 2 giây", "it counts from it, the segments with it");

  // Escape puts back what was held, to the millisecond, and plays on
  travel(350); look(false); await painted(); // 241 650 ms left
  at("m", 0, 2);
  keys("10");
  await painted();
  expect(state() + " " + where(), "602000|0:10:2|held|300000 m 2-2", "edited: 10 minutes");
  press("Escape");
  await painted();
  expect(state(), "242000|0:4:2|playing|300000", "Escape puts back what was held, and plays on");
  travel(650); look(false); await painted();
  expect(state(), "241000|0:4:1|playing|300000", "the millisecond kept: 241 s 650 ms ran down to 241 s in 650 ms");

  // while a segment is edited the clock never writes it: a play() from elsewhere holds as the run it
  // starts would, and plays once the edit is over
  el("s").focus();
  el("play").click(); // a click from code: the focus stays
  travel(3000); look(false); await painted();
  expect(state() + " " + shown("m", "s"), "241000|0:4:1|held|300000 04:01", "a play() while a segment is edited holds");
  el("s").blur();
  await painted();
  expect(state(), "241000|0:4:1|playing|300000", "the edit over, it plays from it");
  travel(1000); look(false); await painted();
  expect(state() + " " + shown("m", "s"), "240000|0:4:0|playing|300000 04:00", "and counts, the segments with it");

  // a total of 0 edited while it plays: the run stops there, nothing sent
  at("m", 0, 2);
  press("Backspace");
  at("s", 0, 2);
  press("Backspace");
  press("Enter");
  await painted();
  expect(state() + " " + shown("m", "s"), "0|0:0:0|held|300000 00:00", "a total of 0 edited while it played stops the run");
  assert(finished("tea") === 0, "and sends nothing, got " + finished("tea"));
  el("play").click();
  expect(state(), "300000|0:5:0|playing|300000", "play() then runs all of duration again");
  el("stop").click();

  // minutes++ and minutes-- write a part, clamped at 59
  at("m", 0, 2);
  keys("5959");
  document.activeElement.blur();
  el("more").click();
  await painted();
  expect(shown("m", "s") + " " + state(), "59:59 3599000|0:59:59|held|3599000", "minutes++ clamps at 59");
  el("less").click();
  expect(shown("m", "s") + " " + state(), "58:59 3539000|0:58:59|held|3539000", "minutes-- writes a part");

  // a narrower max (30 minutes)
  at("n", 0, 2);
  keys("3");
  expect(shown("n") + " " + where(), "3 n 1-1", "3, as typed");
  keys("5");
  expect(shown("n", "ns") + " " + where(), "30:00 n 2-2", "35 over the max of 30 is 30");
  press("ArrowUp");
  expect(shown("n"), "30", "ArrowUp at the max of 30: no wrap");
  el("n").blur();

  // hours: 24 at most, and 24 hours take no minute; said in the page's language
  at("h", 0, 2);
  keys("30");
  await painted();
  expect(shown("h", "dm", "ds") + " " + state("day-state"), "24:00:00 86400000|24:0:0|held|86400000", "30 hours are 24");
  at("dm", 0, 2);
  keys("5");
  await painted();
  expect(shown("h", "dm", "ds") + " " + state("day-state"), "24:0:00 86400000|24:0:0|held|86400000", "24 hours take no minute");
  at("h", 0, 2);
  keys("23");
  at("dm", 0, 2);
  keys("1");
  expect(shown("h", "dm", "ds") + " " + el("dm").getAttribute("aria-valuetext") + ", " + el("h").getAttribute("aria-valuetext"),
    "23:1:00 1 minute, 23 hours", "said in the page's language, one less s for one");
  el("dm").blur();
  expect(shown("h", "dm", "ds"), "23:01:00", "two digits once the edit ends");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// The edits the review of 20/10/2026, evening, found wrong, the clock driven by hand:
//   - the focus taken by the mouse: a press (mousedown) that takes the focus off a segment ends the edit
//     but plays on only once the press is over and its click has run, so nothing moves under the mouse
//     (the toggle still reads Chạy, Dừng stands where it stood) and the click is what the person asked:
//     toggle() plays (once, never paused again by its own click), stop() stops, minutes++ adds a minute
//     to what is left and it plays on, duration kept; a press on nothing plays on after its release; the
//     keyboard leaving (Tab, Enter) plays on at once, as before;
//   - the window going away (a focusout that leaves the segment the page's focus) is no end of the edit:
//     held, the clock writing nothing, and Escape after it puts back the time the edit began with;
//   - a composition (an input method): what it shows as it composes is left, what it commits is read
//     once, as a cancelable insertText (Chrome) or at its end, at the caret it began at (the bar of
//     20/10/2026, late evening): 3 then 5 make 35, 1 then 2 make 12;
//   - a time pasted (1:30) sets the whole time, read from the right, wherever it is pasted; one longer
//     than the clock (2:05:00 without hours) is the most it shows (59:59); 0:01:30 drops the 0; a paste
//     with no colon is typed (12 over the segment selected);
//   - change: a segment the focus leaves changed sends change, once, from the segment, its value as
//     shown, in two digits again (07); one left as it was, or put back by Escape, sends none.
//
// The steps that type select the segment first: the focus selects nothing since the bar.
//
// CONTROL, against the copy of v1.0.0.js from before the review (-overlay), the test failing at the
// step named, every other step's checks made silent in a copy of this test (their actions kept), run
// 20/10/2026: "the press on Chạy moves nothing" (playing, Tạm dừng: the edit's end played on at the
// press, so the click paused it again); "the window going away is no end of the edit" (296000, playing);
// "a composition is read once it commits" (05:05: each digit read as it composed started the segment
// again); "a time pasted is the whole time" (59:05 59:59 59:59 59:59 12:59); "a segment left changed
// sends change, once" (none). Since the bar (run 20/10/2026, late evening), against a copy without the
// composition taken back to where it began, "a composition is read once it commits" failed (05:55:
// each digit went in after what the composition had drawn), and against one whose focusout does not
// write the segment it leaves, "a segment left changed sends change" failed (s=42 m=7).
func TestBrowserCountdownSegmentEdits(t *testing.T) {
	runBrowserProof(t, "countdown segment edits", nil, `
<div id="tea" lang="vi" data-kitwork-component="countdown: $tea" data-kitwork-scope="duration: 300000">
  <time role="group" aria-label="Thời gian còn lại" data-kitwork-bind="datetime: 'PT' + hours + 'H' + minutes + 'M' + seconds + 'S'">`+
		countdownSegment("m", "minutes", "59", "5", "05", "5 phút")+`:`+countdownSegment("s", "seconds", "59", "0", "00", "0 giây")+`</time>
  <b id="state" `+countdownSegmentState+`></b>
  <button id="more" data-kitwork-event="click: minutes++">+1</button>
  <button id="toggle" data-kitwork-event="click: toggle()" data-kitwork-bind="text: playing ? 'Tạm dừng' : 'Chạy'">Chạy</button>
  <button id="stop" data-kitwork-event="click: stop()">Dừng</button>
</div>
<script>`+countdownClock+`
(async function () {
  await wait(80);
  var el = function (id) { return document.getElementById(id); };
  var state = function () { return el("state").textContent; };
  var shown = function () { return el("m").value + ":" + el("s").value; };
  var focused = function () { return document.activeElement === document.body ? "body" : document.activeElement.id; };
  var keys = function (text) {
    for (const d of text) document.activeElement.dispatchEvent(new InputEvent("beforeinput", { inputType: "insertText", data: d, bubbles: true, cancelable: true }));
  };
  var press = function (key) { document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: key, bubbles: true, cancelable: true })); };
  // a press of the mouse: down (the focus goes to the element pressed, or to nothing), then up and the
  // click it makes
  var down = function (id) { document.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })); if (id) el(id).focus(); else document.activeElement.blur(); };
  var up = function (id) { document.dispatchEvent(new MouseEvent("mouseup", { bubbles: true })); if (id) el(id).click(); };
  var expect = function (got, want, step) { assert(got === want, step + ": want " + want + ", got " + got); };
  var changes = [];
  document.addEventListener("change", function (e) { changes.push(e.target.id + "=" + e.target.value); });

  // the mouse: Chạy, a click on the seconds holds it, 30 typed, a click on Chạy plays from 00:30
  el("toggle").click();
  travel(1300); look(false); await painted();
  el("m").focus();
  el("m").setSelectionRange(0, 2);
  keys("0:30");
  await painted();
  expect(state() + " " + el("toggle").textContent + " " + focused(), "30000|0:0:30|held|300000 Chạy s", "held at the typed 00:30");
  down("toggle");
  await painted();
  expect(state() + " " + el("toggle").textContent, "30000|0:0:30|held|300000 Chạy", "the press on Chạy moves nothing");
  up("toggle");
  await wait(20);
  expect(state(), "30000|0:0:30|playing|300000", "the click on Chạy plays");
  travel(1000); look(false); await painted();
  expect(state() + " " + shown(), "29000|0:0:29|playing|300000 00:29", "and it counts from 00:30");

  // a press on Dừng: nothing moves, the click stops it
  el("s").focus();
  down("stop");
  await painted();
  expect(state(), "29000|0:0:29|held|300000", "a press on Dừng moves nothing");
  up("stop");
  await wait(20);
  expect(state() + " " + shown(), "300000|0:5:0|held|300000 05:00", "the click on Dừng stops it");

  // a press on +1: a minute more of what is left, then it plays on, duration kept
  el("toggle").click();
  travel(2000); look(false); await painted();
  el("s").focus();
  down("more");
  up("more");
  await wait(20);
  expect(state(), "358000|0:5:58|playing|300000", "a click on +1 adds a minute to what is left and plays on");
  // a press on nothing plays on once let go
  el("s").focus();
  down();
  await painted();
  expect(state(), "358000|0:5:58|held|300000", "a press on nothing: held while pressed");
  up();
  await wait(20);
  expect(state(), "358000|0:5:58|playing|300000", "a press on nothing plays on once let go");
  // the keyboard leaving plays on at once
  el("s").focus();
  el("s").blur();
  expect(state(), "358000|0:5:58|playing|300000", "the keyboard leaving plays on at once");

  // the window going away: the focus stays the page's, the edit goes on, held
  el("s").focus();
  el("s").setSelectionRange(0, 2);
  keys("1");
  el("s").dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: null }));
  travel(5000); look(false); await painted();
  expect(state() + " " + focused(), "301000|0:5:1|held|300000 s", "the window going away is no end of the edit");
  press("Escape");
  await painted();
  expect(state(), "358000|0:5:58|playing|300000", "Escape after it puts back the time the edit began with, and plays on");
  el("stop").click();

  // a composition: what it commits is read, once
  var compose = function (text, commit) {
    var s = document.activeElement;
    s.dispatchEvent(new CompositionEvent("compositionstart", { data: "", bubbles: true }));
    s.dispatchEvent(new InputEvent("beforeinput", { inputType: "insertCompositionText", data: text, isComposing: true, bubbles: true }));
    s.value = text; // the browser shows what is composed
    s.dispatchEvent(new InputEvent("input", { inputType: "insertCompositionText", data: text, isComposing: true, bubbles: true }));
    if (commit) s.dispatchEvent(new InputEvent("beforeinput", { inputType: "insertText", data: text, bubbles: true, cancelable: true }));
    else s.dispatchEvent(new CompositionEvent("compositionend", { data: text, bubbles: true }));
  };
  el("s").focus();
  el("s").setSelectionRange(0, 2);
  compose("3", true);
  compose("5", true);
  await painted();
  expect(shown() + " " + state(), "05:35 335000|0:5:35|held|335000","a composition is read once it commits, at the caret it began at: 3 then 5 make 35");
  el("m").focus();
  el("m").setSelectionRange(0, 2);
  compose("1");
  compose("2");
  expect(shown() + " " + focused(), "12:35 m", "a composition read at its end: 1 then 2 make 12");

  // a time pasted is the whole time, read from the right
  var paste = function (id, text) {
    el(id).focus();
    el(id).select();
    var data = new DataTransfer();
    data.setData("text", text);
    el(id).dispatchEvent(new InputEvent("beforeinput", { inputType: "insertFromPaste", data: null, dataTransfer: data, bubbles: true, cancelable: true }));
    return shown();
  };
  expect([paste("m", "1:30"), paste("s", "1:30"), paste("m", "2:05:00"), paste("s", "0:01:30"), paste("m", "12")].join(" "),
    "01:30 01:30 59:59 01:30 12:30", "a time pasted is the whole time");
  el("m").blur();
  await painted();
  expect(state(), "750000|0:12:30|held|750000", "the time pasted is the full time");

  // change: once from a segment left changed, none from one left as it was or put back
  changes = [];
  el("s").focus();
  el("s").setSelectionRange(0, 2);
  keys("42");
  el("s").blur();
  el("m").focus();
  el("m").blur();
  el("s").focus();
  el("s").setSelectionRange(0, 2);
  keys("7");
  press("Escape");
  el("m").focus();
  el("m").setSelectionRange(0, 2);
  keys("7");
  el("m").blur();
  expect(changes.join(" "), "s=42 m=07", "a segment left changed sends change, once, its value in two digits");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// What the review of the bar (20/10/2026, late evening) found, on a bar of hours, minutes and seconds,
// the clock driven by hand, typed into with the page's own events:
//   - a digit after one the max took goes on with what was typed, at the caret the typing left: 3 then
//     0 at the start of the hours is 30, 24 (it was 20: the 0 went over the 4 of the 24 shown), 6 then 5
//     at the start of the seconds 65, 59 (it was 55); the caret moved, a digit goes over what is shown;
//   - over 24 hours the time is 24:00:00 and the other parts are kept while the edit lasts: 25 typed for
//     2 hours and taken back is 2:05:00 (it was 2:00:00), ArrowUp then ArrowDown from 23:05:00 is
//     23:05:00 (it was 23:00:00); Escape puts back the time the edit began with;
//   - change: Enter at 24:00:00 sends it from the hours and from the minutes the 24 hours took (it was
//     the hours alone); Escape putting back minutes whose change was sent (03) sends it again (05);
//   - the digits and the colon of a full-width input method (３, １：３０) are taken (they were refused);
//   - Ctrl+Backspace, Alt+Delete and a phone's deleteWordBackward take the segment's digits before or
//     after the caret, as a word (they took one digit);
//   - a press that becomes a drag (no mouseup: dragstart, dragend) lets the edit play on (it stayed held);
//   - Enter takes the segments out of the order of Tab (tabindex -1), so Tab goes on past the bar, and
//     the focus going anywhere puts them back, an author's tabindex as it was (the real Tab is
//     TestBrowserCountdownBarReview in engine/work).
//
// CONTROL, each against a copy of v1.0.0.js with one fix taken out, served through -overlay, the test
// failing at the step named (run 21/10/2026): type() not typing on what was typed ("then 0 goes on with
// the 30 typed": 20:05:00, 72300000; the copy from before the review: 20:00:00, 72000000); part()
// keeping no wrote ("taken back to 2, the 5 minutes are there again": 2:00:00, 7200000); done() telling
// no segment ("Enter at 24:00:00": h=24 alone); wide() giving the text back as it came ("a full-width 3
// is a 3": 00:05:00 m 0-2); erase() with no word ("Ctrl+Backspace takes the digits before the caret":
// 00:0:00 m 1-1); init() with no dragend listener ("a drag of the press lets it go": held); the Enter
// branch setting no tabindex ("Enter takes the segments out of the order of Tab": body 0 null null).
func TestBrowserCountdownBarReview(t *testing.T) {
	runBrowserProof(t, "countdown bar review", nil, `
<div id="tea" lang="vi" data-kitwork-component="countdown: $tea" data-kitwork-scope="duration: 300000">
  <time role="group" aria-label="Thời gian" data-kitwork-bind="datetime: 'PT' + hours + 'H' + minutes + 'M' + seconds + 'S'">`+
		countdownSegment("h", "hours", "24", "0", "00", "0 giờ")+`:`+countdownSegment("m", "minutes", "59", "5", "05", "5 phút")+`:`+
		countdownSegment("s", "seconds", "59", "0", "00", "0 giây")+`</time>
  <b id="state" `+countdownSegmentState+`></b>
  <button id="toggle" data-kitwork-event="click: toggle()">toggle</button>
  <button id="stop" data-kitwork-event="click: stop()">stop</button>
  <button id="h0" data-kitwork-event="click: hours = 0">0</button>
  <button id="m5" data-kitwork-event="click: minutes = 5">5</button>
  <button id="s0" data-kitwork-event="click: seconds = 0">0</button>
</div>
<a id="link" href="#away">away</a>
<script>`+countdownClock+`
(async function () {
  await wait(80);`+countdownCaret+`
  var all = function () { return shown("h", "m", "s") + " " + where() + " " + state(); };
  var changes = [];
  document.addEventListener("change", function (e) { changes.push(e.target.id + "=" + e.target.value); });
  var five = function () { el("h0").click(); el("m5").click(); el("s0").click(); changes = []; };
  var insert = function (text, type) {
    document.activeElement.dispatchEvent(new InputEvent("beforeinput", { inputType: type || "insertText", data: text, bubbles: true, cancelable: true }));
  };
  expect(all(), "00:05:00 body 300000|0:5:0|held|300000", "the start");

  // a digit after one the max took goes on with what was typed
  at("h", 0);
  keys("3");
  await painted();
  expect(all(), "24:00:00 h 1-1 86400000|24:0:0|held|86400000", "3 at the start of the hours is 30, which is 24");
  keys("0");
  await painted();
  expect(all(), "24:00:00 h 2-2 86400000|24:0:0|held|86400000", "then 0 goes on with the 30 typed: 24, not 20");
  press("Escape");
  at("s", 0);
  keys("6");
  keys("5");
  await painted();
  expect(all(), "00:05:59 s 2-2 359000|0:5:59|held|359000", "6 then 5 at the start of the seconds is 65, 59, not 55");
  at("s", 0);
  keys("1");
  await painted();
  expect(all(), "00:05:19 s 1-1 319000|0:5:19|held|319000", "the caret moved: a digit goes over the 59 shown");
  press("Escape");
  await painted();
  expect(all(), "00:05:00 body 300000|0:5:0|held|300000", "Escape");

  // over 24 hours: 24:00:00, the other parts kept while the edit lasts
  at("h", 0, 2);
  keys("25");
  await painted();
  expect(all(), "24:00:00 h 2-2 86400000|24:0:0|held|86400000", "25 hours are 24:00:00");
  press("Backspace");
  await painted();
  expect(all(), "2:05:00 h 1-1 7500000|2:5:0|held|7500000", "taken back to 2, the 5 minutes are there again");
  at("h", 0, 1);
  keys("23");
  press("ArrowUp");
  await painted();
  expect(all(), "24:00:00 h 2-2 86400000|24:0:0|held|86400000", "ArrowUp from 23:05:00 is 24:00:00");
  press("ArrowDown");
  await painted();
  expect(all(), "23:05:00 h 2-2 83100000|23:5:0|held|83100000", "ArrowDown is 23:05:00 again");
  press("Escape");
  await painted();
  expect(all(), "00:05:00 body 300000|0:5:0|held|300000", "Escape puts back the time the edit began with");

  // change, once the edit is over, from every segment whose value is not the one its last change carried
  at("h", 0, 2);
  keys("30");
  press("Enter");
  await painted();
  expect(all() + " " + changes.join(" "), "24:00:00 body 86400000|24:0:0|held|86400000 h=24 m=00",
    "Enter at 24:00:00: change from the hours, and from the minutes the 24 hours took");
  five();
  at("m", 1);
  keys("3");
  at("s", 1);
  await painted();
  expect(all() + " " + changes.join(" "), "00:03:00 s 1-1 180000|0:3:0|held|180000 m=03", "the minutes left at 03: their change");
  press("Escape");
  await painted();
  expect(all() + " " + changes.join(" "), "00:05:00 body 300000|0:5:0|held|300000 m=03 m=05", "Escape puts back 05, and a change says so");

  // a full-width input method's digits and colon
  at("m", 0, 2);
  insert("３");
  await painted();
  expect(all(), "00:3:00 m 1-1 180000|0:3:0|held|180000", "a full-width 3 is a 3");
  insert("１：３０", "insertFromPaste");
  await painted();
  expect(all(), "00:01:30 m 2-2 90000|0:1:30|held|90000", "a full-width time pasted is the time");
  press("Escape");

  // a word: every digit of the segment before the caret, or after it
  at("m", 2);
  press("Backspace", { ctrlKey: true });
  await painted();
  expect(all(), "00::00 m 0-0 0|0:0:0|held|0", "Ctrl+Backspace takes the digits before the caret, as a word");
  press("Escape");
  at("m", 0);
  press("Delete", { altKey: true });
  await painted();
  expect(all(), "00::00 m 0-0 0|0:0:0|held|0", "Alt+Delete takes the digits after it");
  press("Escape");
  at("m", 2);
  insert(null, "deleteWordBackward");
  await painted();
  expect(all(), "00::00 m 0-0 0|0:0:0|held|0", "a phone's deleteWordBackward too");
  press("Escape");
  await painted();
  expect(all(), "00:05:00 body 300000|0:5:0|held|300000", "Escape");

  // a press that becomes a drag is let go by its dragend
  el("toggle").click();
  travel(1300); look(false); await painted();
  at("s", 1);
  await painted();
  expect(state(), "299000|0:4:59|held|300000", "a segment focused while it plays holds it");
  document.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
  el("link").focus();
  el("link").dispatchEvent(new DragEvent("dragstart", { bubbles: true }));
  el("link").dispatchEvent(new DragEvent("dragend", { bubbles: true }));
  await wait(20);
  expect(state() + " " + active(), "299000|0:4:59|playing|300000 link", "a drag of the press lets it go: it plays on");
  document.dispatchEvent(new MouseEvent("mouseup", { bubbles: true }));
  el("stop").click();

  // Enter: out of the order of Tab until the focus goes anywhere
  var tabs = function () { return ["h", "m", "s"].map(function (id) { return String(el(id).getAttribute("tabindex")); }).join(" "); };
  el("h").setAttribute("tabindex", "0");
  at("m", 1);
  press("Enter");
  expect(where() + " " + tabs(), "body -1 -1 -1", "Enter takes the segments out of the order of Tab");
  el("toggle").focus();
  expect(active() + " " + tabs(), "toggle 0 null null", "the focus elsewhere puts them back, an author's tabindex as it was");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
