package js

import (
	"strings"
	"testing"
)

func TestCalendarIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<div data-kit-component="calendar"><b data-kit-text="title"></b></div>`)
	if !strings.Contains(out, `/kit/components/calendar@`) {
		t.Fatalf("calendar was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"calendar"}), `component("calendar@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// September 2026 begins on a Tuesday. With weeks starting Monday the grid holds 31 August to
// 4 October — five rows — and every step below is a date a person can check on a wall calendar.
func TestBrowserCalendarWalksTheGridByKey(t *testing.T) {
	runBrowserProof(t, "calendar", []string{"calendar"}, `
<div id="cal" data-kit-component="calendar" lang="en-GB" data-selected="2026-09-18" data-min="2026-09-03">
  <button id="prev" data-kit-click="previous()">prev</button>
  <h3 id="title" data-kit-text="title"></h3>
  <button id="next" data-kit-click="next()">next</button>
  <div role="grid" aria-label="Days">
    <div role="row"><span role="columnheader" data-kit-for="name of weekdays" data-kit-element="head" data-kit-text="name"></span></div>
    <div role="row" data-kit-for="week of weeks" data-kit-key="week[0].iso" data-kit-element="week">
      <button role="gridcell" data-kit-for="cell of week" data-kit-key="cell.iso"
              data-kit-element="day" data-kit-bind:data-iso="cell.iso"
              data-kit-bind:tabindex="cell.iso === focused ? 0 : -1"
              data-kit-bind:aria-selected="cell.iso === selected ? 'true' : 'false'"
              data-kit-bind:aria-label="cell.label" data-kit-bind:disabled="cell.disabled"
              data-kit-click="select(cell.iso)" data-kit-text="cell.day"></button>
    </div>
  </div>
  <span id="chosen" data-kit-text="selected ? formatted : 'No day chosen'"></span>
</div>
<script>
(async function () {
  await wait(100);
  var text = function (id) { return document.getElementById(id).textContent; };
  var cell = function (iso) { return document.querySelector('[data-iso="' + iso + '"]'); };
  var focusedIso = function () { return document.activeElement ? document.activeElement.getAttribute("data-iso") : null; };
  var weeks = function () { return document.querySelectorAll('[data-kit-element="week"]').length; };

  // The dates: title and names in the page's language (the comma after the weekday depends on the
  // browser's ICU version, so it is optional here), real rows, Monday first, the seeded day chosen.
  assert(text("title") === "September 2026", "the title is the month in en-GB, got " + text("title"));
  var heads = [].slice.call(document.querySelectorAll('[data-kit-element="head"]')).map(function (h) { return h.textContent; }).join(",");
  assert(heads === "Mon,Tue,Wed,Thu,Fri,Sat,Sun", "weeks start on Monday, got " + heads);
  assert(weeks() === 5 && cell("2026-08-31") && cell("2026-10-04"), "five real rows from 31 August to 4 October, got " + weeks());
  assert(cell("2026-09-18").getAttribute("aria-selected") === "true" && /^Friday,? 18 September 2026$/.test(text("chosen")), "the seeded day is chosen, the page says " + text("chosen"));
  assert(cell("2026-09-02").disabled && !cell("2026-09-03").disabled, "days before data-min are disabled");

  // One tab stop, on the chosen day; the keys walk from there.
  assert(cell("2026-09-18").getAttribute("tabindex") === "0" && cell("2026-09-17").getAttribute("tabindex") === "-1", "one tab stop, on the chosen day");
  focusIn(cell("2026-09-18"));
  key(cell("2026-09-18"), "ArrowRight"); await wait(60);
  assert(focusedIso() === "2026-09-19", "ArrowRight is the next day, focus is on " + focusedIso());
  key(document.activeElement, "ArrowDown"); await wait(60);
  assert(focusedIso() === "2026-09-26", "ArrowDown is a week later, focus is on " + focusedIso());
  key(document.activeElement, "Home"); await wait(60);
  assert(focusedIso() === "2026-09-21", "Home is the Monday of that week, focus is on " + focusedIso());
  key(document.activeElement, "End"); await wait(60);
  assert(focusedIso() === "2026-09-27", "End is the Sunday, focus is on " + focusedIso());

  // Walking off the page turns the month.
  key(document.activeElement, "ArrowDown"); await wait(60);
  key(document.activeElement, "ArrowDown"); await wait(80);
  assert(focusedIso() === "2026-10-11" && text("title") === "October 2026", "two weeks on crosses into October, focus on " + focusedIso() + ", title " + text("title"));
  key(document.activeElement, "PageUp"); await wait(80);
  assert(focusedIso() === "2026-09-11" && text("title") === "September 2026", "PageUp is the same day a month earlier, focus on " + focusedIso() + " (active=" + (document.activeElement && document.activeElement.tagName) + " cell=" + !!cell("2026-09-11") + " cellTab=" + (cell("2026-09-11") && cell("2026-09-11").getAttribute("tabindex")) + " connected=" + (cell("2026-09-11") && cell("2026-09-11").isConnected) + " title=" + text("title") + ")");

  // The keys stop at data-min rather than walking into what cannot be chosen.
  key(document.activeElement, "ArrowUp"); await wait(60);
  key(document.activeElement, "ArrowUp"); await wait(60);
  assert(focusedIso() === "2026-09-03", "the walk stops at data-min, focus is on " + focusedIso());

  // Enter chooses the focused day.
  key(document.activeElement, "Enter"); await wait(60);
  assert(cell("2026-09-03").getAttribute("aria-selected") === "true" && /^Thursday,? 3 September 2026$/.test(text("chosen")), "Enter chooses the focused day, the page says " + text("chosen"));

  // The month buttons are the grammar's; the grid redraws for the new month.
  document.getElementById("next").click(); await wait(60);
  assert(text("title") === "October 2026" && cell("2026-10-31"), "Next month shows October");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
