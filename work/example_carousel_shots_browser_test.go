package work

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// exampleCarouselSay brings the carousel section of /components to the middle of the screen, waits for
// the fonts and two frames, and says what a person sees of it: the section's rectangle on the page (to
// picture it), the instance (current, count, playing), the track's and the first slide's sizes and the
// tallest card, the scroll position, each button the author drew — previous, next, toggle — with its
// rectangle and its icon's name and alt, the dots (how many, which is aria-current, each rectangle,
// the colour of a current one and of another), the counter's words and rectangle, the words of the
// section's first paragraph (its white space as one space) and of each <code> in it, how far the page
// scrolls sideways, and the scheme.
const exampleCarouselSay = `(function () {
  var section = document.getElementById("carousel");
  section.scrollIntoView({ block: "center" });
  return document.fonts.ready.then(function () { return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
    var host = section.querySelector('[data-kitwork-component^="carousel"]'), scope = kit.scopeFor(host);
    var track = host.querySelector(".carousel-track"), slides = Array.prototype.slice.call(track.children);
    var box = function (el) { if (!el) return "-"; var r = el.getBoundingClientRect(); return Math.round(r.left) + "," + Math.round(r.top) + " " + Math.round(r.width) + "x" + Math.round(r.height); };
    var button = function (call) {
      var b = host.querySelector('button[data-kitwork-event="click: ' + call + '"]'), icon = b && b.querySelector("i");
      return call + " " + box(b) + " " + (icon ? (icon.className.match(/icon-([a-z0-9-]+)/) || [])[1] + ":" + icon.getAttribute("aria-label") : "no icon");
    };
    var dots = Array.prototype.slice.call(host.querySelectorAll("button[aria-controls]"));
    var colour = function (dot) { var i = dot && dot.querySelector("i"); return i ? getComputedStyle(i).backgroundColor : "-"; };
    var counter = Array.prototype.filter.call(host.querySelectorAll("p"), function (p) { return /current \+ 1/.test(p.getAttribute("data-kitwork-bind") || ""); })[0];
    var r = section.getBoundingClientRect();
    done(JSON.stringify({
      x: r.left + scrollX - 16, y: r.top + scrollY - 16, width: r.width + 32, height: r.height + 32,
      current: scope.current, count: scope.count, playing: scope.playing,
      track: box(track), slide: box(slides[0]), tallest: Math.max.apply(null, slides.map(function (s) { return Math.round(s.firstElementChild.getBoundingClientRect().height); })),
      left: Math.round(track.scrollLeft), live: track.getAttribute("aria-live"),
      buttons: [button("previous()"), button("next()"), button("toggle()")].join(" · "),
      dots: dots.map(function (d) { return d.getAttribute("aria-current") === "true" ? "1" : "0"; }).join(""),
      dotNames: dots.map(function (d) { var i = d.querySelector("i"); return i ? i.getAttribute("aria-label") : ""; }).join("|"),
      dotBoxes: dots.map(box).join(" ; "),
      dotColours: colour(dots.filter(function (d) { return d.getAttribute("aria-current") === "true"; })[0]) + " / " + colour(dots.filter(function (d) { return d.getAttribute("aria-current") !== "true"; })[0]),
      counter: counter ? counter.textContent : "", counterBox: box(counter),
      words: (function (p) { return p ? p.textContent.replace(/\s+/g, " ").trim() : ""; })(section.querySelector("p")),
      code: Array.prototype.map.call(section.querySelectorAll("p code"), function (code) { return code.textContent; }).join("|"),
      overflow: Math.max(document.documentElement.scrollWidth, innerWidth) - innerWidth,
      dark: matchMedia("(prefers-color-scheme: dark)").matches
    }));
  }); }); }); });
})()`

// exampleCarouselHost finds the carousel's host on /components as the engine wrote it.
var exampleCarouselHost = regexp.MustCompile(`<div class="([^"]*)" data-kitwork-component="carousel: \$surfaces"([^>]*)>`)

