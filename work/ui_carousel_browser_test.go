package work

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// carouselSlides writes n slides as an author would, each a box with its words ("Trang 1"…), every
// slide given width when one is named (three cards a row: width="33.333%").
func carouselSlides(n int, width string) string {
	var out strings.Builder
	for i := 1; i <= n; i++ {
		item := "<item>"
		if width != "" {
			item = `<item width="` + width + `">`
		}
		fmt.Fprintf(&out, "      %s<box padding=\"6\" background=\"panel\"><text>Trang %d</text></box></item>\n", item, i)
	}
	return out.String()
}

// carouselBrowserFiles is a site of carousels, one page for each way of moving:
//   - / holds two: $written, three slides written out, with previous, next and a counter; $repeated,
//     four slides a for repeats over the page's data, with previous, next, toggle, a dot for each slide
//     (for + go(i)) and a counter;
//   - /long: twelve slides 1200px wide with previous, next, a counter and a button for the first (go(0))
//     and the last (go(11)): a move across all of them runs over many frames, current held all the way;
//   - /play, /none, /fade: three slides with previous, next, toggle, pause and a counter, after a button
//     the keyboard starts on: duration="2s"; transition="none"; transition="fade";
//   - /cards: six cards, three a row (width="33.333%" on each <item>), with previous and next;
//   - /empty: a for over a list with nothing in it, and the list's <empty>;
//   - /named: $named, three slides written out, the first and the last named by their author (alt), and
//     $poured, two slides a for repeats, each named by alt from the page's data;
//   - /clicks: duration="2s", three slides with previous, next, toggle and a dot for each slide written
//     out (go(0), go(1), go(2)), after a button the keyboard starts on;
//   - /start: current="1", three slides with previous, next, a dot for each slide and a counter.
func carouselBrowserFiles() map[string]string {
	plain := func(alias, words string, extra string) string {
		return `<content padding="4" gap="4">
  <button id="before">Trước trình chiếu</button>
  <carousel as="$` + alias + `" alt="` + words + `"` + extra + ` max-width="640px" gap="2">
    <list>
` + carouselSlides(3, "") + `    </list>
    <box flex="row" gap="2">
      <button id="` + alias + `-previous" click="previous()">Trước</button>
      <button id="` + alias + `-next" click="next()">Sau</button>
      <button id="` + alias + `-toggle" click="toggle()">{{ playing ? 'Dừng' : 'Chạy' }}</button>
      <button id="` + alias + `-pause" click="pause()">Dừng hẳn</button>
      <text id="` + alias + `-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
</content>`
	}
	return map[string]string{
		"localhost/router.work": `import * as router from "kitwork/router";

router.colors({ brand: "#c8742c" });
router.language("vi");
router.get((context) => {
  const slides = [
    { title: "Một" },
    { title: "Hai" },
    { title: "Ba" },
    { title: "Bốn" }
  ];
  return context.view({ slides });
});
`,
		"localhost/view.ui": `<view><content /></view>`,
		"localhost/content.ui": `<content padding="4" gap="4">
  <carousel as="$written" alt="Ba trang" max-width="640px" gap="2">
    <list>
` + carouselSlides(3, "") + `    </list>
    <box flex="row" gap="2">
      <button id="written-previous" click="previous()">Trước</button>
      <button id="written-next" click="next()">Sau</button>
      <text id="written-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
  <carousel as="$repeated" alt="Món nổi bật" max-width="640px" gap="2">
    <list>
      <item for="slide of slides"><box padding="6" background="panel"><text>{{ slide.title }}</text></box></item>
    </list>
    <box flex="row" gap="2">
      <button id="repeated-previous" click="previous()">Trước</button>
      <button id="repeated-next" click="next()">Sau</button>
      <button id="repeated-toggle" click="toggle()">{{ playing ? 'Dừng' : 'Chạy' }}</button>
      <button for="slide, i of slides" click="go(i)" active:color="brand+30">{{ slide.title }}</button>
      <text id="repeated-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
</content>`,
		"localhost/long/content.ui": `<content padding="4">
  <carousel as="$long" alt="Mười hai trang" max-width="1200px" gap="2">
    <list>
` + carouselSlides(12, "") + `    </list>
    <box flex="row" gap="2">
      <button id="long-previous" click="previous()">Trước</button>
      <button id="long-next" click="next()">Sau</button>
      <button id="long-first" click="go(0)">Đầu</button>
      <button id="long-last" click="go(11)">Cuối</button>
      <text id="long-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
</content>`,
		"localhost/play/content.ui": plain("play", "Tự chạy", ` duration="2s"`),
		"localhost/none/content.ui": plain("none", "Nhảy", ` transition="none"`),
		"localhost/fade/content.ui": plain("fade", "Hiện dần", ` transition="fade"`),
		"localhost/cards/content.ui": `<content padding="4" gap="4">
  <carousel as="$cards" alt="Ba thẻ một hàng" max-width="900px" gap="2">
    <list>
` + carouselSlides(6, "33.333%") + `    </list>
    <box flex="row" gap="2">
      <button id="cards-previous" click="previous()">Trước</button>
      <button id="cards-next" click="next()">Sau</button>
      <text id="cards-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
</content>`,
		"localhost/empty/router.work": `import * as router from "kitwork/router";

router.get((context) => {
  const none = [];
  return context.view({ none });
});
`,
		"localhost/empty/content.ui": `<content padding="4" gap="4">
  <carousel as="$none" alt="Chưa có món" max-width="640px" gap="2">
    <list>
      <item for="slide of none"><box padding="6" background="panel"><text>{{ slide.title }}</text></box></item>
      <empty>Chưa có trang nào</empty>
    </list>
    <box flex="row" gap="2">
      <button id="none-next" click="next()">Sau</button>
      <text id="none-counter">{{ count }}</text>
    </box>
  </carousel>
</content>`,
		"localhost/named/router.work": `import * as router from "kitwork/router";

router.get((context) => {
  const drinks = [
    { name: "Trà đào" },
    { name: "Cacao" }
  ];
  return context.view({ drinks });
});
`,
		"localhost/named/content.ui": `<content padding="4" gap="4">
  <carousel as="$named" alt="Ba món" max-width="640px" gap="2">
    <list>
      <item alt="Cà phê sữa"><box padding="6" background="panel"><text>Một</text></box></item>
      <item><box padding="6" background="panel"><text>Hai</text></box></item>
      <item alt="Bạc xỉu"><box padding="6" background="panel"><text>Ba</text></box></item>
    </list>
    <box flex="row" gap="2">
      <button id="named-next" click="next()">Sau</button>
      <text id="named-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
  <carousel as="$poured" alt="Đồ uống" max-width="640px" gap="2">
    <list>
      <item for="drink of drinks" alt="{{ drink.name }}"><box padding="6" background="panel"><text>{{ drink.name }}</text></box></item>
    </list>
    <box flex="row" gap="2">
      <button id="poured-next" click="next()">Sau</button>
      <text id="poured-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
</content>`,
		"localhost/clicks/content.ui": `<content padding="4" gap="4">
  <button id="before">Trước trình chiếu</button>
  <carousel as="$clicks" alt="Bấm chuột" duration="2s" max-width="640px" gap="2">
    <list>
` + carouselSlides(3, "") + `    </list>
    <box flex="row" gap="2">
      <button id="clicks-previous" click="previous()">Trước</button>
      <button id="clicks-next" click="next()">Sau</button>
      <button id="clicks-toggle" click="toggle()">{{ playing ? 'Dừng' : 'Chạy' }}</button>
      <button id="clicks-dot-1" click="go(0)">1</button>
      <button id="clicks-dot-2" click="go(1)">2</button>
      <button id="clicks-dot-3" click="go(2)">3</button>
      <text id="clicks-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
</content>`,
		"localhost/start/content.ui": `<content padding="4" gap="4">
  <carousel as="$start" alt="Từ trang hai" current="1" max-width="640px" gap="2">
    <list>
` + carouselSlides(3, "") + `    </list>
    <box flex="row" gap="2">
      <button id="start-previous" click="previous()">Trước</button>
      <button id="start-next" click="next()">Sau</button>
      <button click="go(0)">1</button>
      <button click="go(1)">2</button>
      <button click="go(2)">3</button>
      <text id="start-counter">{{ (current + 1) + ' / ' + count }}</text>
    </box>
  </carousel>
</content>`,
	}
}

