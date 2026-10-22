package work

import (
	"testing"
)

// carouselPaddedFiles is the carousel site with one more page: /padded, a track with padding of its own
// (padding="4", 16px), four slides written out. Scroll-snap rests each slide 16px further than its
// place on the row: the track's padding comes before the first.
func carouselPaddedFiles() map[string]string {
	files := carouselBrowserFiles()
	files["localhost/padded/content.ui"] = `<content padding="4">
  <carousel as="$padded" alt="Dải có lề" max-width="640px" gap="2">
    <list padding="4">
` + carouselSlides(4, "") + `    </list>
    <button id="padded-next" click="next()">Sau</button>
  </carousel>
</content>`
	return files
}

// A press is not taken while a move asked for still slides (19/10/2026), and is taken again the moment
// the track rests at its slide, whatever moves it:
//   - a track with padding of its own: it opens on the first slide at its start (scroll-snap rests it
//     past its place on the row by the padding; the component puts it there at once, never the browser's
//     smooth snap as the page opens); next() is taken three frames after the track rests at the slide;
//     go() to the slide showing moves nothing, so the next press is taken at once;
//   - transition="none", and less motion: the track is there at once, so two next() in one task both
//     count;
//   - a hidden page: its smooth scroll does not move until the page shows, so a move there is at once,
//     and the press after it is taken.
//
// CONTROL: the track not put on its first slide as the component starts left it snapping smoothly, at
// 7px of 16 when read; a slide's place without the track's padding (the component before the evening of 19/10/2026)
// kept the padded track busy ten frames after it rested (next() answered 1) and made go() to the slide
// showing a move of 16px (the press after it answered 0); go() not clearing a move that is there at once
// answered 1 for "none", less motion and the hidden page (run 19/10/2026).
func TestBrowserCarouselTakesThePressOnceItRests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, carouselPaddedFiles())
	server := carouselServer(tenant)
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	check := func(step, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", step, got, want)
		}
	}

	carouselVisit(d, server.URL+"/padded", "no-preference")
	// where a slide stands on the screen: its left edge from the track's (0: the track shows it at its start)
	at := `function (p, i) { return Math.round(p.slides[i].getBoundingClientRect().left - p.track.getBoundingClientRect().left); }`
	check("the padded track opens on the first slide at its start, past the padding", d.evaluate(`(function () {
  var p = window.__carousel.parts("padded");
  return "slide 1 at " + (`+at+`)(p, 0) + ", left " + Math.round(p.track.scrollLeft);
})()`), "slide 1 at 0, left 16")
	check("go() to the slide showing, then next() at once", d.evaluate(`(function () {
  var scope = kit.scopeFor(window.__carousel.parts("padded").host);
  scope.go(0);
  return String(scope.next());
})()`), "1")
	check("next() three frames after the track rests", d.evaluate(`(function () {
  var p = window.__carousel.parts("padded"), scope = kit.scopeFor(p.host);
  return new Promise(function (done) {
    var frames = function (n, then) { requestAnimationFrame(function () { if (n > 1) frames(n - 1, then); else then(); }); };
    p.track.addEventListener("scrollend", function () {
      frames(3, function () { var here = (`+at+`)(p, 1); done(String(scope.next()) + ", slide 2 was at " + here); });
    }, { once: true });
  });
})()`), "2, slide 2 was at 0")

	for _, c := range []struct{ name, path, motion, alias string }{
		{"transition none", "/none", "no-preference", "none"},
		{"less motion", "/", "reduce", "repeated"},
	} {
		carouselVisit(d, server.URL+c.path, c.motion)
		check(c.name+": next() twice in one task", d.evaluate(`(function () {
  var scope = kit.scopeFor(window.__carousel.parts("`+c.alias+`").host);
  scope.next();
  return String(scope.next());
})()`), "2")
	}

	carouselVisit(d, server.URL+"/", "no-preference")
	check("a hidden page: go(1) then go(2)", d.evaluate(`(function () {
  var p = window.__carousel.parts("repeated"), scope = kit.scopeFor(p.host);
  Object.defineProperty(document, "hidden", { configurable: true, get: function () { return true; } });
  scope.go(1);
  var taken = scope.go(2) + " at " + Math.round(p.track.scrollLeft);
  delete document.hidden;
  return taken;
})()`), "2 at 1280")
}
