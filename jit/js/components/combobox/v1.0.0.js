/* combobox component — the keyboard half of a text field with a list of suggestions.
 *
 * Usage — the page's scope holds the data, the grammar filters it, the component owns the keys:
 *
 *   <div data-kit-scope="options: ['Apple', 'Apricot', 'Banana'], query: '', selected: ''">
 *     <div data-kit-component="combobox" data-kit-click:outside="hide()">
 *       <input role="combobox" aria-controls="fruit-list" aria-autocomplete="list"
 *              data-kit-model="query" data-kit-focusin="show()"
 *              data-kit-bind:aria-expanded="open ? 'true' : 'false'"
 *              data-kit-bind:aria-activedescendant="active >= 0 ? 'fruit-' + active : null">
 *       <ul id="fruit-list" role="listbox" data-kit-show="open" hidden>
 *         <li data-kit-for="option, i of options.filter((o) => o.toLowerCase().includes(query.trim().toLowerCase()))"
 *             role="option" data-kit-element="option"
 *             data-kit-bind:id="'fruit-' + i"
 *             data-kit-bind:aria-selected="i === active ? 'true' : 'false'"
 *             data-kit-class="i === active ? 'bg-panel' : ''"
 *             data-kit-click="selected = option; query = option; hide()"
 *             data-kit-text="option"></li>
 *       </ul>
 *     </div>
 *   </div>
 *
 * WHY THIS IS A COMPONENT AND FILTERING IS NOT. The list is data: which options match what was
 * typed is a filter expression, written where a reader can see it. Choosing one is an assignment on
 * the option's click. Opening on focus and closing on an outside click are the grammar too. None of
 * that is here.
 *
 * What the grammar cannot do is the KEYBOARD. ↓ opens the list and marks the first option, ↓↑ walk
 * and wrap, Enter chooses the marked one, Escape closes, Tab closes and moves on. And unlike a menu,
 * focus NEVER leaves the text field — the person is typing, and taking the caret away would lose
 * their place. The marked option is announced through aria-activedescendant on the field instead,
 * which the markup binds to `active`. That split is the WAI-ARIA combobox pattern.
 *
 * Typing clears the mark: the options under it have changed, so an old index would point at a
 * different word. Rows the filter HIDES (data-kit-show) and rows that are disabled are skipped, so
 * authored rows work as well as rows from a data-kit-for; `activeId` is the marked row's own id,
 * for aria-activedescendant when the rows already carry ids.
 *
 * The public surface: open, active, activeId, show, hide, toggle. The rest lives behind a Symbol.
 */
var COMBOBOX = Symbol("kit:combobox");

// The options that can be marked, as they stand after the last paint. A list can be filtered two
// ways and both are honoured: data-kit-for leaves only the rows that match, and data-kit-show hides
// the ones that do not (a command palette's rows are authored, so it filters by hiding). A hidden
// row and a disabled one are skipped — the arrows must never land on something that is not there
// or cannot be chosen. The component never needs to know what the filter was.
function comboboxOptions(scope) {
  var own = scope[COMBOBOX];
  if (!own) return [];
  return own.context.elements("option").filter(function (option) {
    return !option.hidden && !option.disabled && option.getAttribute("aria-disabled") !== "true";
  });
}

// Keep the marked option in view without moving focus, once the paint has put the mark on it.
function revealActive(scope) {
  var own = scope[COMBOBOX];
  if (!own) return;
  own.context.afterRender(function () {
    var option = comboboxOptions(scope)[scope.active];
    if (option && typeof option.scrollIntoView === "function") {
      try { option.scrollIntoView({ block: "nearest" }); } catch (error) { }
    }
  });
}

function mark(scope, index) {
  scope.active = index;
  revealActive(scope);
}

var comboboxDef = {
  open: false,
  active: -1,

  // The marked option's own id, or "" — what aria-activedescendant needs. An index works when the
  // markup numbers its rows ('fruit-' + i); authored rows already have ids, and this reads them.
  get activeId() {
    var option = comboboxOptions(this)[this.active];
    return option && option.id ? option.id : "";
  },

  show: function () {
    if (!this.open) this.open = true;
  },

  hide: function () {
    if (this.open) this.open = false;
    if (this.active !== -1) this.active = -1;
  },

  toggle: function () {
    if (this.open) this.hide();
    else this.show();
  },

  init: function (context) {
    var scope = this;
    scope[COMBOBOX] = { context: context };
    context.cleanup(function () { delete scope[COMBOBOX]; });

    // Typing changes which options exist, so the mark goes; the list opens to show what matched.
    context.listen(context.host, "input", function () {
      scope.open = true;
      scope.active = -1;
    });

    context.listen(context.host, "keydown", function (event) {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
      var count = comboboxOptions(scope).length;

      if (event.key === "ArrowDown") {
        event.preventDefault();
        if (!scope.open) { scope.open = true; if (count) mark(scope, 0); return; }
        if (count) mark(scope, scope.active < 0 ? 0 : (scope.active + 1) % count);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        if (!scope.open) { scope.open = true; if (count) mark(scope, count - 1); return; }
        if (count) mark(scope, scope.active <= 0 ? count - 1 : scope.active - 1);
        return;
      }
      if (event.key === "Enter") {
        var chosen = scope.open && scope.active >= 0 ? comboboxOptions(scope)[scope.active] : null;
        if (!chosen) return; // nothing marked: let Enter do what it does in a form
        event.preventDefault();
        chosen.click(); // the option's own data-kit-click decides what choosing means
        return;
      }
      if (event.key === "Escape") {
        if (!scope.open) return;
        event.preventDefault();
        scope.hide();
        return;
      }
      if (event.key === "Tab" && scope.open) scope.hide(); // moving on is an answer; let it through
    });
  }
};

window.kit.component("combobox", comboboxDef);