// carouselProbe reads the carousels of a page, installed once it has loaded. A carousel is named by its
// alias (a template page's carousel, which had none, went with template pages' client on 26/10/2026).
// read(alias) is what a person and a screen reader have of it now: the instance's current, count and
// playing, the track's scroll position and width, the first slide's width, each slide's aria-current
// ("0100") and name, each dot's aria-current, the counter's words (#<alias>-counter), the track's
// aria-live, each slide's computed opacity ("h" when it is visibility: hidden), the slides
// that have a CSS order ("0:1", "" for none) and what the screen shows: the slide under the middle of
// the track (elementFromPoint; its place among the slides as written, -1 for none) and how far its left
// edge is from the track's.
// From the moment it is installed, every order set on a slide of every carousel of the page is kept,
// with what the screen showed just before (the order not yet set) and just after the task that set it
// (the track's jump in the same task included): a slide that comes round, and goes home, in plain sight
// or not.
// watch(alias, act, within) reads it before act, at once after act (the same task), then in every frame
// from the next one until 50ms after the track's last scrollend, once no slide has an order and the
// track has stood still for two frames (within ms, 3 seconds unless given, at most), and says when the
// last scrollend came and which orders were set.
// moves(alias, within) waits, a frame at a time, until current changes, and says to what and after how
// long, or that it stayed.
const carouselProbe = `(function () {
  if (window.__carousel) return "ready";
  var host = function (alias) {
    return document.querySelector('[data-kitwork-component="carousel: $' + alias + '"]');
  };
  var partsOf = function (h) {
    var track = h.querySelector('.carousel-track');
    return { host: h, track: track, slides: Array.prototype.slice.call(track.children),
      dots: Array.prototype.slice.call(h.querySelectorAll('button[aria-controls]')) };
  };
  var parts = function (alias) { return partsOf(host(alias)); };
  var marks = function (list) { return list.map(function (el) { return el.getAttribute("aria-current") === "true" ? "1" : "0"; }).join(""); };
  var seen = function (p) {
    var r = p.track.getBoundingClientRect(), el = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    while (el && el.parentElement !== p.track) el = el.parentElement;
    return { seen: el ? p.slides.indexOf(el) : -1, at: el ? Math.round(el.getBoundingClientRect().left - r.left) : 0, left: Math.round(p.track.scrollLeft) };
  };
  var orders = function (p) { return p.slides.map(function (s, i) { return s.style.order ? i + ":" + s.style.order : ""; }).filter(Boolean).join(","); };
  var read = function (alias) {
    var p = parts(alias), scope = kitwork.scopeFor(p.host), screen = seen(p);
    var counter = document.getElementById(alias + "-counter");
    return { current: scope.current, count: scope.count, playing: !!scope.playing, left: Math.round(p.track.scrollLeft),
      width: p.track.clientWidth, slide: Math.round(p.slides[0].getBoundingClientRect().width),
      slides: marks(p.slides), dots: marks(p.dots), counter: counter ? counter.textContent : "",
      live: p.track.getAttribute("aria-live") || "", labels: p.slides.map(function (s) { return s.getAttribute("aria-label"); }).join("|"),
      opacity: p.slides.map(function (s) { var c = getComputedStyle(s); return c.visibility === "hidden" ? "h" : String(Math.round(parseFloat(c.opacity) * 100) / 100); }).join(" "),
      order: orders(p), seen: screen.seen, at: screen.at,
      inline: p.slides.some(function (s) { return s.style.cssText; }) ? p.slides.map(function (s) { return s.style.cssText; }).join("|") : "" };
  };
  // each slide's style is its own declaration seen through a proxy, whose order keeps the record (the
  // browser's declaration answers its properties itself: they cannot be redefined on it)
  var kept = [], inline = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "style");
  Array.prototype.forEach.call(document.querySelectorAll('[data-kitwork-component^="carousel"]'), function (h) {
    var alias = (h.getAttribute("data-kitwork-component") || "").replace(/^carousel: \$/, "");
    partsOf(h).slides.forEach(function (slide, index) {
      var declaration = inline.get.call(slide);
      var watched = new Proxy(declaration, {
        get: function (target, key) { var value = target[key]; return typeof value === "function" ? value.bind(target) : value; },
        set: function (target, key, value) {
          if (key !== "order") { target[key] = value; return true; }
          var record = { alias: alias, slide: index, value: String(value), at: performance.now(), before: seen(partsOf(h)) };
          target.order = value;
          kept.push(record);
          queueMicrotask(function () { record.after = seen(partsOf(h)); });
          return true;
        }
      });
      Object.defineProperty(slide, "style", { configurable: true, get: function () { return watched; }, set: function (text) { declaration.cssText = text; } });
    });
  });
  window.__carousel = {
    parts: parts, read: read, kept: kept,
    state: function (alias) { return JSON.stringify(read(alias)); },
    wait: function (ms) { return new Promise(function (done) { setTimeout(done, ms); }); },
    watch: function (alias, act, within) {
      return new Promise(function (done) {
        var p = parts(alias), before = read(alias), from = kept.length, samples = [], ended = 0, started = performance.now();
        var end = function () { ended = performance.now() - started; };
        p.track.addEventListener("scrollend", end);
        act();
        var after = read(alias);
        (function frame() {
          requestAnimationFrame(function () {
            var t = performance.now() - started, now = read(alias);
            now.t = t;
            samples.push(now);
            var moving = samples.length < 3 || now.left !== samples[samples.length - 2].left || now.left !== samples[samples.length - 3].left;
            if ((!ended || t < ended + 50 || now.order || moving) && t < (within || 3000)) { frame(); return; }
            p.track.removeEventListener("scrollend", end);
            var set = kept.slice(from).filter(function (r) { return r.alias === alias; }).map(function (r) {
              return { slide: r.slide, value: r.value, t: r.at - started, before: r.before, after: r.after };
            });
            done(JSON.stringify({ before: before, after: after, ended: ended, samples: samples, orders: set }));
          });
        })();
      });
    },
    moves: function (alias, within) {
      return new Promise(function (done) {
        var scope = kitwork.scopeFor(host(alias)), from = scope.current, started = performance.now();
        (function frame() {
          requestAnimationFrame(function () {
            var t = performance.now() - started;
            if (scope.current !== from || t >= within) {
              done(JSON.stringify({ moved: scope.current !== from, to: scope.current, after: t, playing: !!scope.playing }));
              return;
            }
            frame();
          });
        })();
      });
    }
  };
  return "installed";
})()`

// carouselRead is one read of a carousel (carouselProbe's read).
type carouselRead struct {
	T       float64 `json:"t"`
	Current float64 `json:"current"`
	Count   float64 `json:"count"`
	Playing bool    `json:"playing"`
	Left    float64 `json:"left"`
	Width   float64 `json:"width"`
	Slide   float64 `json:"slide"`
	Slides  string  `json:"slides"`
	Dots    string  `json:"dots"`
	Counter string  `json:"counter"`
	Live    string  `json:"live"`
	Labels  string  `json:"labels"`
	Opacity string  `json:"opacity"`
	Order   string  `json:"order"`
	Seen    float64 `json:"seen"`
	At      float64 `json:"at"`
	Inline  string  `json:"inline"`
}

// carouselScreen is what the screen showed at one moment: the slide under the middle of the track, its
// left edge from the track's, and the track's scroll position.
type carouselScreen struct {
	Seen float64 `json:"seen"`
	At   float64 `json:"at"`
	Left float64 `json:"left"`
}

// carouselOrder is one order set on a slide: which slide, to what ("" takes it away), when (ms after
// the act), and the screen just before and just after the task that set it.
type carouselOrder struct {
	Slide  float64        `json:"slide"`
	Value  string         `json:"value"`
	T      float64        `json:"t"`
	Before carouselScreen `json:"before"`
	After  carouselScreen `json:"after"`
}

// carouselWatched is what watch read: before the act, at once after it (the same task), every frame
// after it, when the last scrollend came (ms after the act; 0 for never), and the orders set.
type carouselWatched struct {
	Before  carouselRead    `json:"before"`
	After   carouselRead    `json:"after"`
	Ended   float64         `json:"ended"`
	Samples []carouselRead  `json:"samples"`
	Orders  []carouselOrder `json:"orders"`
}

// carouselState reads a carousel now.
func carouselState(t *testing.T, d *devtools, alias string) carouselRead {
	t.Helper()
	d.evaluate(carouselProbe)
	var out carouselRead
	raw := d.evaluate(`window.__carousel.state(` + strconv.Quote(alias) + `)`)
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return out
}

// carouselSays is a read as a line: current, count, playing, the slides and dots marked, the counter's
// words and the track's aria-live.
func carouselSays(r carouselRead) string {
	return fmt.Sprintf("current %g of %g, playing %v | slides %s | dots %s | counter %s | live %s", r.Current, r.Count, r.Playing, r.Slides, r.Dots, r.Counter, r.Live)
}

// carouselWatch runs act (a script) on a carousel and returns what watch read. With pending, act was
// started already by window.__pending (a watch begun before keys or the mouse were sent from Go).
func carouselWatch(t *testing.T, d *devtools, alias, act string) carouselWatched {
	t.Helper()
	return carouselWatchFor(t, d, alias, act, 3000)
}

// carouselWatchFor is carouselWatch that reads for within ms at most.
func carouselWatchFor(t *testing.T, d *devtools, alias, act string, within int) carouselWatched {
	t.Helper()
	d.evaluate(carouselProbe)
	script := `window.__carousel.watch(` + strconv.Quote(alias) + `, function () { ` + act + ` }, ` + strconv.Itoa(within) + `)`
	if act == "pending" {
		script = `window.__pending`
	}
	raw := d.evaluate(script)
	var out carouselWatched
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return out
}

