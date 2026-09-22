package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// context.element("name") / context.elements("name") — a component's own named parts, the same
// data-kit-element the markup reads as $element.name, through the same owned() fence: the first
// (or all, in document order), never a nested host's, null / [] when absent, and a rejected name
// throws rather than returning something else.
func TestBrowserContextElementReadsTheComponentsOwnParts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping context.element browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "switch", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/context-element.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(contextElementDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/context-element.html")
}

var contextElementDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS context.element</title></head><body>
  <div id="rail" data-kit-component="rail">
    <div id="track" data-kit-element="track">
      <div class="slide" data-kit-element="slide">one</div>
      <div class="slide" data-kit-element="slide">two</div>
      <div id="nested" data-kit-component="rail"><div data-kit-element="slide">not ours</div></div>
      <div class="slide" data-kit-element="slide">three</div>
    </div>
    <output id="report" data-kit-text="report"></output>
  </div>
  <script src="/kit.js"></script><script>
kit.component("rail", {
  report: "",
  init: function (context) {
    var track = context.element("track");
    var slides = context.elements("slide");
    var missing = context.element("nothing");
    var none = context.elements("nothing");
    var rejected = "no";
    try { context.elements("bad-name"); } catch (error) { rejected = /identifier/.test(error.message) ? "yes" : error.message; }
    this.report = (track ? track.id : "null") + "|" + slides.length + "|" + Array.prototype.map.call(slides, function (s) { return s.textContent; }).join(",") + "|" + (missing === null ? "null" : "element") + "|" + none.length + "|" + rejected;
  }
});
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  var report = document.getElementById("report");
  await waitFor(function () { return report.textContent.length > 0; }, "the component should have reported");
  assert(report.textContent === "track|3|one,two,three|null|0|yes", "context.element/elements, got " + report.textContent);
});
  </script>
</body></html>`, browserHarness)
