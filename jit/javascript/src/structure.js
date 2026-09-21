;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "dom") throw new Error("KitJS: structure loaded out of order");
  if (core.reuse) { core.phase = "structure"; return; }

  var OWN = core.OWN;
  var SELECTOR = "[data-kit-if],[data-kit-for],[data-kit-key]";
  var MAX_DEPTH = 64;
  var UNSET = {};
  var contexts = new WeakMap();
  var levels = new WeakMap();

  function copyOwn(target, source) {
    if (!source) return target;
    Object.keys(source).forEach(function (name) { target[name] = source[name]; });
    return target;
  }

  function replaceOwn(target, source) {
    source = source || Object.create(null);
    var targetKeys = Object.keys(target);
    var sourceKeys = Object.keys(source);
    var changed = targetKeys.length !== sourceKeys.length || sourceKeys.some(function (name) {
      return !OWN.call(target, name) || !core.equal(target[name], source[name]);
    });
    Object.keys(target).forEach(function (name) { delete target[name]; });
    copyOwn(target, source);
    return changed;
  }

  // The row overlay words (count first last even odd) are lexical to the row: they do not enter a
  // nested component host, whose own `count` stays its own. The authored item/index names do flow
  // in, as they always have. A context met after climbing past a boundary host is "from outside".
  var OVERLAY_WORDS = ["count", "first", "last", "even", "odd"];
  var OVERLAY = Symbol("kit:overlay");
  function isBoundaryHost(node) {
    return node.nodeType === 1 && (node.hasAttribute("data-kit-component") || node.hasAttribute("data-kit-scope"));
  }
  function withoutOverlay(local) {
    var output = Object.create(null);
    Object.keys(local).forEach(function (name) {
      if (OVERLAY_WORDS.indexOf(name) < 0) output[name] = local[name];
    });
    return output;
  }
  function localsFor(element, extra) {
    var chain = [];
    var node = element;
    var crossed = false;
    while (node && node !== document) {
      var local = contexts.get(node);
      if (local) chain.push(crossed && local[OVERLAY] ? withoutOverlay(local) : local);
      if (isBoundaryHost(node)) crossed = true;
      node = node.parentElement;
    }
    if (!chain.length) return extra || null;
    if (chain.length === 1 && !extra) return chain[0];
    var output = Object.create(null);
    for (var index = chain.length - 1; index >= 0; index--) copyOwn(output, chain[index]);
    return copyOwn(output, extra);
  }

  function validLocal(name) {
    return /^[A-Za-z_][A-Za-z0-9_]*$/.test(name) &&
      !core.blocked(name) && !core.FORBIDDEN[name];
  }

  function parseFor(source) {
    source = core.expressionSource(source || "");
    var match = /^\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:,\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*)?\s+of\s+([\s\S]+?)\s*$/.exec(source);
    if (!match || !validLocal(match[1]) || match[2] && !validLocal(match[2]) ||
        match[2] && match[1] === match[2]) {
      throw new SyntaxError("KitJS: invalid for specification");
    }
    return { item: match[1], index: match[2] || "", source: match[3] };
  }

  function fail(state, error) {
    var message = String(error && error.message || error);
    if (state.error !== message) {
      state.error = message;
      core.report(error);
    }
    return false;
  }

  function clearFailure(state) { state.error = ""; }

  function rejectDirectIf(element, error) {
    var modules = core.elementRecord(element).modules;
    modules.structure = null;
    core.report(error);
    return null;
  }

  function prepareDirectIf(element) {
    if (!element || element.nodeType !== 1 || element.tagName === "TEMPLATE" ||
      !element.hasAttribute("data-kit-if") || core.ignoredForRuntime(element)) return null;
    if (element.hasAttribute("data-kit-for") || element.hasAttribute("data-kit-key")) return null;
    var modules = core.elementRecord(element).modules;
    if (OWN.call(modules, "structure")) return null;
    if (!element.parentNode) {
      return rejectDirectIf(element, new TypeError("KitJS: direct if requires a connected element"));
    }
    if (!core.ownerFor(element.parentElement)) {
      return rejectDirectIf(element,
        new TypeError("KitJS: direct if requires an enclosing component or scope"));
    }
    if (element.tagName === "SCRIPT" || element.querySelector("script")) {
      return rejectDirectIf(element,
        new TypeError("KitJS: structural branches cannot contain script elements"));
    }
    if (element.hasAttribute("data-kit-retain") || element.querySelector("[data-kit-retain]")) {
      return rejectDirectIf(element,
        new TypeError("KitJS: data-kit-retain cannot be used in a structural branch"));
    }
    var condition = core.safeProgram(element, "data-kit-if", "binding");
    if (!condition) {
      modules.structure = null;
      return null;
    }

    var source = element.getAttribute("data-kit-if");
    var blueprint = element.cloneNode(true);
    blueprint.removeAttribute("data-kit-if");
    element.removeAttribute("data-kit-if");

    var anchor = document.createElement("template");
    anchor.setAttribute("data-kit-if", source);
    anchor.content.appendChild(blueprint);
    var start = document.createComment("kit-structure-start");
    var end = document.createComment("kit-structure-end");
    var parent = element.parentNode;
    var locals = copyOwn(Object.create(null), localsFor(element));
    var level = contextLevel(element) + 1;
    parent.insertBefore(start, element);
    parent.insertBefore(end, element.nextSibling);
    parent.insertBefore(anchor, end.nextSibling);

    var state = {
      kind: "if",
      condition: condition,
      branch: {
        start: start,
        end: end,
        nodes: null,
        locals: locals,
        level: level
      },
      direct: true,
      error: ""
    };
    core.elementRecord(anchor).modules.structure = state;
    bindRange(state.branch);
    return anchor;
  }

  // A list is authored on the row itself (ideaship-final §7): <li data-kit-for="item, i of items"
  // data-kit-key="item.id">. The row is the blueprint — clean markup nobody has hydrated — and it
  // leaves the document: a <template> carrying the same for/key takes its place and the rows are
  // materialised from it, so the template machinery below is the one list engine.
  function rejectDirectFor(element, error) {
    var modules = core.elementRecord(element).modules;
    modules.structure = null;
    core.report(error);
    return null;
  }
  function prepareDirectFor(element) {
    if (!element || element.nodeType !== 1 || element.tagName === "TEMPLATE" ||
      !element.hasAttribute("data-kit-for") || core.ignoredForRuntime(element)) return null;
    var modules = core.elementRecord(element).modules;
    if (OWN.call(modules, "structure")) return null;
    if (element.hasAttribute("data-kit-if")) {
      return rejectDirectFor(element, new TypeError("KitJS: one structural host cannot combine if and for"));
    }
    if (!element.parentNode) {
      return rejectDirectFor(element, new TypeError("KitJS: for requires a connected element"));
    }
    if (!core.ownerFor(element.parentElement)) {
      return rejectDirectFor(element, new TypeError("KitJS: for requires an enclosing component or scope"));
    }
    if (element.tagName === "SCRIPT" || element.querySelector("script")) {
      return rejectDirectFor(element, new TypeError("KitJS: structural branches cannot contain script elements"));
    }
    if (element.hasAttribute("data-kit-retain") || element.querySelector("[data-kit-retain]")) {
      return rejectDirectFor(element, new TypeError("KitJS: data-kit-retain cannot be used in a structural branch"));
    }
    var blueprint = element.cloneNode(true);
    blueprint.removeAttribute("data-kit-for");
    blueprint.removeAttribute("data-kit-key");
    var template = document.createElement("template");
    template.setAttribute("data-kit-for", element.getAttribute("data-kit-for"));
    if (element.hasAttribute("data-kit-key")) template.setAttribute("data-kit-key", element.getAttribute("data-kit-key"));
    template.content.appendChild(blueprint);
    element.parentNode.replaceChild(template, element);
    core.records.delete(element);
    return template;
  }

  function prepareStructureTree(root) {
    if (!root || !root.querySelectorAll) return false;
    var elements = [];
    if (root.nodeType === 1 && root.matches && root.matches("[data-kit-if],[data-kit-for]") &&
      root.tagName !== "TEMPLATE") elements.push(root);
    Array.prototype.push.apply(elements, root.querySelectorAll("[data-kit-if],[data-kit-for]"));
    var changed = false;
    elements.forEach(function (element) {
      if (element.tagName === "TEMPLATE") return;
      if (element.hasAttribute("data-kit-for") ? prepareDirectFor(element) : prepareDirectIf(element)) changed = true;
    });
    return changed;
  }

  function structureState(element) {
    if (core.ignoredForRuntime(element)) return null;
    var modules = core.elementRecord(element).modules;
    if (OWN.call(modules, "structure")) return modules.structure;
    if (core.invalidRetainStructure && core.invalidRetainStructure(element)) {
      modules.structure = null;
      return null;
    }
    try {
      var hasIf = element.hasAttribute("data-kit-if");
      var hasFor = element.hasAttribute("data-kit-for");
      var hasKey = element.hasAttribute("data-kit-key");
      if (hasIf && hasFor) throw new TypeError("KitJS: one structural host cannot combine if and for");
      if (hasKey && !hasFor) throw new TypeError("KitJS: key requires for on the same template");
      if (element.tagName !== "TEMPLATE") {
        throw new TypeError(hasFor || hasKey
          ? "KitJS: for could not be prepared on this element"
          : "KitJS: direct if could not be prepared");
      }
      if (!hasIf && !hasFor) throw new TypeError("KitJS: orphan structural template");
      if (element.content.querySelector("script")) {
        throw new TypeError("KitJS: structural templates cannot contain script elements");
      }
      if (hasIf) {
        var condition = core.safeProgram(element, "data-kit-if", "binding");
        if (!condition) throw new SyntaxError("KitJS: invalid if expression");
        modules.structure = {
          kind: "if",
          condition: condition,
          branch: null,
          error: ""
        };
      } else {
        var spec = parseFor(element.getAttribute("data-kit-for"));
        modules.structure = {
          kind: "for",
          item: spec.item,
          index: spec.index,
          list: core.compile(spec.source, "binding"),
          key: hasKey ? core.compile(element.getAttribute("data-kit-key"), "binding") : null,
          keyInvalid: false,
          lastList: UNSET,
          rows: new Map(),
          order: [],
          error: ""
        };
      }
    } catch (error) {
      core.report(error);
      modules.structure = null;
    }
    return modules.structure;
  }

  function rangeNodes(range) {
    if (range.nodes) return range.nodes.slice();
    var output = [];
    var node = range.start;
    while (node) {
      output.push(node);
      if (node === range.end) break;
      node = node.nextSibling;
    }
    return output;
  }

  function bindRange(range) {
    rangeNodes(range).forEach(function (node) {
      if (node.nodeType !== 1) return;
      contexts.set(node, range.locals);
      levels.set(node, range.level);
    });
  }

  function createRange(template, locals) {
    var fragment = template.content.cloneNode(true);
    var start = document.createComment("kit-structure-start");
    var end = document.createComment("kit-structure-end");
    var nodes = [start].concat(Array.prototype.slice.call(fragment.childNodes), [end]);
    var range = {
      start: start,
      end: end,
      nodes: nodes,
      locals: locals,
      level: contextLevel(template) + 1
    };
    bindRange(range);
    return range;
  }

  function insertRange(range, before, fresh) {
    var nodes = rangeNodes(range);
    var parent = before.parentNode;
    nodes.forEach(function (node) { parent.insertBefore(node, before); });
    range.nodes = null;
    if (fresh && core.prepareEventTree) {
      nodes.forEach(function (node) {
        if (node.nodeType === 1) core.prepareEventTree(node);
      });
    }
  }

  function disposeElement(element) {
    if (core.disposeElementEvents) core.disposeElementEvents(element);
    if (core.disposeComponent) core.disposeComponent(element);
    core.records.delete(element);
    core.scopes.delete(element);
    contexts.delete(element);
    levels.delete(element);
  }

  function disposeTree(root) {
    if (!root || root.nodeType !== 1) return;
    var descendants = Array.prototype.slice.call(root.querySelectorAll("*")).reverse();
    descendants.forEach(disposeElement);
    disposeElement(root);
  }

  function removeRange(range) {
    var nodes = rangeNodes(range);
    nodes.forEach(function (node) { if (node.nodeType === 1) disposeTree(node); });
    nodes.forEach(function (node) { if (node.parentNode) node.parentNode.removeChild(node); });
    range.nodes = [];
  }

  // The row's scope is an overlay on the outer locals (ideaship-final §7, rule 2): the item under
  // its authored name, the index under its authored name, and count / first / last / even / odd —
  // the item object itself is never touched.
  function makeLocals(outer, itemName, item, indexName, index, count) {
    var output = Object.create(null);
    copyOwn(output, outer);
    output[OVERLAY] = true;
    output.count = count;
    output.first = index === 0;
    output.last = index === count - 1;
    output.even = index % 2 === 0;
    output.odd = index % 2 === 1;
    output[itemName] = item;
    if (indexName) output[indexName] = index;
    return output;
  }

  function dirtyRangeComponents(range, owner) {
    rangeNodes(range).forEach(function (node) {
      if (node.nodeType !== 1) return;
      core.liveComponents(node).forEach(function (current) {
        if (current !== owner) core.invalidate(current);
      });
    });
  }

  function keyValue(value) {
    if (typeof value === "string") return value;
    if (typeof value === "number" && Number.isFinite(value)) return value;
    throw new TypeError("KitJS: key must return a string or finite number");
  }

  function processIf(template, state) {
    var current = core.scopeRecordFor(template);
    if (!current) return false;
    core.initialize(current);
    var outer = localsFor(template);
    var visible;
    try {
      visible = state.condition.read(current.scope, outer);
      if (core.asyncBinding(visible)) return false;
      visible = !!visible;
      clearFailure(state);
    } catch (error) { return fail(state, error); }

    if (!visible) {
      if (!state.branch) return false;
      removeRange(state.branch);
      state.branch = null;
      return true;
    }
    if (state.branch) {
      if (replaceOwn(state.branch.locals, outer)) dirtyRangeComponents(state.branch, current);
      return false;
    }
    var locals = copyOwn(Object.create(null), outer);
    state.branch = createRange(template, locals);
    insertRange(state.branch, template, true);
    return true;
  }

  function sameOrder(left, right) {
    if (left.length !== right.length) return false;
    for (var index = 0; index < left.length; index++) {
      if (left[index] !== right[index]) return false;
    }
    return true;
  }

  function contextLevel(element) {
    var node = element;
    while (node && node !== document) {
      if (levels.has(node)) return levels.get(node);
      node = node.parentElement;
    }
    return 0;
  }

  function elementDepth(element) {
    var depth = 0;
    while (element && element !== document) {
      depth++;
      element = element.parentElement;
    }
    return depth;
  }

  function processFor(template, state) {
    if (state.keyInvalid) return false;
    var current = core.scopeRecordFor(template);
    if (!current) return false;
    core.initialize(current);
    var outer = localsFor(template);
    var items;
    var plan = [];
    var keys = [];
    var seen = new Map();
    var listChanged = false;
    try {
      items = state.list(current.scope, outer);
      if (core.asyncBinding(items)) return false;
      if (!Array.isArray(items)) throw new TypeError("KitJS: for expression must return an array");
      for (var index = 0; index < items.length; index++) {
        var locals = makeLocals(outer, state.item, items[index], state.index, index, items.length);
        var key = index;
        if (state.key) {
          var rawKey = state.key(current.scope, locals);
          if (core.asyncBinding(rawKey)) {
            state.keyInvalid = true;
            return false;
          }
          key = keyValue(rawKey);
        }
        if (seen.has(key)) throw new TypeError("KitJS: duplicate for key \"" + key + "\"");
        seen.set(key, true);
        keys.push(key);
        plan.push({ key: key, locals: locals, row: state.rows.get(key) || null, fresh: false });
      }
      for (var createIndex = 0; createIndex < plan.length; createIndex++) {
        if (!plan[createIndex].row) {
          plan[createIndex].row = createRange(template, plan[createIndex].locals);
          plan[createIndex].fresh = true;
        }
      }
      listChanged = !core.equal(state.lastList, items);
      clearFailure(state);
    } catch (error) { return fail(state, error); }

    var nextRows = new Map();
    plan.forEach(function (entry) {
      if (!entry.fresh) {
        var localsChanged = replaceOwn(entry.row.locals, entry.locals);
        if (listChanged || localsChanged) dirtyRangeComponents(entry.row, current);
      }
      nextRows.set(entry.key, entry.row);
    });

    var removed = false;
    state.rows.forEach(function (row, key) {
      if (nextRows.has(key)) return;
      removeRange(row);
      removed = true;
    });

    var orderChanged = !sameOrder(state.order, keys);
    if (orderChanged) {
      var before = template;
      for (var moveIndex = plan.length - 1; moveIndex >= 0; moveIndex--) {
        var entry = plan[moveIndex];
        if (!entry.row.start.parentNode || entry.row.end.nextSibling !== before) {
          insertRange(entry.row, before, entry.fresh);
        }
        before = entry.row.start;
      }
    }
    state.rows = nextRows;
    state.order = keys;
    state.lastList = items;
    return removed || orderChanged;
  }

  function reconcile(current) {
    if (prepareStructureTree(current.host) && current.structures === false) {
      current.structures = undefined;
    }
    if (current.structures === false) return false;
    var changedAny = false;
    for (var pass = 0; pass < 64; pass++) {
      if (pass > 0 && prepareStructureTree(current.host)) current.structures = undefined;
      var changed = false;
      var elements = core.ownedElements(current, SELECTOR);
      if (current.structures === undefined) current.structures = elements.length > 0;
      if (!current.structures) return false;
      elements.sort(function (left, right) {
        return contextLevel(left) - contextLevel(right) || elementDepth(left) - elementDepth(right);
      });
      elements.forEach(function (element) {
        if (!element.isConnected) return;
        var state = structureState(element);
        if (!state) return;
        if (contextLevel(element) >= MAX_DEPTH) {
          fail(state, new RangeError("KitJS: structural nesting exceeds " + MAX_DEPTH + " levels"));
          return;
        }
        if (state.kind === "if" ? processIf(element, state) : processFor(element, state)) changed = true;
      });
      if (!changed) return changedAny;
      changedAny = true;
    }
    core.report(new RangeError("KitJS: structural reconciliation exceeds " + MAX_DEPTH + " passes"));
    return changedAny;
  }

  function resetStructures(root) {
    if (!root || root.nodeType !== 1) return;
    var templates = [];
    if (root.matches && root.matches(SELECTOR)) templates.push(root);
    Array.prototype.push.apply(templates, root.querySelectorAll(SELECTOR));
    templates.reverse().forEach(function (template) {
      if (core.ignoredForRuntime(template)) return;
      var record = core.records.get(template);
      var state = record && record.modules.structure;
      if (!state) return;
      if (state.kind === "if") {
        if (state.branch) removeRange(state.branch);
        state.branch = null;
      } else {
        state.rows.forEach(removeRange);
        state.rows.clear();
        state.order = [];
        state.lastList = UNSET;
      }
    });
    core.liveComponents(root).forEach(function (current) {
      current.structures = undefined;
    });
  }

  core.localsFor = localsFor;
  core.disposeTree = disposeTree;
  core.prepareStructureTree = prepareStructureTree;
  core.resetStructures = resetStructures;
  core.reconcileStructures = reconcile;
  core.phase = "structure";
})(document);