// carouselSequence is what a field read in every frame went through, a run of equal values said once:
// "3" when it held, "3 2 3" when it went back and forth.
func carouselSequence(samples []carouselRead, field func(carouselRead) string) string {
	var out []string
	for _, s := range samples {
		if v := field(s); len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return strings.Join(out, " ")
}

// carouselMove says what a watch read, frame by frame: what current, the slides and dots marked and the
// counter went through, then how the track went from where it was to where it rests — "smoothly"
// through at least three frames between, "at once" through none, "by way of" the farthest place past
// either end when it went beyond (a scroll that snaps back), "stays at" when it never left — and
// whether it rests there by its scrollend.
func carouselMove(w carouselWatched) string {
	if len(w.Samples) == 0 {
		return "never read"
	}
	last := w.Samples[len(w.Samples)-1]
	from, to := w.Before.Left, last.Left
	between, beyond, past := 0, 0.0, 0.0
	for _, s := range w.Samples {
		if s.Left > min(from, to)+1 && s.Left < max(from, to)-1 {
			between++
		}
		if over := max(min(from, to)-s.Left, s.Left-max(from, to)); over > beyond {
			beyond, past = over, s.Left
		}
	}
	var how string
	switch {
	case from == to:
		how = fmt.Sprintf("stays at %g", from)
	case beyond > 1:
		how = fmt.Sprintf("%g → %g by way of %g", from, to, past)
	case between >= 3:
		how = fmt.Sprintf("%g → %g smoothly", from, to)
	case between == 0:
		how = fmt.Sprintf("%g → %g at once", from, to)
	default:
		how = fmt.Sprintf("%g → %g in %d frames between", from, to, between)
	}
	if from != to {
		if w.Ended > 0 {
			how += ", rests by its scrollend"
		} else {
			how += ", no scrollend"
		}
	}
	return fmt.Sprintf("current %s | slides %s | dots %s | counter %s | %s",
		carouselSequence(w.Samples, func(s carouselRead) string { return strconv.FormatFloat(s.Current, 'f', -1, 64) }),
		carouselSequence(w.Samples, func(s carouselRead) string { return s.Slides }),
		carouselSequence(w.Samples, func(s carouselRead) string { return s.Dots }),
		carouselSequence(w.Samples, func(s carouselRead) string { return s.Counter }), how)
}

// carouselTrace is the frames of a watch on one line, for a failure to show: time, current, scroll,
// the order set ([0:1]) and the slide seen (s3), then each order set: when, on which slide, to what,
// and the screen before and after.
func carouselTrace(w carouselWatched) string {
	line := []string{fmt.Sprintf("after the act %g@%g[%s]s%g", w.After.Current, w.After.Left, w.After.Order, w.After.Seen)}
	for _, s := range w.Samples {
		line = append(line, fmt.Sprintf("%.0f:%g@%g[%s]s%g", s.T, s.Current, s.Left, s.Order, s.Seen))
	}
	for _, o := range w.Orders {
		line = append(line, fmt.Sprintf("order at %.0f: slide %g %q, %s", o.T, o.Slide, o.Value, carouselSaw(o)))
	}
	return fmt.Sprintf("ended %.0f; %s", w.Ended, strings.Join(line, " "))
}

// carouselSaw says what the screen showed just before an order was set and just after: "slide 3 at 0 →
// slide 3 at 0" when the same slide stood in the same place (nothing moved on the screen), then the
// track's scroll position before and after.
func carouselSaw(o carouselOrder) string {
	return fmt.Sprintf("slide %g at %g → slide %g at %g, left %g → %g", o.Before.Seen, o.Before.At, o.After.Seen, o.After.At, o.Before.Left, o.After.Left)
}

// carouselWay says how the track went through a run of scroll positions: from the first to the last,
// "only forward" when it never went back (by more than a pixel), "only back" when it never went
// forward, "turns at <left>" when it did both, "stays" when it never moved; and "smoothly" through at
// least three positions between, "at once" through none.
func carouselWay(lefts []float64) string {
	first, last := lefts[0], lefts[len(lefts)-1]
	direction, turned, between := 0.0, math.NaN(), 0
	for i := 1; i < len(lefts); i++ {
		step := lefts[i] - lefts[i-1]
		if math.Abs(step) <= 1 {
			continue
		}
		if direction == 0 {
			direction = math.Copysign(1, step)
		} else if math.Copysign(1, step) != direction && math.IsNaN(turned) {
			turned = lefts[i-1]
		}
	}
	for _, left := range lefts {
		if left > min(first, last)+1 && left < max(first, last)-1 {
			between++
		}
	}
	way := "stays"
	switch {
	case !math.IsNaN(turned):
		way = fmt.Sprintf("turns at %g", turned)
	case direction > 0:
		way = "only forward"
	case direction < 0:
		way = "only back"
	}
	how := fmt.Sprintf("in %d frames between", between)
	switch {
	case between >= 3:
		how = "smoothly"
	case between == 0:
		how = "at once"
	}
	return fmt.Sprintf("%g → %g %s, %s", first, last, way, how)
}

// carouselRound says what a watch read of a move that comes round (next() from the last slide, or
// previous() from the first), frame by frame, in what a person and a screen reader have of it:
//   - what current, the slides and dots marked and the counter went through (a run of equal values said
//     once: "0" when current went straight there and never flickered);
//   - the slides seen under the middle of the track, before the act and in every frame after ("3 0": the
//     last, then the first; a track that ran back across would show "3 2 1 0");
//   - the slide that came round, its order, and the screen just before and after the task that set it
//     (the track jumps by one slide in that task: the same slide in the same place is no move);
//   - how the track went while a slide had an order: from where that task left it, frame by frame;
//   - home: the screen just before the order went and just after (the same slide in the same place);
//   - where it rests: the scroll position, the order then ("none"), the slide seen and where; and
//     whether a scrollend came.
//
// Any order set beyond those two is said too.
func carouselRound(w carouselWatched) string {
	if len(w.Samples) == 0 {
		return "never read"
	}
	last := w.Samples[len(w.Samples)-1]
	said := []string{
		// current from the act on, the same task included (the instance's own; the words follow by frame)
		"current " + carouselSequence(append([]carouselRead{w.After}, w.Samples...), func(s carouselRead) string { return strconv.FormatFloat(s.Current, 'f', -1, 64) }),
		"slides " + carouselSequence(w.Samples, func(s carouselRead) string { return s.Slides }),
		"dots " + carouselSequence(w.Samples, func(s carouselRead) string { return s.Dots }),
		"counter " + carouselSequence(w.Samples, func(s carouselRead) string { return s.Counter }),
		"seen " + carouselSequence(append([]carouselRead{w.Before}, w.Samples...), func(s carouselRead) string { return strconv.FormatFloat(s.Seen, 'f', -1, 64) }),
	}
	var came, home *carouselOrder
	var others []string
	for i := range w.Orders {
		o := &w.Orders[i]
		switch {
		case came == nil && o.Value != "":
			came = o
		case came != nil && home == nil && o.Value == "":
			home = o
		default:
			others = append(others, fmt.Sprintf("slide %g %q", o.Slide, o.Value))
		}
	}
	if came == nil {
		said = append(said, "no slide comes round")
	} else {
		said = append(said, fmt.Sprintf("slide %g comes round (order %s): %s", came.Slide, came.Value, carouselSaw(*came)))
		lefts := []float64{came.After.Left}
		for _, s := range w.Samples {
			if s.Order != "" {
				lefts = append(lefts, s.Left)
			}
		}
		if len(lefts) == 1 {
			said = append(said, "no frame while round")
		} else {
			said = append(said, "while round "+carouselWay(lefts))
		}
	}
	if home == nil {
		said = append(said, "never home")
	} else {
		said = append(said, "home: "+carouselSaw(*home))
	}
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
	if last.Inline != "" {
		rests += ", inline styles " + last.Inline // what the slides keep on them (nothing, unless the page wrote it)
	}
	said = append(said, rests)
	if len(others) > 0 {
		said = append(said, "orders besides: "+strings.Join(others, ", "))
	}
	return strings.Join(said, " | ")
}

// carouselWaits says what moves read: "moves to 2 after ~2s" when current changed between 1850ms and
// 2400ms after it began (the duration, 2s, from a moment just before), "moves to 2 after <ms>" any
// other time, "stays at 1" when it did not within the time given; and whether it is playing then.
func carouselWaits(t *testing.T, d *devtools, alias string, within int, before string) string {
	t.Helper()
	d.evaluate(carouselProbe)
	raw := d.evaluate(`(function () { ` + before + `; return window.__carousel.moves(` + strconv.Quote(alias) + `, ` + strconv.Itoa(within) + `); })()`)
	var out struct {
		Moved   bool
		To      float64
		After   float64
		Playing bool
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	switch {
	case !out.Moved:
		return fmt.Sprintf("stays at %g, playing %v", out.To, out.Playing)
	case out.After >= 1850 && out.After <= 2400:
		return fmt.Sprintf("moves to %g after ~2s, playing %v", out.To, out.Playing)
	default:
		return fmt.Sprintf("moves to %g after %.0fms, playing %v", out.To, out.After, out.Playing)
	}
}

// carouselPointer moves the mouse over the middle of what a selector finds, or to x,y when it is "".
func carouselPointer(t *testing.T, d *devtools, selector string, x, y float64) {
	t.Helper()
	x, y = carouselCentre(t, d, selector, x, y)
	d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y})
}

// carouselCentre is the middle of what a selector finds on screen, or x,y when it is "".
func carouselCentre(t *testing.T, d *devtools, selector string, x, y float64) (float64, float64) {
	t.Helper()
	if selector == "" {
		return x, y
	}
	var at struct{ X, Y float64 }
	if err := json.Unmarshal([]byte(d.evaluate(exampleNetworkCentre+`(`+strconv.Quote(selector)+`)`)), &at); err != nil {
		t.Fatalf("nothing on screen is %s", selector)
	}
	return at.X, at.Y
}

// carouselClick clicks with the screen's mouse, as a person does: over the middle of what a selector
// finds (or x,y when it is ""), pressed, let go. The mouse stays there.
func carouselClick(t *testing.T, d *devtools, selector string, x, y float64) {
	t.Helper()
	x, y = carouselCentre(t, d, selector, x, y)
	d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y})
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y, "button": "left", "clickCount": 1})
	}
}