// The carousel of the kitwork example, drawn by a real browser: /components' section #carousel, four
// surfaces of the runtime from router.work (a for), previous, next and toggle, a dot for each surface
// (for + go(i)), and "current + 1 / count". At a desktop's width (1280×800) and a phone's (390×844),
// light and dark:
//   - the server writes the host as a region named "Kitwork surfaces", playing by itself every five
//     seconds (duration: 5000), with count from the page's data (4);
//   - the page links no warning (examplePage): the dots' active:color has the contrast it needs;
//   - as it starts: the first of four, playing (the toggle's icon a pause named Pause), the first dot
//     current, "1 / 4", the page not scrolling sideways, each slide the track's width; the words above
//     it read "A carousel is a list of slides…", list a <code>, not a tag written out;
//   - a dot goes to the third surface and pause() stops it: the track rests at the third slide, its dot
//     current, "3 / 4", the toggle's icon a play named Play;
//   - the pictures differ by scheme and by step, and are saved to KITWORK_CAROUSEL_SHOTS when it names a
//     folder. What each shot measures is logged.
func TestBrowserExampleCarouselShots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel screenshots in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant, page := examplePage(t, "/components")
	host := exampleCarouselHost.FindStringSubmatch(page)
	if host == nil || !strings.Contains(host[1], "carousel") ||
		!strings.Contains(host[2], `role="region" aria-roledescription="carousel" aria-label="Kitwork surfaces"`) ||
		!strings.Contains(host[2], `data-kitwork-scope="duration: 5000; count: 4"`) && !strings.Contains(host[2], `data-kitwork-scope="count: 4; duration: 5000"`) {
		t.Fatalf("/components has no carousel host named Kitwork surfaces that plays every five seconds with four slides: %q", host)
	}
	server := exampleServer(tenant)
	defer server.Close()
	folder := os.Getenv("KITWORK_CAROUSEL_SHOTS")
	if folder != "" {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	type said struct {
		X, Y, Width, Height     float64
		Current, Count          float64
		Playing, Dark           bool
		Track, Slide            string
		Tallest, Left, Overflow float64
		Live, Buttons, Dots     string
		DotNames, DotBoxes      string
		DotColours              string
		Counter, CounterBox     string
		Words, Code             string
	}
	pictures := map[string]image.Image{}
	// one browser for the four screens (it lives 40 seconds, openDevtools)
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	for _, screen := range []struct {
		width, height int
		scheme        string
	}{{1280, 800, "light"}, {1280, 800, "dark"}, {390, 844, "light"}, {390, 844, "dark"}} {
		suffix := screen.scheme + "-" + strconv.Itoa(screen.width)
		emulate(d, screen.width, screen.height)
		d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{
			{"name": "prefers-color-scheme", "value": screen.scheme},
			{"name": "prefers-reduced-motion", "value": "no-preference"},
		}})
		d.evaluate(`window.__stale = true`)
		d.call("Page.navigate", map[string]any{"url": server.URL + "/components"})
		d.loaded(`(window.__stale ? "wait of 0" : ` + exampleSettle + `)`)
		say := func() said {
			t.Helper()
			var out said
			raw := d.evaluate(exampleCarouselSay)
			if err := json.Unmarshal([]byte(raw), &out); err != nil {
				t.Fatalf("%v: %s", err, raw)
			}
			return out
		}
		// shoot pictures the section as the screen shows it (inside the screen: a capture beyond it lays the
		// page out again, and the browser takes the track back to its first slide), then reads it again:
		// the picture must be of the state measured
		shoot := func(name string, at said) {
			t.Helper()
			raw, picture := screenShot(t, d, at.X, at.Y, at.Width, at.Height)
			if again := say(); again.Current != at.Current || again.Left != at.Left || again.Dots != at.Dots || again.Counter != at.Counter {
				t.Errorf("%s: pictured at current %g, left %g, dots %s, %q; read after: current %g, left %g, dots %s, %q",
					name, at.Current, at.Left, at.Dots, at.Counter, again.Current, again.Left, again.Dots, again.Counter)
			}
			pictures[name] = picture
			if folder != "" {
				if err := os.WriteFile(filepath.Join(folder, name+".png"), raw, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("%s: current %g of %g playing %v | track %s slide %s tallest card %g left %g live %s | %s | dots %s %s (%s) colours current/other %s | counter %q %s | overflow %g dark %v",
				name, at.Current, at.Count, at.Playing, at.Track, at.Slide, at.Tallest, at.Left, at.Live, at.Buttons, at.Dots, at.DotNames, at.DotBoxes, at.DotColours, at.Counter, at.CounterBox, at.Overflow, at.Dark)
		}
		check := func(name, got, want string) {
			t.Helper()
			if got != want {
				t.Errorf("%s:\n got  %s\n want %s", name, got, want)
			}
		}
		state := func(at said) string {
			return fmt.Sprintf("current %g of %g, playing %v, live %s, dots %s, counter %s, overflow %g, dark %v", at.Current, at.Count, at.Playing, at.Live, at.Dots, at.Counter, at.Overflow, at.Dark)
		}
		dark := screen.scheme == "dark"

		at := say()
		check("start-"+suffix, state(at), fmt.Sprintf("current 0 of 4, playing true, live off, dots 1000, counter 1 / 4, overflow 0, dark %v", dark))
		check("start-"+suffix+" names", at.DotNames, "Website|Desktop|Mobile|Server")
		check("start-"+suffix+" the words above it", at.Words+" | code "+at.Code,
			"A carousel is a list of slides on a row the browser scrolls and snaps: swipe it, scroll it, or use the buttons. It moves on by itself every five seconds and holds while you point at it or reach it with the keyboard. | code list")
		if !strings.Contains(at.Buttons, "toggle() ") || !strings.HasSuffix(at.Buttons, "player-pause:Pause") {
			t.Errorf("start-%s: the toggle is %s, want a pause icon named Pause while it plays", suffix, at.Buttons)
		}
		if track, slide := strings.Fields(at.Track), strings.Fields(at.Slide); len(track) != 2 || len(slide) != 2 || strings.Split(track[1], "x")[0] != strings.Split(slide[1], "x")[0] {
			t.Errorf("start-%s: the track is %s and a slide %s, want each slide the track's width", suffix, at.Track, at.Slide)
		}
		shoot("start-"+suffix, at)

		// the third surface by its dot, then paused: the picture holds still
		d.evaluate(`(function () {
  var host = document.querySelector('[data-kitwork-component^="carousel"]'), track = host.querySelector(".carousel-track");
  return new Promise(function (done) {
    track.addEventListener("scrollend", function () { done(""); }, { once: true });
    host.querySelectorAll("button[aria-controls]")[2].click();
    setTimeout(function () { done("no scrollend"); }, 2000);
  }).then(function (said) { kit.scopeFor(host).pause(); return said; });
})()`)
		at = say()
		check("third-"+suffix, state(at), fmt.Sprintf("current 2 of 4, playing false, live polite, dots 0010, counter 3 / 4, overflow 0, dark %v", dark))
		if width, _ := strconv.Atoi(strings.Split(strings.Fields(at.Track)[1], "x")[0]); at.Left < float64(2*width-2) || at.Left > float64(2*width+2) {
			t.Errorf("third-%s: the track rests at %g, want the third slide (2 × %d)", suffix, at.Left, width)
		}
		if !strings.HasSuffix(at.Buttons, "player-play:Play") {
			t.Errorf("third-%s: the toggle is %s, want a play icon named Play once paused", suffix, at.Buttons)
		}
		shoot("third-"+suffix, at)
	}
	for _, pair := range [][2]string{{"start-light-1280", "start-dark-1280"}, {"start-light-390", "start-dark-390"}, {"start-light-1280", "third-light-1280"}, {"start-dark-390", "third-dark-390"}} {
		if samePicture(pictures[pair[0]], pictures[pair[1]]) {
			t.Errorf("%s and %s are drawn the same", pair[0], pair[1])
		}
	}
}

