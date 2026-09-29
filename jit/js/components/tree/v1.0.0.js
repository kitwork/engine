/* tree component @v1.0.0 — the keyboard half of a tree view.
 *
 * Usage — the markup declares every state a reader can see; the component moves focus:
 *
 *   <ul role="tree" aria-label="Files" data-kit-component="tree">
 *     <li role="treeitem" data-kit-element="item" data-id="docs"
 *         data-kit-bind:aria-expanded="expanded.includes('docs') ? 'true' : 'false'"
 *         data-kit-bind:aria-selected="selected === 'docs' ? 'true' : 'false'"
 *         data-kit-bind:tabindex="focused === 'docs' ? 0 : -1"
 *         data-kit-click:self="toggle('docs')">docs
 *       <ul role="group" data-kit-show="expanded.includes('docs')" hidden>
 *         <li role="treeitem" data-kit-element="item" data-id="intro" … data-kit-click:self="select('intro')">intro</li>
 *       </ul>
 *     </li>
 *   </ul>
 *
 * A child is an item nested inside another item; there is nothing else to declare. data-id is the
 * item's name, unique within the tree.
 *
 * WHY THIS IS A COMPONENT, AND WHY IT WRITES NO ATTRIBUTES. Which branches are open, which item is
 * chosen and which one is the tree's single tab stop are all state the markup reads — aria-expanded,
 * aria-selected and tabindex are bindings above, where a reader can see the rule. (The KitJS tree
 * wrote them from code; that is the thing this one does not do.) What cannot be said in markup is
 * WHERE FOCUS GOES along the tree's shape:
 *
 *   ↓ ↑        the next / previous item that is on screen — a closed branch's children are skipped
 *   →          opens a closed branch; on an open one, moves into its first child
 *   ←          closes an open branch; on a child, moves up to its parent
 *   Home End   the first / last item on screen
 *   Enter      selects the focused item
 *
 * That is the WAI-ARIA tree view pattern. The tree has exactly one tab stop — `focused` — so Tab
 * enters and leaves it in one step, and the arrows do the walking.
 *
 * The public surface: expanded, selected, focused, toggle, expand, collapse, select.
 */
var TREE = Symbol("kit:tree");

function treeItems(scope) {
  var own = scope[TREE];
  return own ? own.context.elements("item") : [];
}

function nameOf(item) {
  return item ? item.getAttribute("data-id") || "" : "";
}

// An item's parent is the nearest item that contains it, inside this tree.
function parentOf(scope, item) {
  var own = scope[TREE];
  for (var node = item.parentElement; node && own && node !== own.context.host; node = node.parentElement) {
    if (node.getAttribute("data-kit-element") === "item") return node;
  }
  return null;
}

function hasChildren(scope, item) {
  return treeItems(scope).some(function (other) { return parentOf(scope, other) === item; });
}

function isOpen(scope, item) {
  return (scope.expanded || []).indexOf(nameOf(item)) >= 0;
}

// On screen means every ancestor is open. Read from state, not from the DOM, so a branch opened by
// this very keystroke already counts before the paint that reveals it.
function onScreen(scope) {
  return treeItems(scope).filter(function (item) {
    for (var up = parentOf(scope, item); up; up = parentOf(scope, up)) {
      if (!isOpen(scope, up)) return false;
    }
    return true;
  });
}

function focusedItem(scope) {
  var name = scope.focused;
  return treeItems(scope).filter(function (item) { return nameOf(item) === name; })[0] || null;
}

// Moving focus is two steps: `focused` names the tab stop (the markup's tabindex follows), then the
// element takes focus — after the paint when it was hidden until now.
function moveTo(scope, item) {
  if (!item) return;
  scope.focused = nameOf(item);
  var own = scope[TREE];
  var land = function () {
    if (typeof item.focus !== "function") return;
    try { item.focus({ preventScroll: false }); } catch (error) { item.focus(); }
  };
  land();
  if (own && own.context.host.ownerDocument.activeElement !== item) own.context.afterRender(land);
}

var treeDef = {
  expanded: [],
  selected: "",
  focused: "",

  expand: function (name) {
    if ((this.expanded || []).indexOf(name) < 0) this.expanded = (this.expanded || []).concat([name]);
  },

  collapse: function (name) {
    this.expanded = (this.expanded || []).filter(function (open) { return open !== name; });
  },

  toggle: function (name) {
    if ((this.expanded || []).indexOf(name) >= 0) this.collapse(name);
    else this.expand(name);
    this.focused = name;
  },

  select: function (name) {
    this.selected = name;
    this.focused = name;
  },

  init: function (context) {
    var scope = this;
    scope[TREE] = { context: context };
    context.cleanup(function () { delete scope[TREE]; });

    // One tab stop from the start: the chosen item if there is one, else the first.
    if (!scope.focused) {
      var first = scope.selected || nameOf(treeItems(scope)[0]);
      if (first) scope.focused = first;
    }

    context.listen(context.host, "keydown", function (event) {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
      var current = focusedItem(scope);
      if (!current) return;
      var visible = onScreen(scope);
      var index = visible.indexOf(current);

      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          if (index < visible.length - 1) moveTo(scope, visible[index + 1]);
          return;
        case "ArrowUp":
          event.preventDefault();
          if (index > 0) moveTo(scope, visible[index - 1]);
          return;
        case "Home":
          event.preventDefault();
          moveTo(scope, visible[0]);
          return;
        case "End":
          event.preventDefault();
          moveTo(scope, visible[visible.length - 1]);
          return;
        case "ArrowRight":
          event.preventDefault();
          if (!hasChildren(scope, current)) return;
          if (!isOpen(scope, current)) { scope.expand(nameOf(current)); return; }
          moveTo(scope, onScreen(scope)[index + 1]); // an open branch's first child comes next
          return;
        case "ArrowLeft":
          event.preventDefault();
          if (hasChildren(scope, current) && isOpen(scope, current)) { scope.collapse(nameOf(current)); return; }
          moveTo(scope, parentOf(scope, current));
          return;
        case "Enter":
          event.preventDefault();
          scope.select(nameOf(current));
          return;
      }
    });
  }
};

window.kit.component("tree", treeDef);
window.kit.component("tree@v1.0.0", treeDef);
