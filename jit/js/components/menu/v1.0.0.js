/* menu component — the keyboard half of a menu button.
 *
 * NOT the deleted `dropdown`. That one opened and closed a panel, which is the grammar, and it
 * went on 22/09 with toggle/tab/dismiss for exactly that reason. This is the half the grammar
 * cannot reach — where focus goes — and it stands in the same relation to it as `tabs` does to the
 * deleted `tab`.
 *
 * Usage — the markup declares everything a reader can see; the component owns where focus goes:
 *
 *   <div data-kit-component="menu" data-kit-click:outside="hide()">
 *     <button data-kit-element="trigger" aria-haspopup="true"
 *             data-kit-click="toggle()"
 *             data-kit-bind:aria-expanded="open ? 'true' : 'false'">
 *       Developers
 *       <i data-kit-class="open ? 'rotate-180' : 'rotate-0'" aria-hidden="true"></i>
 *     </button>
 *     <div role="menu" data-kit-show="open" hidden aria-label="Developers">
 *       <a role="menuitem" data-kit-element="item" href="/docs">Docs</a>
 *       …one per entry…
 *     </div>
 *   </div>
 *
 * WHY THIS IS A COMPONENT AND OPENING IS NOT. Opening is the grammar: a boolean, a click that
 * flips it, a `show` that reads it. Closing on a click elsewhere is the grammar too — that is
 * data-kit-click:outside above, written where a reader of the page can see it rather than hidden
 * in code. Neither needs JavaScript, and a menu that only opens and closes needs no component at
 * all: say data-kit-scope="{ open: false }" and stop.
 *
 * What the grammar cannot do is MOVE FOCUS. ↓ from the trigger opens the menu and lands on the
 * first entry, ↑ lands on the last, ↓↑ walk and wrap, Home and End jump to the ends, Escape closes
 * and gives focus back to the trigger — because a person who opened a menu from the keyboard must
 * be able to get out of it the same way, and must not be left focused on something that is no
 * longer on screen. Tab closes too: moving on is an answer.
 *
 * ACCESSIBILITY is split the same way tabs splits it. The markup declares meaning —
 * aria-haspopup, aria-expanded, role="menu", role="menuitem", aria-label. The component decides
 * where focus lands. A reader of the page can see the first half; only the second needs code.
 *
 * A disabled entry (disabled, or aria-disabled="true") is skipped by the arrows rather than
 * focused: landing on something that cannot be chosen is a dead end the keyboard cannot see.
 *
 * The public surface is short — open, show, hide, toggle, first, last — because a page can call all
 * of it ($developers.hide()). The rest lives behind a Symbol, which markup cannot name and
 * Object.keys does not show.
 */
var MENU = Symbol("kit:menu");

// The entries this instance owns, in document order, read live so a menu a template rebuilt
// between renders is never stale. An entry that cannot be chosen is not an entry.
function menuItems(scope) {
  var own = scope[MENU];
  if (!own) return [];
  return own.context.elements("item").filter(function (element) {
    return !element.disabled && element.getAttribute("aria-disabled") !== "true";
  });
}

function menuTrigger(scope) {
  var own = scope[MENU];
  return own ? own.context.element("trigger") : null;
}

// focusEntry moves focus without scrolling the page out from under the person doing it.
function focusEntry(element) {
  if (!element || typeof element.focus !== "function") return;
  try {
    element.focus({ preventScroll: true });
  } catch (error) {
    element.focus();
  }
}

// An entry inside a menu that is still hidden cannot take focus, so a landing that follows an
// OPEN has to wait for the paint data-kit-show performs. A landing inside a menu already on screen
// must not wait, because nothing is going to be repainted and afterRender would never run — which
// is exactly how End did nothing the first time this was written.
//
// So: try now, and only fall back to the next paint if the entry refused focus.
function landOn(scope, index) {
  var own = scope[MENU];
  if (!own) return;
  var reach = function () {
    var items = menuItems(scope);
    if (!items.length) return null;
    var target = items[index < 0 ? items.length + index : index];
    focusEntry(target);
    return target;
  };
  var target = reach();
  if (target && own.document.activeElement === target) return;
  own.context.afterRender(reach);
}

function currentIndex(scope) {
  var items = menuItems(scope);
  var active = scope[MENU] ? scope[MENU].document.activeElement : null;
  for (var i = 0; i < items.length; i++) {
    if (items[i] === active) return i;
  }
  return -1;
}

function walk(scope, by) {
  var items = menuItems(scope);
  if (!items.length) return;
  var index = currentIndex(scope);
  focusEntry(items[index < 0 ? (by > 0 ? 0 : items.length - 1) : (index + by + items.length) % items.length]);
}

var menuDef = {
  open: false,

  show: function () {
    if (!this.open) this.open = true;
  },

  // hide() returns focus to the trigger ONLY when focus is inside the menu: a click elsewhere on
  // the page closed it too, and stealing focus back from wherever the person just went would undo
  // their own move.
  hide: function () {
    if (!this.open) return;
    var own = this[MENU];
    var inside = own && own.context.host.contains(own.document.activeElement);
    this.open = false;
    if (inside) focusEntry(menuTrigger(this));
  },

  toggle: function () {
    if (this.open) this.hide();
    else this.show();
  },

  first: function () { this.show(); landOn(this, 0); },
  last: function () { this.show(); landOn(this, -1); },

  init: function (context) {
    var scope = this;
    var document = context.host.ownerDocument;
    scope[MENU] = { context: context, document: document };
    context.require("trigger");
    context.require("item");
    context.cleanup(function () { delete scope[MENU]; });

    context.listen(context.host, "keydown", function (event) {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
      var onTrigger = event.target === menuTrigger(scope);

      if (event.key === "Escape") {
        if (!scope.open) return;
        event.preventDefault();
        scope.hide();
        return;
      }
      // Tab is the person leaving. Let it through — closing without preventDefault means focus
      // lands where Tab was always going to put it.
      if (event.key === "Tab") {
        if (scope.open) scope.hide();
        return;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        if (!scope.open || onTrigger) scope.first();
        else walk(scope, 1);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        if (!scope.open || onTrigger) scope.last();
        else walk(scope, -1);
        return;
      }
      if (!scope.open || onTrigger) return;
      if (event.key === "Home") {
        event.preventDefault();
        landOn(scope, 0);
      } else if (event.key === "End") {
        event.preventDefault();
        landOn(scope, -1);
      }
    });
  }
};

window.kit.component("menu", menuDef);
