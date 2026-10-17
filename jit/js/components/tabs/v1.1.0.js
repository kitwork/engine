/* tabs component — one panel at a time, chosen by its tab: the keyboard half of a tablist, and a
 * choice kept across pages and visits (16/10/2026, docs/components/tabs.md).
 *
 * Kitwork UI (the engine writes everything but the author's words). The author writes:
 *
 *   <box component="tabs" as="$shell" persist="true" alt="Shell">
 *     <button click="select('bash')">bash</button>
 *     <button click="select('powershell')">PowerShell</button>
 *   </box>
 *   <box show="$shell.selected == 'bash'">…</box>
 *   <box show="$shell.selected == 'powershell'">…</box>
 *
 * and the engine (ui/tabs.go) writes role="tablist" on the host; on each button whose click is
 * select('<name>') role="tab", value="<name>", an id, aria-controls and the bindings of aria-selected
 * and tabindex (selected == '<name>'); on each element whose show compares $shell.selected with a
 * tab's name role="tabpanel", an id, aria-labelledby and tabindex="0"; and, in the host's
 * data-kitwork-scope, selected: '<first tab>' (what the server painted) and persist: the name the
 * choice is kept under — for persist="true" the set of the tab names, sorted and joined by "+"
 * ('bash+powershell'), for persist="shell" that name.
 *
 * A template page (frozen, written before Kitwork UI) writes it all itself and reads `current`:
 *
 *   <div data-kit-component="tabs" data-kit-alias="$docs">
 *     <div role="tablist" data-kit-element="list" aria-label="Documentation">
 *       <button role="tab" id="tab-guide" data-kit-element="tab" data-tab="guide" aria-controls="panel-guide"
 *               data-kit-click="select('guide')"
 *               data-kit-bind:aria-selected="current === 'guide'"
 *               data-kit-bind:tabindex="current === 'guide' ? 0 : -1">Guide</button>
 *     </div>
 *     <section role="tabpanel" id="panel-guide" aria-labelledby="tab-guide" data-kit-show="current === 'guide'">…</section>
 *   </div>
 *
 *   selected   the name of the chosen tab: the host's literal (selected: 'router'), else the first tab;
 *              then what the name it persists under keeps, once the store answers
 *   current    the same name, under the word template pages read
 *   persist    the name the choice is kept under, "" for none. Every tabs of the page with the same
 *              name follows a choice made in any of them (when it has a tab of that name), and the
 *              choice goes to kit.storage under "tabs:<name>" — in a browser localStorage
 *              "kitwork:tabs:<name>", in a desktop or mobile app the native store over the bridge
 *              (which no shell keeps yet: storage.* answers UNSUPPORTED there, so nothing is kept —
 *              docs/components/tabs.md). It is read as the instance starts: a store that answers late
 *              (the bridge is asynchronous) is followed only if nobody chose since. A choice another
 *              window of the site makes is followed (a browser's storage event). A name kept that is
 *              no tab of this host is left alone; a store that refuses or keeps nothing changes nothing
 *   select(name)                     choose a tab
 *   next() previous() first() last() move along the tabs, focus with them
 *
 * Add aria-orientation="vertical" to a template page's tablist and ↑↓ drive it instead of ←→.
 *
 * WHY THIS IS A COMPONENT. Showing one panel is the grammar: a value, a click, a show. What markup
 * cannot say: ← → wrap along the tablist, Home and End jump to its ends, the tab they reach takes FOCUS
 * as well as selection, and a choice outlives the page. Moving focus and keeping the choice is the
 * component's job. It never writes an attribute: role, aria-* and tabindex are markup and bindings.
 *
 * In a browser, the engine's pre-paint (ui/tabs.go) has shown the tab kept before the first frame,
 * reading the same localStorage entry kit.storage writes; the restore here makes the same choice, in
 * the same task as the kernel starts, so nothing moves again. It is what shows the tab kept on a page
 * Drive brings, and in an app, where no first frame can wait for the bridge.
 */
var TABS = Symbol("kit:tabs");
// the key a choice is kept under in kit.storage, and the prefix kit.storage writes before it in a
// browser's localStorage (what a storage event names)
var KEPT = "tabs:";
var WEB = "kitwork:";
// SHARED is, by persist name, the instances of the page that keep their choice under it, and how many
// choices a person made there: a choice in one reaches the others, and a store that answers after a
// choice does not undo it
var SHARED = {};

