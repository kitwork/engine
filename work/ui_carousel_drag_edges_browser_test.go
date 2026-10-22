package work

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// carouselEdgeFiles is the carousel site of the mouse's drag with two pages more:
//   - /padded: $padded, four slides on a track with padding of its own (padding="4", 16px), with
//     previous, next and a counter;
//   - /nested: $outer, three slides, the first holding a carousel of its own, $inner, three slides.
func carouselEdgeFiles() map[string]string {
	files := carouselDragFiles()
	files["localhost/padded/content.ui"] = `<content padding="4">
  <carousel as="$padded" alt="Dải có lề" max-width="640px" gap="2">
    <list padding="4">
` + carouselSlides(4, "") + `    </list>
    <box flex="row" gap="2">
      <button id="padded-previous" click="previous()">Trước</button>
      <button id="padded-next" click="next()">Sau</button>
      <text id="padded-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
</content>`
	files["localhost/nested/content.ui"] = `<content padding="4">
  <carousel as="$outer" alt="Ngoài" max-width="640px" gap="2">
    <list>
      <item><box padding="4" background="panel"><carousel as="$inner" alt="Trong" max-width="480px" gap="2"><list>
` + carouselSlides(3, "") + `      </list></carousel></box></item>
` + carouselSlides(2, "") + `    </list>
  </carousel>
</content>`
	return files
}

// carouselAt is a carousel in a few words: current, the slides marked, the counter, where the track is,
// the slide under its middle and where its left edge stands, the order on its slides, and the track's
// own style.
func carouselAt(t *testing.T, d *devtools, alias string) string {
	t.Helper()
	s := carouselState(t, d, alias)
	order := s.Order
	if order == "" {
		order = "none"
	}
	style := d.evaluate(`window.__carousel.parts(` + strconv.Quote(alias) + `).track.style.cssText`)
	return fmt.Sprintf("current %g | slides %s | counter %s | left %g | slide %g seen at %g | order %s | style %q",
		s.Current, s.Slides, s.Counter, s.Left, s.Seen, s.At, order, style)
}

// carouselButtons sends one event of the screen's mouse at x,y with the buttons held after it (1 the
// main, 2 the second, 3 both); button names the one pressed or let go ("" for a move).
func carouselButtons(d *devtools, kind string, x, y float64, button string, buttons int) {
	params := map[string]any{"type": kind, "x": x, "y": y, "buttons": buttons}
	if button != "" {
		params["button"] = button
		params["clickCount"] = 1
	}
	d.call("Input.dispatchMouseEvent", params)
}

