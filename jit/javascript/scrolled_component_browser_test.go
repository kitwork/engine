package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The scrolled browser proof scrolls a real document. The component's whole value is that the
// flag follows the viewport — so the test moves the viewport, watches both faces of the answer
// (the host attribute and the reactive state), honours a seeded offset, and proves a removed
// boundary stops listening.
func TestBrowserScrolledFollowsTheViewport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scrolled component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "scrolled", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/scrolled.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/scrolled.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(scrolledComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowserWithBudget(t, browser, server.URL+"/scrolled.html", 20000)
}

var scrolledComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS scrolled component</title>
<style>body{margin:0} .spacer{height:4000px}</style></head><body>
  <nav id="bar" data-kit-component="scrolled@1.0.0" data-scrolled="false">
    <output id="bar-state" data-kit-text="scrolled ? 'past' : 'top'"></output>
  </nav>
  <nav id="deep" data-kit-component="scrolled@1.0.0" data-kit-scope="offset: 500" data-scrolled="false">
    <output id="deep-state" data-kit-text="scrolled ? 'past' : 'top'"></output>
    <output id="deep-offset" data-kit-text="offset"></output>
  </nav>
  <div class="spacer"></div>

  <script src="/scrolled.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  function sleep(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }
  function attr(id) { return document.getElementById(id).getAttribute("data-scrolled"); }
  function read(id) { return document.getElementById(id).textContent.trim(); }
  // scrollTo moves the viewport synchronously; the browser dispatches the scroll event on its
  // next rendering step, which headless virtual time does not reliably reach while timers are
  // draining. Dispatching it here keeps the proof about the component, not about frame timing.
  function scroll(y) { window.scrollTo(0, y); window.dispatchEvent(new Event("scroll")); }
  var bar = document.getElementById("bar");

  // 1. At the top both faces say so, and the mount measured rather than assumed.
  await waitFor(function () { return read("bar-state") === "top" && attr("bar") === "false"; }, "scrolled did not start at rest");

  // 2. Past the default 24px threshold: attribute and state flip together, within a frame.
  scroll(300);
  await waitFor(function () { return attr("bar") === "true"; }, "attribute did not follow the scroll");
  await waitFor(function () { return read("bar-state") === "past"; }, "reactive state did not follow the scroll");

  // 3. The seeded offset is honoured: 300px is under a 500px threshold.
  assert(attr("deep") === "false" && read("deep-state") === "top", "offset: 500 flipped at 300px");
  assert(read("deep-offset") === "500", "offset seed not applied: offset=" + read("deep-offset") + " scrollY=" + window.scrollY);
  scroll(800);
  await sleep(100);
  assert(window.scrollY === 800, "viewport did not reach 800: scrollY=" + window.scrollY + " deep=" + attr("deep") + " bar=" + attr("bar"));
  await waitFor(function () { return attr("deep") === "true" && read("deep-state") === "past"; }, "offset: 500 did not flip at 800px");

  // 4. Back to the top, both come back.
  scroll(0);
  await waitFor(function () { return attr("bar") === "false" && read("bar-state") === "top"; }, "scrolled did not return to rest");
  await waitFor(function () { return attr("deep") === "false"; }, "offset host did not return to rest");

  // 5. A removed boundary stops listening: its attribute no longer changes with the viewport.
  bar.remove();
  await sleep(50); // the kernel disposes a removed boundary on its mutation pass, not synchronously
  scroll(600);
  await waitFor(function () { return attr("deep") === "true"; }, "the surviving host stopped following the viewport");
  await sleep(200);
  assert(bar.getAttribute("data-scrolled") === "false", "a removed scrolled host kept measuring");
});
  </script>
</body></html>`, browserHarness)
