package js

import "testing"

// The date-picker recipe (kitwork.io /components/date-picker): a calendar inside a panel that is
// hidden until the trigger opens it. Opening moves focus onto the chosen day — the WAI-ARIA date
// picker dialog — through calendar.focus(), which waits for the paint that shows the grid: a hidden
// cell cannot take focus. Closing again leaves focus where the person put it.
func TestBrowserDatePickerOpensOntoTheChosenDay(t *testing.T) {
	runBrowserProof(t, "date picker", []string{"calendar"}, `
<div data-kit-scope="open: false">
  <button id="trigger" type="button" data-kit-element="trigger" data-kit-click="open = !open; open && $launch.focus()">pick</button>
  <div role="dialog" data-kit-show="open" hidden>
    <div data-kit-component="calendar" data-kit-alias="$launch" lang="en-GB" data-selected="2026-09-18">
      <div role="grid">
        <div role="row" data-kit-for="week of weeks" data-kit-key="week[0].iso">
          <button type="button" role="gridcell" data-kit-for="cell of week" data-kit-key="cell.iso" data-kit-element="day"
                  data-kit-bind:data-iso="cell.iso" data-kit-bind:tabindex="cell.iso === focused ? 0 : -1"
                  data-kit-click="select(cell.iso)" data-kit-text="cell.day"></button>
        </div>
      </div>
    </div>
  </div>
</div>
<script>
(async function () {
  await wait(150);
  var trigger = document.getElementById("trigger");
  trigger.focus();
  trigger.click();
  await wait(150);
  var focused = document.activeElement;
  assert(focused && focused.getAttribute("data-iso") === "2026-09-18",
    "opening puts focus on the chosen day, it is on " + (focused ? (focused.getAttribute("data-iso") || focused.id || focused.tagName) : "(none)"));
  key(focused, "ArrowRight"); await wait(150);
  assert(document.activeElement.getAttribute("data-iso") === "2026-09-19", "the keys work at once: → reaches the next day");
  trigger.focus();
  trigger.click(); await wait(150);
  assert(document.activeElement === trigger, "closing does not pull focus into the hidden grid");
  root.setAttribute("data-kit-test", "passed");
})();
</script>`)
}