// TestBrowserCarouselMouseDragEdges proves what meets the mouse's drag beyond a plain drag (the review of
// 20/10/2026), in a real browser whose frames run, through the screen's own mouse, each part with a
// browser of its own:
//   - the browser's own scroll under a press (the arrow key on the focused track glides it to the next
//     slide): a press held still while the glide ends lands like any scroll (current, the slide marked,
//     the counter on the slide the track rests at), and a drag begun after it begins where the track is
//     then, following the mouse from there (8, 16, 24px past the second slide), never from where it was
//     at the press;
//   - a carousel inside another's slide: dragged, only the inner one follows the mouse and moves on; the
//     outer one keeps its slide and its own style;
//   - a track with padding of its own: next() reaches the fourth slide (the end of the row, its start
//     where the padding ends) and from there comes round to the first, only forward; a drag back at the
//     first comes round to the last, a drag on at the last to the first;
//   - three cards a row: a drag across nearly two cards (520px) goes on to the card nearest, the third,
//     only forward; dragged on at the last place, or back at the first, it stays (next() and previous()
//     would run across the row against the drag);
//   - a second button: pressed and let go while the main one drags, it opens no menu (contextmenu
//     cancelled) and the drag goes on; the main one let go while the second is held ends the drag there
//     (it moves on, the closed hand gone) and the track no longer follows the mouse.
//
// CONTROL (run 20/10/2026, each a disabled copy of v1.1.0.js given to go test -overlay; the file itself
// untouched; each run on the part it guards): the scroll and scrollend landing nothing while a press has
// not moved left the held press, and the release, on the first slide with the track at the second
// (current 0, counter 1 / 3, left 640); the drag's start read at the press threw the track back to 8, 16,
// 24 and let go on the first slide (left 0); without the press claimed by the nearest carousel the outer
// one followed the inner one's drag (left 200, the closed hand) while the inner one, its pointer taken by
// the outer's capture, stopped at 20, and let go the outer moved on to its second slide (640), the inner
// one out of sight; reachable() without the track's padding sent the third next() back to the first
// (1232 → 16), the fourth slide never reached, and the drags at either end came round to nothing (424 →
// 16, back to the first; 216 → 624, the second); let go by next() and previous() as before, the 520px
// drag slid back to the second card (520 → 300 only back), dragged on at the last place it ran back
// across the row (900 → 0) and back at the first on to the last place (0 → 900); the pointermove
// without the buttons it holds kept the drag on the second button alone (the track at 840 with the
// closed hand once the main one was let go); the contextmenu left alone opened its menu mid-drag.
func TestBrowserCarouselMouseDragEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, carouselEdgeFiles())
	server := carouselServer(tenant)
	defer server.Close()
	check := func(t *testing.T, step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	open := func(t *testing.T, path string) *devtools {
		d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
		d.loaded(exampleSettle)
		carouselVisit(d, server.URL+path, "no-preference")
		return d
	}
	slow := func(x, y float64, steps ...float64) carouselHand {
		return carouselHand{x: x, y: y, steps: steps, apart: 40 * time.Millisecond, hold: 200 * time.Millisecond}
	}
	letGo := func(t *testing.T, step string, g carouselDragged, want string) {
		t.Helper()
		if got := carouselLetGoSays(g.Watch); got != want {
			t.Errorf("%s, let go:\n got  %s\n want %s\n frames %s", step, got, want, carouselTrace(g.Watch))
		}
	}
	// glide presses the right arrow on the focused track (the browser glides it to the next slide) and the
	// mouse at once on its middle, and counts the scroll events that come after the press: none would
	// mean the glide was over before the press, which proves nothing
	glide := func(t *testing.T, d *devtools, alias string) (float64, float64) {
		x, y := carouselTrackCentre(t, d, alias)
		d.evaluate(`(function () { var track = window.__carousel.parts(` + strconv.Quote(alias) + `).track; window.__under = -1;
  document.addEventListener("pointerdown", function () { window.__under = 0; }, true);
  track.addEventListener("scroll", function () { if (window.__under >= 0) window.__under++; });
  track.focus(); return ""; })()`)
		carouselMouse(d, "mouseMoved", x, y, false)
		toastKey(d, "ArrowRight", 39)
		carouselMouse(d, "mousePressed", x, y, true)
		return x, y
	}
	under := func(d *devtools) string {
		if n, _ := strconv.Atoi(d.evaluate(`String(window.__under)`)); n > 0 {
			return "the glide went on under the press"
		}
		return "the glide was over before the press"
	}

	t.Run("the browser's own scroll under a press", func(t *testing.T) {
		d := open(t, "/")
		x, y := glide(t, d, "written")
		time.Sleep(1200 * time.Millisecond)
		check(t, "held still while it glided", under(d)+" | "+carouselAt(t, d, "written"),
			`the glide went on under the press | current 1 | slides 010 | counter 2 / 3 | left 640 | slide 1 seen at 0 | order none | style ""`)
		carouselMouse(d, "mouseReleased", x, y, false)
		time.Sleep(300 * time.Millisecond)
		check(t, "let go", carouselAt(t, d, "written"),
			`current 1 | slides 010 | counter 2 / 3 | left 640 | slide 1 seen at 0 | order none | style ""`)

		d = open(t, "/")
		x, y = glide(t, d, "written")
		time.Sleep(1000 * time.Millisecond)
		var lefts []string
		for _, step := range []float64{-8, -16, -24} {
			carouselMouse(d, "mouseMoved", x+step, y, true)
			time.Sleep(40 * time.Millisecond)
			lefts = append(lefts, strconv.FormatFloat(carouselState(t, d, "written").Left, 'f', -1, 64))
		}
		check(t, "dragged 8, 16, 24px after the glide", under(d)+" | the track at "+strings.Join(lefts, ", "), "the glide went on under the press | the track at 648, 656, 664")
		carouselMouse(d, "mouseReleased", x-24, y, false)
		time.Sleep(1200 * time.Millisecond)
		check(t, "let go", carouselAt(t, d, "written"),
			`current 1 | slides 010 | counter 2 / 3 | left 640 | slide 1 seen at 0 | order none | style ""`)
	})

	t.Run("a carousel inside another's slide", func(t *testing.T) {
		d := open(t, "/nested")
		x, y := carouselTrackCentre(t, d, "inner")
		carouselMouse(d, "mouseMoved", x, y, false)
		carouselMouse(d, "mousePressed", x, y, true)
		for _, step := range []float64{-20, -60, -100, -140, -180, -200} {
			time.Sleep(40 * time.Millisecond)
			carouselMouse(d, "mouseMoved", x+step, y, true)
		}
		time.Sleep(150 * time.Millisecond)
		check(t, "the outer one, held", carouselAt(t, d, "outer"),
			`current 0 | slides 100 | counter  | left 0 | slide 0 seen at 0 | order none | style ""`)
		check(t, "the inner one, held", carouselAt(t, d, "inner"),
			`current 0 | slides 100 | counter  | left 200 | slide 0 seen at -200 | order none | style "cursor: grabbing; user-select: none; scroll-snap-type: none;"`)
		carouselMouse(d, "mouseReleased", x-200, y, false)
		time.Sleep(1500 * time.Millisecond)
		check(t, "the outer one, let go", carouselAt(t, d, "outer"),
			`current 0 | slides 100 | counter  | left 0 | slide 0 seen at 0 | order none | style ""`)
		check(t, "the inner one, let go", carouselAt(t, d, "inner"),
			`current 1 | slides 010 | counter  | left 480 | slide 1 seen at 0 | order none | style ""`)
	})

	t.Run("a track with padding of its own", func(t *testing.T) {
		d := open(t, "/padded")
		var went []string
		for i := 0; i < 3; i++ {
			w := carouselWatch(t, d, "padded", `document.getElementById("padded-next").click();`)
			went = append(went, carouselMove(w))
		}
		check(t, "next() three times", strings.Join(went, " ; "),
			"current 1 | slides 0100 | dots  | counter 2 / 4 | 16 → 624 smoothly, rests by its scrollend ; "+
				"current 2 | slides 0010 | dots  | counter 3 / 4 | 624 → 1232 smoothly, rests by its scrollend ; "+
				"current 3 | slides 0001 | dots  | counter 4 / 4 | 1232 → 1824 smoothly, rests by its scrollend")
		w := carouselWatch(t, d, "padded", `document.getElementById("padded-next").click();`)
		check(t, "next() at the fourth", carouselRound(w),
			"current 0 | slides 1000 | dots  | counter 1 / 4 | seen 3 0 | slide 0 comes round (order 1): slide 3 at 16 → slide 3 at 0, left 1824 → 1232 | while round 1232 → 1824 only forward, smoothly | home: slide 0 at 16 → slide 0 at 0, left 1824 → 16 | rests at 16, order none, slide 0 seen at 0, by its scrollend")

		x, y := carouselTrackCentre(t, d, "padded")
		g := carouselDrag(t, d, "padded", slow(x, y, 20, 60, 100, 140, 180, 200))
		letGo(t, "dragged back at the first", g, "current 3 | slides 0001 | dots  | counter 4 / 4 | seen 0 3 | while round 424 → 16 only back, smoothly | rests at 1824, order none, slide 3 seen at 16, by its scrollend")
		g = carouselDrag(t, d, "padded", slow(x, y, -20, -60, -100, -140, -180, -200))
		letGo(t, "dragged on at the last", g, "current 0 | slides 1000 | dots  | counter 1 / 4 | seen 3 0 | while round 1416 → 1824 only forward, smoothly | rests at 16, order none, slide 0 seen at 0, by its scrollend")
	})

	t.Run("three cards a row", func(t *testing.T) {
		d := open(t, "/cards")
		_, y := carouselTrackCentre(t, d, "cards")
		g := carouselDrag(t, d, "cards", slow(860, y, -50, -150, -250, -350, -450, -520))
		letGo(t, "dragged 520px from the first card", g, "current 2 | slides 001000 | dots  | counter 3 / 6 | seen 3 | 520 → 600 only forward, smoothly | rests at 600, order none, slide 3 seen at 300, by its scrollend")
		w := carouselWatch(t, d, "cards", `kit.scopeFor(window.__carousel.parts("cards").host).go(3);`)
		if last := w.Samples[len(w.Samples)-1]; last.Current != 3 {
			t.Fatalf("go(3): current %g\n frames %s", last.Current, carouselTrace(w))
		}
		g = carouselDrag(t, d, "cards", slow(600, y, -40, -80, -120))
		letGo(t, "dragged on at the last place", g, "current 3 | slides 000100 | dots  | counter 4 / 6 | seen 4 | 900 → 900 stays, at once | rests at 900, order none, slide 4 seen at 300, no scrollend")
		check(t, "dragged on at the last place, then", carouselAt(t, d, "cards"),
			`current 3 | slides 000100 | counter 4 / 6 | left 900 | slide 4 seen at 300 | order none | style ""`)
		w = carouselWatch(t, d, "cards", `kit.scopeFor(window.__carousel.parts("cards").host).go(0);`)
		if last := w.Samples[len(w.Samples)-1]; last.Current != 0 {
			t.Fatalf("go(0): current %g\n frames %s", last.Current, carouselTrace(w))
		}
		g = carouselDrag(t, d, "cards", slow(300, y, 40, 80, 120))
		letGo(t, "dragged back at the first", g, "current 0 | slides 100000 | dots  | counter 1 / 6 | seen 1 | 0 → 0 stays, at once | rests at 0, order none, slide 1 seen at 300, no scrollend")
	})

	t.Run("a second button", func(t *testing.T) {
		d := open(t, "/")
		d.evaluate(`window.__menus = []; document.addEventListener("contextmenu", function (e) { window.__menus.push(e.defaultPrevented ? "prevented" : "opened"); }); ""`)
		x, y := carouselTrackCentre(t, d, "written")
		carouselMouse(d, "mouseMoved", x, y, false)
		carouselMouse(d, "mousePressed", x, y, true)
		for _, step := range []float64{-20, -60, -100} {
			time.Sleep(40 * time.Millisecond)
			carouselMouse(d, "mouseMoved", x+step, y, true)
		}
		// the second pressed and let go while the main one drags
		carouselButtons(d, "mousePressed", x-100, y, "right", 3)
		time.Sleep(40 * time.Millisecond)
		carouselButtons(d, "mouseReleased", x-100, y, "right", 1)
		time.Sleep(40 * time.Millisecond)
		for _, step := range []float64{-150, -200} {
			carouselMouse(d, "mouseMoved", x+step, y, true)
			time.Sleep(40 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond)
		check(t, "a right click mid-drag, then on to 200px", d.evaluate(`window.__menus.join(",") || "no menu"`)+" | "+carouselAt(t, d, "written"),
			`prevented | current 0 | slides 100 | counter 1 / 3 | left 200 | slide 0 seen at -200 | order none | style "cursor: grabbing; user-select: none; scroll-snap-type: none;"`)
		carouselMouse(d, "mouseReleased", x-200, y, false)
		time.Sleep(1200 * time.Millisecond)
		check(t, "let go", carouselAt(t, d, "written"),
			`current 1 | slides 010 | counter 2 / 3 | left 640 | slide 1 seen at 0 | order none | style ""`)

		// the main one let go while the second is held
		carouselMouse(d, "mousePressed", x, y, true)
		for _, step := range []float64{-20, -60, -100, -140, -180, -200} {
			time.Sleep(40 * time.Millisecond)
			carouselMouse(d, "mouseMoved", x+step, y, true)
		}
		carouselButtons(d, "mousePressed", x-200, y, "right", 3)
		time.Sleep(40 * time.Millisecond)
		carouselButtons(d, "mouseReleased", x-200, y, "left", 2)
		time.Sleep(1200 * time.Millisecond)
		check(t, "the main one let go, the second held", carouselAt(t, d, "written"),
			`current 2 | slides 001 | counter 3 / 3 | left 1280 | slide 2 seen at 0 | order none | style ""`)
		for _, step := range []float64{-260, -320} {
			carouselButtons(d, "mouseMoved", x+step, y, "", 2)
			time.Sleep(40 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond)
		check(t, "moved with the second alone", carouselAt(t, d, "written"),
			`current 2 | slides 001 | counter 3 / 3 | left 1280 | slide 2 seen at 0 | order none | style ""`)
		carouselButtons(d, "mouseReleased", x-320, y, "right", 0)
		time.Sleep(600 * time.Millisecond)
		check(t, "all let go", carouselAt(t, d, "written"),
			`current 2 | slides 001 | counter 3 / 3 | left 1280 | slide 2 seen at 0 | order none | style ""`)
	})
}

// TestBrowserCarouselRightToLeft proves the carousel on a page written right to left
// (router.language("ar") writes dir="rtl"), the mirror of left to right, in a real browser whose
// frames run: the row starts at the right, so the track scrolls below 0 (scrollLeft -640 shows the
// second slide), and:
//   - next() slides to the second slide and previous() back, the slide seen under the middle of the
//     track each time the one current names; next() from the last comes round to the first, only on
//     (down the row: the track's scrollLeft only falling);
//   - the screen's mouse dragged 200px to the right moves on (the next slide lies to the left), to the
//     left goes back; dragged on to the right at the last, the first comes round;
//   - fade: a mouse drag 100px to the right, ArrowLeft on the focused track and a finger swiped to the
//     right each move on (← is next(), as the browser's own scroll goes on a track that slides).
//
// CONTROL (run 20/10/2026, a disabled copy of v1.1.0.js given to go test -overlay: spot() and sign()
// reading every page as left to right): next(), previous() and go(2) changed current while the track
// stayed at 0 (slide 0 seen); a drag either way let go at 0 with current naming another slide, the round
// never left 0; fade went back on the drag to the right, ArrowLeft and the swipe to the right, and on
// with ArrowRight.
func TestBrowserCarouselRightToLeft(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	files := carouselDragFiles()
	files["localhost/router.work"] = strings.Replace(files["localhost/router.work"], `router.language("vi")`, `router.language("ar")`, 1)
	tenant := buildUISite(t, files)
	server := carouselServer(tenant)
	defer server.Close()
	check := func(t *testing.T, step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	open := func(t *testing.T, path string) *devtools {
		d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
		d.loaded(exampleSettle)
		carouselVisit(d, server.URL+path, "no-preference")
		return d
	}
	slow := func(x, y float64, steps ...float64) carouselHand {
		return carouselHand{x: x, y: y, steps: steps, apart: 40 * time.Millisecond, hold: 200 * time.Millisecond}
	}
	press := func(t *testing.T, d *devtools, id string) string {
		w := carouselWatch(t, d, "written", `document.getElementById("`+id+`").click();`)
		return carouselMove(w)
	}

	t.Run("slide", func(t *testing.T) {
		d := open(t, "/")
		check(t, "the page", d.evaluate(`document.documentElement.dir + " " + getComputedStyle(window.__carousel.parts("written").track).direction`), "rtl rtl")
		check(t, "next()", press(t, d, "written-next"), "current 1 | slides 010 | dots  | counter 2 / 3 | 0 → -640 smoothly, rests by its scrollend")
		check(t, "then", carouselAt(t, d, "written"), `current 1 | slides 010 | counter 2 / 3 | left -640 | slide 1 seen at 0 | order none | style ""`)
		check(t, "previous()", press(t, d, "written-previous"), "current 0 | slides 100 | dots  | counter 1 / 3 | -640 → 0 smoothly, rests by its scrollend")

		x, y := carouselTrackCentre(t, d, "written")
		g := carouselDrag(t, d, "written", slow(x, y, 20, 60, 100, 140, 180, 200))
		if got, want := carouselLetGoSays(g.Watch), "current 1 | slides 010 | dots  | counter 2 / 3 | seen 0 1 | -200 → -640 only back, smoothly | rests at -640, order none, slide 1 seen at 0, by its scrollend"; got != want {
			t.Errorf("dragged right 200px, let go:\n got  %s\n want %s\n frames %s", got, want, carouselTrace(g.Watch))
		}
		g = carouselDrag(t, d, "written", slow(x, y, -20, -60, -100, -140, -180, -200))
		if got, want := carouselLetGoSays(g.Watch), "current 0 | slides 100 | dots  | counter 1 / 3 | seen 1 0 | -440 → 0 only forward, smoothly | rests at 0, order none, slide 0 seen at 0, by its scrollend"; got != want {
			t.Errorf("dragged left 200px, let go:\n got  %s\n want %s\n frames %s", got, want, carouselTrace(g.Watch))
		}
		check(t, "go(2)", carouselMove(carouselWatch(t, d, "written", `kit.scopeFor(window.__carousel.parts("written").host).go(2);`)),
			"current 2 | slides 001 | dots  | counter 3 / 3 | 0 → -1280 smoothly, rests by its scrollend")
		w := carouselWatch(t, d, "written", `document.getElementById("written-next").click();`)
		check(t, "next() at the last", carouselRound(w),
			"current 0 | slides 100 | dots  | counter 1 / 3 | seen 2 0 | slide 0 comes round (order 1): slide 2 at 0 → slide 2 at 0, left -1280 → -640 | while round -640 → -1280 only back, smoothly | home: slide 0 at 0 → slide 0 at 0, left -1280 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend")
		check(t, "go(2) again", carouselMove(carouselWatch(t, d, "written", `kit.scopeFor(window.__carousel.parts("written").host).go(2);`)),
			"current 2 | slides 001 | dots  | counter 3 / 3 | 0 → -1280 smoothly, rests by its scrollend")
		g = carouselDrag(t, d, "written", slow(x, y, 20, 60, 100, 140, 180, 200))
		if got, want := carouselLetGoSays(g.Watch), "current 0 | slides 100 | dots  | counter 1 / 3 | seen 2 0 | while round -840 → -1280 only back, smoothly | rests at 0, order none, slide 0 seen at 0, by its scrollend"; got != want {
			t.Errorf("dragged right at the last, let go:\n got  %s\n want %s\n frames %s", got, want, carouselTrace(g.Watch))
		}
	})

	t.Run("fade", func(t *testing.T) {
		d := open(t, "/fade")
		shown := func() string {
			d.evaluate(`window.__carousel.wait(450)`)
			s := carouselState(t, d, "fade")
			return fmt.Sprintf("current %g | slides %s | counter %s", s.Current, s.Slides, s.Counter)
		}
		x, y := carouselTrackCentre(t, d, "fade")
		carouselDrag(t, d, "fade", slow(x, y, 20, 60, 100))
		check(t, "dragged right 100px", shown(), "current 1 | slides 010 | counter 2 / 3")
		d.evaluate(`window.__carousel.parts("fade").track.focus(); ""`)
		toastKey(d, "ArrowLeft", 37)
		check(t, "ArrowLeft", shown(), "current 2 | slides 001 | counter 3 / 3")
		toastKey(d, "ArrowRight", 39)
		check(t, "ArrowRight", shown(), "current 1 | slides 010 | counter 2 / 3")
		carouselSwipe(t, d, "fade", 120)
		check(t, "a finger swiped right", shown(), "current 2 | slides 001 | counter 3 / 3")
	})
}