// exampleScenesSay brings the carousel of pictures on /components (section #carousel-pictures) to the
// middle of the screen, waits for the fonts, the picture of the slide showing and two frames, and says
// what a person sees of it: the section's rectangle on the page (to picture it), the instance (current,
// count, playing), the track's rectangle (the picture's: what stands over it must stay inside), the
// scroll position and any order on a slide, the slide under the middle of the track; on that slide its
// picture's drawn and natural sizes and the caption: its rectangle, its words, its colour and ground,
// and whether any line of it is cut (wider than its box); the previous and next arrows' rectangles and
// the pill's (the toggle and the dots), the dots (which is aria-current), how far the page scrolls
// sideways, and the scheme.
const exampleScenesSay = `(function () {
  var section = document.getElementById("carousel-pictures");
  section.scrollIntoView({ block: "center" });
  var host = section.querySelector('[data-kitwork-component="carousel: $scenes"]'), scope = kit.scopeFor(host);
  var track = host.querySelector(".carousel-track"), slides = Array.prototype.slice.call(track.children);
  var picture = slides[scope.current].querySelector("img");
  return document.fonts.ready.then(function () { return picture.decode().catch(function () {}); }).then(function () { return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
    var box = function (el) { if (!el) return "-"; var r = el.getBoundingClientRect(); return Math.round(r.left) + "," + Math.round(r.top) + " " + Math.round(r.width) + "x" + Math.round(r.height); };
    var t = track.getBoundingClientRect(), under = document.elementFromPoint(t.left + t.width / 2, t.top + t.height / 2);
    while (under && under.parentElement !== track) under = under.parentElement;
    var slide = under || slides[scope.current], caption = slide.querySelector(".place-bottom-left"), image = slide.querySelector("img");
    var button = function (call) { return host.querySelector('button[data-kitwork-event="click: ' + call + '"]'); };
    var dots = Array.prototype.slice.call(host.querySelectorAll("button[aria-controls]"));
    var r = section.getBoundingClientRect(), style = getComputedStyle(caption);
    done(JSON.stringify({
      x: r.left + scrollX - 16, y: r.top + scrollY - 16, width: r.width + 32, height: r.height + 32,
      current: scope.current, count: scope.count, playing: scope.playing,
      track: box(track), left: Math.round(track.scrollLeft), order: slides.map(function (s) { return s.style.cssText; }).join(""),
      seen: slides.indexOf(slide), image: box(image) + " natural " + image.naturalWidth + "x" + image.naturalHeight,
      caption: box(caption), words: Array.prototype.map.call(caption.children, function (p) { return p.textContent; }).join(" | "),
      ink: style.color + " on " + style.backgroundColor,
      cut: Array.prototype.filter.call(caption.querySelectorAll("p"), function (p) { return p.scrollWidth > p.clientWidth + 1; }).length,
      previous: box(button("previous()")), next: box(button("next()")), pill: box(button("toggle()").parentElement),
      dots: dots.map(function (d) { return d.getAttribute("aria-current") === "true" ? "1" : "0"; }).join(""),
      overflow: Math.max(document.documentElement.scrollWidth, innerWidth) - innerWidth,
      dark: matchMedia("(prefers-color-scheme: dark)").matches
    }));
  }); }); }); });
})()`

