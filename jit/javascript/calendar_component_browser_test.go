package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The calendar browser proof: the month of the selected day is shown, its
// title and weekday labels come from Intl, the grid is six weeks starting on
// the week's first day, next/previous turn the page, select refuses a day
// outside min…max and turns to the month of a neighbouring day, format()
// says the day in words.
func TestBrowserCalendarDrawsAMonthFromArithmetic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping calendar component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "calendar", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/calendar.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/calendar.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(calendarComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/calendar.html")
}

var calendarComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS calendar component</title></head><body>
  <div id="host" data-kit-component="calendar@1.0.0" data-kit-scope="selected: '2026-09-18'; min: '2026-09-10'; locale: 'en-GB'">
    <button id="previous" type="button" data-kit-click="previous()">‹</button>
    <output id="title" data-kit-text="title()"></output>
    <button id="next" type="button" data-kit-click="next()">›</button>
    <div id="weekdays"><template data-kit-for="name of weekdays()"><span data-kit-text="name"></span></template></div>
    <div id="grid"><template data-kit-for="cell of days()" data-kit-key="cell.iso"><button type="button" data-kit-click="select(cell.iso)" data-kit-bind:data-iso="cell.iso" data-kit-bind:aria-pressed="cell.selected" data-kit-bind:disabled="cell.disabled" data-kit-bind:aria-label="cell.label" data-kit-class="(cell.outside ? 'outside ' : '') + (cell.today ? 'today' : '')" data-kit-text="cell.day"></button></template></div>
    <button id="pick" type="button" data-kit-click="select('2026-10-05')">pick</button>
    <button id="early" type="button" data-kit-click="select('2026-09-01')">early</button>
    <button id="today" type="button" data-kit-click="today()">today</button>
    <button id="flip" type="button" data-kit-click="toggle()">flip</button>
    <button id="choose" type="button" data-kit-click="select('2026-09-20'); hide()">choose and close</button>
    <div id="panel" data-kit-show="open" hidden>panel</div>
    <output id="state" data-kit-text="selected + '|' + year + '-' + month + '|' + format()"></output>
  </div>
  <script src="/calendar.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  function title() { return document.getElementById("title").textContent.trim(); }
  function state() { return document.getElementById("state").textContent.trim(); }
  function cells() { return Array.prototype.slice.call(document.querySelectorAll("#grid button")); }

  await waitFor(function () { return title() === "September 2026" && cells().length === 42; }, "the selected day's month did not draw: " + title() + " / " + cells().length);
  var names = Array.prototype.map.call(document.querySelectorAll("#weekdays span"), function (s) { return s.textContent; });
  assert(names.join(",") === "Mon,Tue,Wed,Thu,Fri,Sat,Sun", "weekdays did not start on Monday: " + names.join(","));
  assert(cells()[0].getAttribute("data-iso") === "2026-08-31" && cells()[0].className.indexOf("outside") >= 0, "the grid did not start on the Monday before the 1st: " + cells()[0].getAttribute("data-iso"));
  assert(cells()[41].getAttribute("data-iso") === "2026-10-11", "the grid is not six weeks: " + cells()[41].getAttribute("data-iso"));
  var selected = cells().filter(function (c) { return c.getAttribute("aria-pressed") === "true"; });
  assert(selected.length === 1 && selected[0].getAttribute("data-iso") === "2026-09-18", "the selected day is not pressed");
  assert(cells().filter(function (c) { return c.disabled; }).length === 10, "days before min were not disabled: " + cells().filter(function (c) { return c.disabled; }).length);
  assert(state() === "2026-09-18|0-0|Friday, 18 September 2026", "state: " + state());
  assert(/Friday.*18 September 2026/.test(selected[0].getAttribute("aria-label")), "a cell's label is not the day in words: " + selected[0].getAttribute("aria-label"));

  document.getElementById("next").click();
  await waitFor(function () { return title() === "October 2026" && cells()[0].getAttribute("data-iso") === "2026-09-28"; }, "next() did not turn to October: " + title());
  document.getElementById("previous").click();
  document.getElementById("previous").click();
  await waitFor(function () { return title() === "August 2026"; }, "previous() twice did not reach August: " + title());

  document.getElementById("pick").click();
  await waitFor(function () { return state().indexOf("2026-10-05|2026-10|") === 0 && title() === "October 2026"; }, "select() did not choose and turn the page: " + state());
  document.getElementById("early").click();
  await new Promise(function (resolve) { setTimeout(resolve, 60); });
  assert(state().indexOf("2026-10-05|") === 0, "a day before min was accepted: " + state());

  // A day of the neighbouring month in the grid selects and turns the page.
  var outside = cells().filter(function (c) { return c.className.indexOf("outside") >= 0 && !c.disabled; })[0];
  outside.click();
  await waitFor(function () { return state().indexOf(outside.getAttribute("data-iso")) === 0 && title() !== "October 2026"; }, "an outside day did not select and turn: " + state() + " " + title());

  document.getElementById("today").click();
  var now = new Date();
  await waitFor(function () { return state().indexOf("|" + now.getFullYear() + "-" + (now.getMonth() + 1) + "|") > 0; }, "today() did not show this month: " + state());
  assert(cells().filter(function (c) { return c.className.indexOf("today") >= 0; }).length === 1, "today is not marked once");

  // The picker's disclosure lives on the same scope as the grid.
  var panel = document.getElementById("panel");
  assert(panel.hidden === true, "the panel started open");
  document.getElementById("flip").click();
  await waitFor(function () { return panel.hidden === false; }, "toggle() did not open the panel");
  document.getElementById("choose").click();
  await waitFor(function () { return panel.hidden === true && state().indexOf("2026-09-20|2026-9|") === 0; }, "select(); hide() did not choose and close: " + state());
});
  </script>
</body></html>`, browserHarness)
