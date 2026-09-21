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
      core.report(error);
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
        var value = program.read(scope, core.localsFor ? core.localsFor(element) : null);
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
        var shown = program.read(scope, core.localsFor ? core.localsFor(element) : null);
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
      var value = bound.read(scope, core.localsFor ? core.localsFor(element) : null);
      if (asyncBinding(value) || core.equal(bound.last, value)) return;
      bound.last = value;
      writeBinding(element, target, value);
    });
  }
  function collectRenderPlan(current) {
    var plan = {
      bindings: [],
      classes: [],
      styles: [],
      models: []
    };
    core.ownedElements(current, "*").forEach(function (element) {
      if (!element.attributes.length) return;
      if (element.hasAttribute("data-kit-text") || element.hasAttribute("data-kit-show") ||
        boundNames(element).length) plan.bindings.push(element);
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
        plan.bindings.forEach(function (element) {
          try { renderElement(current, element); } catch (error) { core.report(error); }
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
      // owns the attribute (`$el` its compatibility alias), `$host` the boundary element — the
      // nearest component host or data-kit-scope, else <html> — and `$refs` the elements that
      // boundary named with data-kit-ref. `$event` arrives from the dispatcher in `locals`.
      var system = Object.create(null);
      if (locals) Object.keys(locals).forEach(function (key) { system[key] = locals[key]; });
      system.$this = element;
      system.$el = element;
      system.$host = boundary || document.documentElement;
      system.$refs = core.refsFor(element);
      program.read(current ? current.scope : EMPTY_SCOPE, system, function (value, owner) {
        core.observe(value, owner);
      });
      return true;
    } catch (error) {
      core.report(error);
      return false;
    }
  }

  core.elementRecord = elementRecord;
  core.safeProgram = safeProgram;
  core.asyncBinding = asyncBinding;
  core.executeAttribute = executeAttribute;
  core.render = render;
  core.phase = "dom";
})(globalThis, document);