// exampleScenesRound presses the carousel of pictures' next arrow (from the last picture) and answers
// once the track has slid a third of the way on to the first picture: how far, the scroll position and
// the slides' inline styles (the order of the one that came round).
const exampleScenesRound = `(function () {
  var host = document.querySelector('[data-kitwork-component="carousel: $scenes"]'), track = host.querySelector(".carousel-track");
  host.querySelector('button[data-kitwork-event="click: next()"]').click();
  var start = track.scrollLeft, started = performance.now();
  return new Promise(function (done) {
    (function frame() { requestAnimationFrame(function () {
      var gone = (track.scrollLeft - start) / track.clientWidth;
      if (gone < 0.33 && performance.now() - started < 2000) { frame(); return; }
      done(JSON.stringify({ gone: Math.round(gone * 100) / 100, left: Math.round(track.scrollLeft),
        order: Array.prototype.map.call(track.children, function (s) { return s.style.order; }).join(",") }));
    }); })();
  });
})()`

// exampleScenesHost finds the carousel of pictures' host on /components as the engine wrote it.
var exampleScenesHost = regexp.MustCompile(`<div class="([^"]*)" data-kitwork-component="carousel: \$scenes"([^>]*)>`)

// exampleRectangle reads "x,y wxh" (exampleScenesSay's rectangles) as left, top, right, bottom.
func exampleRectangle(t *testing.T, said string) (float64, float64, float64, float64) {
	t.Helper()
	var x, y, width, height float64
	if _, err := fmt.Sscanf(said, "%g,%g %gx%g", &x, &y, &width, &height); err != nil {
		t.Fatalf("%q is no rectangle: %v", said, err)
	}
	return x, y, x + width, y + height
}

