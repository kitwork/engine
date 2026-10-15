package js

import (
	"strings"
	"testing"
)

func TestKanbanIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<section data-kit-component="kanban"><div data-kit-element="column" data-column="a"></div></section>`)
	if !strings.Contains(out, `/_kitwork/components/kanban.`) {
		t.Fatalf("kanban was not injected for markup that uses it:\n%s", out)
	}
	if src := ModulesJS([]string{"kanban"}); !strings.Contains(src, `component("kanban", `) || strings.Contains(src, `component("kanban@`) {
		t.Error("the file must register the bare name, and only it")
	}
}

// Moving is the component; which cards sit where is the grammar. The proof moves cards by key and
// by pointer, reads the board the page draws, and listens to what a screen reader would be told.
func TestBrowserKanbanMovesCardsAndSaysWhere(t *testing.T) {
	column := func(name, label string) string {
		return `<div data-kit-element="column" data-column="` + name + `" aria-label="` + label + `" id="col-` + name + `"
      style="flex:1;min-height:200px" data-kit-class="over === '` + name + `' ? 'is-over' : ''">
    <article data-kit-for="card of cards.filter((c) => c.column === '` + name + `')" data-kit-key="card.id"
             data-kit-element="card" data-kit-bind:data-card="card.id" data-kit-bind:id="'card-' + card.id" tabindex="0"
             data-kit-text="card.title" style="height:30px"></article>
    <b data-kit-text="cards.filter((c) => c.column === '` + name + `').length" id="count-` + name + `"></b>
  </div>`
	}
	runBrowserProof(t, "kanban", []string{"kanban"}, `
<div data-kit-scope="cards: [{ id: 1, column: 'todo', title: 'Write' }, { id: 2, column: 'todo', title: 'Pick' }, { id: 3, column: 'doing', title: 'Ship' }]">
  <section data-kit-component="kanban" style="display:flex;gap:10px;width:900px">
    `+column("todo", "To do")+column("doing", "Doing")+column("done", "Done")+`
    <p id="said" aria-live="polite" data-kit-text="announcement"></p>
  </section>
  <button id="add" data-kit-click="cards = cards.concat([{ id: Math.max.apply(null, cards.map((c) => c.id)) + 1, column: 'todo', title: 'New' }])">add</button>
</div>
<script>
(async function () {
  await wait(100);
  var titles = function (col) { return [].slice.call(document.querySelectorAll("#col-" + col + " article")).map(function (a) { return a.textContent; }).join(","); };
  var said = function () { return document.getElementById("said").textContent; };

  assert(titles("todo") === "Write,Pick" && titles("doing") === "Ship" && document.getElementById("count-todo").textContent === "2", "the grammar sorts the cards into columns and counts them");

  // → on a focused card moves it to the next column, focus follows it, and the move is said.
  var write = document.getElementById("card-1");
  focusIn(write);
  key(write, "ArrowRight"); await wait(80);
  assert(titles("todo") === "Pick" && titles("doing") === "Ship,Write", "ArrowRight moves Write into Doing, got todo=" + titles("todo") + " doing=" + titles("doing"));
  assert(active() === "card-1", "focus stays on the card it moved, it is on " + active());
  assert(said() === "Write moved to Doing", "the move is announced, got " + said());

  // ↑ swaps it with its neighbour in the same column.
  key(document.activeElement, "ArrowUp"); await wait(80);
  assert(titles("doing") === "Write,Ship" && active() === "card-1", "ArrowUp swaps Write above Ship, got " + titles("doing"));
  assert(said() === "Write moved up in Doing", "the reorder is announced, got " + said());

  // ← at the first column goes nowhere.
  var pick = document.getElementById("card-2");
  focusIn(pick); key(pick, "ArrowLeft"); await wait(40);
  assert(titles("todo") === "Pick", "there is no column before the first");

  // By pointer: press a card, move over Done — the over state lights it — release there.
  var ship = document.getElementById("card-3");
  var done = document.getElementById("col-done").getBoundingClientRect();
  var x = done.left + done.width / 2, y = done.top + 100;
  ship.dispatchEvent(new PointerEvent("pointerdown", { pointerId: 5, button: 0, bubbles: true }));
  document.dispatchEvent(new PointerEvent("pointermove", { pointerId: 5, clientX: x, clientY: y, bubbles: true })); await wait(40);
  assert(document.getElementById("col-done").className.indexOf("is-over") >= 0, "the column under the pointer is lit through the over state");
  document.dispatchEvent(new PointerEvent("pointerup", { pointerId: 5, clientX: x, clientY: y, bubbles: true })); await wait(80);
  assert(titles("done") === "Ship" && said() === "Ship moved to Done", "releasing over Done moves Ship there, got done=" + titles("done") + " said=" + said());
  assert(document.getElementById("col-done").className.indexOf("is-over") < 0, "the light goes out on release");

  // Adding is the grammar's own concat — the component is not involved.
  document.getElementById("add").click(); await wait(60);
  assert(titles("todo") === "Pick,New", "a new card lands in To do, got " + titles("todo"));

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
