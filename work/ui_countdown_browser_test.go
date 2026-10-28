package work

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// countdownBrowserFiles is a site that moves with Drive (router.reload(false)), its overlay the toast
// of the toast test. Its first page holds the countdown as the contract's example writes it, three
// seconds long: started as it comes alive (ready="play()"), its end said through the toast, toggle()
// and stop() inside it, pause() on a button outside it through its alias, and its seconds read outside
// it through the alias too; a link goes to /tiep, which links back.
var countdownBrowserFiles = map[string]string{
	"localhost/router.work": `import * as router from "kitwork/router";

router.language("vi");
router.reload(false);
`,
	"localhost/view.ui":    `<view><content /><overlay /></view>`,
	"localhost/overlay.ui": toastBrowserFiles["localhost/overlay.ui"],
	"localhost/content.ui": `<content padding="4" gap="2">
  <countdown as="$tea" duration="3s" ready="play()" finish="$toast.success('Trà đã sẵn sàng')" flex="row" gap="3">
    <text>Còn <time id="left">{{ minutes }}:{{ String(seconds).padStart(2, '0') }}</time></text>
    <button id="toggle" click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
    <button id="stop" click="stop()">Dừng</button>
  </countdown>
  <button id="hold" click="$tea.pause()">Tạm dừng pha trà</button>
  <text id="outside">{{ $tea.seconds }}</text>
  <link id="next" value="/tiep">Tiếp</link>
</content>`,
	"localhost/tiep/content.ui": `<content padding="4" gap="2">
  <text id="here">Tiếp</text>
  <link id="back" value="/">Về</link>
</content>`,
}

// countdownHeard is put in every document before its own scripts run, so it hears the page start:
// each change of what the countdown's parts show, with when it came (window.__seen: the <time>'s words
// and datetime as "time", the toggle's words as "toggle", the seconds read outside as "outside"; each
// read once the change is drawn, a mutation's record coming after the kernel's paint), each change of
// the page's visibility and of its lifecycle ("page": hidden, visible, frozen, resumed), each finish as
// it passes the window, before any handler of the page runs (window.__finishes: when, what the screen
// shows then, the toggle's words when the host has one, the event's detail, whether the page is in
// sight), and every complaint of the page's scripts in the console (window.__complaints).
const countdownHeard = `(function () {
  window.__seen = [];
  window.__finishes = [];
  window.__complaints = [];
  ["warn", "error"].forEach(function (level) {
    var write = console[level];
    console[level] = function () { window.__complaints.push(Array.prototype.join.call(arguments, " ")); return write.apply(console, arguments); };
  });
  window.addEventListener("error", function (event) { window.__complaints.push(String(event.message)); });
  var at = function (what, value) { window.__seen.push([what, value, performance.now()]); };
  var last = {};
  var look = function () {
    var time = document.getElementById("left"), toggle = document.getElementById("toggle"), outside = document.getElementById("outside");
    var now = { time: time ? time.textContent + " " + time.getAttribute("datetime") : "", toggle: toggle ? toggle.textContent : "", outside: outside ? outside.textContent : "" };
    Object.keys(now).forEach(function (what) {
      if (now[what] !== last[what]) {
        if (now[what]) at(what, now[what]);
        last[what] = now[what];
      }
    });
  };
  new MutationObserver(look).observe(document, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ["datetime"] });
  document.addEventListener("visibilitychange", function () { at("page", document.visibilityState); });
  document.addEventListener("freeze", function () { at("page", "frozen"); });
  document.addEventListener("resume", function () { at("page", "resumed"); });
  window.addEventListener("finish", function (event) {
    var host = event.target, time = host.querySelector("time"), toggle = host.querySelector("#toggle"), scope = kitwork.scopeFor(host);
    window.__finishes.push({ at: performance.now(), detail: event.detail, page: document.visibilityState,
      shown: [time.textContent, time.getAttribute("datetime"), toggle ? toggle.textContent : "", "remaining " + scope.remaining, "playing " + scope.playing].join(" ") });
  }, true);
})()`

// countdownProbe is what each step reads the page with, installed once a page has loaded: the time
// (now() and wait(), on the page's own clock), clicks that say when they were made, how many finishes
// were heard, the screen (the <time>'s words, the toggle's) and the instance's state ($tea: remaining,
// its parts, playing, duration), the moment a part first showed a value after another moment (when),
// the changes of a part between two moments (changes), each said "at 1s" or "on time" when it came
// when it should from a moment (near: from a little early to 350ms late, a timer under load comes late
// and never early), and the toast's notices (their words, those leaving left out).
const countdownProbe = `(function () {
  if (window.__countdown) return "ready";
  var start = window.setTimeout;
  var C = window.__countdown = {
    now: function () { return performance.now(); },
    wait: function (ms) { return new Promise(function (done) { start(done, Math.max(0, ms)); }); },
    frames: function () { return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(done); }); }); },
    until: async function (test, within) {
      for (var waited = 0; waited < within && !test(); waited += 20) await C.wait(20);
      return test();
    },
    click: function (id) { document.getElementById(id).click(); return performance.now(); },
    finished: function () { return window.__finishes.length; },
    screen: function () { return document.getElementById("left").textContent + " " + document.getElementById("toggle").textContent; },
    state: function () { var tea = kitwork.scope.$tea; return [tea.remaining, tea.hours + ":" + tea.minutes + ":" + tea.seconds, tea.playing ? "playing" : "held", tea.duration].join(" "); },
    first: function (what) { var list = window.__seen.filter(function (e) { return e[0] === what; }); return list.length ? list[0][1] : "nothing"; },
    when: function (what, value, after) {
      var list = window.__seen.filter(function (e) { return e[0] === what && e[1] === value && e[2] >= after; });
      return list.length ? list[0][2] : NaN;
    },
    near: function (ms, want) {
      if (want === undefined) return Math.round(ms) + "ms, not expected";
      if (ms >= want - 80 && ms <= want + 350) return want % 1000 === 0 ? "at " + want / 1000 + "s" : "on time";
      return Math.round(ms) + "ms, not " + Math.round(want) + "ms";
    },
    changes: function (what, from, to, t0, wants) {
      var list = window.__seen.filter(function (e) { return e[0] === what && e[2] >= from && e[2] < to; });
      return list.map(function (e, i) { return e[1] + (wants ? " " + C.near(e[2] - t0, wants[i]) : ""); }).join(", ") || "none";
    },
    toast: function () {
      var toast = document.querySelector('[data-kitwork-component^="toast"]');
      var rows = Array.prototype.filter.call(toast.children, function (c) { return c.hasAttribute("data-kitwork-key") && !c.hasAttribute("data-leaving"); });
      return rows.map(function (row) { return row.querySelectorAll("p")[1].textContent; }).join(" ; ") || "no notice";
    },
    clear: function () { kitwork.scope.$toast.clear(); return C.wait(250); }
  };
  return "installed";
})()`

