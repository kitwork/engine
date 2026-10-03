package js

import (
	"strings"
	"testing"
)

// The combobox the KitJS catalogue shipped had next() and previous() and a markup that never bound
// an arrow key to them — measured before the port: zero key handling. It could not be used from a
// keyboard. This one is the keyboard half, and the proof drives it only through keys.

func TestComboboxIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<div data-kit-component="combobox"><input role="combobox"></div>`)
	if !strings.Contains(out, `/kit/components/combobox@`) {
		t.Fatalf("combobox was not injected for markup that uses it:\n%s", out)
	}
	if !strings.Contains(ModulesJS([]string{"combobox"}), `component("combobox@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
	// CONTROL: only what is used.
	if strings.Contains(deliver(`<dialog data-kit-component="dialog"></dialog>`), `/kit/components/combobox@`) {
		t.Error("combobox shipped to a page that never mentions it")
	}
}

func TestBrowserComboboxKeepsFocusInTheFieldWhileTheKeysWalk(t *testing.T) {
	runBrowserProof(t, "combobox", []string{"combobox"}, `
<div data-kit-scope="options: ['Apple', 'Apricot', 'Banana', 'Grape', 'Kiwi'], query: '', selected: ''">
  <div id="box" data-kit-component="combobox" data-kit-click:outside="hide()">
    <input id="field" role="combobox" aria-controls="list" aria-autocomplete="list"
           data-kit-model="query" data-kit-focusin="show()"
           data-kit-bind:aria-expanded="open ? 'true' : 'false'"
           data-kit-bind:aria-activedescendant="active >= 0 ? 'fruit-' + active : null">
    <ul id="list" role="listbox" data-kit-show="open" hidden>
      <li data-kit-for="option, i of options.filter((o) => o.toLowerCase().includes(query.trim().toLowerCase()))"
          role="option" data-kit-element="option"
          data-kit-bind:id="'fruit-' + i"
          data-kit-bind:aria-selected="i === active ? 'true' : 'false'"
          data-kit-click="selected = option; query = option; hide()"
          data-kit-text="option"></li>
    </ul>
    <output id="chosen" data-kit-text="selected"></output>
  </div>
</div>
<button id="elsewhere">elsewhere</button>
<script>
(async function () {
  await wait(80);
  var field = document.getElementById("field"), list = document.getElementById("list");
  function rows() { return [].slice.call(list.querySelectorAll('[role="option"]')); }
  function marked() { return field.getAttribute("aria-activedescendant"); }
  function type(text) { field.value = text; field.dispatchEvent(new Event("input", { bubbles: true })); }

  // Filtering is the grammar: typing "ap" leaves the three names that contain it.
  focusIn(field); type("ap"); await wait(60);
  assert(!list.hidden, "typing opens the list");
  assert(rows().map(function (r) { return r.textContent; }).join(",") === "Apple,Apricot,Grape", "the filter expression should leave Apple,Apricot,Grape, got " + rows().map(function (r) { return r.textContent; }).join(","));
  assert(!field.hasAttribute("aria-activedescendant"), "nothing is marked until a key asks, got " + marked());

  // ↓ marks the first option — and focus stays in the field, which is the whole pattern.
  key(field, "ArrowDown"); await wait(40);
  assert(marked() === "fruit-0", "ArrowDown marks the first option, got " + marked());
  assert(rows()[0].getAttribute("aria-selected") === "true", "the marked option says so");
  assert(active() === "field", "focus must stay in the text field, it is on " + active());

  key(field, "ArrowDown"); key(field, "ArrowDown"); await wait(40);
  assert(marked() === "fruit-2", "two more steps reach the third, got " + marked());
  key(field, "ArrowDown"); await wait(40);
  assert(marked() === "fruit-0", "the arrows wrap, got " + marked());
  key(field, "ArrowUp"); await wait(40);
  assert(marked() === "fruit-2", "ArrowUp wraps back to the last, got " + marked());

  // Enter chooses the MARKED option through that option's own click.
  var enter = key(field, "Enter"); await wait(60);
  assert(enter.defaultPrevented, "Enter on a marked option is consumed");
  assert(document.getElementById("chosen").textContent === "Grape", "Enter should choose Grape, chose " + document.getElementById("chosen").textContent);
  assert(field.value === "Grape" && list.hidden, "the field takes the word and the list closes");
  assert(active() === "field", "focus is still in the field after choosing, it is on " + active());

  // Typing clears the mark: the rows under it have changed.
  type("an"); await wait(40); key(field, "ArrowDown"); await wait(40);
  assert(marked() === "fruit-0", "after new typing the first ArrowDown marks the first again, got " + marked());
  type("ban"); await wait(40);
  assert(!field.hasAttribute("aria-activedescendant"), "typing clears the mark, got " + marked());

  // Escape closes. Enter with nothing marked is left alone for the form.
  var esc = key(field, "Escape"); await wait(40);
  assert(esc.defaultPrevented && list.hidden, "Escape closes an open list");
  var bare = key(field, "Enter"); await wait(20);
  assert(!bare.defaultPrevented, "Enter with nothing marked must reach the form untouched");

  // CONTROL: the grammar's own half still works without a key — an outside click closes it.
  focusIn(field); type("k"); await wait(40);
  assert(!list.hidden, "precondition: open");
  document.getElementById("elsewhere").click(); await wait(40);
  assert(list.hidden, "an outside click closes the list through data-kit-click:outside");

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
