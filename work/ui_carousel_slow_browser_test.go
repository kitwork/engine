package work

import (
	"fmt"
	"testing"
)

// carouselSlowFrames makes the page a browser that draws a frame only now and then (for the carousel
// whose alias it is called with): every
// requestAnimationFrame answers after 1.1 seconds, and a smooth scroll of the track moves a fifth of the
// way each such frame (an instant scroll a step, so its scroll event and its scrollend come once a frame
// too). The app's own browser pane keeps a page it does not show in front at about a frame a second.
const carouselSlowFrames = `(function (alias) {
  var raf = window.requestAnimationFrame.bind(window);
  window.requestAnimationFrame = function (callback) { return setTimeout(function () { raf(callback); }, 1100); };
  var track = window.__carousel.parts(alias).track, instant = track.scrollTo.bind(track), timer = 0;
  // the steps of the way are no snap points: a smooth scroll is not snapped until it rests
  var snap = function (on) { track.style.scrollSnapType = on ? "" : "none"; };
  track.scrollTo = function (options) {
    clearTimeout(timer);
    snap(true);
    if (!options || options.behavior === "instant") return instant(options);
    var from = track.scrollLeft, to = options.left, step = 0;
    var move = function () {
      step++;
      snap(step === 5);
      instant({ left: from + (to - from) * step / 5, behavior: "instant" });
      if (step < 5) timer = setTimeout(move, 1100);
    };
    timer = setTimeout(move, 1100);
  };
  return "slow";
})`

// next() from the last slide in a browser whose frames come a second apart still goes on to the first
// the way it always goes: the first comes round, the track slides only forward to it, step by step, and
// it goes home once the track rests there, the same slide in the same place — never halfway, never back
// to the last. The move is watched in the frames the browser draws, so a slow browser waits as long as
// its scroll takes.
// CONTROL: with the move watched by a 900ms timer instead of frames (the component before the evening of
// 18/10/2026) the slide went home after the first step and the track jumped back to the last, which
// stayed in view ("seen 3", current 0 3); with scrollend taking the slide nearest for the one asked for,
// the slide went home halfway (run 18/10/2026).
func TestBrowserCarouselSlowFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, carouselBrowserFiles())
	server := carouselServer(tenant)
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	carouselVisit(d, server.URL+"/", "no-preference")

	// the last slide by its dot, in the browser as it is
	w := carouselWatch(t, d, "repeated", `window.__carousel.parts("repeated").dots[3].click();`)
	if last := w.Samples[len(w.Samples)-1]; last.Current != 3 || last.Left != 1920 {
		t.Fatalf("dot 4: current %g, left %g; want 3 at 1920\n frames %s", last.Current, last.Left, carouselTrace(w))
	}

	if got := d.evaluate(carouselSlowFrames + `("repeated")`); got != "slow" {
		t.Fatalf("the slow browser: %q", got)
	}
	// the frames read the steps 1408, 1536, 1664, 1792; the last, 1920, and home come between two frames
	// (its scrollend: it rests there), so home says where it went from
	w = carouselWatchFor(t, d, "repeated", `document.getElementById("repeated-next").click();`, 15000)
	want := "current 0 | slides 1000 | dots 1000 | counter 1 / 4 | seen 3 0 | slide 0 comes round (order 1): slide 3 at 0 → slide 3 at 0, left 1920 → 1280 | " +
		"while round 1280 → 1792 only forward, smoothly | home: slide 0 at 0 → slide 0 at 0, left 1920 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend"
	if got := carouselRound(w); got != want {
		t.Errorf("next() from the last, a frame every 1.1s:\n got  %s\n want %s\n frames %s", got, want, carouselTrace(w))
	}
}

// A carousel that plays by itself, in a browser whose frames come a second apart: its move takes longer
// than its duration (2s), so the time runs out while the track still slides, when a press is not taken.
// The time starts again instead, and the carousel goes on playing: it moves on from the first slide and
// again from the second, never stopping on one.
// CONTROL: with the time's turn simply calling next() (not taken while it slides, so no time started
// again) the carousel stopped on the second slide for good (run 19/10/2026).
func TestBrowserCarouselSlowFramesPlays(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, carouselBrowserFiles())
	server := carouselServer(tenant)
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	carouselVisit(d, server.URL+"/play", "no-preference")
	if got := d.evaluate(carouselSlowFrames + `("play")`); got != "slow" {
		t.Fatalf("the slow browser: %q", got)
	}
	// the slides current goes through, read every 250ms for 20 seconds (a move takes five frames of 1.1s)
	got := d.evaluate(`(function () {
  var scope = kit.scopeFor(window.__carousel.parts("play").host), seen = [String(scope.current)], started = performance.now();
  return new Promise(function (done) {
    var read = setInterval(function () {
      var now = String(scope.current);
      if (seen[seen.length - 1] !== now) seen.push(now);
      if (performance.now() - started > 20000 || seen.length >= 3) { clearInterval(read); done(seen.join(" ") + (scope.playing ? ", playing" : ", not playing")); }
    }, 250);
  });
})()`)
	if got != "0 1 2, playing" {
		t.Errorf("duration 2s, a frame every 1.1s: current went %q, want \"0 1 2, playing\"", got)
	}
}

// A slide a carousel reaches by itself stays its whole duration once it is there, however long the move
// took: in a browser whose frames come a second apart the move from the first slide to the second takes
// five frames, longer than the duration (2s), and the second slide then shows two seconds before the
// carousel moves on, never what was left of the two seconds counted from the press.
// CONTROL: with the time started only at the press (and again while the track still slides) the second
// slide showed about half a second (run 19/10/2026).
func TestBrowserCarouselSlowFramesStaysItsDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, carouselBrowserFiles())
	server := carouselServer(tenant)
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	carouselVisit(d, server.URL+"/play", "no-preference")
	if got := d.evaluate(carouselSlowFrames + `("play")`); got != "slow" {
		t.Fatalf("the slow browser: %q", got)
	}
	// when the track came to rest at the second slide's place, and when current moved on from it
	raw := d.evaluate(`(function () {
  var p = window.__carousel.parts("play"), scope = kit.scopeFor(p.host), started = performance.now(), arrived = 0, last = scope.current;
  var place = function () { return p.slides[1].offsetLeft - p.slides[0].offsetLeft; };
  p.track.addEventListener("scrollend", function () {
    if (!arrived && Math.abs(p.track.scrollLeft - place()) < 1) arrived = performance.now() - started;
  });
  return new Promise(function (done) {
    var read = setInterval(function () {
      var t = performance.now() - started;
      if (scope.current !== last) {
        last = scope.current;
        if (last === 2) { clearInterval(read); done(Math.round(arrived) + " " + Math.round(t)); return; }
      }
      if (t > 25000) { clearInterval(read); done(Math.round(arrived) + " never"); }
    }, 50);
  });
})()`)
	var arrived, left float64
	if _, err := fmt.Sscanf(raw, "%g %g", &arrived, &left); err != nil || arrived == 0 {
		t.Fatalf("the second slide: %q", raw)
	}
	if shown := left - arrived; shown < 1800 || shown > 2700 {
		t.Errorf("the second slide showed %.0fms once it was there (arrived %.0fms, moved on %.0fms), want about 2000ms", shown, arrived, left)
	}
}