// countdownStep runs one step of the proof after the probe: body is the inside of an async function,
// which returns what the step reads.
func countdownStep(d *devtools, body string) string {
	d.t.Helper()
	d.evaluate(countdownProbe)
	return d.evaluate(`(async function () { var C = window.__countdown; ` + body + ` })()`)
}

// countdownHide puts a new tab in front of the page, as a person who goes to another tab does: the
// browser hides the page (document.hidden, visibilitychange) and holds back its timers as it holds a
// background tab's. It returns the tab, once the page says it is hidden.
func countdownHide(d *devtools) string {
	d.t.Helper()
	var tab struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(d.call("Target.createTarget", map[string]any{"url": "about:blank"}), &tab); err != nil || tab.TargetID == "" {
		d.t.Fatalf("no tab to put in front of the page: %v", err)
	}
	for tries := 0; d.evaluate(`document.visibilityState`) != "hidden"; tries++ {
		if tries == 100 {
			d.t.Fatal("the page stayed in sight behind another tab")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return tab.TargetID
}

// countdownShow brings the page back to the front, closes the tab that hid it, and waits until the
// page says it is in sight.
func countdownShow(d *devtools, tab string) {
	d.t.Helper()
	d.call("Page.bringToFront", map[string]any{})
	d.call("Target.closeTarget", map[string]any{"targetId": tab})
	for tries := 0; d.evaluate(`document.visibilityState`) != "visible"; tries++ {
		if tries == 100 {
			d.t.Fatal("the page never came back in sight")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBrowserCountdown proves the countdown on pages the engine serves, in a real browser whose clock
// runs in real time (no virtual time: each step polls the page's own clock), each part with a browser
// of its own:
//   - as the server paints it: all of the three seconds (0:03, datetime PT0H0M3S), not playing (the
//     toggle reads Chạy), the seconds read outside through the alias the same; the <time> is the timer
//     (role="timer", no aria-live) and its digits keep one width (tabular-nums), the host has no role;
//     the page warns of nothing;
//   - ready="play()" on the host starts it as it comes alive (the first proof of ready on a component
//     host), and the digits go down on the screen once a second from the moment it started, the
//     datetime and the seconds read outside with them;
//   - at the end: finish once, sent after the end is painted (0:00, PT0H0M0S, the toggle back to Chạy:
//     heard at the window before any handler of the page), carrying nothing; its handler's
//     $toast.success shows the notice;
//   - play() after the end (toggle()) is a new run of all of it; pause() through the alias from a button
//     outside the host holds it, to the millisecond (nothing changes for 1.5s, no finish), and play()
//     goes on from what was held: the next second and the end come when what was held runs out, not a
//     whole second later;
//   - stop() puts all of it back at once and the run is over: past where its end would have come no
//     digit changes and no finish is sent; play() after it runs all of it again, and finishes once;
//   - a hidden page (another tab put in front) keeps counting with nothing written: back in sight it
//     shows what is left at once and goes on to the end on time; hidden and frozen past the end (the
//     browser holds every timer, as it does a background tab it freezes), finish comes once on the
//     return, the end painted; hidden past the end with its timers let run (late, as a background tab's
//     are), finish comes once at the end while hidden, the end painted, and the notice is there on the
//     return;
//   - a Drive move away from the page while it counts: its host goes with the content, no finish is
//     heard after (on the host it left nor at the window), its instance stops where it was (its timers
//     released) and no notice comes; Back brings a new one that ready starts over, which less motion,
//     the mouse resting on it and the keyboard on its toggle do not hold, and which finishes once.
//
// CONTROL, each against a copy served through -overlay (the tree untouched), the proof failing at the
// step named: finish() in countdown v1.0.0.js emitting straight after its writes, not after the paint,
// sent finish with the second before still drawn ("0:01 PT0H0M1S Tạm dừng"); finish() emitting twice
// counted "finish ×2" in each run; stop() ending a run that plays through finish() sent finish and its
// notice on stop ("stop() while it plays"); with no visibilitychange listener the tick chain ran on the
// hidden page (0:02 written while hidden, in both hidden runs that are let run); the listener reading the
// clocks only as the page hides left 0:03 on the screen after the return, until the end ("back in sight
// 1532ms after play(): 0:00 PT0H0M0S 1478ms later"); no timer armed to the end on a hidden page sent no
// finish while hidden ("no finish while hidden"); the cleanup in init emptied let the instance Drive took
// away count on to its end ("2000 true at the move, 0 false after"); ready on a host never queued by the
// kernel (kernel.js) left the countdown unstarted ("no finish: toggle Chạy").
func TestBrowserCountdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the countdown browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, countdownBrowserFiles)
	page := serveUI(t, tenant, "http://localhost/")
	served := page.Body.String()
	if page.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", page.Code, served)
	}
	for _, want := range []string{
		`<div class="flex-row gap-3" data-kitwork-component="countdown: $tea" data-kitwork-event="ready: play(); finish: $toast.success(&#39;Trà đã sẵn sàng&#39;)" data-kitwork-scope="duration: 3000">`,
		`role="timer"`, `datetime="PT0H0M3S">0:03</time>`, `id="toggle" type="button" data-kitwork-bind="text: playing ? &#39;Tạm dừng&#39; : &#39;Chạy&#39;" data-kitwork-event="click: toggle()">Chạy</button>`,
		`id="hold" type="button" data-kitwork-event="click: $tea.pause()">`, `/_kitwork/components/countdown.`,
	} {
		if !strings.Contains(served, want) {
			t.Errorf("the page as the server writes it has no %s", want)
		}
	}
	server := carouselServer(tenant)
	defer server.Close()
	check := func(t *testing.T, step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	// open starts a browser on the first page, which hears the page start from its next document on
	open := func(t *testing.T) *devtools {
		t.Helper()
		d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
		d.loaded(exampleSettle)
		d.call("Page.enable", map[string]any{})
		d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": countdownHeard})
		networkReload(d)
		return d
	}
	const end = "0:00 PT0H0M0S Chạy remaining 0 playing false"

	t.Run("ready, the ticks, finish once after the paint, pause, toggle, stop", func(t *testing.T) {
		d := open(t)
		check(t, "as the server paints it, then started by ready", countdownStep(d, `
  await C.until(function () { return C.finished() >= 1; }, 5000);
  await C.wait(400);
  var played = C.when("toggle", "Tạm dừng", 0), f = window.__finishes[0], left = document.getElementById("left");
  var host = document.querySelector('[data-kitwork-component^="countdown"]');
  if (!f) return "no finish: toggle " + C.changes("toggle", 0, Infinity) + ", time " + C.changes("time", 0, Infinity) + ", " + C.state();
  return [[C.first("time"), C.first("toggle"), C.first("outside")].join(", "),
    "toggle " + C.changes("toggle", played - 1, Infinity, played, [0, 3000]),
    C.changes("time", played, Infinity, played, [1000, 2000, 3000]),
    "outside " + C.changes("outside", played, Infinity, played, [1000, 2000, 3000]),
    "finish ×" + C.finished() + " " + C.near(f.at - played, 3000) + ": " + f.shown + ", detail " + JSON.stringify(f.detail) + ", " + f.page,
    C.state(), C.toast(),
    [left.getAttribute("role"), left.getAttribute("aria-live"), host.getAttribute("role"), getComputedStyle(left).fontVariantNumeric].map(String).join(" ")].join(" | ");`),
			"0:03 PT0H0M3S, Chạy, 3 | toggle Tạm dừng at 0s, Chạy at 3s | 0:02 PT0H0M2S at 1s, 0:01 PT0H0M1S at 2s, 0:00 PT0H0M0S at 3s | outside 2 at 1s, 1 at 2s, 0 at 3s | "+
				"finish ×1 at 3s: "+end+", detail null, visible | 0 0:0:0 held 3000 | Trà đã sẵn sàng | timer null null tabular-nums")

		check(t, "play() after the end, pause() from outside, play() again", countdownStep(d, `
  await C.clear();
  var from = C.finished();
  var played = C.click("toggle");
  await C.frames();
  var started = C.screen() + " " + C.state();
  await C.wait(1500 - (C.now() - played));
  var paused = C.click("hold"), held = 3000 - (paused - played);
  await C.frames();
  var atPause = C.screen() + " " + C.state();
  await C.wait(1500);
  var during = C.changes("time", paused, Infinity) + ", finish ×" + (C.finished() - from) + ", " + C.state();
  var resumed = C.click("toggle");
  await C.until(function () { return C.finished() > from; }, 3000);
  await C.wait(300);
  var f = window.__finishes[from];
  return [started, atPause, during, C.changes("time", resumed, Infinity, resumed, [held - 1000, held]),
    "finish ×" + (C.finished() - from) + " " + C.near(f.at - resumed, held) + ": " + f.shown, C.toast()].join(" | ");`),
			"0:03 Tạm dừng 3000 0:0:3 playing 3000 | 0:02 Chạy 2000 0:0:2 held 3000 | none, finish ×0, 2000 0:0:2 held 3000 | "+
				"0:01 PT0H0M1S on time, 0:00 PT0H0M0S on time | finish ×1 on time: "+end+" | Trà đã sẵn sàng")

		check(t, "stop() while it plays", countdownStep(d, `
  await C.clear();
  var from = C.finished();
  var played = C.click("toggle");
  await C.wait(1200);
  var before = C.screen();
  var stopped = C.click("stop");
  await C.frames();
  var atStop = C.screen() + " " + C.state();
  await C.wait(3000 - (C.now() - played) + 700);
  return [before, atStop, C.changes("time", stopped, Infinity), "finish ×" + (C.finished() - from), C.state(), C.toast()].join(" | ");`),
			"0:02 Tạm dừng | 0:03 Chạy 3000 0:0:3 held 3000 | 0:03 PT0H0M3S | finish ×0 | 3000 0:0:3 held 3000 | no notice")

		check(t, "play() after stop()", countdownStep(d, `
  var from = C.finished();
  var played = C.click("toggle");
  await C.until(function () { return C.finished() > from; }, 4500);
  await C.wait(600);
  var f = window.__finishes[from];
  return [C.changes("time", played, Infinity, played, [1000, 2000, 3000]),
    "finish ×" + (C.finished() - from) + " " + C.near(f.at - played, 3000) + ": " + f.shown, C.toast()].join(" | ");`),
			"0:02 PT0H0M2S at 1s, 0:01 PT0H0M1S at 2s, 0:00 PT0H0M0S at 3s | finish ×1 at 3s: "+end+" | Trà đã sẵn sàng")
		check(t, "complaints of the page", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})

	t.Run("a hidden page", func(t *testing.T) {
		d := open(t)
		check(t, "the run ready started, stopped", countdownStep(d, `
  await C.until(function () { return C.screen() === "0:03 Tạm dừng"; }, 2000);
  C.click("stop");
  await C.frames();
  return C.screen();`), "0:03 Chạy")

		// hidden in its first second, back in sight in its second
		countdownStep(d, `window.__played = C.click("toggle"); return "";`)
		tab := countdownHide(d)
		time.Sleep(1500 * time.Millisecond)
		countdownShow(d, tab)
		check(t, "hidden, then back in sight before the end", countdownStep(d, `
  await C.until(function () { return C.finished() > 0; }, 3500);
  await C.wait(300);
  var played = window.__played, hid = C.when("page", "hidden", played), back = C.when("page", "visible", hid);
  var left = Math.ceil((3000 - (back - played)) / 1000), shown = "0:0" + left + " PT0H0M" + left + "S";
  var after = window.__seen.filter(function (e) { return e[0] === "time" && e[2] >= back; }), f = window.__finishes[0];
  return [hid - played < 900 ? "hidden in its first second" : "hidden " + Math.round(hid - played) + "ms after play()",
    "while hidden " + C.changes("time", hid, back),
    after.length && after[0][1] === shown && after[0][2] - back <= 150 ? "back in sight: what is left, at once" :
      "back in sight " + Math.round(back - played) + "ms after play(): " + (after.length ? after[0][1] + " " + Math.round(after[0][2] - back) + "ms later" : "nothing"),
    after.slice(1).map(function (e, i) { return e[1] + " " + C.near(e[2] - played, [2000, 3000][i]); }).join(", ") || "none",
    "finish ×" + C.finished() + " " + C.near(f.at - played, 3000) + ": " + f.shown + ", " + f.page].join(" | ");`),
			"hidden in its first second | while hidden none | back in sight: what is left, at once | 0:01 PT0H0M1S at 2s, 0:00 PT0H0M0S at 3s | finish ×1 at 3s: "+end+", visible")

		// hidden and frozen past the end: the browser holds every timer until the page comes back
		countdownStep(d, `await C.clear(); window.__played = C.click("toggle"); return "";`)
		tab = countdownHide(d)
		d.call("Page.setWebLifecycleState", map[string]any{"state": "frozen"})
		time.Sleep(3600 * time.Millisecond)
		d.call("Page.setWebLifecycleState", map[string]any{"state": "active"})
		countdownShow(d, tab)
		check(t, "hidden and frozen past the end", countdownStep(d, `
  await C.until(function () { return C.finished() > 1; }, 2500);
  await C.wait(400);
  var played = window.__played, resumed = C.when("page", "resumed", played), f = window.__finishes[1];
  return [C.changes("time", played, Infinity),
    f.at >= resumed && f.at - played >= 3400 ? "finish on the return" : "finish " + Math.round(f.at - played) + "ms after play(), the page resumed at " + Math.round(resumed - played),
    "finish ×" + (C.finished() - 1) + ": " + f.shown, C.toast()].join(" | ");`),
			"0:03 PT0H0M3S, 0:00 PT0H0M0S | finish on the return | finish ×1: "+end+" | Trà đã sẵn sàng")

		// hidden past the end, its timers let run (as a browser lets a background tab's run, late)
		countdownStep(d, `await C.clear(); window.__played = C.click("toggle"); return "";`)
		tab = countdownHide(d)
		time.Sleep(4600 * time.Millisecond)
		check(t, "hidden past the end, read while hidden", d.evaluate(`(function () {
  var C = window.__countdown, played = window.__played, f = window.__finishes[2];
  if (!f) return "no finish while hidden, " + C.changes("time", played, Infinity) + ", " + document.visibilityState;
  return [C.changes("time", played, Infinity), "finish ×" + (C.finished() - 2),
    f.at - played >= 2950 && f.at - played <= 4300 ? "at the end" : Math.round(f.at - played) + "ms after play()", f.shown, f.page].join(" | ");
})()`), "0:03 PT0H0M3S, 0:00 PT0H0M0S | finish ×1 | at the end | "+end+" | hidden")
		countdownShow(d, tab)
		check(t, "hidden past the end, back in sight", countdownStep(d, `
  await C.frames();
  await C.wait(400);
  return [C.screen(), "finish ×" + (C.finished() - 2), C.toast()].join(" | ");`), "0:00 Chạy | finish ×1 | Trà đã sẵn sàng")
		check(t, "complaints of the page", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})

	t.Run("a Drive move away while it counts", func(t *testing.T) {
		d := open(t)
		check(t, "moved away in its second second", countdownStep(d, `
  await C.until(function () { return C.screen() === "0:03 Tạm dừng"; }, 2000);
  var played = C.when("toggle", "Tạm dừng", 0);
  await C.wait(1300 - (C.now() - played));
  var host = document.querySelector('[data-kitwork-component^="countdown"]'), held = kitwork.scopeFor(host), heard = 0;
  host.addEventListener("finish", function () { heard++; });
  var arrived = new Promise(function (done) { document.addEventListener("kitwork:load", function () { done(); }, { once: true }); });
  C.click("next");
  await arrived;
  var atMove = held.remaining + " " + held.playing;
  await C.wait(3800 - (C.now() - played));
  var now = held.remaining + " " + held.playing;
  return [location.pathname + " " + document.getElementById("here").textContent, host.isConnected ? "its host still in the page" : "its host gone",
    document.querySelectorAll('[data-kitwork-component^="countdown"]').length + " countdowns",
    "finish heard on the host " + heard + ", at the window " + C.finished(),
    now === atMove ? "it stopped where it was" : atMove + " at the move, " + now + " after", C.toast()].join(" | ");`),
			"/tiep Tiếp | its host gone | 0 countdowns | finish heard on the host 0, at the window 0 | it stopped where it was | no notice")

		// Back, with less motion asked for; the mouse rests on the new host and the keyboard is on its
		// toggle while it counts: a countdown holds for none of them (a carousel would)
		d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "reduce"}}})
		var centre struct{ X, Y float64 }
		if err := json.Unmarshal([]byte(countdownStep(d, `
  window.__back = C.now();
  var arrived = new Promise(function (done) { document.addEventListener("kitwork:load", function () { done(); }, { once: true }); });
  history.back();
  await arrived;
  var box = document.querySelector('[data-kitwork-component^="countdown"]').getBoundingClientRect();
  return JSON.stringify({ x: box.left + 8, y: box.top + box.height / 2 });`)), &centre); err != nil {
			t.Fatal(err)
		}
		d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": centre.X, "y": centre.Y})
		check(t, "Back: a new one, started over by ready, held by nothing", countdownStep(d, `
  var mark = window.__back;
  document.getElementById("toggle").focus();
  await C.until(function () { return C.finished() > 0; }, 5000);
  await C.wait(600);
  var played = C.when("toggle", "Tạm dừng", mark), f = window.__finishes[0];
  if (!f) return location.pathname + " | " + (isNaN(played) ? "never started" : "started, no finish") + " | " + C.changes("time", mark, Infinity);
  var host = document.querySelector('[data-kitwork-component^="countdown"]');
  return [location.pathname, matchMedia("(prefers-reduced-motion: reduce)").matches ? "less motion" : "motion",
    (host.matches(":hover") ? "the mouse on it" : "the mouse away") + ", " + (document.activeElement.id || document.activeElement.tagName) + " focused",
    C.changes("time", played, Infinity, played, [0, 1000, 2000, 3000]),
    "finish ×" + C.finished() + " " + C.near(f.at - played, 3000) + ": " + f.shown, C.toast()].join(" | ");`),
			"/ | less motion | the mouse on it, toggle focused | 0:03 PT0H0M3S at 0s, 0:02 PT0H0M2S at 1s, 0:01 PT0H0M1S at 2s, 0:00 PT0H0M0S at 3s | finish ×1 at 3s: "+end+" | Trà đã sẵn sàng")
		check(t, "complaints of the pages", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})
}

// countdownSegmentsFiles is a site whose page holds the countdown of the decision of 20/10/2026,
// evening: its time is its two segments, the minutes and the seconds, the author's buttons for a minute
// more and less, toggle(), stop(); its end said through the toast of the toast test.
var countdownSegmentsFiles = map[string]string{
	"localhost/router.work": countdownBrowserFiles["localhost/router.work"],
	"localhost/view.ui":     `<view><content /><overlay /></view>`,
	"localhost/overlay.ui":  toastBrowserFiles["localhost/overlay.ui"],
	"localhost/content.ui": `<content padding="4" gap="2">
  <countdown as="$tea" duration="5m" finish="$toast.success('Trà đã sẵn sàng')" flex="row" gap="3">
    <time id="left"><input id="minutes" value="{{ minutes }}" alt="Phút" />:<input id="seconds" value="{{ seconds }}" alt="Giây" /></time>
    <button id="more" click="minutes++">+1 phút</button>
    <button id="less" click="minutes--">−1 phút</button>
    <button id="toggle" click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
    <button id="stop" click="stop()">Dừng</button>
  </countdown>
</content>`,
}

// countdownKey presses a key as a person does (DevTools Input.dispatchKeyEvent): down, with the text it
// types when it is a digit, a letter or ":" (Shift held for it; Enter, Escape, the arrows, Home, End,
// Tab, Backspace and Delete type none), then up.
func countdownKey(d *devtools, key string) {
	d.t.Helper()
	codes := map[string]int{"Enter": 13, "Escape": 27, "Tab": 9, "ArrowUp": 38, "ArrowDown": 40, "ArrowLeft": 37, "ArrowRight": 39,
		"Home": 36, "End": 35, "Backspace": 8, "Delete": 46}
	down := map[string]any{"type": "rawKeyDown", "key": key, "code": key, "windowsVirtualKeyCode": codes[key]}
	switch {
	case len(key) == 1 && key >= "0" && key <= "9":
		down = map[string]any{"type": "keyDown", "key": key, "code": "Digit" + key, "windowsVirtualKeyCode": int(key[0]), "text": key}
	case key == ":":
		down = map[string]any{"type": "keyDown", "key": key, "code": "Semicolon", "windowsVirtualKeyCode": 186, "text": key, "modifiers": 8}
	case len(key) == 1 && key >= "a" && key <= "z":
		down = map[string]any{"type": "keyDown", "key": key, "code": "Key" + strings.ToUpper(key), "windowsVirtualKeyCode": int(key[0] - 'a' + 'A'), "text": key}
	}
	d.call("Input.dispatchKeyEvent", down)
	up := map[string]any{"type": "keyUp", "key": key, "code": down["code"], "windowsVirtualKeyCode": down["windowsVirtualKeyCode"]}
	if held, ok := down["modifiers"]; ok {
		up["modifiers"] = held
	}
	d.call("Input.dispatchKeyEvent", up)
}

// countdownSelectAll presses Ctrl+A in the field the focus is in, as a person selects what it holds.
func countdownSelectAll(d *devtools) {
	d.t.Helper()
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "rawKeyDown", "key": "a", "code": "KeyA", "windowsVirtualKeyCode": 65, "modifiers": 2, "commands": []string{"selectAll"}})
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": "a", "code": "KeyA", "windowsVirtualKeyCode": 65, "modifiers": 2})
}