// tabElements is every tab this instance owns, in document order, read live. A template page names
// its tabs (data-kit-element="tab", the name in data-tab: one without is an authoring mistake,
// reported once by context.require and then skipped); on a Kitwork UI page the engine marks them
// (role="tab", the name in value).
function tabElements(scope) {
  var own = scope[TABS];
  if (!own) return [];
  var marked = own.context.elements("tab");
  if (marked.length) {
    return marked.filter(function (element) { return !!element.getAttribute("data-tab"); });
  }
  return own.context.owned('[role="tab"]').filter(function (element) { return !!element.value; });
}

function nameOf(element) {
  return element ? element.getAttribute("data-tab") || element.value || "" : "";
}

function named(scope, name) {
  return tabElements(scope).some(function (element) { return nameOf(element) === name; });
}

function persistOf(scope) {
  return scope.persist ? String(scope.persist) : "";
}

// store is the kernel's storage service, or null on a page without one
function store() {
  var kit = window.kit;
  var storage = kit && kit.storage;
  return storage && typeof storage.get === "function" && typeof storage.set === "function" ? storage : null;
}

// keep hands the choice to the store; a store that refuses (a private window, blocked site data, an
// app whose bridge has no store) keeps nothing, and the page works without it
function keep(persist, name) {
  var storage = store();
  if (!storage) return;
  try {
    Promise.resolve(storage.set(KEPT + persist, name)).catch(function () { /* nothing kept */ });
  } catch (problem) { /* nothing kept */ }
}

// follow gives an instance a name kept or chosen elsewhere, when it has a tab of that name
function follow(scope, name) {
  if (typeof name === "string" && name && scope.selected !== name && named(scope, name)) {
    scope.selected = scope.current = name;
  }
}

var tabsDef = {
  selected: "",
  current: "",
  persist: "",

  // select() does NOT move focus: a click already put focus where the person meant it. Only the
  // keyboard paths below move it, which is the one time a page should.
  select: function (name) {
    if (!name) return;
    var self = this;
    name = String(name);
    this.selected = this.current = name;
    var persist = persistOf(this);
    if (!persist) return;
    var shared = SHARED[persist];
    if (shared) shared.choices++;
    keep(persist, name);
    // an instance is known by its own slot: a call from markup and init see it through different proxies
    (shared ? shared.members : []).forEach(function (other) {
      if (other[TABS] !== self[TABS]) follow(other, name);
    });
  },

  next: function () { step(this, 1); },
  previous: function () { step(this, -1); },
  first: function () { jump(this, 0); },
  last: function () { jump(this, -1); },

  init: function (context) {
    var scope = this;
    var own = this[TABS] = { context: context };

    // A template page's tab with no data-tab cannot be selected, so the keyboard skips it — and the
    // kernel says so. A Kitwork UI page's tabs are the engine's, checked when the page compiles.
    if (context.host.hasAttribute("data-kit-component")) context.require("tab", "data-tab");

    // What opens: what the page chose (the host's literal, a seed), with neither the first tab; then
    // what the store keeps, when it answers.
    var chosen = String(this.selected || this.current || "");
    if (!chosen) chosen = nameOf(tabElements(this)[0]);
    this.selected = this.current = chosen;

    var persist = persistOf(this);
    if (persist) {
      var shared = SHARED[persist] || (SHARED[persist] = { members: [], choices: 0 });
      shared.members.push(scope);
      context.cleanup(function () {
        own.gone = true;
        var at = shared.members.indexOf(scope);
        if (at >= 0) shared.members.splice(at, 1);
      });
      // read as early as the instance exists: in a browser the store answers before the next frame;
      // over a native bridge later, and then only if nobody has chosen since
      var since = shared.choices;
      var storage = store();
      if (storage) {
        try {
          Promise.resolve(storage.get(KEPT + persist)).then(function (value) {
            if (!own.gone && shared.choices === since) follow(scope, value);
          }, function () { /* nothing kept */ });
        } catch (problem) { /* nothing kept */ }
      }
      // another window of the site chose: follow, without keeping it again (it was kept there)
      context.listen(window, "storage", function (event) {
        if (event.key !== WEB + KEPT + persist) return;
        var name = "";
        try { name = JSON.parse(event.newValue); } catch (problem) { return; }
        follow(scope, name);
      });
    }

    var tablist = context.element("list") || context.host;
    own.vertical = function () { return tablist.getAttribute("aria-orientation") === "vertical"; };

    context.listen(tablist, "keydown", function (event) {
      // A modifier means the key belongs to the browser or to the page, not to this tablist.
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      var key = String(event.key || "");
      var vertical = own.vertical();
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
    if (nameOf(elements[i]) === scope.selected) index = i;
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
