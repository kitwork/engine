;(function () {
"use strict";

// The kanban board: cards in columns, moved by dragging or by a key. The
// cards are state — { id, column, title, … } — and the markup draws each
// column with data-kit-for over cardsIn('todo'); the columns are authored,
// data-kanban-column="todo", and each drawn card carries data-kanban-card
// bound to its id. The component owns what markup cannot: the pointer drag
// (a ghost that follows the pointer, the column under it lit), the drop, and
// the arrow keys that move a focused card between columns.
//
//   <section data-kit-component="kanban" data-kit-scope="cards: [{ id: 1, column: 'todo', title: '…' }]">
//     <div data-kanban-column="todo">
//       <template data-kit-for="card of cardsIn('todo')" data-kit-key="card.id">
//         <article data-kanban-card tabindex="0" data-kit-bind="data-kanban-card: card.id;">…
//
// A drag starts after the pointer has moved a few pixels, so a click on a
// card is still a click.

var INSTANCE = Symbol("kit:kanban");
var THRESHOLD = 4;

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function list(value) {
  return Array.isArray(value) ? value : [];
}

function id(value) {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return "";
}

function columns(data) {
  return data ? data.context.owned("[data-kanban-column]") : [];
}

function columnIDs(data) {
  return columns(data).map(function (column) { return column.getAttribute("data-kanban-column") || ""; });
}

function cardElement(data, target) {
  if (!target || typeof target.closest !== "function") return null;
  var card = target.closest("[data-kanban-card]");
  return card && data.context.host.contains(card) ? card : null;
}

function columnAt(data, x, y) {
  var doc = data.context.host.ownerDocument;
  var hit = doc.elementFromPoint(x, y);
  var column = hit && hit.closest ? hit.closest("[data-kanban-column]") : null;
  return column && data.context.host.contains(column) ? column : null;
}

function find(scope, cardID) {
  var cards = list(scope.cards);
  for (var i = 0; i < cards.length; i++) {
    if (cards[i] && id(cards[i].id) === cardID) return cards[i];
  }
  return null;
}

function move(scope, cardID, column, before) {
  var cards = list(scope.cards).slice();
  var index = -1;
  for (var i = 0; i < cards.length; i++) if (cards[i] && id(cards[i].id) === cardID) index = i;
  if (index < 0 || typeof column !== "string" || column === "") return false;
  var card = cards.splice(index, 1)[0];
  card.column = column;
  var at = cards.length;
  if (before) {
    for (var j = 0; j < cards.length; j++) if (cards[j] && id(cards[j].id) === before) { at = j; break; }
  }
  cards.splice(at, 0, card);
  scope.cards = cards;
  return true;
}

function light(data, column) {
  columns(data).forEach(function (element) {
    element.setAttribute("data-state", element === column ? "over" : "idle");
  });
}

function endDrag(scope, data) {
  if (data.ghost && data.ghost.parentNode) data.ghost.parentNode.removeChild(data.ghost);
  data.ghost = null;
  data.pointer = null;
  data.card = "";
  data.origin = null;
  data.active = false;
  light(data, null);
  if (scope.dragging !== "") scope.dragging = "";
  if (scope.over !== "") scope.over = "";
}

function startGhost(data, element, x, y) {
  var rect = element.getBoundingClientRect();
  var ghost = element.cloneNode(true);
  ghost.removeAttribute("data-kanban-card");
  ghost.removeAttribute("id");
  ghost.setAttribute("aria-hidden", "true");
  ghost.setAttribute("data-kanban-ghost", "");
  ghost.style.position = "fixed";
  ghost.style.left = rect.left + "px";
  ghost.style.top = rect.top + "px";
  ghost.style.width = rect.width + "px";
  ghost.style.pointerEvents = "none";
  ghost.style.zIndex = "1000";
  ghost.style.margin = "0";
  data.offset = { x: x - rect.left, y: y - rect.top };
  data.ghost = ghost;
  element.ownerDocument.body.appendChild(ghost);
}

kit.component("kanban", {
  cards: [],
  dragging: "",
  over: "",

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false, pointer: null, card: "", origin: null, active: false, ghost: null, offset: null };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });

    context.listen(context.host, "pointerdown", function (event) {
      if (event.button !== 0) return;
      var card = cardElement(data, event.target);
      if (!card) return;
      // A control inside the card keeps its own gesture.
      var control = event.target.closest ? event.target.closest("button, a, input, textarea, select") : null;
      if (control && card.contains(control)) return;
      data.pointer = event.pointerId;
      data.card = id(card.getAttribute("data-kanban-card"));
      data.origin = { x: event.clientX, y: event.clientY, element: card };
      data.active = false;
    });

    context.listen(context.host.ownerDocument, "pointermove", function (event) {
      if (data.pointer === null || event.pointerId !== data.pointer || data.disposed) return;
      if (!data.active) {
        if (Math.abs(event.clientX - data.origin.x) < THRESHOLD && Math.abs(event.clientY - data.origin.y) < THRESHOLD) return;
        data.active = true;
        startGhost(data, data.origin.element, data.origin.x, data.origin.y);
        scope.dragging = data.card;
      }
      event.preventDefault();
      if (data.ghost) {
        data.ghost.style.left = (event.clientX - data.offset.x) + "px";
        data.ghost.style.top = (event.clientY - data.offset.y) + "px";
      }
      var column = columnAt(data, event.clientX, event.clientY);
      light(data, column);
      var name = column ? column.getAttribute("data-kanban-column") || "" : "";
      if (scope.over !== name) scope.over = name;
    });

    function release(event) {
      if (data.pointer === null || event.pointerId !== data.pointer) return;
      var wasActive = data.active;
      var cardID = data.card;
      var column = wasActive ? columnAt(data, event.clientX, event.clientY) : null;
      var before = null;
      if (column) {
        var hit = context.host.ownerDocument.elementFromPoint(event.clientX, event.clientY);
        var target = cardElement(data, hit);
        if (target && column.contains(target) && id(target.getAttribute("data-kanban-card")) !== cardID) {
          var rect = target.getBoundingClientRect();
          before = event.clientY < rect.top + rect.height / 2 ? id(target.getAttribute("data-kanban-card")) : null;
          if (!before) {
            var next = target.nextElementSibling;
            while (next && !next.hasAttribute("data-kanban-card")) next = next.nextElementSibling;
            before = next ? id(next.getAttribute("data-kanban-card")) : null;
          }
        }
      }
      endDrag(scope, data);
      if (column && event.type === "pointerup") move(scope, cardID, column.getAttribute("data-kanban-column") || "", before);
    }
    context.listen(context.host.ownerDocument, "pointerup", release);
    context.listen(context.host.ownerDocument, "pointercancel", release);

    // A focused card moves with the arrow keys: Left/Right between columns, Up/Down within one.
    context.listen(context.host, "keydown", function (event) {
      var card = cardElement(data, event.target);
      if (!card || event.target !== card) return;
      var cardID = id(card.getAttribute("data-kanban-card"));
      var key = event.key;
      var entry = find(scope, cardID);
      if (!entry) return;
      var names = columnIDs(data);
      var at = names.indexOf(entry.column);
      if (key === "ArrowLeft" || key === "ArrowRight") {
        var to = at + (key === "ArrowLeft" ? -1 : 1);
        if (to < 0 || to >= names.length) return;
        move(scope, cardID, names[to]);
        data.refocus = cardID;
      } else if (key === "ArrowUp" || key === "ArrowDown") {
        var siblings = list(scope.cards).filter(function (c) { return c && c.column === entry.column; }).map(function (c) { return id(c.id); });
        var position = siblings.indexOf(cardID);
        var swap = position + (key === "ArrowUp" ? -1 : 1);
        if (swap < 0 || swap >= siblings.length) return;
        move(scope, cardID, entry.column, key === "ArrowUp" ? siblings[swap] : (siblings[swap + 1] || null));
        data.refocus = cardID;
      } else return;
      event.preventDefault();
    });

    function afterRender() {
      if (data.disposed) return;
      if (data.refocus) {
        var wanted = data.refocus;
        data.refocus = "";
        var cards = context.owned("[data-kanban-card]");
        for (var i = 0; i < cards.length; i++) {
          if (id(cards[i].getAttribute("data-kanban-card")) === wanted && typeof cards[i].focus === "function") { cards[i].focus(); break; }
        }
      }
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () { endDrag(scope, data); data.disposed = true; });
  },

  cardsIn: function (column) {
    return list(this.cards).filter(function (card) { return card && card.column === column; });
  },
  count: function (column) { return this.cardsIn(column).length; },
  move: function (cardID, column, before) { return move(this, id(cardID), typeof column === "string" ? column : "", before ? id(before) : null); },
  add: function (column, title) {
    if (typeof column !== "string" || column === "" || typeof title !== "string" || title.trim() === "") return false;
    var cards = list(this.cards).slice();
    var next = cards.reduce(function (top, card) {
      var value = Number(card && card.id);
      return Number.isFinite(value) && value > top ? value : top;
    }, 0) + 1;
    cards.push({ id: next, column: column, title: title.trim() });
    this.cards = cards;
    return next;
  },
  remove: function (cardID) {
    cardID = id(cardID);
    var cards = list(this.cards).filter(function (card) { return !card || id(card.id) !== cardID; });
    var removed = cards.length !== list(this.cards).length;
    if (removed) this.cards = cards;
    return removed;
  },
  isDragging: function (cardID) { return this.dragging !== "" && this.dragging === id(cardID); },
  isOver: function (column) { return this.over !== "" && this.over === column; }
});

})();
