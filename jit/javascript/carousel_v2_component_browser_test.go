package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The carousel 2.0.0 browser proof: the track gets a copy of the last slide in
// front and of the first behind; next from the last slide keeps sliding
// forward onto the copy and then snaps, without a transition, to the real
// first slide; previous from the first does the mirror; select goes straight
// there; a swipe and the arrow keys move it; aria-hidden follows the active
// slide; with loop: false there are no copies and the ends are ends.
func TestBrowserCarouselV2GoesRound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping carousel 2.0.0 browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "carousel", Version: "2.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/carousel.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/carousel.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(carouselV2ComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/carousel.html")
}

var carouselV2ComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS carousel 2.0.0</title><script>
window.__errs = [];
window.addEventListener("error", function (e) { window.__errs.push(String(e.error && e.error.message || e.message)); });
var __ce = console.error; console.error = function () { window.__errs.push(Array.prototype.map.call(arguments, String).join(" ")); return __ce.apply(this, arguments); };
</script><style>
  .frame { width: 300px; overflow: hidden; }
  .track { display: flex; transition: transform 80ms linear; }
  .still { display: flex; transition: none; }
  .slide { width: 300px; flex-shrink: 0; height: 60px; }
</style></head><body>
  <div id="ring" data-kit-component="carousel@2.0.0">
    <div class="frame"><div id="track" class="track" data-carousel-track tabindex="0">
      <div id="s0" class="slide" data-carousel-slide>one</div>
      <div id="s1" class="slide" data-carousel-slide>two</div>
      <div id="s2" class="slide" data-carousel-slide>three</div>
    </div></div>
    <button id="previous" type="button" data-kit-click="previous()">‹</button>
    <button id="next" type="button" data-kit-click="next()">›</button>
    <button id="pick" type="button" data-kit-click="select(1)">2</button>
    <output id="state" data-kit-text="active + '|' + count() + '|' + (moving ? 'moving' : 'still') + '|' + (isActive(0) ? 'a' : '-') + (isActive(1) ? 'b' : '-') + (isActive(2) ? 'c' : '-')"></output>
  </div>
  <div id="line" data-kit-component="carousel@2.0.0" data-kit-scope="loop: false, active: 2">
    <div class="frame"><div id="line-track" class="still" data-carousel-track>
      <div class="slide" data-carousel-slide>one</div>
      <div class="slide" data-carousel-slide>two</div>
      <div class="slide" data-carousel-slide>three</div>
    </div></div>
    <button id="line-next" type="button" data-kit-click="next()">›</button>
    <output id="line-state" data-kit-text="active + '|' + (moving ? 'moving' : 'still')"></output>
  </div>
  <script src="/carousel.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var track = document.getElementById("track");
  function state() { return document.getElementById("state").textContent.trim(); }
  function shift() { return track.style.transform; }
  function hidden() { return ["s0", "s1", "s2"].map(function (id) { return document.getElementById(id).getAttribute("aria-hidden") === "true" ? "h" : "v"; }).join(""); }

  await waitFor(function () { return state() === "0|3|still|a--" && track.children.length === 5; }, "the track did not get its copies: " + state() + " children=" + track.children.length + " " + window.__errs.join(";"));
  assert(track.firstElementChild.hasAttribute("data-carousel-clone") && track.firstElementChild.textContent === "three", "the copy in front is not the last slide");
  assert(track.lastElementChild.hasAttribute("data-carousel-clone") && track.lastElementChild.textContent === "one", "the copy behind is not the first slide");
  assert(track.lastElementChild.getAttribute("aria-hidden") === "true", "a copy is not hidden from readers");
  assert(shift() === "translateX(-100%%)", "the track did not start on the real first slide: " + shift());
  assert(hidden() === "vhh", "only the active slide should be visible to readers: " + hidden());

  document.getElementById("next").click();
  document.getElementById("next").click();
  await waitFor(function () { return state() === "2|3|still|--c" && shift() === "translateX(-300%%)"; }, "two steps forward did not land on the third slide: " + state() + " " + shift());
  assert(hidden() === "hhv", "aria-hidden did not follow: " + hidden());

  // The ring: from the last slide, next keeps going forward onto the copy…
  document.getElementById("next").click();
  await waitFor(function () { return state().indexOf("0|3|moving|a--") === 0; }, "next from the last slide did not activate the first while moving: " + state());
  assert(shift() === "translateX(-400%%)", "next from the last slide slid backwards instead of onto the copy: " + shift());
  // …and then snaps to the real first slide with no transition.
  await waitFor(function () { return state() === "0|3|still|a--" && shift() === "translateX(-100%%)"; }, "the track did not snap from the copy to the real first slide: " + state() + " " + shift());
  assert(track.style.transition === "", "the transition was left disabled after the snap");

  // The mirror: previous from the first slides onto the copy of the last, then snaps.
  document.getElementById("previous").click();
  await waitFor(function () { return state().indexOf("2|3|moving|--c") === 0; }, "previous from the first did not activate the last: " + state());
  assert(/^translateX\(0(px)?\)$/.test(shift()), "previous from the first slid forwards instead of onto the copy: " + shift());
  await waitFor(function () { return state() === "2|3|still|--c" && shift() === "translateX(-300%%)"; }, "the track did not snap from the copy to the real last slide: " + state() + " " + shift());

  document.getElementById("pick").click();
  await waitFor(function () { return state() === "1|3|still|-b-" && shift() === "translateX(-200%%)"; }, "select(1) did not go straight to the second slide: " + state() + " " + shift());

  // A swipe to the left is next; the arrow keys on the track work too.
  track.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, pointerId: 5, button: 0, clientX: 200, clientY: 20 }));
  track.dispatchEvent(new PointerEvent("pointermove", { bubbles: true, pointerId: 5, clientX: 120, clientY: 22 }));
  track.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, pointerId: 5, clientX: 120, clientY: 22 }));
  await waitFor(function () { return state() === "2|3|still|--c"; }, "a swipe left did not go to the next slide: " + state());
  track.focus();
  track.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true, cancelable: true }));
  await waitFor(function () { return state() === "1|3|still|-b-"; }, "ArrowLeft did not go back: " + state());

  // No loop: no copies, and the last slide is the end.
  var line = document.getElementById("line-track");
  assert(line.children.length === 3 && !line.querySelector("[data-carousel-clone]"), "loop: false still got copies");
  await waitFor(function () { return document.getElementById("line-state").textContent.trim() === "2|still" && line.style.transform === "translateX(-200%%)"; }, "loop: false did not start on the seeded slide: " + document.getElementById("line-state").textContent + " " + line.style.transform);
  document.getElementById("line-next").click();
  await new Promise(function (resolve) { setTimeout(resolve, 60); });
  assert(document.getElementById("line-state").textContent.trim() === "2|still" && line.style.transform === "translateX(-200%%)", "loop: false went past the end: " + document.getElementById("line-state").textContent + " " + line.style.transform);
});
  </script>
</body></html>`, browserHarness)
