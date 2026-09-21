package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The kanban browser proof: columns draw their cards from state; a pointer
// drag past the threshold lifts a ghost, lights the column under the pointer
// and drops the card there (before the card it lands on top of); a short
// press is not a drag; the arrow keys move a focused card between and within
// columns and keep focus on it; add / move / remove answer a directive.
func TestBrowserKanbanMovesCardsByPointerAndKeyboard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping kanban component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "kanban", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kanban.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/kanban.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(kanbanComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/kanban.html")
}

var kanbanComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS kanban component</title><script>
window.__errs = [];
window.addEventListener("error", function (e) { window.__errs.push(String(e.error && e.error.message || e.message)); });
var __ce = console.error; console.error = function () { window.__errs.push(Array.prototype.map.call(arguments, String).join(" ")); return __ce.apply(this, arguments); };
</script><style>
  #board { display: flex; gap: 10px; width: 640px; }
  [data-kanban-column] { width: 200px; min-height: 240px; padding: 10px; background: #eee; }
  [data-kanban-card] { height: 40px; margin-bottom: 8px; background: #fff; }
</style></head><body>
  <section id="board" data-kit-component="kanban@1.0.0" data-kit-scope="cards: [{ id: 1, column: 'todo', title: 'Write' }, { id: 2, column: 'todo', title: 'Review' }, { id: 3, column: 'done', title: 'Ship' }]">
    <div id="todo" data-kanban-column="todo" data-kit-bind:data-count="count('todo')">
      <template data-kit-for="card of cardsIn('todo')" data-kit-key="card.id"><article data-kanban-card tabindex="0" data-kit-bind:data-kanban-card="card.id" data-kit-bind:data-lifted="isDragging(card.id)" data-kit-text="card.title"></article></template>
    </div>
    <div id="doing" data-kanban-column="doing" data-kit-bind:data-count="count('doing')">
      <template data-kit-for="card of cardsIn('doing')" data-kit-key="card.id"><article data-kanban-card tabindex="0" data-kit-bind:data-kanban-card="card.id" data-kit-text="card.title"></article></template>
    </div>
    <div id="done" data-kanban-column="done" data-kit-bind:data-count="count('done')">
      <template data-kit-for="card of cardsIn('done')" data-kit-key="card.id"><article data-kanban-card tabindex="0" data-kit-bind:data-kanban-card="card.id" data-kit-text="card.title"></article></template>
    </div>
    <button id="add" type="button" data-kit-click="add('doing', 'Test')">add</button>
    <button id="ship" type="button" data-kit-click="move(1, 'done')">ship</button>
    <button id="drop" type="button" data-kit-click="remove(3)">drop</button>
    <output id="flags" data-kit-text="dragging + '|' + over"></output>
  </section>
  <script src="/kanban.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  // "<todo cards>/<doing cards>/<done cards>|dragging|over", read off the DOM and the flags.
  function state() {
    var columns = ["todo", "doing", "done"].map(function (name) {
      return Array.prototype.map.call(document.querySelectorAll("#" + name + " [data-kanban-card]"), function (c) { return c.getAttribute("data-kanban-card"); }).join(",");
    }).join("/");
    return columns + "|" + document.getElementById("flags").textContent.trim();
  }
  function card(id) { return document.querySelector('[data-kanban-card="' + id + '"]'); }
  function center(el) { var r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; }
  function pointer(type, target, x, y) {
    target.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, pointerId: 3, button: 0, clientX: x, clientY: y }));
  }

  await waitFor(function () { return state() === "1,2//3||" && document.getElementById("todo").getAttribute("data-count") === "2"; }, "the board did not draw from state: " + state() + " " + window.__errs.join(";"));

  // A short press is not a drag.
  var start = center(card(1));
  pointer("pointerdown", card(1), start.x, start.y);
  pointer("pointermove", document, start.x + 1, start.y + 1);
  pointer("pointerup", document, start.x + 1, start.y + 1);
  await new Promise(function (resolve) { setTimeout(resolve, 40); });
  assert(state() === "1,2//3||" && !document.querySelector("[data-kanban-ghost]"), "a short press moved a card or left a ghost: " + state());

  // A real drag: down on card 1, past the threshold, over the done column, onto the top half of card 3, up.
  var target = center(card(3));
  pointer("pointerdown", card(1), start.x, start.y);
  pointer("pointermove", document, start.x + 20, start.y + 5);
  // A boolean bound to a data-* attribute is a bare attribute, present or absent.
  await waitFor(function () { return !!document.querySelector("[data-kanban-ghost]") && card(1).hasAttribute("data-lifted") && state().indexOf("|1|") > 0; }, "the drag did not lift a ghost");
  pointer("pointermove", document, target.x, target.y - 15);
  await waitFor(function () { return document.getElementById("done").getAttribute("data-state") === "over" && state().slice(-5) === "|done"; }, "the column under the pointer was not lit: " + state() + " " + document.getElementById("done").getAttribute("data-state"));
  pointer("pointerup", document, target.x, target.y - 15);
  await waitFor(function () { return state() === "2//1,3||" && !document.querySelector("[data-kanban-ghost]"); }, "the drop did not move the card before the one it landed on: " + state());
  assert(document.getElementById("done").getAttribute("data-state") === "idle" && document.getElementById("done").getAttribute("data-count") === "2", "the column did not settle after the drop");
  var order = Array.prototype.map.call(document.querySelectorAll("#done [data-kanban-card]"), function (c) { return c.getAttribute("data-kanban-card"); });
  assert(order.join(",") === "1,3", "the dropped card is not first in the column: " + order.join(","));

  // A drop outside every column puts the card back.
  var again = center(card(2));
  pointer("pointerdown", card(2), again.x, again.y);
  pointer("pointermove", document, again.x + 30, again.y);
  pointer("pointermove", document, 5, 5);
  pointer("pointerup", document, 5, 5);
  await waitFor(function () { return state() === "2//1,3||"; }, "a drop outside the columns changed something: " + state());

  // Keyboard: Right moves card 2 to doing, Left back; Down/Up reorder within done.
  card(2).focus();
  card(2).dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true }));
  await waitFor(function () { return state() === "/2/1,3||" && document.activeElement === card(2); }, "ArrowRight did not move the card and keep focus: " + state());
  card(2).dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true, cancelable: true }));
  await waitFor(function () { return state() === "2//1,3||"; }, "ArrowLeft did not move the card back: " + state());
  card(1).focus();
  card(1).dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true }));
  await waitFor(function () { return Array.prototype.map.call(document.querySelectorAll("#done [data-kanban-card]"), function (c) { return c.getAttribute("data-kanban-card"); }).join(",") === "3,1"; }, "ArrowDown did not move the card down: " + state());
  card(1).dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true, cancelable: true }));
  await waitFor(function () { return Array.prototype.map.call(document.querySelectorAll("#done [data-kanban-card]"), function (c) { return c.getAttribute("data-kanban-card"); }).join(",") === "1,3"; }, "ArrowUp did not move the card up: " + state());

  document.getElementById("add").click();
  await waitFor(function () { return state().indexOf("/4/") >= 0 && document.getElementById("doing").getAttribute("data-count") === "1"; }, "add() did not add a card: " + state());
  document.getElementById("ship").click();
  document.getElementById("drop").click();
  await waitFor(function () { return state() === "2/4/1||"; }, "move() and remove() from a directive did not apply: " + state());
});
  </script>
</body></html>`, browserHarness)
