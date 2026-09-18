package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The chart browser proof: a series becomes bars with percentages the markup
// draws as heights, points and a line path the SVG binds as d, an area closed
// to the baseline, ticks with their y, and the summary numbers; the range
// starts at zero unless min/max say otherwise; changing the series redraws.
func TestBrowserChartTurnsNumbersIntoGeometry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping chart component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "chart", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/chart.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/chart.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(chartComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/chart.html")
}

var chartComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS chart component</title><script>
window.__errs = [];
window.addEventListener("error", function (e) { window.__errs.push(String(e.error && e.error.message || e.message)); });
window.addEventListener("unhandledrejection", function (e) { window.__errs.push(String(e.reason && e.reason.message || e.reason)); });
var __ce = console.error; console.error = function () { window.__errs.push(Array.prototype.map.call(arguments, String).join(" ")); return __ce.apply(this, arguments); };
var __cw = console.warn; console.warn = function () { window.__errs.push(Array.prototype.map.call(arguments, String).join(" ")); return __cw.apply(this, arguments); };
</script></head><body>
  <div id="host" data-kit-component="chart@1.0.0" data-kit-scope="series: [2, 4, 8, 6]; labels: ['Mon', 'Tue', 'Wed', 'Thu']; width: 100; height: 50; padding: 0">
    <svg id="svg" data-kit-bind="viewBox: viewBox();"><path id="grid" data-kit-bind="d: grid(3);"></path><path id="area" data-kit-bind="d: area();"></path><path id="line" data-kit-bind="d: line();"></path><polyline id="poly" data-kit-bind="points: polyline();"></polyline></svg>
    <div id="bars" style="display:flex;height:100px;align-items:flex-end"><template data-kit-for="bar of bars()" data-kit-key="bar.index"><div data-kit-style="height: bar.percent + '%%';" data-kit-bind="title: bar.label + ' ' + bar.value;"></div></template></div>
    <div id="ticks"><template data-kit-for="tick of ticks(3)"><span data-kit-text="tick.value + '@' + tick.y"></span></template></div>
    <output id="summary" data-kit-text="count() + '|' + total() + '|' + average() + '|' + peak() + '|' + last() + '|' + change() + '|' + low() + '-' + high()"></output>
    <button id="grow" type="button" data-kit-click="series = [10, 20]">grow</button>
    <button id="floor" type="button" data-kit-click="min = 5; max = 25">floor</button>
  </div>
  <script src="/chart.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  function summary() { return document.getElementById("summary").textContent.trim(); }
  function bars() { return Array.prototype.map.call(document.querySelectorAll("#bars div"), function (d) { return d.style.height; }); }

  await waitFor(function () { return summary() === "4|20|5|8|6|200|0-8"; }, "the summary is wrong: " + summary() + " " + window.__errs.join(";"));
  assert(document.getElementById("svg").getAttribute("viewBox") === "0 0 100 50", "viewBox: " + document.getElementById("svg").getAttribute("viewBox"));
  assert(document.getElementById("line").getAttribute("d") === "M0 37.5 L33.33 25 L66.67 0 L100 12.5", "line: " + document.getElementById("line").getAttribute("d"));
  assert(document.getElementById("area").getAttribute("d") === "M0 37.5 L33.33 25 L66.67 0 L100 12.5 L100 50 L0 50 Z", "area: " + document.getElementById("area").getAttribute("d"));
  assert(document.getElementById("poly").getAttribute("points") === "0,37.5 33.33,25 66.67,0 100,12.5", "polyline: " + document.getElementById("poly").getAttribute("points"));
  assert(document.getElementById("grid").getAttribute("d") === "M0 50 H100 M0 25 H100 M0 0 H100", "grid: " + document.getElementById("grid").getAttribute("d"));
  assert(bars().join(",") === "25%%,50%%,100%%,75%%", "bar heights: " + bars().join(","));
  assert(document.querySelector("#bars div").getAttribute("title") === "Mon 2", "bar label: " + document.querySelector("#bars div").getAttribute("title"));
  var ticks = Array.prototype.map.call(document.querySelectorAll("#ticks span"), function (s) { return s.textContent; });
  assert(ticks.join(",") === "0@50,4@25,8@0", "ticks: " + ticks.join(","));

  document.getElementById("grow").click();
  await waitFor(function () { return summary() === "2|30|15|20|20|100|0-20" && bars().join(",") === "50%%,100%%"; }, "a new series did not redraw: " + summary() + " " + bars().join(","));
  document.getElementById("floor").click();
  await waitFor(function () { return summary().indexOf("|5-25") > 0 && bars().join(",") === "25%%,75%%"; }, "min/max did not move the range: " + summary() + " " + bars().join(","));
});
  </script>
</body></html>`, browserHarness)
