package js

import "testing"

// A two-way field (value: query in its data-kitwork-bind; data-kit-model until 26/10/2026) AND its
// own input handler — a search box that also resets the page, input: page = 1 — lost what was typed.
// Both listen for `input` on the document; the handler ran first, its render synced every two-way
// field FROM the scope, the scope still held the old value, so the field was written back to it; then
// the field's listener read the now-empty field. Found on 29/09 by the data-table proof: typing
// "paused" left the table unfiltered and the box empty.
//
// (The same render-overwrites-the-field happened to a debounced field, data-kit-debounce, whenever
// anything else painted while its timer was pending; that directive went on 26/10/2026.)
//
// A real browser, because the bug IS the order two document listeners run in — the node shim has
// no bubbling at all, and a test there went red for that reason instead of this one.
func TestBrowserModelKeepsWhatWasTyped(t *testing.T) {
	runBrowserProof(t, "model order", nil, `
<div data-kitwork-scope="query: ''; page: 3">
  <input id="field" data-kitwork-bind="value: query" data-kitwork-event="input: page = 1">
  <input id="other" data-kitwork-bind="value: query">
  <b id="shown" data-kitwork-bind="text: query + '/' + page"></b>
  <button id="set" data-kitwork-event="click: query = 'from code'">set</button>
</div>
<script>
(async function () {
  await wait(80);
  var field = document.getElementById("field"), other = document.getElementById("other"), shown = document.getElementById("shown");
  function type(el, text) { el.value = text; el.dispatchEvent(new Event("input", { bubbles: true })); }

  type(field, "paused"); await wait(40);
  assert(field.value === "paused", "the field lost what was typed: it holds " + JSON.stringify(field.value));
  assert(shown.textContent === "paused/1", "the field writes the value AND the handler still runs, the page shows " + shown.textContent);
  assert(other.value === "paused", "a second field on the same name follows what was typed, it holds " + JSON.stringify(other.value));

  // CONTROL: a field nobody is typing in still follows the scope — the sync is not switched off.
  document.getElementById("set").click(); await wait(40);
  assert(field.value === "from code" && other.value === "from code", "fields follow a write from code, they hold " + field.value + " / " + other.value);

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
