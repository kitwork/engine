package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The rotator browser proof runs the real timer. A component whose whole job is to advance on its
// own cannot be proven by reading its source: the interesting failures are a timer that never
// starts, one that keeps running after a pointer rests on the region, and one that survives the
// boundary it belongs to. Headless Chrome runs on a virtual clock, so the waits below cost virtual
// milliseconds, not wall time.
func TestBrowserRotatorAdvancesAndHolds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping rotator component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "rotator", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rotator.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/rotator.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(rotatorComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowserWithBudget(t, browser, server.URL+"/rotator.html", 20000)
}

var rotatorComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS rotator component</title></head><body>
  <section id="auto-host" data-kit-component="rotator@1.0.0" data-kit-scope="items: ['one', 'two', 'three']; active: 0; interval: 250">
    <output id="auto-active" data-kit-text="active"></output>
    <output id="auto-running" data-kit-text="running ? 'running' : 'held'"></output>
    <button id="auto-pause" data-kit-click="pause()">Pause</button>
    <button id="auto-play" data-kit-click="play()">Play</button>
    <button id="auto-select" data-kit-click="select(2)">Third</button>
  </section>

  <section data-kit-component="rotator@1.0.0" data-kit-scope="items: ['only']; active: 0; interval: 400">
    <output id="solo-active" data-kit-text="active"></output>
  </section>

  <section data-kit-component="rotator@1.0.0" data-kit-scope="active: 0; interval: 400">
    <output id="markup-active" data-kit-text="active"></output>
    <span data-rotator-item>alpha</span>
    <span data-rotator-item>beta</span>
    <span data-rotator-item>gamma</span>
  </section>

  <script src="/rotator.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  function sleep(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }
  function read(id) { return document.getElementById(id).textContent.trim(); }
  var host = document.getElementById("auto-host");

  // 1. It advances by itself and wraps.
  await waitFor(function () { return read("auto-active") === "0"; }, "rotator did not start on the first item");
  await waitFor(function () { return read("auto-active") === "1"; }, "rotator did not advance on its own");
  await waitFor(function () { return read("auto-active") === "2"; }, "rotator did not reach the third item");
  await waitFor(function () { return read("auto-active") === "0"; }, "rotator did not wrap back to the first item");
  assert(read("auto-running") === "running", "an advancing rotator did not report itself running");

  // 2. A pointer resting on the region holds it still — the WCAG 2.2.2 reason this component
  //    exists rather than a bare setInterval on the page.
  host.dispatchEvent(new Event("pointerenter"));
  await waitFor(function () { return read("auto-running") === "held"; }, "hover did not hold the rotator");
  var frozen = read("auto-active");
  await sleep(1000); // four intervals of virtual time
  assert(read("auto-active") === frozen, "a hovered rotator advanced anyway");

  // 3. …and releases when the pointer leaves.
  host.dispatchEvent(new Event("pointerleave"));
  await waitFor(function () { return read("auto-running") === "running"; }, "the rotator did not resume after pointerleave");
  await waitFor(function () { return read("auto-active") !== frozen; }, "the rotator did not advance after pointerleave");

  // 4. pause()/play() are the author's own hold, independent of hover.
  document.getElementById("auto-pause").click();
  await waitFor(function () { return read("auto-running") === "held"; }, "pause() did not hold the rotator");
  var paused = read("auto-active");
  await sleep(1000);
  assert(read("auto-active") === paused, "a paused rotator advanced anyway");

  // 5. A manual jump while paused still moves, and does not secretly resume.
  document.getElementById("auto-select").click();
  await waitFor(function () { return read("auto-active") === "2"; }, "select(2) did not move a paused rotator");
  assert(read("auto-running") === "held", "select() resumed a rotator the author had paused");

  document.getElementById("auto-play").click();
  await waitFor(function () { return read("auto-active") === "0"; }, "play() did not resume from the third item");

  // 6. One item has nowhere to go; the timer must never be armed.
  await sleep(1000);
  assert(read("solo-active") === "0", "a single-item rotator advanced");

  // 7. With no items array the count comes from the authored [data-rotator-item] elements.
  await waitFor(function () { return read("markup-active") === "1"; }, "rotator did not count authored [data-rotator-item] elements");
  await waitFor(function () { return read("markup-active") === "2"; }, "markup rotator did not reach the third item");

  // 8. Removing the boundary must take the timer with it. A leaked timer is invisible from the DOM
  //    — the kernel suppresses rendering on a detached root, and next() on a dead scope throws
  //    nothing — so watch the scheduling itself, counting only the timeouts armed at the rotator's
  //    own 250ms dwell, which no other rotator on this page uses.
  var rearmed = 0;
  var realSetTimeout = globalThis.setTimeout;
  globalThis.setTimeout = function (callback, delay) {
    if (delay === 250) rearmed++;
    return realSetTimeout.apply(this, arguments);
  };
  assert(read("auto-running") === "running", "step 8 needs a running rotator to prove cleanup");

  // First prove the counter can see this rotator at all. Without this the assertion below would
  // pass just as happily against a counter that was never wired to anything.
  await sleep(1000);
  assert(rearmed > 0, "the re-arm counter never observed a running rotator; step 8 proves nothing");

  host.remove();
  rearmed = 0;
  await sleep(1000); // four intervals: a rotator that outlived its boundary would re-arm several times
  globalThis.setTimeout = realSetTimeout;
  assert(rearmed === 0, "a removed rotator re-armed its timer " + rearmed + " times");
});
  </script>
</body></html>`, browserHarness)