// countdownClickAt clicks with the screen's mouse into a segment at a place between its characters
// (0: before the first; its length: after the last), as a person points into a field: the digits are
// measured in the segment's own font, in the middle of it, and the press goes one pixel past the place,
// over the character after it (before it at the end), so the browser puts the caret there.
func countdownClickAt(t *testing.T, d *devtools, id string, at int) {
	t.Helper()
	var p struct{ X, Y float64 }
	if err := json.Unmarshal([]byte(d.evaluate(`(function (id, at) {
  var f = document.getElementById(id), r = f.getBoundingClientRect(), c = document.createElement("canvas").getContext("2d");
  var s = getComputedStyle(f);
  c.font = [s.fontStyle, s.fontWeight, s.fontSize, s.fontFamily].join(" "); // the shorthand reads "" with tabular figures
  var x = r.left + (r.width - c.measureText(f.value).width) / 2 + c.measureText(f.value.slice(0, at)).width;
  return JSON.stringify({ x: x + (at < f.value.length ? 1 : -1), y: r.top + r.height / 2 });
})(`+strconv.Quote(id)+`, `+strconv.Itoa(at)+`)`)), &p); err != nil {
		t.Fatalf("no segment %s on screen: %v", id, err)
	}
	carouselClick(t, d, "", p.X, p.Y)
}