// carouselFocus says which element has the focus (its id, "body" for none) and whether the browser
// shows it (:focus-visible: reached by the keyboard, not by a click).
const carouselFocus = `(function () { var a = document.activeElement; return (a && a.id ? a.id : a ? a.tagName.toLowerCase() : "none") + " focus-visible " + !!(a && a.matches(":focus-visible")); })()`

// carouselShiftTab presses Shift+Tab, as a person does.
func carouselShiftTab(d *devtools) {
	for _, kind := range []string{"rawKeyDown", "keyUp"} {
		d.call("Input.dispatchKeyEvent", map[string]any{"type": kind, "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9, "modifiers": 8})
	}
}

// carouselSwipe drags one finger across the track, from its middle by dx (negative: toward the start,
// as to see the next slide), through the screen's touch input: the browser decides, as on a phone,
// whether the gesture is the page's (a pan) or the script's.
func carouselSwipe(t *testing.T, d *devtools, alias string, dx float64) {
	t.Helper()
	var at struct{ X, Y float64 }
	if err := json.Unmarshal([]byte(d.evaluate(`(function () { var r = window.__carousel.parts(`+strconv.Quote(alias)+`).track.getBoundingClientRect(); return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 }); })()`)), &at); err != nil {
		t.Fatal(err)
	}
	steps := []float64{0, dx * 0.2, dx * 0.5, dx * 0.8, dx}
	for i, step := range steps {
		kind := "touchMove"
		if i == 0 {
			kind = "touchStart"
		}
		d.call("Input.dispatchTouchEvent", map[string]any{"type": kind, "touchPoints": []map[string]any{{"x": at.X + step, "y": at.Y}}})
	}
	d.call("Input.dispatchTouchEvent", map[string]any{"type": "touchEnd", "touchPoints": []map[string]any{}})
}

// carouselWheelWay is how a move went in carouselMove's words, after where it rests: a scroll of the
// trackpad that snaps is said without it.
var carouselWheelWay = regexp.MustCompile(`→ ([0-9.]+) (?:smoothly|at once|by way of [0-9.]+|in [0-9]+ frames between),`)

// carouselServer serves a tenant to a browser as localhost, one request at a time.
func carouselServer(tenant *Tenant) *httptest.Server {
	var serving sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
}

// carouselVisit loads a page of the server in the browser, with motion allowed or less motion asked
// for, and installs the probe.
func carouselVisit(d *devtools, url, motion string) {
	d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": motion}}})
	d.evaluate(`window.__stale = true`)
	d.call("Page.navigate", map[string]any{"url": url})
	d.loaded(`(window.__stale ? "wait of 0" : ` + exampleSettle + `)`)
	d.evaluate(carouselProbe)
}

// TestBrowserCarousel proves the carousel on pages the engine serves, in a real browser whose frames
// run (headless Chrome or Edge), each part with a browser of its own:
//   - first paint: the server writes the region and its name, the track (id, tabindex, aria-live
//     polite), each slide's role and, written out, its name "1 / 3"; for slides a for repeats, count
//     from the page's data and the dots (value, aria-controls, the first aria-current); once the
//     component starts, every slide is named "i / N" and the first is marked, slide and dot;
//   - a dot goes to its slide: the track scrolls there smoothly and rests by its scrollend at the
//     slide's place, and current, the slide and the dot marked and the counter go straight to it, in
//     every frame of the way (where the app's browser pane showed a lag); go(11) across twelve slides
//     1200px wide, a move of over a second, and back, do the same; a scroll by hand (instant)
//     and the arrow keys on the focused track (the browser's own scrolling) move current; a move cut
//     short by a scroll by hand ends on where the track rests; no move that goes straight there sets
//     an order on any slide;
//   - next() from the last goes on to the first, the way next() always goes (carouselRound, frame by
//     frame): the first comes round after the last (order 1) in a task that leaves the same slide in
//     the same place on the screen, the track slides only forward to it (the slides seen under its
//     middle: the last, then the first, never the ones between), current is the first from the press
//     on, and once the track rests the order is gone, the track at 0, the same slide in the same place
//     just before and just after (one task); previous() from the first is the mirror (order -1, only
//     back, the last's own place); written-out slides do the same;
//   - what else meets a round: a dot from the last to the first runs back across (a place chosen); a
//     hand that takes the track mid-round leaves current on the slide it left in view, which stays where
//     it is when the slide goes home; next(), previous() or a dot pressed again while the round still
//     slides is not taken (the round goes on as if pressed once); slides whose
//     transition covers every property come round with the order at once and keep their transition; a
//     page busy for 1.5s right after the press still slides on and goes home on the first; duration="2s"
//     from the last comes round the same way;
//   - duration="2s": playing from the start (aria-live off), it moves on after two seconds; it holds
//     while the mouse is over the carousel (the screen's mouse), while the focus is inside it (Tab) and
//     while the page is hidden, playing unchanged, and goes on two seconds after; pause() and toggle()
//     stop and start it, the track polite while it does not play;
//   - prefers-reduced-motion: reduce: not playing at the start, nothing moves, and a move is at once;
//     next() from the last comes round and goes home in the press's own task (no frame has an order,
//     the track at 0 when the press returns); play() still plays when asked;
//   - transition="none": a move is at once, next() from the last goes back to the first, nothing comes
//     round; transition="fade": the track never scrolls, the current slide shows (opacity 1) and fades
//     in, the others are hidden; ArrowRight and ArrowLeft on the focused track and a finger swiped
//     across it (the screen's touch input) move it; no order is ever set;
//   - three cards a row (width on each <item>): each card is a third of the track, a scroll of the
//     trackpad snaps to a card, and current is the first one seen; the last place the track reaches is
//     the fourth card's (the last two are seen from it): next() there goes back to the first in one
//     press (nothing comes round), and previous() at the first goes to that fourth card, never to a card
//     the track cannot scroll to;
//   - a for over a list with nothing: the list's <empty> shows, and it is no slide: count 0, no name and
//     no aria-current on it, next() moves nothing;
//   - a slide its author named (alt, written out or from a for's data) keeps that name once the component
//     starts and after every move; a slide with none is named "i / N";
//   - a click of the mouse on next or on a dot focuses the button but does not hold a carousel that
//     plays: it moves on after its duration; the keyboard's focus (Tab to a button) still holds it, and
//     it goes on once the focus leaves;
//   - current="1": the server's first paint already says the second slide (its dot aria-current, the
//     counter "2 / 3"), and the component starts there, the track at the second slide.
func TestBrowserCarousel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, carouselBrowserFiles())
	for _, path := range []string{"/", "/long", "/play", "/none", "/fade", "/cards", "/empty", "/named", "/clicks", "/start"} {
		if page := serveUI(t, tenant, "http://localhost"+path); page.Code != http.StatusOK || warningsScriptRe.MatchString(page.Body.String()) {
			t.Fatalf("%s = %d, or it links warnings:\n%.2000s", path, page.Code, page.Body.String())
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
	// move is a move that goes straight to its slide: no slide comes round on the way (no order set)
	move := func(t *testing.T, d *devtools, step, alias, act, want string) carouselWatched {
		t.Helper()
		w := carouselWatch(t, d, alias, act)
		if got := carouselMove(w); got != want {
			t.Errorf("%s:\n got  %s\n want %s\n frames %s", step, got, want, carouselTrace(w))
		}
		if len(w.Orders) > 0 {
			t.Errorf("%s: a slide came round on a move that goes straight there\n frames %s", step, carouselTrace(w))
		}
		return w
	}
	// round is a move that comes round (carouselRound)
	round := func(t *testing.T, d *devtools, step, alias, act, want string) carouselWatched {
		t.Helper()
		w := carouselWatch(t, d, alias, act)
		if got := carouselRound(w); got != want {
			t.Errorf("%s:\n got  %s\n want %s\n frames %s", step, got, want, carouselTrace(w))
		}
		return w
	}
	open := func(t *testing.T, path, motion string) *devtools {
		d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
		d.loaded(exampleSettle)
		carouselVisit(d, server.URL+path, motion)
		return d
	}

	t.Run("first paint, dots, wrap, by hand, keys", func(t *testing.T) {
		page := serveUI(t, tenant, "http://localhost/").Body.String()
		for _, want := range []string{
			`<div class="flex-column max-width-640px gap-2 carousel" data-kitwork-component="carousel: $written" role="region" aria-roledescription="trình chiếu" aria-label="Ba trang">`,
			`<ul class="flex-column carousel-track" id="kitwork-carousel-1" tabindex="0" role="list" data-kitwork-bind="aria-live: playing ? &#39;off&#39; : &#39;polite&#39;" aria-live="polite">`,
			`<li class="flex-column" aria-roledescription="trang chiếu" aria-label="1 / 3" aria-current="true">`,
			`<li class="flex-column" aria-roledescription="trang chiếu" aria-label="3 / 3" aria-current="false">`,
			`<p id="written-counter" data-kitwork-bind="text: (current + 1) + &#39; / &#39; + count">1 / 3</p>`,
			`data-kitwork-component="carousel: $repeated" role="region" aria-roledescription="trình chiếu" aria-label="Món nổi bật" data-kitwork-scope="count: 4">`,
			`<li class="flex-column" aria-roledescription="trang chiếu"><div class="flex-column padding-6 background-panel"><p>Bốn</p>`,
			`aria-controls="kitwork-carousel-2" data-kitwork-event="click: go(Number($this.value))" value="0" aria-current="true">Một</button>`,
			`aria-controls="kitwork-carousel-2" data-kitwork-event="click: go(Number($this.value))" value="3" aria-current="false">Bốn</button>`,
			`<p id="repeated-counter" data-kitwork-bind="text: (current + 1) + &#39; / &#39; + count">1 / 4</p>`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("the server's first paint has no %s", want)
			}
		}
		d := open(t, "/", "no-preference")
		started := carouselState(t, d, "repeated")
		check(t, "the repeated slides once the component started", carouselSays(started)+" | "+started.Labels,
			"current 0 of 4, playing false | slides 1000 | dots 1000 | counter 1 / 4 | live polite | 1 / 4|2 / 4|3 / 4|4 / 4")
		written := carouselState(t, d, "written")
		check(t, "the written slides once the component started", carouselSays(written)+" | "+written.Labels,
			"current 0 of 3, playing false | slides 100 | dots  | counter 1 / 3 | live polite | 1 / 3|2 / 3|3 / 3")
		if started.Width != 640 || started.Slide != 640 {
			t.Fatalf("the track is %gpx wide and a slide %gpx, want 640 both (max-width 640px)", started.Width, started.Slide)
		}

		dot := func(n int) string {
			return fmt.Sprintf(`window.__carousel.parts("repeated").dots[%d].click();`, n)
		}
		move(t, d, "dot 3 (go(2))", "repeated", dot(2), "current 2 | slides 0010 | dots 0010 | counter 3 / 4 | 0 → 1280 smoothly, rests by its scrollend")
		move(t, d, "dot 4 (go(3))", "repeated", dot(3), "current 3 | slides 0001 | dots 0001 | counter 4 / 4 | 1280 → 1920 smoothly, rests by its scrollend")
		// next() from the last goes on to the first, the way next() always goes: the first comes round after
		// the last (order 1) with nothing moving on the screen, the track slides only forward to it, current
		// is the first from the press on, and once the track rests the first goes home, the track jumping to
		// its own place in the same task: the same slide in the same place, then and after
		round(t, d, "next() from the last: the first comes round after it", "repeated", `document.getElementById("repeated-next").click();`,
			"current 0 | slides 1000 | dots 1000 | counter 1 / 4 | seen 3 0 | slide 0 comes round (order 1): slide 3 at 0 → slide 3 at 0, left 1920 → 1280 | "+
				"while round 1280 → 1920 only forward, smoothly | home: slide 0 at 0 → slide 0 at 0, left 1920 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend")
		// previous() from the first, the mirror: the last comes round before the first (order -1)
		round(t, d, "previous() from the first: the last comes round before it", "repeated", `document.getElementById("repeated-previous").click();`,
			"current 3 | slides 0001 | dots 0001 | counter 4 / 4 | seen 0 3 | slide 3 comes round (order -1): slide 0 at 0 → slide 0 at 0, left 0 → 640 | "+
				"while round 640 → 0 only back, smoothly | home: slide 3 at 0 → slide 3 at 0, left 0 → 1920 | rests at 1920, order none, slide 3 seen at 0, by its scrollend")
		move(t, d, "a scroll by hand", "repeated", `window.__carousel.parts("repeated").track.scrollTo({ left: 640, behavior: "instant" });`,
			"current 1 | slides 0100 | dots 0100 | counter 2 / 4 | 1920 → 640 at once, rests by its scrollend")
		// the arrow keys are the browser's: the focused track scrolls to the next snap, and current follows
		// the scroll as a hand's (no move was asked for): the next slide once the track is nearer to it
		d.evaluate(`window.__carousel.parts("repeated").track.focus(); window.__pending = window.__carousel.watch("repeated", function () {}); ""`)
		toastKey(d, "ArrowRight", 39)
		move(t, d, "ArrowRight on the focused track", "repeated", "pending",
			"current 1 2 | slides 0100 0010 | dots 0100 0010 | counter 2 / 4 3 / 4 | 640 → 1280 smoothly, rests by its scrollend")
		d.evaluate(`document.activeElement.blur(); ""`)
		// a move cut short: a dot asks for the last, a scroll by hand takes the track toward the second on
		// the way; once the track has stood still short of it (STILL frames), current is the slide it rests nearest (the
		// browser may leave it between snaps after such a scroll: where it rests is the browser's)
		move(t, d, "dot 1 (go(0))", "repeated", dot(0), "current 0 | slides 1000 | dots 1000 | counter 1 / 4 | 1280 → 0 smoothly, rests by its scrollend")
		w := carouselWatch(t, d, "repeated", dot(3)+` setTimeout(function () { window.__carousel.parts("repeated").track.scrollTo({ left: 640, behavior: "instant" }); }, 150);`)
		d.evaluate(`window.__carousel.wait(1200)`)
		rests := carouselState(t, d, "repeated")
		check(t, "a move cut short by a scroll by hand ends where the track rests",
			fmt.Sprintf("%s | rests nearest slide %.0f", carouselSays(rests), math.Round(rests.Left/rests.Width)),
			"current 1 of 4, playing false | slides 0100 | dots 0100 | counter 2 / 4 | live polite | rests nearest slide 1")
		if t.Failed() {
			t.Logf("the move cut short: %s; rests at %g", carouselTrace(w), rests.Left)
		}

		move(t, d, "written: next()", "written", `document.getElementById("written-next").click();`,
			"current 1 | slides 010 | dots  | counter 2 / 3 | 0 → 640 smoothly, rests by its scrollend")
		move(t, d, "written: previous()", "written", `document.getElementById("written-previous").click();`,
			"current 0 | slides 100 | dots  | counter 1 / 3 | 640 → 0 smoothly, rests by its scrollend")
		round(t, d, "written: previous() from the first", "written", `document.getElementById("written-previous").click();`,
			"current 2 | slides 001 | dots  | counter 3 / 3 | seen 0 2 | slide 2 comes round (order -1): slide 0 at 0 → slide 0 at 0, left 0 → 640 | "+
				"while round 640 → 0 only back, smoothly | home: slide 2 at 0 → slide 2 at 0, left 0 → 1280 | rests at 1280, order none, slide 2 seen at 0, by its scrollend")
	})

	t.Run("a long move", func(t *testing.T) {
		d := open(t, "/long", "no-preference")
		if got := carouselState(t, d, "long"); got.Width != 1200 || got.Count != 12 {
			t.Fatalf("the long track is %gpx wide with %g slides, want 1200px and 12", got.Width, got.Count)
		}
		// a place chosen far away: the track runs across eleven slides for over a second, and current stays
		// the one asked for in every frame (the move is watched until the track rests, the slides it passes
		// never count)
		for _, step := range []struct{ name, button, want string }{
			{"go(11) from the first of twelve", "long-last", "current 11 | slides 000000000001 | dots 01 | counter 12 / 12 | 0 → 13200 smoothly, rests by its scrollend"},
			{"go(0) from the last of twelve", "long-first", "current 0 | slides 100000000000 | dots 10 | counter 1 / 12 | 13200 → 0 smoothly, rests by its scrollend"},
		} {
			w := move(t, d, step.name, "long", `document.getElementById("`+step.button+`").click();`, step.want)
			if w.Ended < 1000 {
				t.Errorf("%s ended %.0fms after it began: it must last over a second to prove anything", step.name, w.Ended)
			}
		}
	})

	t.Run("next() comes round: a dot, a hand, two presses, a transition, a busy page, by itself", func(t *testing.T) {
		d := open(t, "/", "no-preference")
		dot := func(n int) string {
			return fmt.Sprintf(`window.__carousel.parts("repeated").dots[%d].click();`, n)
		}
		seen := func(w carouselWatched) string {
			return carouselSequence(append([]carouselRead{w.Before}, w.Samples...), func(s carouselRead) string { return strconv.FormatFloat(s.Seen, 'f', -1, 64) })
		}
		// toLast goes to the last slide by its dot, from wherever the track is: a move straight there
		toLast := func() {
			t.Helper()
			w := carouselWatch(t, d, "repeated", dot(3))
			last := w.Samples[len(w.Samples)-1]
			if got := fmt.Sprintf("current %s, slides %s, rests at %g, orders set %d", carouselSequence(w.Samples, func(s carouselRead) string { return strconv.FormatFloat(s.Current, 'f', -1, 64) }), last.Slides, last.Left, len(w.Orders)); got != "current 3, slides 0001, rests at 1920, orders set 0" {
				t.Fatalf("dot 4 (go(3)):\n got  %s\n want current 3, slides 0001, rests at 1920, orders set 0\n frames %s", got, carouselTrace(w))
			}
		}
		toLast()
		// a dot is a place chosen, like a tab: from the last to the first it runs back across the slides
		// between, and nothing comes round
		w := move(t, d, "dot 1 (go(0)) from the last", "repeated", dot(0), "current 0 | slides 1000 | dots 1000 | counter 1 / 4 | 1920 → 0 smoothly, rests by its scrollend")
		check(t, "dot 1 from the last: the slides seen on the way", seen(w), "3 2 1 0")
		toLast()

		// how a watch ends: the slides seen on the way, the orders set, home, and where the track rests
		ends := func(w carouselWatched) string {
			last := w.Samples[len(w.Samples)-1]
			home, at := "never home", math.Inf(1)
			var after []float64
			for _, o := range w.Orders {
				if o.Value == "" {
					home, at, after = carouselSaw(o), o.T, []float64{o.After.Left}
				}
			}
			for _, s := range w.Samples {
				if s.T > at {
					after = append(after, s.Left)
				}
			}
			way := "no frame after home"
			if len(after) > 1 {
				way = "after home " + carouselWay(after)
			}
			return fmt.Sprintf("current %s | seen %s | orders set %d | home: %s | %s | rests at %g, order %q, slide %g seen at %g, inline %q",
				carouselSequence(append([]carouselRead{w.After}, w.Samples...), func(s carouselRead) string { return strconv.FormatFloat(s.Current, 'f', -1, 64) }),
				seen(w), len(w.Orders), home, way, last.Left, last.Order, last.Seen, last.At, last.Inline)
		}

		// a hand takes the track mid-round: an instant scroll to the second place of the row as it stands,
		// the third slide's, held there a frame later too (Chrome adds one more frame of the smooth scroll
		// it cuts short to an instant scroll in the same frame, and leaves the track between two places).
		// Once the track has stood still there (STILL frames) the slide that came round goes home with the
		// screen as it is, and current is the slide the hand left in view
		w = carouselWatch(t, d, "repeated", `document.getElementById("repeated-next").click(); setTimeout(function () {
  var track = window.__carousel.parts("repeated").track;
  track.scrollTo({ left: 640, behavior: "instant" });
  requestAnimationFrame(function () { track.scrollTo({ left: 640, behavior: "instant" }); });
}, 150);`)
		if got := ends(w); !strings.HasSuffix(got, `| orders set 2 | home: slide 2 at 0 → slide 2 at 0, left 640 → 1280 | after home 1280 → 1280 stays, at once | rests at 1280, order "", slide 2 seen at 0, inline ""`) ||
			!strings.HasPrefix(got, "current 0 2 | ") {
			t.Errorf("a hand mid-round:\n got  %s\n want current 0 2 | seen … | orders set 2 | home: slide 2 at 0 → slide 2 at 0, left 640 → 1280 | after home 1280 → 1280 stays, at once | rests at 1280, order \"\", slide 2 seen at 0, inline \"\"\n frames %s", got, carouselTrace(w))
		}
		toLast()

		// presses again while the round still slides — next() twice, then previous(), then a dot — are not
		// taken: the round goes on exactly as if pressed once, forward only, and goes home on the first
		round(t, d, "next() from the last, then next(), previous() and a dot while it slides", "repeated",
			`document.getElementById("repeated-next").click();
setTimeout(function () { document.getElementById("repeated-next").click(); }, 80);
setTimeout(function () { document.getElementById("repeated-previous").click(); }, 160);
setTimeout(function () { `+dot(2)+` }, 240);`,
			"current 0 | slides 1000 | dots 1000 | counter 1 / 4 | seen 3 0 | slide 0 comes round (order 1): slide 3 at 0 → slide 3 at 0, left 1920 → 1280 | "+
				"while round 1280 → 1920 only forward, smoothly | home: slide 0 at 0 → slide 0 at 0, left 1920 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend")
		// a press while a plain move still slides to the last is not taken either: no slide comes round in
		// the middle of a move (where it used to jump half a slide), the move ends on the last
		move(t, d, "dot 3 (go(2))", "repeated", dot(2), "current 2 | slides 0010 | dots 0010 | counter 3 / 4 | 0 → 1280 smoothly, rests by its scrollend")
		move(t, d, "next() to the last, next() again while it slides", "repeated",
			`document.getElementById("repeated-next").click(); setTimeout(function () { document.getElementById("repeated-next").click(); }, 150);`,
			"current 3 | slides 0001 | dots 0001 | counter 4 / 4 | 1280 → 1920 smoothly, rests by its scrollend")

		// slides whose transition covers every property (an author's transition on all): the order a slide
		// comes round with holds at once, not eased in over the transition, and the slide's own transition is
		// back once it is home
		d.evaluate(`window.__carousel.parts("repeated").slides.forEach(function (s) { s.style.transition = "all 0.3s"; }); ""`)
		round(t, d, "next() from the last, the slides transitioning everything", "repeated", `document.getElementById("repeated-next").click();`,
			"current 0 | slides 1000 | dots 1000 | counter 1 / 4 | seen 3 0 | slide 0 comes round (order 1): slide 3 at 0 → slide 3 at 0, left 1920 → 1280 | "+
				"while round 1280 → 1920 only forward, smoothly | home: slide 0 at 0 → slide 0 at 0, left 1920 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend, "+
				"inline styles transition: 0.3s;|transition: 0.3s;|transition: 0.3s;|transition: 0.3s;")
		d.evaluate(`window.__carousel.parts("repeated").slides.forEach(function (s) { s.style.transition = ""; }); ""`)
		toLast()

		// a page busy for 1.5 seconds from the first frame after the press (a page drawing something heavy):
		// no frame is drawn while it is busy, so none is counted. The first still comes round, the track
		// slides on to it, and it goes home there (it does not give up and leave the last in view, current
		// naming the first)
		w = round(t, d, "next() from the last, the page then busy for 1.5s", "repeated",
			`document.getElementById("repeated-next").click(); requestAnimationFrame(function () { var busy = performance.now(); for (let i = 0; performance.now() - busy < 1500; i++) {} });`,
			"current 0 | slides 1000 | dots 1000 | counter 1 / 4 | seen 3 0 | slide 0 comes round (order 1): slide 3 at 0 → slide 3 at 0, left 1920 → 1280 | "+
				"while round 1280 → 1920 only forward, smoothly | home: slide 0 at 0 → slide 0 at 0, left 1920 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend")

		// by itself: duration="2s" from the last slide comes round the same way
		carouselVisit(d, server.URL+"/play", "no-preference")
		check(t, "/play at the last", d.evaluate(`(function () {
  var p = window.__carousel.parts("play");
  return new Promise(function (done) {
    p.track.addEventListener("scrollend", function () { done(String(kitwork.scopeFor(p.host).current)); }, { once: true });
    kitwork.scopeFor(p.host).go(2);
  });
})()`), "2")
		// read until the round is over: it comes two seconds after go(2), half a second of which is gone
		w = carouselWatchFor(t, d, "play", "", 4500)
		if got, want := carouselRound(w), "current 2 0 | slides 001 100 | dots  | counter 3 / 3 1 / 3 | seen 2 0 | slide 0 comes round (order 1): slide 2 at 0 → slide 2 at 0, left 1280 → 640 | "+
			"while round 640 → 1280 only forward, smoothly | home: slide 0 at 0 → slide 0 at 0, left 1280 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend"; got != want {
			t.Errorf("duration 2s from the last:\n got  %s\n want %s\n frames %s", got, want, carouselTrace(w))
		}
	})

	t.Run("duration plays, holds, pauses", func(t *testing.T) {
		page := serveUI(t, tenant, "http://localhost/play").Body.String()
		for _, want := range []string{
			`data-kitwork-component="carousel: $play" role="region" aria-roledescription="trình chiếu" aria-label="Tự chạy" data-kitwork-scope="duration: 2000">`,
			`data-kitwork-bind="aria-live: playing ? &#39;off&#39; : &#39;polite&#39;" aria-live="off">`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("/play's first paint has no %s", want)
			}
		}
		d := open(t, "/play", "no-preference")
		check(t, "at the start", carouselSays(carouselState(t, d, "play"))+" | "+d.evaluate(`document.getElementById("play-toggle").textContent`),
			"current 0 of 3, playing true | slides 100 | dots  | counter 1 / 3 | live off | Dừng")
		check(t, "moves on by itself", carouselWaits(t, d, "play", 3000, `kitwork.scopeFor(window.__carousel.parts("play").host).go(0)`), "moves to 1 after ~2s, playing true")
		d.evaluate(`window.__carousel.wait(900)`) // the move lands

		carouselPointer(t, d, `[data-kitwork-component="carousel: $play"] .carousel-track`, 0, 0)
		check(t, "the mouse over it", carouselWaits(t, d, "play", 2600, ""), "stays at 1, playing true")
		carouselPointer(t, d, "", 5, 790)
		check(t, "the mouse gone", carouselWaits(t, d, "play", 3000, ""), "moves to 2 after ~2s, playing true")
		d.evaluate(`window.__carousel.wait(900)`)

		d.evaluate(`document.getElementById("before").focus(); ""`)
		toastKey(d, "Tab", 9)
		check(t, "Tab reaches the track", d.evaluate(`document.activeElement.className`), "flex-column carousel-track")
		check(t, "the focus inside", carouselWaits(t, d, "play", 2600, ""), "stays at 2, playing true")
		carouselShiftTab(d)
		check(t, "Shift+Tab leaves it", d.evaluate(`document.activeElement.id`), "before")
		check(t, "the focus gone", carouselWaits(t, d, "play", 3000, ""), "moves to 0 after ~2s, playing true")
		d.evaluate(`window.__carousel.wait(900)`)

		check(t, "the page hidden", carouselWaits(t, d, "play", 2600, `Object.defineProperty(document, "hidden", { configurable: true, get: function () { return true; } }); document.dispatchEvent(new Event("visibilitychange"))`),
			"stays at 0, playing true")
		check(t, "the page shown again", carouselWaits(t, d, "play", 3000, `delete document.hidden; document.dispatchEvent(new Event("visibilitychange"))`),
			"moves to 1 after ~2s, playing true")
		d.evaluate(`window.__carousel.wait(900)`)

		d.evaluate(`document.getElementById("play-pause").click(); ""`)
		check(t, "pause()", carouselSays(carouselState(t, d, "play"))+" | "+d.evaluate(`document.getElementById("play-toggle").textContent`),
			"current 1 of 3, playing false | slides 010 | dots  | counter 2 / 3 | live polite | Chạy")
		check(t, "paused", carouselWaits(t, d, "play", 2600, ""), "stays at 1, playing false")
		check(t, "toggle() plays again", carouselWaits(t, d, "play", 3000, `document.getElementById("play-toggle").click()`), "moves to 2 after ~2s, playing true")
		check(t, "playing again", carouselSays(carouselState(t, d, "play")), "current 2 of 3, playing true | slides 001 | dots  | counter 3 / 3 | live off")
		d.evaluate(`document.getElementById("play-toggle").click(); ""`)
		check(t, "toggle() stops it", carouselSays(carouselState(t, d, "play")), "current 2 of 3, playing false | slides 001 | dots  | counter 3 / 3 | live polite")
	})

	t.Run("less motion", func(t *testing.T) {
		d := open(t, "/play", "reduce")
		if got := d.evaluate(`String(matchMedia("(prefers-reduced-motion: reduce)").matches)`); got != "true" {
			t.Fatalf("prefers-reduced-motion: reduce is not emulated (%s)", got)
		}
		check(t, "at the start", carouselSays(carouselState(t, d, "play"))+" | "+d.evaluate(`document.getElementById("play-toggle").textContent`),
			"current 0 of 3, playing false | slides 100 | dots  | counter 1 / 3 | live polite | Chạy")
		check(t, "nothing moves by itself", carouselWaits(t, d, "play", 2600, ""), "stays at 0, playing false")
		move(t, d, "next()", "play", `document.getElementById("play-next").click();`, "current 1 | slides 010 | dots  | counter 2 / 3 | 0 → 640 at once, rests by its scrollend")
		move(t, d, "next() again", "play", `document.getElementById("play-next").click();`, "current 2 | slides 001 | dots  | counter 3 / 3 | 640 → 1280 at once, rests by its scrollend")
		// next() from the last: the track scrolls at once, so the first comes round and goes home in the same
		// task, the press's own: no frame ever has an order
		w := round(t, d, "next() from the last", "play", `document.getElementById("play-next").click();`,
			"current 0 | slides 100 | dots  | counter 1 / 3 | seen 2 0 | slide 0 comes round (order 1): slide 2 at 0 → slide 0 at 0, left 1280 → 0 | "+
				"no frame while round | home: slide 0 at 0 → slide 0 at 0, left 1280 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend")
		check(t, "next() from the last, in the press's own task", fmt.Sprintf("current %g, left %g, order %q", w.After.Current, w.After.Left, w.After.Order), `current 0, left 0, order ""`)
		check(t, "play() when asked", carouselWaits(t, d, "play", 3000, `document.getElementById("play-toggle").click()`), "moves to 1 after ~2s, playing true")
	})

	t.Run("transition none", func(t *testing.T) {
		d := open(t, "/none", "no-preference")
		check(t, "the host", d.evaluate(`window.__carousel.parts("none").host.className`), "flex-column max-width-640px gap-2 carousel carousel-none")
		move(t, d, "next()", "none", `document.getElementById("none-next").click();`, "current 1 | slides 010 | dots  | counter 2 / 3 | 0 → 640 at once, rests by its scrollend")
		move(t, d, "next() again", "none", `document.getElementById("none-next").click();`, "current 2 | slides 001 | dots  | counter 3 / 3 | 640 → 1280 at once, rests by its scrollend")
		move(t, d, "next() from the last", "none", `document.getElementById("none-next").click();`, "current 0 | slides 100 | dots  | counter 1 / 3 | 1280 → 0 at once, rests by its scrollend")
	})

	t.Run("transition fade", func(t *testing.T) {
		d := open(t, "/fade", "no-preference")
		check(t, "the host", d.evaluate(`window.__carousel.parts("fade").host.className`), "flex-column max-width-640px gap-2 carousel carousel-fade")
		shown := func() string {
			s := carouselState(t, d, "fade")
			return fmt.Sprintf("current %g | slides %s | counter %s | left %g | opacity %s", s.Current, s.Slides, s.Counter, s.Left, s.Opacity)
		}
		settle := func() { d.evaluate(`window.__carousel.wait(450)`) }
		check(t, "at the start", shown(), "current 0 | slides 100 | counter 1 / 3 | left 0 | opacity 1 h h")
		// the slide coming in fades: some frame has it between hidden and whole
		fades := d.evaluate(`new Promise(function (done) {
  var slide = window.__carousel.parts("fade").slides[1], seen = [], started = performance.now();
  document.getElementById("fade-next").click();
  (function frame() { requestAnimationFrame(function () {
    seen.push(parseFloat(getComputedStyle(slide).opacity));
    if (performance.now() - started < 450) frame(); else done(seen.some(function (o) { return o > 0.05 && o < 0.95; }) ? "fades in" : "jumps: " + seen.join(" "));
  }); })();
})`)
		check(t, "next(): the slide comes in", fades, "fades in")
		check(t, "next()", shown(), "current 1 | slides 010 | counter 2 / 3 | left 0 | opacity h 1 h")
		d.evaluate(`document.getElementById("before").focus(); ""`)
		toastKey(d, "Tab", 9)
		toastKey(d, "ArrowRight", 39)
		settle()
		check(t, "ArrowRight on the focused track", shown(), "current 2 | slides 001 | counter 3 / 3 | left 0 | opacity h h 1")
		toastKey(d, "ArrowRight", 39)
		settle()
		check(t, "ArrowRight on the last", shown(), "current 0 | slides 100 | counter 1 / 3 | left 0 | opacity 1 h h")
		toastKey(d, "ArrowLeft", 37)
		settle()
		check(t, "ArrowLeft on the first", shown(), "current 2 | slides 001 | counter 3 / 3 | left 0 | opacity h h 1")
		d.evaluate(`document.activeElement.blur(); ""`)
		// each swipe lands somewhere the one before did not, so none passes by staying put
		carouselSwipe(t, d, "fade", 160)
		settle()
		check(t, "a finger swiped toward the end (the slide before)", shown(), "current 1 | slides 010 | counter 2 / 3 | left 0 | opacity h 1 h")
		carouselSwipe(t, d, "fade", 160)
		settle()
		check(t, "and again", shown(), "current 0 | slides 100 | counter 1 / 3 | left 0 | opacity 1 h h")
		carouselSwipe(t, d, "fade", -160)
		settle()
		check(t, "a finger swiped toward the start (the slide after)", shown(), "current 1 | slides 010 | counter 2 / 3 | left 0 | opacity h 1 h")
		carouselSwipe(t, d, "fade", -20)
		settle()
		check(t, "a finger that barely moves", shown(), "current 1 | slides 010 | counter 2 / 3 | left 0 | opacity h 1 h")
		// next() from the last and previous() from the first went plain: nothing scrolls, nothing comes round
		check(t, "orders set on a slide since the page loaded", d.evaluate(`String(window.__carousel.kept.length)`), "0")
	})

	t.Run("three cards a row", func(t *testing.T) {
		d := open(t, "/cards", "no-preference")
		s := carouselState(t, d, "cards")
		check(t, "each card", fmt.Sprintf("track %g, card %g, %s", s.Width, s.Slide, carouselSays(s)),
			"track 900, card 300, current 0 of 6, playing false | slides 100000 | dots  | counter 1 / 6 | live polite")
		// a trackpad's scroll a little past the second card: it snaps to it, the first one seen
		carouselPointer(t, d, `[data-kitwork-component="carousel: $cards"] .carousel-track`, 0, 0)
		var at struct{ X, Y float64 }
		_ = json.Unmarshal([]byte(d.evaluate(exampleNetworkCentre+`('[data-kitwork-component="carousel: $cards"] .carousel-track')`)), &at)
		d.evaluate(`window.__pending = window.__carousel.watch("cards", function () {}); ""`)
		d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": at.X, "y": at.Y, "deltaX": 380, "deltaY": 0})
		// how the browser gets there (straight, or past the card and back) is its own: where it rests is not
		wheel := carouselWatch(t, d, "cards", "pending")
		check(t, "a scroll of the trackpad", carouselWheelWay.ReplaceAllString(carouselMove(wheel), "→ $1,"),
			"current 0 1 | slides 100000 010000 | dots  | counter 1 / 6 2 / 6 | 0 → 300, rests by its scrollend")
		move(t, d, "next()", "cards", `document.getElementById("cards-next").click();`, "current 2 | slides 001000 | dots  | counter 3 / 6 | 300 → 600 smoothly, rests by its scrollend")
		carouselPointer(t, d, "", 5, 790)
		// the fourth card is the last place: the track's scroll ends there (6 × 300 − 900), the fifth and the
		// sixth are seen from it
		move(t, d, "next() to the last place", "cards", `document.getElementById("cards-next").click();`,
			"current 3 | slides 000100 | dots  | counter 4 / 6 | 600 → 900 smoothly, rests by its scrollend")
		move(t, d, "next() from the last place: round to the first in one press", "cards", `document.getElementById("cards-next").click();`,
			"current 0 | slides 100000 | dots  | counter 1 / 6 | 900 → 0 smoothly, rests by its scrollend")
		move(t, d, "previous() at the first: the last place", "cards", `document.getElementById("cards-previous").click();`,
			"current 3 | slides 000100 | dots  | counter 4 / 6 | 0 → 900 smoothly, rests by its scrollend")
		move(t, d, "previous() from the last place", "cards", `document.getElementById("cards-previous").click();`,
			"current 2 | slides 001000 | dots  | counter 3 / 6 | 900 → 600 smoothly, rests by its scrollend")
	})

	t.Run("a for with nothing: the empty is no slide", func(t *testing.T) {
		page := serveUI(t, tenant, "http://localhost/empty").Body.String()
		for _, want := range []string{
			`data-kitwork-component="carousel: $none" role="region" aria-roledescription="trình chiếu" aria-label="Chưa có món" data-kitwork-scope="count: 0">`,
			`aria-live="polite"><li class="flex-column" role="status">Chưa có trang nào</li></ul>`,
			`<p id="none-counter" data-kitwork-bind="text: count">0</p>`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("/empty's first paint has no %s", want)
			}
		}
		d := open(t, "/empty", "no-preference")
		// what the track holds as the browser has it: each child's tag, role, roledescription, name,
		// aria-current (- for none), words and whether it shows
		held := func() string {
			return d.evaluate(`(function () {
  var p = window.__carousel.parts("none"), scope = kitwork.scopeFor(p.host);
  var say = function (el, name) { var v = el.getAttribute(name); return v === null ? "-" : v; };
  return "count " + scope.count + ", current " + scope.current + " | " + p.slides.map(function (el) {
    return el.tagName.toLowerCase() + " role " + say(el, "role") + ", roledescription " + say(el, "aria-roledescription") + ", name " + say(el, "aria-label") +
      ", aria-current " + say(el, "aria-current") + ", " + JSON.stringify(el.textContent) + (el.checkVisibility() ? " shown" : " hidden");
  }).join(" ; ") + " | counter " + document.getElementById("none-counter").textContent;
})()`)
		}
		check(t, "once the component started", held(),
			`count 0, current 0 | li role status, roledescription -, name -, aria-current -, "Chưa có trang nào" shown | counter 0`)
		d.evaluate(`document.getElementById("none-next").click(); window.__carousel.wait(300)`)
		check(t, "next() with no slide", held(),
			`count 0, current 0 | li role status, roledescription -, name -, aria-current -, "Chưa có trang nào" shown | counter 0`)
	})

	t.Run("a name the author gave a slide stays", func(t *testing.T) {
		page := serveUI(t, tenant, "http://localhost/named").Body.String()
		for _, want := range []string{
			`<li class="flex-column" aria-label="Cà phê sữa" aria-roledescription="trang chiếu" aria-current="true">`,
			`<li class="flex-column" aria-roledescription="trang chiếu" aria-label="2 / 3" aria-current="false">`,
			`<li class="flex-column" aria-label="Bạc xỉu" aria-roledescription="trang chiếu" aria-current="false">`,
			`<li class="flex-column" aria-roledescription="trang chiếu" aria-label="Trà đào">`,
			`<li class="flex-column" aria-roledescription="trang chiếu" aria-label="Cacao">`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("/named's first paint has no %s", want)
			}
		}
		d := open(t, "/named", "no-preference")
		named := func(alias string) string {
			s := carouselState(t, d, alias)
			return fmt.Sprintf("current %g of %g | slides %s | names %s", s.Current, s.Count, s.Slides, s.Labels)
		}
		check(t, "written out, once the component started", named("named"), "current 0 of 3 | slides 100 | names Cà phê sữa|2 / 3|Bạc xỉu")
		check(t, "from a for, once the component started", named("poured"), "current 0 of 2 | slides 10 | names Trà đào|Cacao")
		move(t, d, "written out: next()", "named", `document.getElementById("named-next").click();`,
			"current 1 | slides 010 | dots  | counter 2 / 3 | 0 → 640 smoothly, rests by its scrollend")
		check(t, "written out, after next()", named("named"), "current 1 of 3 | slides 010 | names Cà phê sữa|2 / 3|Bạc xỉu")
		move(t, d, "written out: next() again", "named", `document.getElementById("named-next").click();`,
			"current 2 | slides 001 | dots  | counter 3 / 3 | 640 → 1280 smoothly, rests by its scrollend")
		round(t, d, "written out: next() from the last", "named", `document.getElementById("named-next").click();`,
			"current 0 | slides 100 | dots  | counter 1 / 3 | seen 2 0 | slide 0 comes round (order 1): slide 2 at 0 → slide 2 at 0, left 1280 → 640 | "+
				"while round 640 → 1280 only forward, smoothly | home: slide 0 at 0 → slide 0 at 0, left 1280 → 0 | rests at 0, order none, slide 0 seen at 0, by its scrollend")
		check(t, "written out, after going round", named("named"), "current 0 of 3 | slides 100 | names Cà phê sữa|2 / 3|Bạc xỉu")
		move(t, d, "from a for: next()", "poured", `document.getElementById("poured-next").click();`,
			"current 1 | slides 01 | dots  | counter 2 / 2 | 0 → 640 smoothly, rests by its scrollend")
		check(t, "from a for, after next()", named("poured"), "current 1 of 2 | slides 01 | names Trà đào|Cacao")
	})

	t.Run("a click does not hold it, the keyboard does", func(t *testing.T) {
		d := open(t, "/clicks", "no-preference")
		check(t, "at the start", carouselSays(carouselState(t, d, "clicks")), "current 0 of 3, playing true | slides 100 | dots 100 | counter 1 / 3 | live off")
		away := func() { carouselPointer(t, d, "", 5, 790) }
		// rested waits until the track rests at the slide a click moved it to: the time of the slide reached
		// starts then (it stays its whole duration once it is there)
		rested := func() {
			t.Helper()
			check(t, "the track rests", d.evaluate(`(function () {
  var p = window.__carousel.parts("clicks");
  return new Promise(function (done) {
    var late = setTimeout(function () { done("no scrollend"); }, 2000);
    p.track.addEventListener("scrollend", function () { clearTimeout(late); done("rested"); }, { once: true });
  });
})()`), "rested")
		}

		carouselClick(t, d, "#clicks-next", 0, 0)
		check(t, "the mouse clicks next", d.evaluate(carouselFocus)+" | "+carouselSays(carouselState(t, d, "clicks")),
			"clicks-next focus-visible false | current 1 of 3, playing true | slides 010 | dots 010 | counter 2 / 3 | live off")
		away()
		rested()
		check(t, "after the click on next, the mouse gone", carouselWaits(t, d, "clicks", 3000, ""), "moves to 2 after ~2s, playing true")
		d.evaluate(`document.activeElement.blur(); window.__carousel.wait(900)`)

		carouselClick(t, d, "#clicks-dot-1", 0, 0)
		check(t, "the mouse clicks the first dot", d.evaluate(carouselFocus)+" | "+carouselSays(carouselState(t, d, "clicks")),
			"clicks-dot-1 focus-visible false | current 0 of 3, playing true | slides 100 | dots 100 | counter 1 / 3 | live off")
		away()
		rested()
		check(t, "after the click on a dot, the mouse gone", carouselWaits(t, d, "clicks", 3000, ""), "moves to 1 after ~2s, playing true")
		d.evaluate(`window.__carousel.wait(900)`)

		// the focus a click left on next, then Tab: the toggle, reached by the keyboard, holds it
		carouselClick(t, d, "#clicks-next", 0, 0)
		away()
		toastKey(d, "Tab", 9)
		check(t, "Tab from next", d.evaluate(carouselFocus), "clicks-toggle focus-visible true")
		check(t, "the keyboard's focus on a button inside", carouselWaits(t, d, "clicks", 2600, ""), "stays at 2, playing true")
		carouselClick(t, d, "", 5, 790)
		check(t, "a click outside takes the focus", d.evaluate(carouselFocus), "body focus-visible false")
		check(t, "the focus gone", carouselWaits(t, d, "clicks", 3000, ""), "moves to 0 after ~2s, playing true")
	})

	t.Run("current starts it", func(t *testing.T) {
		page := serveUI(t, tenant, "http://localhost/start").Body.String()
		for _, want := range []string{
			`data-kitwork-component="carousel: $start" role="region" aria-roledescription="trình chiếu" aria-label="Từ trang hai" data-kitwork-scope="current: 1">`,
			`<li class="flex-column" aria-roledescription="trang chiếu" aria-label="2 / 3" aria-current="true">`, // the slide it starts on, from the first paint
			`data-kitwork-event="click: go(Number($this.value))" value="0" aria-current="false">1</button>`,
			`data-kitwork-event="click: go(Number($this.value))" value="1" aria-current="true">2</button>`,
			`data-kitwork-event="click: go(Number($this.value))" value="2" aria-current="false">3</button>`,
			`<p id="start-counter" data-kitwork-bind="text: (current + 1) + &#39; / &#39; + count">2 / 3</p>`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("/start's first paint has no %s", want)
			}
		}
		d := open(t, "/start", "no-preference")
		s := carouselState(t, d, "start")
		check(t, "once the component started", fmt.Sprintf("%s | left %g", carouselSays(s), s.Left),
			"current 1 of 3, playing false | slides 010 | dots 010 | counter 2 / 3 | live polite | left 640")
		// at once: the frames that follow do not slide there from the first (the track's CSS is smooth), and
		// current never names the first on the way
		move(t, d, "the frames after it started", "start", "", "current 1 | slides 010 | dots 010 | counter 2 / 3 | stays at 640")
		move(t, d, "previous()", "start", `document.getElementById("start-previous").click();`,
			"current 0 | slides 100 | dots 100 | counter 1 / 3 | 640 → 0 smoothly, rests by its scrollend")
	})
}