// exampleOverlaps says which of the named rectangles cover part of the caption, and by how much
// ("previous 34x8"), "nothing" when none does: logged, for the person who looks at the pictures.
func exampleOverlaps(t *testing.T, caption string, others map[string]string) string {
	t.Helper()
	left, top, right, bottom := exampleRectangle(t, caption)
	var said []string
	for _, name := range []string{"previous", "next", "pill"} {
		l, tp, r, b := exampleRectangle(t, others[name])
		if width, height := min(right, r)-max(left, l), min(bottom, b)-max(top, tp); width > 0 && height > 0 {
			said = append(said, fmt.Sprintf("%s %gx%g", name, width, height))
		}
	}
	if len(said) == 0 {
		return "nothing"
	}
	return strings.Join(said, ", ")
}

// The carousel of pictures of the kitwork example, drawn by a real browser: /components' section
// #carousel-pictures, four scenes from router.work (a for), each a picture 16/9 with its caption on it
// (place="bottom-left", white on black/60), the arrows over the middle of each side (place left and
// right) and a pill in the top right corner with the toggle and a dot for each scene. At a desktop's width
// (1280×800) and a phone's (390×844), light and dark:
//   - the server writes the host as a region named "Scenes", playing every six seconds (duration: 6000),
//     with count from the page's data (4); the page links no warning (examplePage);
//   - each of the four pictures, reached by its dot: its picture loaded and the track's width, the
//     caption the scene's words, white on a dark ground, no line of it cut, the caption, both arrows and
//     the pill inside the picture, the page not scrolling sideways;
//   - from the last picture, the next arrow: a frame a third of the way, the first picture having come
//     round after the last (order 1) and sliding in (the picture differs from both), then the track at
//     rest on the first, at 0, with no order left on any slide;
//   - the pictures are saved to KITWORK_CAROUSEL_SHOTS when it names a folder (scenes-1…4 and
//     scenes-round, per scheme and width). What each shot measures is logged.
func TestBrowserExampleCarouselPicturesShots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel of pictures screenshots in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant, page := examplePage(t, "/components")
	host := exampleScenesHost.FindStringSubmatch(page)
	if host == nil || !strings.Contains(host[1], "carousel") ||
		!strings.Contains(host[2], `role="region" aria-roledescription="carousel" aria-label="Scenes"`) ||
		!strings.Contains(host[2], `data-kitwork-scope="duration: 6000; count: 4"`) && !strings.Contains(host[2], `data-kitwork-scope="count: 4; duration: 6000"`) {
		t.Fatalf("/components has no carousel host named Scenes that plays every six seconds with four slides: %q", host)
	}
	server := exampleServer(tenant)
	defer server.Close()
	folder := os.Getenv("KITWORK_CAROUSEL_SHOTS")
	if folder != "" {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	type said struct {
		X, Y, Width, Height  float64
		Current, Count, Left float64
		Seen, Cut, Overflow  float64
		Playing, Dark        bool
		Track, Order, Image  string
		Caption, Words, Ink  string
		Previous, Next, Pill string
		Dots                 string
	}
	scenes := []string{"Dawn | A page that answers before the coffee is ready.", "Night shift | Jobs keep running while the city sleeps.",
		"Open water | One runtime, wherever you sail it.", "Far out | Small enough to carry, steady in the heat."}
	pictures := map[string]image.Image{}
	// a browser for each width (each lives 40 seconds, openDevtools)
	for _, width := range []int{1280, 390} {
		height := 800
		if width == 390 {
			height = 844
		}
		d := openDevtools(t, browser, server.URL+"/", width, height, "")
		d.loaded(exampleSettle)
		emulate(d, width, height)
		for _, scheme := range []string{"light", "dark"} {
			suffix := scheme + "-" + strconv.Itoa(width)
			d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{
				{"name": "prefers-color-scheme", "value": scheme},
				{"name": "prefers-reduced-motion", "value": "no-preference"},
			}})
			d.evaluate(`window.__stale = true`)
			d.call("Page.navigate", map[string]any{"url": server.URL + "/components"})
			d.loaded(`(window.__stale ? "wait of 0" : ` + exampleSettle + `)`)
			say := func() said {
				t.Helper()
				var out said
				raw := d.evaluate(exampleScenesSay)
				if err := json.Unmarshal([]byte(raw), &out); err != nil {
					t.Fatalf("%v: %s", err, raw)
				}
				return out
			}
			save := func(name string, raw []byte, picture image.Image) {
				t.Helper()
				pictures[name] = picture
				if folder != "" {
					if err := os.WriteFile(filepath.Join(folder, name+".png"), raw, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			// inside reports whether a rectangle stands inside the picture (the track)
			inside := func(rectangle, track string) bool {
				left, top, right, bottom := exampleRectangle(t, rectangle)
				l, tp, r, b := exampleRectangle(t, track)
				return left >= l-0.5 && top >= tp-0.5 && right <= r+0.5 && bottom <= b+0.5
			}
			var at said
			for i := range scenes {
				name := fmt.Sprintf("scenes-%d-%s", i+1, suffix)
				if i > 0 {
					// the scene by its dot, once the track rests there
					d.evaluate(fmt.Sprintf(`(function () {
  var host = document.querySelector('[data-kitwork-component="carousel: $scenes"]'), track = host.querySelector(".carousel-track");
  return new Promise(function (done) {
    track.addEventListener("scrollend", function () { done(""); }, { once: true });
    host.querySelectorAll("button[aria-controls]")[%d].click();
    setTimeout(function () { done("no scrollend"); }, 2000);
  });
})()`, i))
				}
				at = say()
				dots := []byte("0000")
				dots[i] = '1'
				state := fmt.Sprintf("current %g of %g, playing %v, seen %g, left %g, order %q, dots %s, words %s, cut %g, overflow %g, dark %v",
					at.Current, at.Count, at.Playing, at.Seen, at.Left, at.Order, at.Dots, at.Words, at.Cut, at.Overflow, at.Dark)
				_, _, trackRight, _ := exampleRectangle(t, at.Track)
				trackLeft, _, _, _ := exampleRectangle(t, at.Track)
				want := fmt.Sprintf("current %d of 4, playing true, seen %d, left %g, order \"\", dots %s, words %s, cut 0, overflow 0, dark %v",
					i, i, float64(i)*(trackRight-trackLeft), dots, scenes[i], scheme == "dark")
				if state != want {
					t.Errorf("%s:\n got  %s\n want %s", name, state, want)
				}
				// the browser says a colour with a slash as color(srgb …), the older way as rgba
				if at.Ink != "rgb(255, 255, 255) on color(srgb 0 0 0 / 0.6)" && at.Ink != "rgb(255, 255, 255) on rgba(0, 0, 0, 0.6)" {
					t.Errorf("%s: the caption is %s, want white on black/60", name, at.Ink)
				}
				if !strings.Contains(at.Image, " natural ") || strings.Contains(at.Image, "natural 0x") || strings.Fields(at.Image)[1] != strings.Fields(at.Track)[1] {
					t.Errorf("%s: the picture is %s in a track %s, want it loaded and the track's size", name, at.Image, at.Track)
				}
				for _, part := range []struct{ what, rectangle string }{{"the caption", at.Caption}, {"the previous arrow", at.Previous}, {"the next arrow", at.Next}, {"the pill", at.Pill}} {
					if !inside(part.rectangle, at.Track) {
						t.Errorf("%s: %s is at %s, outside the picture %s", name, part.what, part.rectangle, at.Track)
					}
				}
				raw, picture := screenShot(t, d, at.X, at.Y, at.Width, at.Height)
				if again := say(); again.Current != at.Current || again.Left != at.Left {
					t.Errorf("%s: pictured at current %g, left %g; read after: current %g, left %g", name, at.Current, at.Left, again.Current, again.Left)
				}
				save(name, raw, picture)
				t.Logf("%s: current %g of %g playing %v | track %s left %g | picture %s | caption %s %q %s | previous %s next %s pill %s | dots %s | overflow %g dark %v | over the caption: %s",
					name, at.Current, at.Count, at.Playing, at.Track, at.Left, at.Image, at.Caption, at.Words, at.Ink, at.Previous, at.Next, at.Pill, at.Dots, at.Overflow, at.Dark, exampleOverlaps(t, at.Caption, map[string]string{"previous": at.Previous, "next": at.Next, "pill": at.Pill}))
			}

			// from the last, the next arrow: a frame on the way round (the frame is the browser's own: the
			// track may have slid on by the time it is drawn, so it is taken again until one is neither the
			// last picture nor the first)
			name := "scenes-round-" + suffix
			var round struct {
				Gone, Left float64
				Order      string
			}
			taken := false
			for try := 0; try < 3 && !taken; try++ {
				if try > 0 {
					d.evaluate(`(function () {
  var host = document.querySelector('[data-kitwork-component="carousel: $scenes"]'), track = host.querySelector(".carousel-track");
  return new Promise(function (done) {
    track.addEventListener("scrollend", function () { done(""); }, { once: true });
    host.querySelectorAll("button[aria-controls]")[3].click();
    setTimeout(function () { done("no scrollend"); }, 2000);
  });
})().then(function () { return new Promise(function (done) { setTimeout(done, 300); }); })`)
				}
				if err := json.Unmarshal([]byte(d.evaluate(exampleScenesRound)), &round); err != nil {
					t.Fatal(err)
				}
				raw, picture := screenShot(t, d, at.X, at.Y, at.Width, at.Height)
				if round.Order == "1,,," && !samePicture(picture, pictures["scenes-4-"+suffix]) && !samePicture(picture, pictures["scenes-1-"+suffix]) {
					save(name, raw, picture)
					taken = true
				}
				t.Logf("%s, try %d: next() from the last, a third of the way: gone %g, left %g, orders %q, taken %v", name, try+1, round.Gone, round.Left, round.Order, taken)
			}
			if !taken {
				t.Errorf("%s: no frame on the way round was drawn between the last picture and the first", name)
			}
			// the round over: the first picture, at 0, no order left
			d.evaluate(`new Promise(function (done) { setTimeout(done, 1200); })`)
			rest := say()
			if got := fmt.Sprintf("current %g, seen %g, left %g, order %q, dots %s, words %s", rest.Current, rest.Seen, rest.Left, rest.Order, rest.Dots, rest.Words); got != "current 0, seen 0, left 0, order \"\", dots 1000, words "+scenes[0] {
				t.Errorf("%s, at rest:\n got  %s\n want current 0, seen 0, left 0, order \"\", dots 1000, words %s", name, got, scenes[0])
			}
		}
	}
	for _, pair := range [][2]string{{"scenes-1-light-1280", "scenes-1-dark-1280"}, {"scenes-1-light-390", "scenes-1-dark-390"},
		{"scenes-1-light-1280", "scenes-2-light-1280"}, {"scenes-3-dark-390", "scenes-4-dark-390"}} {
		if samePicture(pictures[pair[0]], pictures[pair[1]]) {
			t.Errorf("%s and %s are drawn the same", pair[0], pair[1])
		}
	}
}
