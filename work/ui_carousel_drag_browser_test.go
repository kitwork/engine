package work

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// carouselDragFiles is the carousel site with two pages more:
//   - /links: $links, three slides, the first holding words (#words) and a link (#slide-link, to #went);
//   - /one: $one, a single slide.
func carouselDragFiles() map[string]string {
	files := carouselBrowserFiles()
	files["localhost/links/content.ui"] = `<content padding="4">
  <carousel as="$links" alt="Có liên kết" max-width="640px" gap="2">
    <list>
      <item><box padding="6" gap="2" background="panel"><text id="words">Một trang có chữ để chọn bằng chuột</text><link id="slide-link" value="#went">Xem thêm</link></box></item>
` + carouselSlides(2, "") + `    </list>
  </carousel>
</content>`
	files["localhost/one/content.ui"] = `<content padding="4">
  <carousel as="$one" alt="Một trang" max-width="640px">
    <list>
` + carouselSlides(1, "") + `    </list>
  </carousel>
</content>`
	return files
}

// carouselHand is a drag of the screen's mouse: pressed at x,y with the main button, moved across by
// each step in turn (from where it was pressed), apart between, held still for hold, then let go where
// it is.
type carouselHand struct {
	x, y  float64
	steps []float64
	apart time.Duration
	hold  time.Duration
}

// carouselDragged is what a drag left: the carousel read while the mouse still held it (when it was
// held still long enough to read), the track's own style, its cursor and the words selected then, the frames from the
// moment it let go (a watch begun by the pointerup, before the component has it), and every order set
// on a slide of the carousel from the press on.
type carouselDragged struct {
	Held     carouselRead
	Style    string
	Cursor   string
	Selected string
	Watch    carouselWatched
	Orders   []carouselOrder
}

// carouselMouse sends one event of the screen's mouse at x,y, the main button pressed or not.
func carouselMouse(d *devtools, kind string, x, y float64, pressed bool) {
	params := map[string]any{"type": kind, "x": x, "y": y, "buttons": 0}
	if pressed {
		params["buttons"] = 1
	}
	if pressed || kind != "mouseMoved" {
		params["button"] = "left"
	}
	if kind != "mouseMoved" {
		params["clickCount"] = 1
	}
	d.call("Input.dispatchMouseEvent", params)
}

// carouselTrackCentre is the middle of a carousel's track on the screen.
func carouselTrackCentre(t *testing.T, d *devtools, alias string) (float64, float64) {
	t.Helper()
	d.evaluate(carouselProbe)
	var at struct{ X, Y float64 }
	if err := json.Unmarshal([]byte(d.evaluate(`(function () { var r = window.__carousel.parts(`+strconv.Quote(alias)+`).track.getBoundingClientRect(); return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 }); })()`)), &at); err != nil {
		t.Fatal(err)
	}
	return at.X, at.Y
}

// carouselTrackLook is the track's own style and its cursor, as the browser has them now, and what is
// selected on the page (read off the range: words a fade slide hides once it gives way leave the
// selection's own text).
const carouselTrackLook = `(function (alias) { var track = window.__carousel.parts(alias).track, selection = getSelection();
  return JSON.stringify({ style: track.style.cssText, cursor: getComputedStyle(track).cursor, selected: selection.isCollapsed ? "" : selection.getRangeAt(0).toString() }); })`

// carouselDrag drags a carousel with the screen's mouse (carouselHand) and says what it did.
func carouselDrag(t *testing.T, d *devtools, alias string, hand carouselHand) carouselDragged {
	t.Helper()
	d.evaluate(carouselProbe)
	mark := d.evaluate(`String(window.__carousel.kept.length)`)
	d.evaluate(`window.__pending = new Promise(function (done) {
  window.addEventListener("pointerup", function () { done(window.__carousel.watch(` + strconv.Quote(alias) + `, function () {}, 4000)); }, { once: true, capture: true });
}); ""`)
	carouselMouse(d, "mouseMoved", hand.x, hand.y, false)
	carouselMouse(d, "mousePressed", hand.x, hand.y, true)
	x := hand.x
	for _, step := range hand.steps {
		time.Sleep(hand.apart)
		x = hand.x + step
		carouselMouse(d, "mouseMoved", x, hand.y, true)
	}
	time.Sleep(hand.hold)
	var out carouselDragged
	if hand.hold >= 50*time.Millisecond {
		out.Held = carouselState(t, d, alias)
		var look struct{ Style, Cursor, Selected string }
		_ = json.Unmarshal([]byte(d.evaluate(carouselTrackLook+`(`+strconv.Quote(alias)+`)`)), &look)
		out.Style, out.Cursor, out.Selected = look.Style, look.Cursor, look.Selected
	}
	carouselMouse(d, "mouseReleased", x, hand.y, false)
	out.Watch = carouselWatchFor(t, d, alias, "pending", 4000)
	raw := d.evaluate(`JSON.stringify(window.__carousel.kept.slice(` + mark + `).filter(function (r) { return r.alias === ` + strconv.Quote(alias) + `; }).map(function (r) {
  return { slide: r.slide, value: r.value, before: r.before, after: r.after };
}))`)
	if err := json.Unmarshal([]byte(raw), &out.Orders); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return out
}

