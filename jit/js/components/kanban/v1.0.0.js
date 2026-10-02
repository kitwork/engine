/* kanban component @v1.0.0 — cards moved between columns by pointer or by key.
 *
 * Usage — the page's scope holds the cards and the grammar sorts them into columns; the
 * component moves them:
 *
 *   <div data-kit-scope="cards: [{ id: 1, column: 'todo', title: 'Write the post' }, …]">
 *     <section data-kit-component="kanban">
 *       <div data-kit-element="column" data-column="todo" aria-label="To do"
 *            data-kit-class="over === 'todo' ? 'bg-brand-wash' : ''">
 *         <article data-kit-for="card of cards.filter((c) => c.column === 'todo')" data-kit-key="card.id"
 *                  data-kit-element="card" data-kit-bind:data-card="card.id" tabindex="0"
 *                  data-kit-class="dragging === card.id ? 'opacity-40' : ''"
 *                  data-kit-text="card.title"></article>
 *       </div>
 *       …one column per state…
 *       <p aria-live="polite" class="sr-only" data-kit-text="announcement"></p>
 *     </section>
 *   </div>
 *
 * The cards are the page's: an array of objects with an `id` and a `column`, in the page's scope.
 * The columns are the page's: elements with data-column, in the order they appear. Which cards
 * sit in a column, how many there are and adding one are all the grammar.
 *
 * WHY THIS IS A COMPONENT. MOVING a card is what markup cannot do:
 *
 *   · by pointer — a press on a card, the column under the pointer found as it moves (`over`,
 *     for the markup to light), the card moved on release;
 *   · by key, on a focused card — ← → to the previous / next column, ↑ ↓ to swap with its
 *     neighbour in the same column — and focus stays on the card it moved, wherever it lands;
 *   · and saying so — `announcement` is a sentence for an aria-live region ("Write the post
 *     moved to Doing"), because someone who cannot see the board has to be told where a card went.
 *     The KitJS kanban moved cards silently.
 *
 * The public surface: dragging, over, announcement, move(id, column).
 */
var KANBAN = Symbol("kit:kanban");

function kanbanColumns(scope) {
  var own = scope[KANBAN];
  if (!own) return [];
  return own.context.elements("column").map(function (el) { return el.getAttribute("data-column"); });
}

function columnLabel(scope, name) {
  var own = scope[KANBAN];
  var el = own ? own.context.elements("column").filter(function (c) { return c.getAttribute("data-column") === name; })[0] : null;
  return el ? el.getAttribute("aria-label") || name : name;
}

function cardById(scope, id) {
  return (scope.cards || []).filter(function (card) { return String(card.id) === String(id); })[0] || null;
}

// Focus the card again once the paint has moved its element — the old one may be gone.
function refocus(scope, id) {
  var own = scope[KANBAN];
  if (!own) return;
  own.context.afterRender(function () {
    var el = own.context.elements("card").filter(function (c) { return c.getAttribute("data-card") === String(id); })[0];
    if (el && typeof el.focus === "function") el.focus();
  });
}

var kanbanDef = {
  dragging: null,
  over: "",
  announcement: "",

  // Move a card to a column, to the end of it. The array is replaced, never edited in place, so
  // every binding that reads it repaints.
  move: function (id, column) {
    var card = cardById(this, id);
    if (!card || kanbanColumns(this).indexOf(column) < 0 || card.column === column) return false;
    var moved = Object.assign({}, card, { column: column });
    this.cards = (this.cards || []).filter(function (c) { return c !== card; }).concat([moved]);
    this.announcement = card.title + " moved to " + columnLabel(this, column);
    return true;
  },

  init: function (context) {
    var scope = this;
    var document = context.host.ownerDocument;
    scope[KANBAN] = { context: context, pointer: null };
    context.require("column", "data-column");
    context.cleanup(function () { delete scope[KANBAN]; });

    function cardOf(target) {
      var el = target && target.closest ? target.closest('[data-kit-element="card"]') : null;
      return el && context.host.contains(el) ? el : null;
    }
    function columnAt(x, y) {
      var el = document.elementFromPoint(x, y);
      el = el && el.closest ? el.closest('[data-kit-element="column"]') : null;
      return el && context.host.contains(el) ? el.getAttribute("data-column") : "";
    }

    context.listen(context.host, "pointerdown", function (event) {
      var card = cardOf(event.target);
      if (!card || event.button !== 0) return;
      scope[KANBAN].pointer = event.pointerId;
      scope.dragging = Number(card.getAttribute("data-card")) || card.getAttribute("data-card");
    });
    context.listen(document, "pointermove", function (event) {
      if (scope.dragging == null || event.pointerId !== scope[KANBAN].pointer) return;
      var column = columnAt(event.clientX, event.clientY);
      if (scope.over !== column) scope.over = column;
    });
    var finish = function (event) {
      if (scope.dragging == null || event.pointerId !== scope[KANBAN].pointer) return;
      var id = scope.dragging, column = event.type === "pointerup" ? columnAt(event.clientX, event.clientY) : "";
      scope.dragging = null;
      scope.over = "";
      scope[KANBAN].pointer = null;
      if (column) scope.move(id, column);
    };
    context.listen(document, "pointerup", finish);
    context.listen(document, "pointercancel", finish);

    context.listen(context.host, "keydown", function (event) {
      if (event.altKey || event.ctrlKey || event.metaKey) return;
      var el = cardOf(event.target);
      if (!el || el !== event.target) return;
      var id = el.getAttribute("data-card"), card = cardById(scope, id);
      if (!card) return;
      var columns = kanbanColumns(scope), at = columns.indexOf(card.column);

      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        var to = columns[at + (event.key === "ArrowLeft" ? -1 : 1)];
        if (to && scope.move(card.id, to)) refocus(scope, card.id);
        return;
      }
      if (event.key === "ArrowUp" || event.key === "ArrowDown") {
        event.preventDefault();
        // Swap with the nearest card of the same column in that direction.
        var list = (scope.cards || []).slice();
        var from = list.indexOf(card), step = event.key === "ArrowUp" ? -1 : 1;
        for (var i = from + step; i >= 0 && i < list.length; i += step) {
          if (list[i].column !== card.column) continue;
          list[from] = list[i];
          list[i] = card;
          scope.cards = list;
          scope.announcement = card.title + " moved " + (step < 0 ? "up" : "down") + " in " + columnLabel(scope, card.column);
          refocus(scope, card.id);
          return;
        }
      }
    });
  }
};

window.kit.component("kanban", kanbanDef);
window.kit.component("kanban@v1.0.0", kanbanDef);
