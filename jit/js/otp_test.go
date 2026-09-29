package js

import (
	"strings"
	"testing"
)

func TestOtpIsShippedWhenUsed(t *testing.T) {
	out := Render(`<div data-kit-component="otp"><input data-kit-element="slot"></div>`)
	if !strings.Contains(out, `components=component%3Aotp`) {
		t.Fatalf("otp was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"otp"}), `component("otp@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// Six inputs are six places to type; the component is what makes them one field. Everything here is
// the caret moving between boxes, which only a real browser can show.
func TestBrowserOtpWalksTheCaretAcrossTheBoxes(t *testing.T) {
	runBrowserProof(t, "otp", []string{"otp"}, `
<div data-kit-component="otp" data-kit-alias="$code">
  <input id="d1" data-kit-element="slot" inputmode="numeric" aria-label="Digit 1">
  <input id="d2" data-kit-element="slot" inputmode="numeric" aria-label="Digit 2">
  <input id="d3" data-kit-element="slot" inputmode="numeric" aria-label="Digit 3">
  <input id="d4" data-kit-element="slot" inputmode="numeric" aria-label="Digit 4">
  <output id="out" data-kit-text="complete ? 'code ' + value : 'digits: ' + value"></output>
  <button id="clear" data-kit-click="clear()">Clear</button>
</div>
<script>
(async function () {
  await wait(80);
  var box = function (n) { return document.getElementById("d" + n); };
  var out = document.getElementById("out");
  function typeInto(el, text) { el.value = text; el.dispatchEvent(new Event("input", { bubbles: true })); }

  // A digit moves the caret on; a letter is dropped rather than half-accepted.
  focusIn(box(1)); typeInto(box(1), "7"); await wait(40);
  assert(active() === "d2", "a digit moves the caret to the next box, it is on " + active());
  typeInto(box(2), "x"); await wait(40);
  assert(box(2).value === "" && active() === "d2", "a non-digit is dropped and the caret stays");
  assert(out.textContent === "digits: 7", "value is the digits joined, got " + out.textContent);

  // Backspace in an EMPTY box steps back and clears the one before.
  var back = key(box(2), "Backspace"); await wait(40);
  assert(back.defaultPrevented && active() === "d1" && box(1).value === "", "Backspace in an empty box steps back and clears it; caret on " + active() + ", d1=" + box(1).value);

  // A pasted code fills every box from where it landed, and only its digits count.
  var data = new DataTransfer(); data.setData("text", "12-34");
  box(1).dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true })); await wait(60);
  assert([1, 2, 3, 4].map(function (n) { return box(n).value; }).join("") === "1234", "paste fills the boxes with the digits only");
  assert(out.textContent === "code 1234", "complete once every box holds a digit, got " + out.textContent);
  assert(active() === "d4", "the caret lands on the last box filled, it is on " + active());

  // The arrows walk.
  key(box(4), "ArrowLeft"); await wait(20);
  assert(active() === "d3", "ArrowLeft walks back, caret on " + active());
  key(box(3), "ArrowRight"); await wait(20);
  assert(active() === "d4", "ArrowRight walks on, caret on " + active());

  // clear() empties every box and returns to the first.
  document.getElementById("clear").click(); await wait(40);
  assert([1, 2, 3, 4].every(function (n) { return box(n).value === ""; }) && out.textContent === "digits: " && active() === "d1", "clear empties the boxes and starts over, caret on " + active());

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