// carouselHeldSays is what a carousel was while the mouse held it: current, the slides and dots marked,
// the counter, where the track was, the order on its slides, the track's own style and its cursor.
func carouselHeldSays(g carouselDragged) string {
	return fmt.Sprintf("current %g | slides %s | dots %s | counter %s | left %g | order %q | style %q | cursor %s",
		g.Held.Current, g.Held.Slides, g.Held.Dots, g.Held.Counter, g.Held.Left, g.Held.Order, g.Style, g.Cursor)
}

// carouselLetGoSays is what a drag did from the moment it let go, frame by frame: what current, the
// slides and dots marked and the counter went through (one value: straight there), the slides seen under
// the middle of the track, how the track went (carouselWay) from where the drag left it, and where it
// rests: the order on the slides then, the slide seen and where, whether a scrollend came.
func carouselLetGoSays(w carouselWatched) string {
	if len(w.Samples) == 0 {
		return "never read"
	}
	// a slide that came round to meet the drag: how the track went while it was round (home is the
	// orders' to say)
	lefts, round := []float64{w.Before.Left}, w.Before.Order != ""
	for _, s := range w.Samples {
		if !round || s.Order != "" {
			lefts = append(lefts, s.Left)
		}
	}
	way := carouselWay(lefts)
	if round {
		way = "while round " + way
	}
	last := w.Samples[len(w.Samples)-1]
	order := last.Order
	if order == "" {
		order = "none"
	}
	rests := fmt.Sprintf("rests at %g, order %s, slide %g seen at %g", last.Left, order, last.Seen, last.At)
	if w.Ended > 0 {
		rests += ", by its scrollend"
	} else {
		rests += ", no scrollend"
	}
	return fmt.Sprintf("current %s | slides %s | dots %s | counter %s | seen %s | %s | %s",
		carouselSequence(w.Samples, func(s carouselRead) string { return strconv.FormatFloat(s.Current, 'f', -1, 64) }),
		carouselSequence(w.Samples, func(s carouselRead) string { return s.Slides }),
		carouselSequence(w.Samples, func(s carouselRead) string { return s.Dots }),
		carouselSequence(w.Samples, func(s carouselRead) string { return s.Counter }),
		carouselSequence(append([]carouselRead{w.Before}, w.Samples...), func(s carouselRead) string { return strconv.FormatFloat(s.Seen, 'f', -1, 64) }),
		way, rests)
}

// carouselOrdersSaid is each order set on a slide, in turn, with the screen just before and just after
// the task that set it ("none": the order taken away).
func carouselOrdersSaid(orders []carouselOrder) string {
	if len(orders) == 0 {
		return "no order set"
	}
	var out []string
	for _, o := range orders {
		value := o.Value
		if value == "" {
			value = "none"
		}
		out = append(out, fmt.Sprintf("slide %g order %s: %s", o.Slide, value, carouselSaw(o)))
	}
	return strings.Join(out, "; ")
}

