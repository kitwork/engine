; (function (global, document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "directives") throw new Error("KitJS: DOM fragment loaded out of order");
  if (core.reuse) { core.phase = "dom"; return; }

  var OWN = core.OWN;
  var EMPTY = {};
  var EMPTY_SCOPE = Object.freeze(Object.create(null));
  // A binding carries its target in the attribute name — data-kit-bind:disabled, data-kit-bind:aria-expanded —
  // so a CSS selector cannot list them; render targets are found by walking every owned element and
  // reading its attribute names once (see boundNames). One form; the name decides property or
  // attribute, by the three groups of the spec (ideaship-final §5).
  var BIND_PREFIX = "data-kit-bind:";
  // Reflected boolean: the property AND the attribute, so CSS [disabled] and the form both agree.
  var REFLECTED_BOOLEAN = {
    disabled: "disabled", required: "required", readonly: "readOnly", multiple: "multiple", hidden: "hidden", open: "open"
  };
  // Live state: the property only, so a form reset still returns to the authored attribute.
  var LIVE_PROPERTY = { checked: "checked", selected: "selected", value: "value", indeterminate: "indeterminate" };
  var LIVE_BOOLEAN = { checked: true, selected: true, indeterminate: true };
  // HTML lowercases attribute names, so a target written data-kit-bind:viewBox reaches us as
  // "viewbox"; SVG attributes are case-sensitive, so the known mixed-case names are restored on an
  // SVG element.
  var SVG_CASE = {
    viewbox: "viewBox", preserveaspectratio: "preserveAspectRatio", gradientunits: "gradientUnits",
    gradienttransform: "gradientTransform", patternunits: "patternUnits", patterntransform: "patternTransform",
    patterncontentunits: "patternContentUnits", markerwidth: "markerWidth", markerheight: "markerHeight",
    markerunits: "markerUnits", refx: "refX", refy: "refY", textlength: "textLength", lengthadjust: "lengthAdjust",
    stddeviation: "stdDeviation", basefrequency: "baseFrequency", numoctaves: "numOctaves", tablevalues: "tableValues",
    clippathunits: "clipPathUnits", maskunits: "maskUnits", maskcontentunits: "maskContentUnits",
    spreadmethod: "spreadMethod", startoffset: "startOffset", primitiveunits: "primitiveUnits", filterunits: "filterUnits",
    repeatcount: "repeatCount", repeatdur: "repeatDur", keytimes: "keyTimes", keysplines: "keySplines", attributename: "attributeName"
  };
  var UNSAFE_NAMES = {
    srcdoc: true, style: true, innerhtml: true, outerhtml: true, insertadjacenthtml: true,
    textcontent: true, innertext: true, outertext: true
  };

  function elementRecord(element) {
    var record = core.records.get(element);
    if (record) return record;
    record = {
      programs: Object.create(null),
      events: Object.create(null),
      modules: Object.create(null),
      invalid: Object.create(null)
    };
    core.records.set(element, record);
    return record;
  }
  function safeProgram(element, name, mode) {
    var programs = elementRecord(element).programs;
    if (OWN.call(programs, name)) return programs[name];
    try {
      programs[name] = {
        read: core.compile(core.expressionSource(element.getAttribute(name)), mode),
        last: EMPTY
      };
    } catch (error) {
      core.report(error, element, name);
      programs[name] = null;
    }
    return programs[name];
  }

  // The binding attributes an element carries, read once and kept on its record.
  function boundNames(element) {
    var record = elementRecord(element);
    if (record.bound) return record.bound;
    var names = [];
    element.getAttributeNames().forEach(function (name) {
      if (name.indexOf(BIND_PREFIX) === 0) names.push(name);
    });
    record.bound = names;
    return names;
  }
  // ---- data-kit-seed: DOM → state, once — the mirror of data-kit-bind (chốt 21/09) ----
  // The server already rendered the value; seed hands it to state instead of the author writing it
  // a second time into a scope literal. data-kit-seed="key" reads the element's text (or, on a
  // <script type="application/json">, its JSON); data-kit-seed:<name>="key" reads the property or
  // attribute <name> by the same three groups bind writes with, in reverse. The target is a state
  // key, a dotted path, or list[] — one entry per element in document order, the list rebuilt each
  // time a new element of that list appears. It runs once per element (boot, and again only for a
  // new element after a swap). Attribute values stay strings: no type guessing beyond what the
  // element itself says (a boolean property, a numeric input).
  var SEED_PREFIX = "data-kit-seed:";
  var SEED_TARGET = /^([A-Za-z_][A-Za-z0-9_]*)((?:\.[A-Za-z_][A-Za-z0-9_]*)*)(\[\])?$/;
  function seedNames(element) {
    var record = elementRecord(element);
    if (record.seeds) return record.seeds;
    var names = [];
    if (element.hasAttribute("data-kit-seed")) names.push("data-kit-seed");
    element.getAttributeNames().forEach(function (name) {
      if (name.indexOf(SEED_PREFIX) === 0) names.push(name);
    });
    record.seeds = names;
    record.seeded = Object.create(null);
    return names;
  }
  function parseSeedTarget(source) {
    var match = SEED_TARGET.exec(source || "");
    if (!match) throw new SyntaxError("KitJS: data-kit-seed target must be a key, a dotted path, or list[]; got \"" + source + "\"");
    var path = [match[1]].concat(match[2] ? match[2].slice(1).split(".") : []);
    path.forEach(function (segment) {
      if (core.blocked(segment) || core.FORBIDDEN[segment]) throw new SyntaxError("KitJS: data-kit-seed target uses blocked name \"" + segment + "\"");
    });
    return { path: path, list: !!match[3] };
  }
  function checkSeedData(value, seen) {
    if (value === null || typeof value !== "object") return value;
    if (seen.has(value)) throw new TypeError("KitJS: circular seed data");
    seen.add(value);
    Object.keys(value).forEach(function (name) {
      if (core.blockedScopeKey ? core.blockedScopeKey(name) : core.blocked(name)) throw new TypeError("KitJS: blocked seed key \"" + name + "\"");
      checkSeedData(value[name], seen);
    });
    return value;
  }
  function numericInput(element) {
    if (!element.tagName || element.tagName.toLowerCase() !== "input") return false;
    var type = String(element.type || "").toLowerCase();
    return type === "number" || type === "range";
  }
  function readSeed(element, name) {
    if (!name) {
      if (element.tagName && element.tagName.toLowerCase() === "script" &&
        String(element.type || "").toLowerCase() === "application/json") {
        return checkSeedData(JSON.parse(element.textContent), new WeakSet());
      }
      return String(element.textContent || "").trim();
    }
    if (unsafeName(name)) throw new SyntaxError("KitJS: unsafe seed source \"" + name + "\"");
    var reflected = REFLECTED_BOOLEAN[name];
    if (reflected) return !!element[reflected];
    var live = LIVE_PROPERTY[name];
    if (live) {
      if (LIVE_BOOLEAN[name]) return !!element[live];
      if (numericInput(element)) {
        if (element.value === "") return null;
        var number = Number(element.value);
        return Number.isFinite(number) ? number : null;
      }
      return element[live] == null ? "" : String(element[live]);
    }
    if (name.indexOf("-") < 0 && name in element && typeof element[name] !== "function" && typeof element[name] !== "object") {
      return element[name];
    }
    return element.hasAttribute(name) ? element.getAttribute(name) : null;
  }
  function assignSeed(current, target, value) {
    var scope = current.scope;
    var root = target.path[0];
    if (current.componentIdentity && !OWN.call(scope, root)) {
      throw new TypeError("KitJS: data-kit-seed field \"" + root + "\" is not declared by component \"" + current.componentIdentity.name + "\"");
    }
    if (target.path.length === 1) {
      scope[root] = value;
      return;
    }
    var holder = scope[root];
    if (holder === null || typeof holder !== "object" || Array.isArray(holder)) {
      holder = {};
      scope[root] = holder;
    }
    for (var index = 1; index < target.path.length - 1; index++) {
      var next = holder[target.path[index]];
      if (next === null || typeof next !== "object" || Array.isArray(next)) {
        next = {};
        holder[target.path[index]] = next;
      }
      holder = next;
    }
    holder[target.path[target.path.length - 1]] = value;
    core.invalidate(current);
  }
  // seedBoundary seeds the elements a boundary owns, before its bindings render, so a binding that
  // reads the same key sees the DOM's value. A list is rebuilt from every present member when a
  // member that has not been seeded yet is met.
  function seedBoundary(current, plan) {
    var lists = Object.create(null);
    var rebuild = Object.create(null);
    plan.seeds.forEach(function (element) {
      var record = elementRecord(element);
      seedNames(element).forEach(function (attr) {
        try {
          var target = parseSeedTarget(core.expressionSource(element.getAttribute(attr)));
          if (target.list) {
            var key = target.path.join(".");
            if (!record.seeded[attr]) rebuild[key] = true;
            (lists[key] || (lists[key] = [])).push({ element: element, attr: attr, name: attr === "data-kit-seed" ? "" : attr.slice(SEED_PREFIX.length), target: target });
            return;
          }
          if (record.seeded[attr]) return;
          record.seeded[attr] = true;
          assignSeed(current, target, readSeed(element, attr === "data-kit-seed" ? "" : attr.slice(SEED_PREFIX.length)));
        } catch (error) {
          record.seeded[attr] = true;
          core.report(error, element, attr);
        }
      });
    });
    Object.keys(rebuild).forEach(function (key) {
      var members = lists[key];
      var values = [];
      var target = null;
      members.forEach(function (member) {
        try {
          values.push(readSeed(member.element, member.name));
          elementRecord(member.element).seeded[member.attr] = true;
          target = member.target;
        } catch (error) {
          core.report(error, member.element, member.attr);
        }
      });
      if (target) {
        try { assignSeed(current, target, values); } catch (error) { core.report(error, members[0].element, members[0].attr); }
      }
    });
  }
  function unsafeName(name) {
    var lower = name.toLowerCase();
    return /^on/.test(lower) || /^data-kit/.test(lower) || UNSAFE_NAMES[lower] === true;
  }

  function safeURL(name, value) {
    if (["href", "src", "action", "formaction", "poster", "xlink:href"].indexOf(name.toLowerCase()) < 0) {
      return true;
    }
    var text = String(value).replace(/[\u0000-\u0020]+/g, "").toLowerCase();
    return text.indexOf("javascript:") !== 0 && text.indexOf("vbscript:") !== 0 &&
      text.indexOf("data:text/html") !== 0;
  }
  // Attribute-only (aria-*, data-*, any name the element has no property for): setAttribute.
  // null / undefined / false remove it; true is a bare attribute — except on aria-*, which wants the
  // words "true" and "false".
  function writeAttribute(element, name, value) {
    if (!safeURL(name, value)) throw new TypeError("KitJS: unsafe URL binding");
    if (element.namespaceURI === "http://www.w3.org/2000/svg" && SVG_CASE[name]) name = SVG_CASE[name];
    var aria = name.indexOf("aria-") === 0;
    if (value === null || value === undefined || value === false && !aria) {
      if (element.hasAttribute(name)) element.removeAttribute(name);
      return;
    }
    var text = value === true && !aria ? "" : String(value);
    if (element.getAttribute(name) !== text) element.setAttribute(name, text);
  }
  // data-kit-bind:<name>: reflected boolean → property + attribute; live state → property only; any
  // other property the element has → property; a hyphenated name or one it does not have → attribute.
  function writeBinding(element, name, value) {
    if (!safeURL(name, value)) throw new TypeError("KitJS: unsafe URL binding");
    var reflected = REFLECTED_BOOLEAN[name];
    if (reflected) {
      var on = !!value;
      if (element[reflected] !== on) element[reflected] = on;
      if (element.hasAttribute(name) !== on) element.toggleAttribute(name, on);
      return;
    }
    var live = LIVE_PROPERTY[name];
    if (live) {
      var next = LIVE_BOOLEAN[name] ? !!value : value === null || value === undefined ? "" : value;
      if (!core.equal(element[live], next)) element[live] = next;
      return;
    }
    if (name.indexOf("-") < 0 && name in element && typeof element[name] !== "function" && typeof element[name] !== "object") {
      var plain = value === null || value === undefined ? "" : value;
      if (!core.equal(element[name], plain)) element[name] = plain;
      return;
    }
    writeAttribute(element, name, value);
  }
  // readBinding names the attribute on an error it lets through, so the boundary's $error.directive
  // can say which binding failed.
  function readBinding(program, name, scope, element) {
    try {
      return program.read(scope, core.localsFor ? core.localsFor(element) : null);
    } catch (error) {
      if (error && typeof error === "object" && !error.kitDirective) error.kitDirective = name;
      throw error;
    }
  }
  core.readBinding = readBinding;
  function asyncBinding(value) {
    if (!value || typeof value.then !== "function") return false;
    value.then(function () { }, core.report);
    core.report(new TypeError("KitJS: bindings must return synchronously"));
    return true;
  }

  function prepareBoundary(current) {
    if (!current || current.disposed || !current.host || !current.host.isConnected ||
      core.ignoredForRuntime(current.host)) return [];
    core.initialize(current);
    var structuresChanged = core.reconcileStructures && core.reconcileStructures(current);
    core.prepareHooks.forEach(function (prepare) { prepare(current); });
    if (!structuresChanged) return [];
    return core.liveComponents(current.host).filter(function (candidate) {
      return candidate !== current && !candidate.rendered;
    });
  }
  function renderElement(current, element) {
    if (!core.ownsElement(current, element)) return;
    var scope = current.scope;
    var program;
    if (element.hasAttribute("data-kit-text")) {
      program = safeProgram(element, "data-kit-text", "binding");
      if (program) {
        var value = readBinding(program, "data-kit-text", scope, element);
        if (!asyncBinding(value)) {
          var text = value === null || value === undefined ? "" : String(value);
          if (!core.equal(program.last, text)) {
            program.last = text;
            if (element.textContent !== text) element.textContent = text;
          }
        }
      }
    }
    if (element.hasAttribute("data-kit-show")) {
      program = safeProgram(element, "data-kit-show", "binding");
      if (program) {
        var shown = readBinding(program, "data-kit-show", scope, element);
        if (!asyncBinding(shown)) {
          var hidden = !shown;
          if (!core.equal(program.last, hidden)) {
            program.last = hidden;
            if (element.hidden !== hidden) element.hidden = hidden;
          }
        }
      }
    }
    boundNames(element).forEach(function (name) {
      var target = name.slice(BIND_PREFIX.length);
      if (!target || unsafeName(target)) {
        core.report(new SyntaxError("KitJS: unsafe binding target in attribute \"" + name + "\""));
        return;
      }
      var bound = safeProgram(element, name, "binding");
      if (!bound) return;
      var value = readBinding(bound, name, scope, element);
      if (asyncBinding(value) || core.equal(bound.last, value)) return;
      bound.last = value;
      writeBinding(element, target, value);
    });
  }
  function collectRenderPlan(current) {
    var plan = {
      seeds: [],
      bindings: [],
      classes: [],
      styles: [],
      models: []
    };
    core.ownedElements(current, "*").forEach(function (element) {
      if (!element.attributes.length) return;
      if (element.hasAttribute("data-kit-text") || element.hasAttribute("data-kit-show") ||
        boundNames(element).length) plan.bindings.push(element);
      if (seedNames(element).length) plan.seeds.push(element);
      if (element.hasAttribute("data-kit-class")) plan.classes.push(element);
      if (element.hasAttribute("data-kit-style")) plan.styles.push(element);
      if (element.hasAttribute("data-kit-model")) plan.models.push(element);
    });
    return plan;
  }
  function render(records) {
    var initial = Array.isArray(records) ? records : core.liveComponents();
    if (Array.isArray(records)) {
      var order = new Map();
      var depths = new Map();
      initial.forEach(function (current, index) {
        order.set(current, index);
        var depth = 0;
        var ancestor = current.host && current.host.parentElement;
        while (ancestor) {
          if (ancestor.hasAttribute("data-kit-component") || ancestor.hasAttribute("data-kit-scope")) depth++;
          ancestor = ancestor.parentElement;
        }
        depths.set(current, depth);
      });
      initial.sort(function (left, right) {
        return depths.get(left) - depths.get(right) || order.get(left) - order.get(right);
      });
    }
    var queue = [];
    var pending = new Set();
    function enqueue(current) {
      if (!current || current.disposed || !current.host || !current.host.isConnected ||
        core.ignoredForRuntime(current.host) || pending.has(current)) return;
      pending.add(current);
      queue.push(current);
    }
    initial.forEach(enqueue);
    core.renderPending = pending;
    try {
      for (var index = 0; index < queue.length; index++) {
        var current = queue[index];
        pending.delete(current);
        if (current.disposed || !current.host || !current.host.isConnected) continue;
        var children;
        try { children = prepareBoundary(current); }
        catch (error) { core.report(error); children = []; }
        var plan = collectRenderPlan(current);
        if (plan.seeds.length) seedBoundary(current, plan);
        plan.bindings.forEach(function (element) {
          try { renderElement(current, element); } catch (error) { core.report(error, element, error && error.kitDirective); }
        });
        core.renderHooks.forEach(function (renderHook) {
          try { renderHook(current, plan); } catch (error) { core.report(error); }
        });
        current.rendered = true;
        core.flushAfterRender(current);
        children.forEach(enqueue);
      }
    } finally {
      core.renderPending = null;
    }
  }
  function executeAttribute(element, name, locals) {
    if (core.ignoredForRuntime(element)) return false;
    var boundary = core.ownerFor(element);
    var current = core.scopeRecordFor(element);
    if (boundary && !current) return false;
    var program = safeProgram(element, name, "action");
    if (!program) return false;
    if (current) core.initialize(current);
    try {
      if (core.localsFor) locals = core.localsFor(element, locals);
      // The system variables of ideaship-final §3 ride every action: `$this` is the element that
      // owns the attribute, `$host` the boundary element — the nearest component host or
      // data-kit-scope, else <html> — and `$element` the elements that boundary named with
      // data-kit-element. `$event` arrives from the dispatcher in `locals`. `$el` (the old spelling)
      // is gone (B1, 22/09); the name stays reserved.
      var system = Object.create(null);
      if (locals) Object.keys(locals).forEach(function (key) { system[key] = locals[key]; });
      system.$this = element;
      system.$host = boundary || document.documentElement;
      system.$element = core.namedElements(element);
      program.read(current ? current.scope : EMPTY_SCOPE, system, function (value, owner) {
        core.observe(value, owner);
      });
      return true;
    } catch (error) {
      core.report(error, element, name);
      return false;
    }
  }

  // handleError runs a boundary's data-kit-error action with `$error`: the cause, its message, the
  // attribute that was running, and the element it ran on (read through the closed element table).
  function handleError(boundary, error, element, directive) {
    var context = Object.create(null);
    context.cause = error;
    context.message = String(error && error.message || error);
    context.directive = directive || "";
    context.element = element || null;
    var locals = Object.create(null);
    locals.$error = Object.freeze(context);
    var handled = executeAttribute(boundary, "data-kit-error", locals);
    if (handled && core.booting) core.boundaryWrote = true;
    return handled;
  }
  core.handleError = handleError;
  core.elementRecord = elementRecord;
  core.safeProgram = safeProgram;
  core.asyncBinding = asyncBinding;
  core.executeAttribute = executeAttribute;
  core.render = render;
  core.phase = "dom";
})(globalThis, document);
