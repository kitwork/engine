/* tabs component @v1.0.0 — the keyboard half of a tablist.
 *
 * Usage — the markup declares every state a reader can see, the component owns the keyboard:
 *
 *   <div data-kit-component="tabs" data-kit-alias="$docs">
 *     <div role="tablist" data-kit-element="list" aria-label="Documentation">
 *       <button role="tab" id="tab-guide" data-kit-element="tab" data-tab="guide"
 *               aria-controls="panel-guide"
 *               data-kit-click="select('guide')"
 *               data-kit-bind:aria-selected="current === 'guide'"
 *               data-kit-bind:tabindex="current === 'guide' ? 0 : -1">Guide</button>
 *       …one button per tab…
 *     </div>
 *     <section role="tabpanel" id="panel-guide" aria-labelledby="tab-guide"
 *              data-kit-show="current === 'guide'">…</section>
 *   </div>
 *
 * Add aria-orientation="vertical" to the tablist and ↑↓ drive it instead of ←→, which is what a
 * screen reader will have told the person to expect.
 *
 * WHY THIS IS A COMPONENT AND SWITCHING PANELS IS NOT. Showing one panel at a time is the grammar:
 * a value, a click that assigns it, a `show` that reads it. The old `tab` component did only that
 * and was deleted on 22/09. Keeping exactly one tab in the tab order is the grammar too — that is
 * the `tabindex` binding above, and writing it from JavaScript would hide a visible rule in code.
 *
 * What is left cannot be said in markup at all: ← → wrap along the tablist, Home and End jump to
 * its ends, and the tab they reach takes FOCUS as well as selection. Moving focus is the component's
 * whole job, and it is why a tablist is reachable by keyboard instead of merely clickable.
 *
 * ACCESSIBILITY is split on purpose. The markup declares meaning — role, aria-controls,
 * aria-labelledby, aria-selected, tabindex. The component decides where focus goes. A reader of the
 * page can see the first half; only the second needs code.
 *
 * The public surface is deliberately short — current, select, next, previous, first, last — because
 * a page can call all of it ($docs.next()). Everything else lives behind a Symbol, which markup
 * cannot name and Object.keys does not show: an implementation detail that leaks into the contract
 * is one nobody can change later.
 */
var TABS = Symbol("kit:tabs");

// tabElements is every tab this instance owns, in document order, read live so a tablist a template
// rebuilt between renders is never stale. A button with no data-tab is not a tab: it is an
// authoring mistake, reported once at mount and then skipped, so the arrows cannot land on a
// control that has no panel and leave focus and selection disagreeing.
function tabElements(scope) {
  var own = scope[TABS];
  if (!own) return [];
  return own.context.elements("tab").filter(function (element) {
    return !!element.getAttribute("data-tab");
  });
}

function nameOf(element) {
  return element ? element.getAttribute("data-tab") || "" : "";
}

var tabsDef = {
  current: "",

  // select() does NOT move focus: a click already put focus where the person meant it. Only the
  // keyboard paths below move it, which is the one time a page should.
  select: function (name) {
    if (name) this.current = String(name);
  },

  next: function () { step(this, 1); },
  previous: function () { step(this, -1); },
  first: function () { jump(this, 0); },
  last: function () { jump(this, -1); },

  init: function (context) {
    var scope = this;
    this[TABS] = { context: context };

    var elements = context.elements("tab");
    var unnamed = elements.length - tabElements(this).length;
    if (unnamed > 0 && typeof console !== "undefined" && console.warn) {
      console.warn('kitwork: data-kit-component="tabs" — ' + unnamed +
        ' tab' + (unnamed === 1 ? "" : "s") + ' without data-tab="…" cannot be selected, so the keyboard skips them.');
    }

    // A tablist opens on its first tab unless the page already chose one — a scope value, a
    // data-kit-seed, or whatever the server rendered.
    if (!this.current) {
      this.current = nameOf(tabElements(this)[0]);
    }

    var list = context.element("list") || context.host;
    this[TABS].vertical = function () { return list.getAttribute("aria-orientation") === "vertical"; };

    context.listen(list, "keydown", function (event) {
      // A modifier means the key belongs to the browser or to the page, not to this tablist.
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      var key = String(event.key || "");
      var vertical = scope[TABS].vertical();
      if (key === (vertical ? "ArrowDown" : "ArrowRight")) step(scope, 1);
      else if (key === (vertical ? "ArrowUp" : "ArrowLeft")) step(scope, -1);
      else if (key === "Home") jump(scope, 0);
      else if (key === "End") jump(scope, -1);
      else return;
      event.preventDefault();
    });
  }
};

// step moves one tab along and wraps: a tablist is a ring, so the end is never a dead stop.
function step(scope, by) {
  var elements = tabElements(scope);
  if (elements.length < 2) return;
  var index = 0;
  for (var i = 0; i < elements.length; i++) {
    if (nameOf(elements[i]) === scope.current) index = i;
  }
  focusTab(scope, elements[(index + by + elements.length) % elements.length]);
}

function jump(scope, index) {
  var elements = tabElements(scope);
  if (elements.length) focusTab(scope, index < 0 ? elements[elements.length - 1] : elements[index]);
}

// focusTab is select() plus the focus move the arrows owe the person pressing them.
function focusTab(scope, element) {
  if (!element) return;
  scope.select(nameOf(element));
  if (typeof element.focus !== "function") return;
  try { element.focus({ preventScroll: true }); } catch (error) { element.focus(); }
}

window.kit.component("tabs", tabsDef);
window.kit.component("tabs@v1.0.0", tabsDef);