// countdownInsert puts text in where the focus is, as the browser's input does (DevTools
// Input.insertText: a keyboard's character, a word an input method gives).
func countdownInsert(d *devtools, text string) {
	d.t.Helper()
	d.call("Input.insertText", map[string]any{"text": text})
}

// countdownSegmentsRead is what a step reads of the segments: their values, and which element has the
// focus with what of it is selected.
const countdownSegmentsRead = `var fields = function () { return document.getElementById("minutes").value + ":" + document.getElementById("seconds").value; };
  var focused = function () { var a = document.activeElement; return a === document.body ? "body" : a.id + " " + a.selectionStart + "-" + a.selectionEnd; };`

// TestBrowserCountdownTimeSegments proves the time's segments (the decision of 20/10/2026, evening, and
// the bar of that late evening) on a page the engine serves, in a real browser whose clock runs in real
// time, typed into with its real input (DevTools Input.insertText and Input.dispatchKeyEvent, the mouse
// of Input.dispatchMouseEvent at a point between two digits):
//   - as the server paints it: the segments are the clock, 05:00, text fields of digits with no spinner
//     (type text, inputmode numeric, two digits wide in the bar: narrower than half a field the browser
//     sizes), spinbuttons named Phút and Giây in a group named Thời gian còn lại, each with the
//     placeholder 00 and no binding of the kernel's; the page warns of nothing;
//   - Chạy: the segments count, 04:59 a second in; a click between the 5 and the 9 of the seconds
//     pauses it there, held to the millisecond (nothing changes for 1.5 s), the caret where the click
//     put it (seconds 1-1);
//   - typed while it was playing, after a click between the 0 and the 4 of the minutes: 0 put in takes
//     the place of the 4 (00), 0 typed at the end of the full minutes flows into the seconds and takes
//     the place of their 5 (09), 3 put in takes the place of the 9 (03): the time is 0:03, held, the
//     tag's 5 minutes kept as duration; Enter plays on from it, the focus off the segment: the seconds go
//     down once a second, finish comes once at 3 s, the end painted (00:00), the notice shown;
//   - Dừng returns to the 5 minutes (an edit of a run sets what is left, not the full time);
//   - not playing, 75 put in over the minutes selected (Ctrl+A) is 59 (never above the max), ArrowUp
//     keeps it there, and the time is the full time; Escape puts back the 5 minutes; +1 phút and −1 phút
//     write the minutes; 9 typed over the seconds selected shows 9, as typed; Tab takes the focus away,
//     the time set, the seconds in two digits again (09).
//
// CONTROL, each against a copy served through -overlay (the tree untouched), the proof failing at the
// step named (run 20/10/2026): countdownSegmentWords in ui/countdown.go writing nothing left the fields
// the kernel's two-way ones (value: minutes in the page, "as the server paints it": 5:0, no inputmode,
// no spinbutton, as wide as any field). Run again for the bar (20/10/2026, late evening), against copies
// of countdown v1.0.0.js: begin() without pause() counted on under the fingers ("a click on a segment
// while it plays pauses it": playing, PT0H4M58S a second later); done() without play() left it held
// after Enter ("Enter plays on from the typed value": no finish, 00:03 held); with no beforeinput
// listener the browser put the keys in and only the input was read for its first two digits ("typed
// while it was playing": 00:59, the caret kept in the minutes).
func TestBrowserCountdownTimeSegments(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the countdown browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, countdownSegmentsFiles)
	page := serveUI(t, tenant, "http://localhost/")
	served := page.Body.String()
	if page.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", page.Code, served)
	}
	const field = ` role="spinbutton" inputmode="numeric" autocomplete="off" spellcheck="false" size="2" placeholder="00" aria-valuemin="0" aria-valuemax="59"`
	for _, want := range []string{
		`role="group" aria-label="Thời gian còn lại"`,
		`aria-label="Phút"` + field + ` aria-valuenow="5" aria-valuetext="5 phút" data-part="minutes" value="05">`,
		`aria-label="Giây"` + field + ` aria-valuenow="0" aria-valuetext="0 giây" data-part="seconds" value="00">`,
		`data-kitwork-event="click: minutes++">+1 phút</button>`, `data-kitwork-event="click: minutes--">−1 phút</button>`,
	} {
		if !strings.Contains(served, want) {
			t.Errorf("the page as the server writes it has no %s", want)
		}
	}
	for _, unwanted := range []string{`type="number"`, `value: minutes`, `value: seconds`, `role="timer"`} {
		if strings.Contains(served, unwanted) {
			t.Errorf("the page as the server writes it has %s", unwanted)
		}
	}
	server := carouselServer(tenant)
	defer server.Close()
	check := func(t *testing.T, step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": countdownHeard})
	networkReload(d)

	check(t, "as the server paints it", countdownStep(d, countdownSegmentsRead+`
  await C.frames();
  // two digits wide in the bar (reboot.css), measured against a field as the browser sizes one (20)
  var m = document.getElementById("minutes"), plain = document.createElement("input");
  document.getElementById("left").after(plain);
  var wide = m.getBoundingClientRect().width, plainWide = plain.getBoundingClientRect().width;
  plain.remove();
  return [fields(), C.state(), m.type + " " + m.inputMode + " " + (wide < plainWide / 2 ? "narrow" : wide + "px wide beside " + plainWide + "px"),
    document.getElementById("left").getAttribute("role") + " " + m.getAttribute("role")].join(" | ");`),
		"05:00 | 300000 0:5:0 held 300000 | text numeric narrow | group spinbutton")

	check(t, "Chạy: the segments count", countdownStep(d, countdownSegmentsRead+`
  window.__played = C.click("toggle");
  await C.wait(1300 - (C.now() - window.__played));
  return fields() + " " + C.state();`), "04:59 299000 0:4:59 playing 300000")
	countdownClickAt(t, d, "seconds", 1)
	check(t, "a click on a segment while it plays pauses it", countdownStep(d, countdownSegmentsRead+`
  var clicked = C.now(), at = [fields(), C.state(), focused()].join(" ");
  await C.wait(1500);
  return [at, "then " + C.changes("time", clicked, Infinity), fields() + " " + C.state()].join(" | ");`),
		"04:59 299000 0:4:59 held 300000 seconds 1-1 | then none | 04:59 299000 0:4:59 held 300000")

	countdownClickAt(t, d, "minutes", 1)
	countdownInsert(d, "0")
	countdownKey(d, "0")
	countdownInsert(d, "3")
	check(t, "typed while it was playing: held, the typed value kept", countdownStep(d, countdownSegmentsRead+`
  await C.frames();
  return [fields(), C.state(), focused()].join(" ");`), "00:03 3000 0:0:3 held 300000 seconds 2-2")
	countdownKey(d, "Enter")
	check(t, "Enter plays on from the typed value", countdownStep(d, countdownSegmentsRead+`
  var held = window.__played, resumed = C.when("toggle", "Tạm dừng", held + 1500);
  await C.until(function () { return C.finished() >= 1; }, 4500);
  await C.wait(400);
  var f = window.__finishes[0];
  if (!f) return "no finish: " + fields() + " " + C.state() + " | " + C.changes("time", held, Infinity);
  return [focused(), C.changes("time", resumed, Infinity, resumed, [1000, 2000, 3000]),
    "finish ×" + C.finished() + " " + C.near(f.at - resumed, 3000) + ": " + f.shown, C.toast(), fields() + " " + C.state()].join(" | ");`),
		"body | : PT0H0M2S at 1s, : PT0H0M1S at 2s, : PT0H0M0S at 3s | finish ×1 at 3s: : PT0H0M0S Chạy remaining 0 playing false | Trà đã sẵn sàng | 00:00 0 0:0:0 held 300000")

	check(t, "Dừng returns to the tag's 5 minutes", countdownStep(d, countdownSegmentsRead+`
  C.click("stop");
  await C.frames();
  return fields() + " " + C.state();`), "05:00 300000 0:5:0 held 300000")

	countdownClickAt(t, d, "minutes", 2)
	countdownSelectAll(d)
	countdownInsert(d, "75")
	countdownKey(d, "ArrowUp")
	check(t, "75 put in the minutes is 59, ArrowUp keeps it there", countdownStep(d, countdownSegmentsRead+`
  await C.frames();
  return [fields(), C.state(), focused()].join(" ");`), "59:00 3540000 0:59:0 held 3540000 minutes 2-2")
	countdownKey(d, "Escape")
	check(t, "Escape puts back the 5 minutes", countdownStep(d, countdownSegmentsRead+`
  await C.frames();
  return [fields(), C.state(), focused()].join(" ");`), "05:00 300000 0:5:0 held 300000 body")

	check(t, "+1 phút, −1 phút", countdownStep(d, countdownSegmentsRead+`
  C.click("more");
  await C.frames();
  var more = fields() + " " + C.state();
  C.click("less");
  C.click("less");
  await C.frames();
  return [more, fields() + " " + C.state()].join(" | ");`), "06:00 360000 0:6:0 held 360000 | 04:00 240000 0:4:0 held 240000")

	countdownClickAt(t, d, "seconds", 2)
	countdownSelectAll(d)
	countdownKey(d, "9")
	check(t, "9 over the seconds selected shows 9, as typed", countdownStep(d, countdownSegmentsRead+`
  await C.frames();
  return [fields(), C.state(), focused()].join(" ");`), "04:9 249000 0:4:9 held 249000 seconds 1-1")
	countdownKey(d, "Tab")
	check(t, "Tab takes the focus away, the time set, 09 in two digits", countdownStep(d, countdownSegmentsRead+`
  await C.frames();
  return [fields(), C.state(), document.activeElement.id].join(" ");`), "04:09 249000 0:4:9 held 249000 more")
	check(t, "complaints of the page", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}

// countdownEditsFiles is the countdown of the segments' contract (countdown.md, "Các phần của thời gian")
// with change= on its seconds, and two countdowns chained, the end of the first playing the second,
// whose one segment is the seconds.
var countdownEditsFiles = map[string]string{
	"localhost/router.work": countdownBrowserFiles["localhost/router.work"],
	"localhost/view.ui":     `<view><content /><overlay /></view>`,
	"localhost/overlay.ui":  toastBrowserFiles["localhost/overlay.ui"],
	"localhost/content.ui": `<content padding="4" gap="2">
  <countdown as="$tea" duration="5m" finish="$toast.success('Trà đã sẵn sàng')" flex="row" gap="3">
    <time id="left"><input id="minutes" value="{{ minutes }}" alt="Phút" />:<input id="seconds" value="{{ seconds }}" alt="Giây" change="$toast.info('Giây đã đổi')" /></time>
    <button id="more" click="minutes++">+1 phút</button>
    <button id="less" click="minutes--">−1 phút</button>
    <button id="toggle" click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
    <button id="stop" click="stop()">Dừng</button>
  </countdown>
  <countdown as="$first" duration="2s" finish="$second.play()" flex="row" gap="3">
    <time><input id="first" value="{{ seconds }}" alt="Giây" /></time>
    <button id="first-toggle" click="toggle()">{{ playing ? 'Tạm dừng' : 'Chạy' }}</button>
  </countdown>
  <countdown as="$second" duration="3s" finish="$toast.success('Hết lượt hai')" flex="row" gap="3">
    <time><input id="second" value="{{ seconds }}" alt="Giây" /></time>
  </countdown>
</content>`,
}

// countdownEditsHeard is put in every document before its scripts: the presses of the mouse, each with
// the element it reached (window.__events), the change events the page's handlers hear (window.__changes:
// the segment and its value, heard where the kernel hears them) and those the browser sent itself
// (window.__trusted, heard at the window before anything can stop them).
const countdownEditsHeard = `(function () {
  window.__events = [];
  ["mousedown", "mouseup", "click"].forEach(function (type) {
    document.addEventListener(type, function (e) { window.__events.push(type + ":" + (e.target.id || e.target.tagName)); }, true);
  });
  window.__changes = [];
  window.__trusted = 0;
  window.addEventListener("change", function (e) { if (e.isTrusted) window.__trusted++; }, true);
  document.addEventListener("change", function (e) { window.__changes.push(e.target.id + "=" + e.target.value); });
})()`

// countdownEditsRead is what each step reads: the segments and the state, the toggle's words, which
// element has the focus, where a button stands (rect), the presses heard since the last step (presses).
const countdownEditsRead = countdownSegmentsRead + `
  var read = function () { return [fields(), C.state(), document.getElementById("toggle").textContent].join(" "); };
  var rect = function (id) { var b = document.getElementById(id).getBoundingClientRect(); return Math.round(b.left) + "-" + Math.round(b.right); };
  var presses = function () { var e = window.__events.join(" "); window.__events = []; return e; };`

// countdownTap taps an element with one finger, as on a phone (DevTools touch input, touch emulated).
func countdownTap(t *testing.T, d *devtools, selector string) {
	t.Helper()
	x, y := carouselCentre(t, d, selector, 0, 0)
	d.call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5})
	d.call("Input.dispatchTouchEvent", map[string]any{"type": "touchStart", "touchPoints": []map[string]any{{"x": x, "y": y}}})
	d.call("Input.dispatchTouchEvent", map[string]any{"type": "touchEnd", "touchPoints": []map[string]any{}})
	d.call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": false})
}