// TestBrowserCarouselMouseDrag proves a mouse drags the carousel (20/10/2026: Quốc dragged it with the
// mouse, forward and back, and it did not move), in a real browser whose frames run, through the
// screen's own mouse (DevTools input: pressed, moved in steps, let go), each part with a browser of its
// own:
//   - slide: dragged left past a fifth of a slide the track follows the mouse (current, the slide and
//     the dot marked stay the slide it began on, no snap, the closed hand, no text selectable) and, let
//     go, slides on to the next, current and the dot going straight there, the track's own style empty
//     again; dragged right, the previous; a drag short of a fifth slides back to the slide it began on;
//     a quick flick shorter than that moves on; the track's cursor is the open hand at rest and the
//     closed one held, and a carousel of one slide has neither and never moves;
//   - round: dragged forward at the last slide the first comes round after it at the first step (the
//     same slide seen, moved by the drag alone), the track follows into it and, let go, slides on only
//     forward to it and goes home there (the same slide in the same place); dragged back at the first,
//     the mirror; a drag at the last that turns back past where it began sends the first home again
//     (the screen kept) and, let go, goes to the slide before;
//   - a press while a move asked for still slides is not taken: the move goes on to its slide, the
//     track never follows the mouse, and the words under the mouse are not selected (the browser
//     scrolls a selection into view, which cut the move short);
//   - a drag that begins on a link in a slide moves on and follows nothing: the link's click never
//     comes, no click reaches the page, no image or link is dragged away (dragstart cancelled), and a
//     drag over words selects none of them; a plain click on the link still follows it;
//   - duration="2s": held mid-drag with the mouse away from the carousel for longer than the duration,
//     it does not move on (current, playing unchanged, the track where the drag left it); let go, it
//     slides back and moves on two seconds after;
//   - less motion: let go, the track is at the next slide at once;
//   - fade and none: a mouse dragged 100px across is next() or previous(), 30px nothing; the track does
//     not follow (fade never scrolls, none jumps once let go);
//   - a finger's swipe is still the browser's: while the finger moves, the track takes none of the
//     mouse's drag (no closed hand, snap untouched), and let go the track rests at a slide (flung on or
//     snapped back, the browser's), current naming it.
//
// CONTROL (run 20/10/2026, each a disabled copy of v1.1.0.js, or of reboot.css, given to go test
// -overlay; the files themselves untouched): the mouse's pointerdown returning at once (the component
// before this) left every drag where it began — current 0, the track at 0, no closed hand — failing
// every part but the finger's and the press while sliding (nothing there to take); edge() doing nothing held the track at 1920 at the last slide (nothing came
// round to meet the drag, the order set only once let go); without the busy check the press while
// sliding pulled the track back to 0 while current said 3; without selectstart cancelled while busy
// the press selected "\nHai" and the move stopped at 640, current 1; with FLICK at Infinity the flick
// slid back; without the click cancel the page counted the drags' clicks (1, then 2), and without the
// capture as well the link was followed (#went); with dragstart left alone the link drag began a native
// drag that stalled the browser's input (DevTools read timed out after 30s); with the mouse's
// pointerType check gone a finger's swipe turned snap off on the track; with fade and none left out of
// the 40px rule the mouse moved neither; without the reboot's rule the track's cursor was auto; with
// both the drag hold and the hover hold removed the carousel moved on at two seconds while held
// (current 1, the track at 640). Three stayed green, each for a reason of Chrome's: with go(hand.at)
// removed, a short drag still slid back, by Chrome's re-snap when snap came back (removing that too
// left the track at 60, between slides); with user-select and the clearing removed, nothing was
// selected, because Chrome stops a selection once the track captures the pointer (removing the
// capture too selected "chuột"); with only the drag hold removed it still held, because the hover hold
// covers a captured mouse in Chrome (no pointerleave until release). Those guards stay for browsers
// that do not do the same.
func TestBrowserCarouselMouseDrag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, carouselDragFiles())
	server := carouselServer(tenant)
	defer server.Close()
	check := func(t *testing.T, step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}
	open := func(t *testing.T, path, motion string) *devtools {
		d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
		d.loaded(exampleSettle)
		carouselVisit(d, server.URL+path, motion)
		return d
	}
	slow := func(x, y float64, steps ...float64) carouselHand {
		return carouselHand{x: x, y: y, steps: steps, apart: 40 * time.Millisecond, hold: 200 * time.Millisecond}
	}
	// letGo checks what a drag did once let go, and that it set no order on a slide
	letGo := func(t *testing.T, step string, g carouselDragged, want string) {
		t.Helper()
		if got := carouselLetGoSays(g.Watch); got != want {
			t.Errorf("%s, let go:\n got  %s\n want %s\n frames %s", step, got, want, carouselTrace(g.Watch))
		}
	}
	// rest reads the track's own style and cursor once a drag is over
	rest := func(t *testing.T, d *devtools, alias string) string {
		var look struct{ Style, Cursor string }
		_ = json.Unmarshal([]byte(d.evaluate(carouselTrackLook+`(`+strconv.Quote(alias)+`)`)), &look)
		return fmt.Sprintf("style %q | cursor %s", look.Style, look.Cursor)
	}
	// goes moves a carousel to slide n by its instance and waits until the track rests there
	goes := func(t *testing.T, d *devtools, alias string, n int) {
		t.Helper()
		w := carouselWatch(t, d, alias, fmt.Sprintf(`kit.scopeFor(window.__carousel.parts(%q).host).go(%d);`, alias, n))
		if last := w.Samples[len(w.Samples)-1]; last.Current != float64(n) {
			t.Fatalf("go(%d): current %g\n frames %s", n, last.Current, carouselTrace(w))
		}
	}
	const grabbing = "cursor: grabbing; user-select: none; scroll-snap-type: none;"

	t.Run("slide: next, previous, back, a flick, the cursor", func(t *testing.T) {
		d := open(t, "/", "no-preference")
		x, y := carouselTrackCentre(t, d, "repeated")
		check(t, "at rest", rest(t, d, "repeated"), `style "" | cursor grab`)

		g := carouselDrag(t, d, "repeated", slow(x, y, -20, -60, -100, -140, -180, -200))
		check(t, "dragged left 200px, held", carouselHeldSays(g),
			`current 0 | slides 1000 | dots 1000 | counter 1 / 4 | left 200 | order "" | style "`+grabbing+`" | cursor grabbing`)
		letGo(t, "dragged left 200px", g, "current 1 | slides 0100 | dots 0100 | counter 2 / 4 | seen 0 1 | 200 → 640 only forward, smoothly | rests at 640, order none, slide 1 seen at 0, by its scrollend")
		check(t, "dragged left 200px, orders", carouselOrdersSaid(g.Orders), "no order set")
		check(t, "dragged left 200px, at rest", rest(t, d, "repeated"), `style "" | cursor grab`)

		g = carouselDrag(t, d, "repeated", slow(x, y, 20, 60, 100, 140, 180, 200))
		check(t, "dragged right 200px, held", carouselHeldSays(g),
			`current 1 | slides 0100 | dots 0100 | counter 2 / 4 | left 440 | order "" | style "`+grabbing+`" | cursor grabbing`)
		letGo(t, "dragged right 200px", g, "current 0 | slides 1000 | dots 1000 | counter 1 / 4 | seen 1 0 | 440 → 0 only back, smoothly | rests at 0, order none, slide 0 seen at 0, by its scrollend")

		g = carouselDrag(t, d, "repeated", carouselHand{x: x, y: y, steps: []float64{-10, -25, -40, -60}, apart: 60 * time.Millisecond, hold: 200 * time.Millisecond})
		letGo(t, "dragged left 60px, short of a fifth (128px)", g, "current 0 | slides 1000 | dots 1000 | counter 1 / 4 | seen 0 | 60 → 0 only back, smoothly | rests at 0, order none, slide 0 seen at 0, by its scrollend")
		check(t, "the short drag, at rest", rest(t, d, "repeated"), `style "" | cursor grab`)

		// the same 60px in a flick: three steps 8ms apart, let go at once
		g = carouselDrag(t, d, "repeated", carouselHand{x: x, y: y, steps: []float64{-10, -30, -60}, apart: 8 * time.Millisecond})
		letGo(t, "a flick of 60px", g, "current 1 | slides 0100 | dots 0100 | counter 2 / 4 | seen 0 1 | 60 → 640 only forward, smoothly | rests at 640, order none, slide 1 seen at 0, by its scrollend")

		// one slide: nothing to drag to, no hand
		carouselVisit(d, server.URL+"/one", "no-preference")
		check(t, "one slide, at rest", rest(t, d, "one"), `style "" | cursor auto`)
		ox, oy := carouselTrackCentre(t, d, "one")
		g = carouselDrag(t, d, "one", slow(ox, oy, -20, -60, -100, -200))
		check(t, "one slide, dragged, held", carouselHeldSays(g), `current 0 | slides 1 | dots  | counter  | left 0 | order "" | style "" | cursor auto`)
	})

	t.Run("round: forward at the last, back at the first, turning back", func(t *testing.T) {
		d := open(t, "/", "no-preference")
		x, y := carouselTrackCentre(t, d, "repeated")
		goes(t, d, "repeated", 3)

		g := carouselDrag(t, d, "repeated", slow(x, y, -20, -60, -100, -140, -180, -200))
		check(t, "dragged forward at the last, held", carouselHeldSays(g),
			`current 3 | slides 0001 | dots 0001 | counter 4 / 4 | left 1480 | order "0:1" | style "`+grabbing+`" | cursor grabbing`)
		letGo(t, "dragged forward at the last", g, "current 0 | slides 1000 | dots 1000 | counter 1 / 4 | seen 3 0 | while round 1480 → 1920 only forward, smoothly | rests at 0, order none, slide 0 seen at 0, by its scrollend")
		check(t, "dragged forward at the last, orders", carouselOrdersSaid(g.Orders),
			"slide 0 order 1: slide 3 at 0 → slide 3 at -20, left 1920 → 1300; slide 0 order none: slide 0 at 0 → slide 0 at 0, left 1920 → 0")

		g = carouselDrag(t, d, "repeated", slow(x, y, 20, 60, 100, 140, 180, 200))
		check(t, "dragged back at the first, held", carouselHeldSays(g),
			`current 0 | slides 1000 | dots 1000 | counter 1 / 4 | left 440 | order "3:-1" | style "`+grabbing+`" | cursor grabbing`)
		letGo(t, "dragged back at the first", g, "current 3 | slides 0001 | dots 0001 | counter 4 / 4 | seen 0 3 | while round 440 → 0 only back, smoothly | rests at 1920, order none, slide 3 seen at 0, by its scrollend")
		check(t, "dragged back at the first, orders", carouselOrdersSaid(g.Orders),
			"slide 3 order -1: slide 0 at 0 → slide 0 at 20, left 0 → 620; slide 3 order none: slide 3 at 0 → slide 3 at 0, left 0 → 1920")

		// at the last again: forward 100px (the first comes round), then back to 150px the other way (it goes
		// home again), let go: the slide before
		g = carouselDrag(t, d, "repeated", slow(x, y, -20, -60, -100, -40, 30, 100, 150))
		check(t, "dragged at the last, turning back, held", carouselHeldSays(g),
			`current 3 | slides 0001 | dots 0001 | counter 4 / 4 | left 1770 | order "" | style "`+grabbing+`" | cursor grabbing`)
		letGo(t, "dragged at the last, turning back", g, "current 2 | slides 0010 | dots 0010 | counter 3 / 4 | seen 3 2 | 1770 → 1280 only back, smoothly | rests at 1280, order none, slide 2 seen at 0, by its scrollend")
		check(t, "dragged at the last, turning back, orders", carouselOrdersSaid(g.Orders),
			"slide 0 order 1: slide 3 at 0 → slide 3 at -20, left 1920 → 1300; slide 0 order none: slide 3 at -40 → slide 3 at 30, left 1320 → 1890")
	})

	t.Run("a press while it slides is not taken", func(t *testing.T) {
		d := open(t, "/", "no-preference")
		x, y := carouselTrackCentre(t, d, "repeated")
		d.evaluate(`window.__pending = window.__carousel.watch("repeated", function () { window.__carousel.parts("repeated").dots[3].click(); }); ""`)
		carouselMouse(d, "mouseMoved", x, y, false)
		carouselMouse(d, "mousePressed", x, y, true)
		for _, step := range []float64{40, 80, 120, 160} {
			time.Sleep(30 * time.Millisecond)
			carouselMouse(d, "mouseMoved", x+step, y, true)
		}
		look := rest(t, d, "repeated")
		carouselMouse(d, "mouseReleased", x+160, y, false)
		w := carouselWatch(t, d, "repeated", "pending")
		if got, want := carouselMove(w), "current 3 | slides 0001 | dots 0001 | counter 4 / 4 | 0 → 1920 smoothly, rests by its scrollend"; got != want {
			t.Errorf("dot 4 (go(3)), dragged right while it slides:\n got  %s\n want %s\n frames %s", got, want, carouselTrace(w))
		}
		lefts := []float64{w.Before.Left}
		for _, s := range w.Samples {
			lefts = append(lefts, s.Left)
		}
		check(t, "the way the track went", carouselWay(lefts), "0 → 1920 only forward, smoothly")
		check(t, "while the mouse dragged", look, `style "" | cursor grab`)
		check(t, "the words under the mouse", d.evaluate(`JSON.stringify(getSelection().toString())`), `""`)
	})

	t.Run("a link and words in a slide", func(t *testing.T) {
		d := open(t, "/links", "no-preference")
		d.evaluate(`window.__clicks = { link: 0, page: 0, dragstart: "" };
document.getElementById("slide-link").addEventListener("click", function () { window.__clicks.link++; });
document.addEventListener("click", function () { window.__clicks.page++; });
document.addEventListener("dragstart", function (e) { window.__clicks.dragstart += (e.defaultPrevented ? "cancelled " : "dragged "); });
""`)
		said := func() string {
			return d.evaluate(`"link clicks " + window.__clicks.link + ", page clicks " + window.__clicks.page + ", dragstart " + (window.__clicks.dragstart.trim() || "none") + ", hash " + JSON.stringify(location.hash) + ", selected " + JSON.stringify(getSelection().toString())`)
		}
		wx, wy := carouselCentre(t, d, "#words", 0, 0)
		g := carouselDrag(t, d, "links", slow(wx, wy, -20, -60, -100, -140, -200))
		check(t, "dragged over the words, held: selected", strconv.Quote(g.Selected), `""`)
		letGo(t, "dragged over the words", g, "current 1 | slides 010 | dots  | counter  | seen 0 1 | 200 → 640 only forward, smoothly | rests at 640, order none, slide 1 seen at 0, by its scrollend")
		check(t, "dragged over the words", said(), `link clicks 0, page clicks 0, dragstart none, hash "", selected ""`)
		goes(t, d, "links", 0)

		lx, ly := carouselCentre(t, d, "#slide-link", 0, 0)
		g = carouselDrag(t, d, "links", slow(lx, ly, -20, -60, -100, -140, -200))
		letGo(t, "dragged from the link", g, "current 1 | slides 010 | dots  | counter  | seen 0 1 | 200 → 640 only forward, smoothly | rests at 640, order none, slide 1 seen at 0, by its scrollend")
		check(t, "dragged from the link", said(), `link clicks 0, page clicks 0, dragstart cancelled, hash "", selected ""`)
		goes(t, d, "links", 0)

		carouselClick(t, d, "#slide-link", 0, 0)
		d.evaluate(`window.__carousel.wait(300)`)
		check(t, "a plain click on the link", said()+" | "+carouselSays(carouselState(t, d, "links")),
			`link clicks 1, page clicks 1, dragstart cancelled, hash "#went", selected "" | current 0 of 3, playing false | slides 100 | dots  | counter  | live polite`)
	})

	t.Run("duration holds while a drag holds it", func(t *testing.T) {
		d := open(t, "/play", "no-preference")
		x, y := carouselTrackCentre(t, d, "play")
		d.evaluate(carouselProbe)
		mark := d.evaluate(`String(window.__carousel.kept.length)`)
		d.evaluate(`window.__pending = new Promise(function (done) {
  window.addEventListener("pointerup", function () { done(window.__carousel.watch("play", function () {}, 4000)); }, { once: true, capture: true });
}); ""`)
		carouselMouse(d, "mouseMoved", x, y, false)
		carouselMouse(d, "mousePressed", x, y, true)
		for _, step := range []float64{-20, -60, -100} {
			time.Sleep(40 * time.Millisecond)
			carouselMouse(d, "mouseMoved", x+step, y, true)
		}
		// away from the carousel, still pressed, for longer than the duration
		carouselMouse(d, "mouseMoved", x-100, 790, true)
		time.Sleep(2600 * time.Millisecond)
		held := carouselState(t, d, "play")
		check(t, "held away from it for 2.6s", fmt.Sprintf("current %g, playing %v, left %g", held.Current, held.Playing, held.Left), "current 0, playing true, left 100")
		carouselMouse(d, "mouseReleased", x-100, 790, false)
		w := carouselWatchFor(t, d, "play", "pending", 4000)
		if got, want := carouselLetGoSays(w), "current 0 | slides 100 | dots  | counter 1 / 3 | seen 0 | 100 → 0 only back, smoothly | rests at 0, order none, slide 0 seen at 0, by its scrollend"; got != want {
			t.Errorf("let go away from it:\n got  %s\n want %s\n frames %s", got, want, carouselTrace(w))
		}
		check(t, "orders", d.evaluate(`String(window.__carousel.kept.length - `+mark+`)`), "0")
		check(t, "let go, it plays on", carouselWaits(t, d, "play", 3000, ""), "moves to 1 after ~2s, playing true")
	})

	t.Run("less motion", func(t *testing.T) {
		d := open(t, "/", "reduce")
		x, y := carouselTrackCentre(t, d, "written")
		g := carouselDrag(t, d, "written", slow(x, y, -20, -60, -100, -140, -180, -200))
		letGo(t, "dragged left 200px", g, "current 1 | slides 010 | dots  | counter 2 / 3 | seen 0 1 | 200 → 640 only forward, at once | rests at 640, order none, slide 1 seen at 0, by its scrollend")
		check(t, "at rest", rest(t, d, "written"), `style "" | cursor grab`)
	})

	t.Run("fade and none", func(t *testing.T) {
		d := open(t, "/fade", "no-preference")
		shown := func(alias string) string {
			s := carouselState(t, d, alias)
			return fmt.Sprintf("current %g | slides %s | counter %s | left %g", s.Current, s.Slides, s.Counter, s.Left)
		}
		settle := func() { d.evaluate(`window.__carousel.wait(450)`) }
		x, y := carouselTrackCentre(t, d, "fade")
		g := carouselDrag(t, d, "fade", slow(x, y, -20, -60, -100))
		check(t, "fade: dragged left 100px, held", carouselHeldSays(g), `current 0 | slides 100 | dots  | counter 1 / 3 | left 0 | order "" | style "cursor: grabbing; user-select: none;" | cursor grabbing`)
		settle()
		check(t, "fade: dragged left 100px", shown("fade"), "current 1 | slides 010 | counter 2 / 3 | left 0")
		carouselDrag(t, d, "fade", slow(x, y, 20, 60, 100))
		settle()
		check(t, "fade: dragged right 100px", shown("fade"), "current 0 | slides 100 | counter 1 / 3 | left 0")
		carouselDrag(t, d, "fade", slow(x, y, -10, -20, -30))
		settle()
		check(t, "fade: dragged 30px", shown("fade"), "current 0 | slides 100 | counter 1 / 3 | left 0")
		check(t, "fade: at rest", rest(t, d, "fade"), `style "" | cursor grab`)

		carouselVisit(d, server.URL+"/none", "no-preference")
		x, y = carouselTrackCentre(t, d, "none")
		g = carouselDrag(t, d, "none", slow(x, y, -20, -60, -100))
		check(t, "none: dragged left 100px, held", carouselHeldSays(g), `current 0 | slides 100 | dots  | counter 1 / 3 | left 0 | order "" | style "cursor: grabbing; user-select: none;" | cursor grabbing`)
		letGo(t, "none: dragged left 100px", g, "current 1 | slides 010 | dots  | counter 2 / 3 | seen 0 1 | 0 → 640 only forward, at once | rests at 640, order none, slide 1 seen at 0, by its scrollend")
		carouselDrag(t, d, "none", slow(x, y, -10, -20, -30))
		settle()
		check(t, "none: dragged 30px", shown("none"), "current 1 | slides 010 | counter 2 / 3 | left 640")
		carouselDrag(t, d, "none", slow(x, y, 20, 60, 100))
		settle()
		check(t, "none: dragged right 100px", shown("none"), "current 0 | slides 100 | counter 1 / 3 | left 0")
	})

	t.Run("a finger is the browser's", func(t *testing.T) {
		d := open(t, "/", "no-preference")
		x, y := carouselTrackCentre(t, d, "written")
		touch := func(kind string, dx float64) {
			points := []map[string]any{{"x": x + dx, "y": y}}
			if kind == "touchEnd" {
				points = []map[string]any{}
			}
			d.call("Input.dispatchTouchEvent", map[string]any{"type": kind, "touchPoints": points})
		}
		touch("touchStart", 0)
		var looks []string
		for _, dx := range []float64{-20, -60, -100, -160} {
			time.Sleep(30 * time.Millisecond)
			touch("touchMove", dx)
			looks = append(looks, rest(t, d, "written"))
		}
		touch("touchEnd", 0)
		d.evaluate(`window.__carousel.wait(900)`)
		for i, look := range looks {
			check(t, fmt.Sprintf("the finger's move %d", i+1), look, `style "" | cursor grab`)
		}
		s := carouselState(t, d, "written")
		// the browser's own swipe: whether it flings on to the next slide or snaps back is the browser's
		// (both seen), current is the slide the track rests at, at that slide's place
		at := math.Round(s.Left / 640)
		check(t, "the finger let go", fmt.Sprintf("current %g, at a slide's place %v", s.Current, s.Left == at*640), fmt.Sprintf("current %g, at a slide's place true", at))
	})
}
