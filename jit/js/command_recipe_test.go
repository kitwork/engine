package js

import "testing"

// The KitJS catalogue had a `command` component — 263 lines of palette. Looked at closely it is
// three things the kernel already has, plus grammar:
//
//   · opening on ⌘K / Ctrl+K     — the grammar's key combination: data-kit-keydown:mod+k:window
//   · a modal that traps focus   — a native <dialog>, driven by the dialog component
//   · a field over a list, arrows marking rows, Enter running one, focus kept in the field
//                                — the combobox component
//   · which rows match           — the grammar: each authored row hides with data-kit-show
//
// So it is a RECIPE, not a component: markup over components the page already ships. This test is
// the evidence. If it ever needs code of its own to pass, that code is the component it would need.
func TestBrowserCommandPaletteIsARecipeOverDialogAndCombobox(t *testing.T) {
	row := func(id, label, keywords, extra string) string {
		return `<button type="button" id="` + id + `" role="option" data-kit-element="option" ` + extra +
			` data-kit-show="!query || '` + keywords + `'.includes(query.trim().toLowerCase())"` +
			` data-kit-bind:aria-selected="activeId === '` + id + `' ? 'true' : 'false'"` +
			` data-kit-click="ran = '` + label + `'; $palette.close()">` + label + `</button>`
	}
	runBrowserProof(t, "command palette", []string{"dialog", "combobox"}, `
<div data-kit-scope="query: '', ran: ''" data-kit-keydown:mod+k:window="$palette.show()">
  <button id="trigger" data-kit-click="$palette.show()">Search commands…</button>
  <dialog id="palette" data-kit-component="dialog" data-kit-alias="$palette" aria-label="Commands">
    <div data-kit-component="combobox">
      <input id="field" role="combobox" autofocus aria-controls="list" aria-autocomplete="list"
             data-kit-model="query" data-kit-bind:aria-activedescendant="activeId || null">
      <div id="list" role="listbox" aria-label="Commands">
        `+row("cmd-docs", "docs", "docs documentation guide", "")+`
        `+row("cmd-components", "components", "components catalogue ui", "")+`
        `+row("cmd-deploy", "deploy", "deploy ship release", "disabled")+`
        `+row("cmd-design", "design", "design tokens colours", "")+`
      </div>
    </div>
  </dialog>
  <output id="ran" data-kit-text="ran"></output>
</div>
<script>
(async function () {
  await wait(80);
  var palette = document.getElementById("palette"), field = document.getElementById("field");
  var marked = function () { return field.getAttribute("aria-activedescendant") || "(none)"; };
  var visible = function () { return [].slice.call(document.querySelectorAll('[role=option]')).filter(function (o) { return !o.hidden; }).map(function (o) { return o.id.slice(4); }).join(","); };
  function type(text) { field.value = text; field.dispatchEvent(new Event("input", { bubbles: true })); }

  // Ctrl+K anywhere opens it — the grammar's combination, not a listener of the palette's own.
  document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "k", ctrlKey: true, bubbles: true, cancelable: true }));
  await wait(60);
  assert(palette.open, "Ctrl+K opens the palette through data-kit-keydown:mod+k:window");
  assert(document.activeElement === field, "the native dialog puts focus in the field, it is on " + active());

  // The arrows mark rows and step over the disabled one; focus never leaves the field.
  key(field, "ArrowDown"); await wait(30);
  assert(marked() === "cmd-docs", "ArrowDown marks the first row, got " + marked());
  key(field, "ArrowDown"); key(field, "ArrowDown"); await wait(30);
  assert(marked() === "cmd-design", "two more steps skip the disabled deploy, got " + marked());
  assert(document.getElementById("cmd-design").getAttribute("aria-selected") === "true", "the marked row says so");
  assert(document.activeElement === field, "focus stays in the field");

  // Typing filters by hiding rows — the grammar's includes — and the arrows only see what is left.
  type("comp"); await wait(40);
  assert(visible() === "components", "typing 'comp' leaves only components, got " + visible());
  assert(marked() === "(none)", "typing clears the mark");
  key(field, "ArrowDown"); await wait(30);
  assert(marked() === "cmd-components", "the arrows see only the visible rows, got " + marked());

  // Enter runs the marked row — its own expression — which also closes the dialog.
  var enter = key(field, "Enter"); await wait(60);
  assert(enter.defaultPrevented && document.getElementById("ran").textContent === "components", "Enter runs the marked command, ran " + document.getElementById("ran").textContent);
  assert(!palette.open, "running a command closes the palette");

  // Nothing matching means nothing to mark.
  document.getElementById("trigger").click(); await wait(60);
  type("zzz"); await wait(40);
  key(field, "ArrowDown"); await wait(30);
  assert(visible() === "" && marked() === "(none)", "no rows, no mark; got rows=" + visible() + " mark=" + marked());

  // Escape closes the list first; the NEXT one is left to the platform, which closes the dialog.
  type("d"); await wait(40);
  var first = key(field, "Escape"); await wait(30);
  var second = key(field, "Escape"); await wait(30);
  assert(first.defaultPrevented && !second.defaultPrevented, "the first Escape closes the list, the second is left for the dialog to close");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
