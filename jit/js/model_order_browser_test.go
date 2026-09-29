package js

import "testing"

// A field with data-kit-model AND its own input handler — a search box that also resets the page,
// data-kit-input="page = 1" — lost what was typed. Both listen for `input` on the document; the
// handler ran first, its render synced every model field FROM the scope, the scope still held the
// old value, so the field was written back to it; then the model listener read the now-empty field.
// Found on 29/09 by the data-table proof: typing "paused" left the table unfiltered and the box
// empty.
//
// The same render-overwrites-the-field happens to a debounced field whenever anything else paints
// while its timer is pending: the field is ahead of the scope on purpose, and must not be dragged
// back to it.
//
// A real browser, because the bug IS the order two document listeners run in — the node shim has
// no bubbling at all, and a test there went red for that reason instead of this one.
func TestBrowserModelKeepsWhatWasTyped(t *testing.T) {
	runBrowserProof(t, "model order", nil, `
<div data-kit-scope="{ query: '', page: 3 }">
  <input id="field" data-kit-model="query" data-kit-input="page = 1">
  <input id="slow" data-kit-model="query" data-kit-debounce="400">
  <b id="shown" data-kit-text="query + '/' + page"></b>
  <button id="bump" data-kit-click="page = page + 1">bump</button>
  <button id="set" data-kit-click="query = 'from code'">set</button>
</div>
<script>
(async function () {
  await wait(80);
  var field = document.getElementById("field"), slow = document.getElementById("slow"), shown = document.getElementById("shown");
  function type(el, text) { el.value = text; el.dispatchEvent(new Event("input", { bubbles: true })); }

  type(field, "paused"); await wait(40);
  assert(field.value === "paused", "the field lost what was typed: it holds " + JSON.stringify(field.value));
  assert(shown.textContent === "paused/1", "the model takes the value AND the handler still runs, the page shows " + shown.textContent);

  // A debounced field is ahead of the scope until its timer fires; a paint in between must not
  // drag it back.
  type(slow, "half-typed"); await wait(20);
  document.getElementById("bump").click(); await wait(20);
  assert(slow.value === "half-typed", "a paint during the debounce wiped the field: it holds " + JSON.stringify(slow.value));
  await wait(500);
  assert(shown.textContent.indexOf("half-typed/") === 0, "once the timer fires the value lands, the page shows " + shown.textContent);

  // CONTROL: a field nobody is typing in still follows the scope — the sync is not switched off.
  document.getElementById("set").click(); await wait(40);
  assert(field.value === "from code" && slow.value === "from code", "fields follow a write from code, they hold " + field.value + " / " + slow.value);

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