// TestBrowserCountdownSegmentEdits proves, on a page the engine serves, in a real browser whose clock
// runs in real time, typed into with its real input (the mouse and touch of DevTools, its keys, its
// input method, its clipboard), what the review of 20/10/2026, evening, found wrong with the segments:
//   - Chạy, then a click on a segment holds it, the toggle reading Chạy; 00:30 typed from the start of
//     the minutes (0, 0, then 3 and 0 flowing on into the seconds); one click on Chạy plays from 00:30
//     (the press reached the toggle, its click too, and it counts on);
//   - while a segment holds it, Dừng stands where it stood, and one click on it (a quick one, then one
//     held 150 ms) stops it at 05:00, the press, the release and the click all on Dừng;
//   - a finger's tap on Chạy after a segment held it plays it, once;
//   - another tab in front during an edit: held while hidden, the typed value kept; back, held still,
//     the focus on the segment, and Escape puts back the time the edit began with and plays on;
//   - an input method composing 3, then 5, over the seconds selected makes 35, each read at the caret
//     its composition began at (the bar of 20/10/2026, late evening); Tab then sends change once, and the
//     change the browser sent itself after the composition is not heard twice; 4 then 2 typed and Tab:
//     once more;
//   - 1:30 pasted from the clipboard into the minutes is 01:30, the caret at their end; Tab, Tab: change
//     from the minutes, and from the seconds the paste set (42 to 30) once the focus leaves them (the
//     review of the bar, 21/10/2026: change was told against the value a segment had when it took the
//     focus, so the seconds, changed before they had it, said nothing);
//
// The steps that type select the segment first (Ctrl+A) or click at a place between its digits: since
// the bar the focus selects nothing.
//   - a run started while a segment has the focus ($first's end plays $second, whose segment is focused)
//     holds, the segment showing the time it holds; Tab plays it, it counts down to 00, and its finish
//     says so.
//
// CONTROL, against the copy of countdown v1.0.0.js from before the review (-overlay, the tree
// untouched), run 20/10/2026, the proof failing at the step named: "one click on Chạy plays from 00:30"
// (00:30 held, Chạy: the edit's end played on at the press, the click paused it again); "a quick click on
// Dừng stops it" (mousedown:stop mouseup:DIV click:DIV, still playing: Tạm dừng, wider, pushed Dừng from
// under the mouse); "a click on Dừng held 150 ms: pressed, nothing moves" (506-573 playing, not 470-536
// held); "hidden during an edit: held" (playing while hidden); "an input method composing 3 then 5"
// (05:05); "Tab after the composition: change once" (seconds=05, the browser's own, and no change of
// the countdown's after 4 then 2); "1:30 pasted into the minutes" (59:42); "$first's end plays
// $second, whose segment is focused: held" (03 3000 playing: counting under the focused segment). Since
// the bar (run 20/10/2026, late evening), against a copy that does not take a composition back to where
// it began, "an input method composing 3 then 5" failed (05:335: the digit read after the one the
// browser had drawn). Against the copy from before the review of the bar (run 21/10/2026), "Tab, Tab:
// change from the minutes and from the seconds the paste set" failed (minutes=01 alone), and no other
// step.
func TestBrowserCountdownSegmentEdits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the countdown browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, countdownEditsFiles)
	page := serveUI(t, tenant, "http://localhost/")
	if served := page.Body.String(); page.Code != http.StatusOK || warningsScriptRe.MatchString(served) {
		t.Fatalf("/ = %d, or it links warnings:\n%.3000s", page.Code, served)
	}
	server := carouselServer(tenant)
	defer server.Close()
	check := func(t *testing.T, step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": countdownHeard})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": countdownEditsHeard})
	networkReload(d)
	step := func(body string) string { d.t.Helper(); return countdownStep(d, countdownEditsRead+body) }

	// Chạy, a click on the seconds, 00:30 typed, one click on Chạy
	countdownStep(d, `window.__played = C.click("toggle"); await C.wait(1300); window.__events = []; return "";`)
	countdownClickAt(t, d, "seconds", 1)
	check(t, "a click on a segment holds it", step(`await C.frames(); return [read(), focused(), presses()].join(" | ");`),
		"04:59 299000 0:4:59 held 300000 Chạy | seconds 1-1 | mousedown:seconds mouseup:seconds click:seconds")
	countdownClickAt(t, d, "minutes", 0)
	for _, key := range []string{"0", "0", "3", "0"} {
		countdownKey(d, key)
	}
	check(t, "00:30 typed", step(`await C.frames(); window.__events = []; return [read(), focused()].join(" | ");`),
		"00:30 30000 0:0:30 held 300000 Chạy | seconds 2-2")
	carouselClick(t, d, "#toggle", 0, 0)
	check(t, "one click on Chạy plays from 00:30", step(`await C.wait(1300); return [presses(), read(), document.activeElement.id].join(" | ");`),
		"mousedown:toggle mouseup:toggle click:toggle | 00:29 29000 0:0:29 playing 300000 Tạm dừng | toggle")

	// Dừng while a segment holds it: a quick click, then one held 150 ms, during which nothing moves
	for _, slow := range []bool{false, true} {
		name := "a quick click on Dừng"
		if slow {
			name = "a click on Dừng held 150 ms"
		}
		countdownStep(d, `if (!kitwork.scope.$tea.playing) C.click("toggle"); await C.wait(1300); return "";`)
		carouselClick(t, d, "#seconds", 0, 0)
		held := step(`await C.frames(); window.__events = []; return rect("stop") + " " + read();`)
		if slow {
			x, y := carouselCentre(t, d, "#stop", 0, 0)
			d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y})
			d.call("Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1})
			time.Sleep(150 * time.Millisecond)
			check(t, name+": pressed, nothing moves", step(`await C.frames(); return rect("stop") + " " + read();`), held)
			d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1})
		} else {
			carouselClick(t, d, "#stop", 0, 0)
		}
		check(t, name+" stops it", step(`await C.wait(1200); return [presses(), read()].join(" | ");`),
			"mousedown:stop mouseup:stop click:stop | 05:00 300000 0:5:0 held 300000 Chạy")
	}

	// a finger's tap on Chạy after a segment held it
	countdownStep(d, `C.click("toggle"); await C.wait(1300); return "";`)
	carouselClick(t, d, "#seconds", 0, 0)
	countdownStep(d, `window.__events = []; return "";`)
	countdownTap(t, d, "#toggle")
	check(t, "a tap on Chạy plays", step(`await C.wait(1200); return [presses(), read()].join(" | ");`),
		"mousedown:toggle mouseup:toggle click:toggle | 04:58 298000 0:4:58 playing 300000 Tạm dừng")

	// another tab in front during an edit
	countdownClickAt(t, d, "seconds", 2)
	countdownSelectAll(d)
	countdownKey(d, "1")
	before := step(`await C.frames(); return read();`)
	check(t, "1 typed in the seconds while it played", before, "04:1 241000 0:4:1 held 300000 Chạy")
	tab := countdownHide(d)
	time.Sleep(1500 * time.Millisecond)
	check(t, "hidden during an edit: held", step(`return [read(), focused()].join(" | ");`), "04:1 241000 0:4:1 held 300000 Chạy | seconds 1-1")
	countdownShow(d, tab)
	check(t, "back: held still, the focus on the segment", step(`await C.frames(); return [read(), focused()].join(" | ");`), "04:1 241000 0:4:1 held 300000 Chạy | seconds 1-1")
	countdownKey(d, "Escape")
	check(t, "Escape puts back the time the edit began with, and plays on", step(`await C.frames(); return [read(), focused()].join(" | ");`),
		"04:58 298000 0:4:58 playing 300000 Tạm dừng | body")
	countdownStep(d, `C.click("stop"); await C.frames(); window.__changes = []; return "";`)

	// an input method composing 3, then 5; Tab: change once; 4 then 2 and Tab: once more
	carouselClick(t, d, "#seconds", 0, 0)
	countdownSelectAll(d)
	for _, digit := range []string{"3", "5"} {
		d.call("Input.imeSetComposition", map[string]any{"text": digit, "selectionStart": 1, "selectionEnd": 1})
		countdownInsert(d, digit)
	}
	check(t, "an input method composing 3 then 5", step(`await C.frames(); return [read(), focused()].join(" | ");`), "05:35 335000 0:5:35 held 335000 Chạy | seconds 2-2")
	countdownKey(d, "Tab")
	check(t, "Tab after the composition: change once", step(`await C.wait(300); return [window.__changes.join(" "), "the browser's own " + window.__trusted, C.toast()].join(" | ");`),
		"seconds=35 | the browser's own 1 | Giây đã đổi")
	carouselClick(t, d, "#seconds", 0, 0)
	countdownSelectAll(d)
	countdownKey(d, "4")
	countdownKey(d, "2")
	countdownKey(d, "Tab")
	check(t, "4 then 2 and Tab: once more", step(`await C.wait(300); return [read(), window.__changes.join(" "), "the browser's own " + window.__trusted].join(" | ");`),
		"05:42 342000 0:5:42 held 342000 Chạy | seconds=35 seconds=42 | the browser's own 1")

	// 1:30 pasted from the clipboard into the minutes
	d.call("Browser.grantPermissions", map[string]any{"permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"}})
	check(t, "the clipboard holds 1:30", step(`await navigator.clipboard.writeText("1:30"); return navigator.clipboard.readText();`), "1:30")
	carouselClick(t, d, "#minutes", 0, 0)
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "rawKeyDown", "key": "v", "code": "KeyV", "windowsVirtualKeyCode": 86, "modifiers": 2, "commands": []string{"paste"}})
	d.call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": "v", "code": "KeyV", "windowsVirtualKeyCode": 86, "modifiers": 2})
	check(t, "1:30 pasted into the minutes", step(`await C.frames(); return [read(), focused()].join(" | ");`), "01:30 90000 0:1:30 held 90000 Chạy | minutes 2-2")
	countdownKey(d, "Tab")
	countdownKey(d, "Tab")
	check(t, "Tab, Tab: change from the minutes and from the seconds the paste set", step(`await C.frames(); return [window.__changes.join(" "), document.activeElement.id].join(" | ");`),
		"seconds=35 seconds=42 minutes=01 seconds=30 | more")

	// a run started while a segment has the focus
	countdownStep(d, `await C.clear(); document.getElementById("first-toggle").click(); return "";`)
	carouselClick(t, d, "#second", 0, 0)
	check(t, "$first's end plays $second, whose segment is focused: held", step(`await C.wait(2600); var s = kitwork.scope.$second;
  return [document.getElementById("second").value, s.remaining, s.playing ? "playing" : "held", document.activeElement.id].join(" ");`), "03 3000 held second")
	countdownKey(d, "Tab")
	check(t, "Tab plays it to its end", step(`await C.wait(3500); var s = kitwork.scope.$second;
  return [document.getElementById("second").value, s.remaining, s.playing ? "playing" : "held", C.toast()].join(" | ");`), "00 | 0 | held | Hết lượt hai")
	check(t, "complaints of the page", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}
