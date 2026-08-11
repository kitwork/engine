; (function (global, document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "directives") throw new Error("KitJS: DOM fragment loaded out of order");
  if (core.reuse) { core.phase = "dom"; return; }

  var OWN = core.OWN;
  var EMPTY = {};
  var EMPTY_SCOPE = Object.freeze(Object.create(null));
  var BINDINGS = "[data-kit-text],[data-kit-show],[data-kit-bind]";
  var SAFE_PROPERTIES = {
    value: "value",
    checked: "checked",
    selected: "selected",
    disabled: "disabled",
    hidden: "hidden",
    readonly: "readOnly",
    required: "required",
    multiple: "multiple",
    indeterminate: "indeterminate"
  };
  var BOOLEAN_PROPERTIES = {
    checked: true, selected: true, disabled: true, hidden: true,
    readonly: true, required: true, multiple: true, indeterminate: true
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
        read: core.compile(element.getAttribute(name), mode),
        last: EMPTY
      };
    } catch (error) {
      core.report(error);
      programs[name] = null;
    }
    return programs[name];
  }

  function splitTop(source, separators) {
    var output = [];
    var start = 0;
    var depth = 0;
    var quote = "";
    for (var index = 0; index < source.length; index++) {
      var character = source.charAt(index);
      if (quote) {
        if (character === "\\") index++;
        else if (character === quote) quote = "";
      } else if (character === "'" || character === '"') quote = character;
      else if (character === "(" || character === "[" || character === "{") depth++;
      else if (character === ")" || character === "]" || character === "}") depth--;
      else if (depth === 0 && separators.indexOf(character) >= 0) {
        output.push(source.slice(start, index));
        start = index + 1;
      }
    }
    output.push(source.slice(start));
    return output;
  }
  function bindEntries(source) {
    source = source.trim();
    if (source.charAt(0) === "{" && source.charAt(source.length - 1) === "}") {
      source = source.slice(1, -1);
    }
    var entries = [];
    splitTop(source, ",;").forEach(function (part) {
      if (!part.trim()) return;
      var pieces = splitTop(part, ":");
      if (pieces.length < 2) core.syntax("invalid bind entry", source, 0);
      var key = pieces.shift().trim();
      var quoted = /^(['"])([A-Za-z_][A-Za-z0-9_.:-]*)\1$/.exec(key);
      if (quoted) key = quoted[2];
      else if (!/^[A-Za-z_][A-Za-z0-9_.:-]*$/.test(key)) core.syntax("invalid bind name", source, 0);
      var lowerKey = key.toLowerCase();
      if (/^on/i.test(key) || /^data-kit-/i.test(key) ||
        ["srcdoc", "style", "innerhtml", "outerhtml", "insertadjacenthtml",
          "textcontent", "innertext", "outertext"].indexOf(lowerKey) >= 0) {
        core.syntax("unsafe bind name \"" + key + "\"", source, 0);
      }
      entries.push({
        name: key,
        read: core.compile(pieces.join(":").trim(), "binding"),
        last: EMPTY
      });
    });
    if (!entries.length) core.syntax("empty bind map", source, 0);
    return entries;
  }
  function safeBind(element) {
    var programs = elementRecord(element).programs;
    if (OWN.call(programs, "data-kit-bind")) return programs["data-kit-bind"];
    try { programs["data-kit-bind"] = bindEntries(element.getAttribute("data-kit-bind")); }
    catch (error) { core.report(error); programs["data-kit-bind"] = null; }
    return programs["data-kit-bind"];
  }

  function safeURL(name, value) {
    if (["href", "src", "action", "formaction", "poster", "xlink:href"].indexOf(name.toLowerCase()) < 0) {
      return true;
    }
    var text = String(value).replace(/[\u0000-\u0020]+/g, "").toLowerCase();
    return text.indexOf("javascript:") !== 0 && text.indexOf("vbscript:") !== 0 &&
      text.indexOf("data:text/html") !== 0;
  }
  function writeBound(element, name, value) {
    if (!safeURL(name, value)) throw new TypeError("KitJS: unsafe URL binding");
    var lowerName = name.toLowerCase();
    var property = SAFE_PROPERTIES[lowerName];
    if (property) {
      value = BOOLEAN_PROPERTIES[lowerName] ? !!value :
        value === null || value === undefined ? "" : value;
      if (!core.equal(element[property], value)) element[property] = value;
    } else if (value === null || value === undefined || value === false && name.indexOf("aria-") !== 0) {
      if (element.hasAttribute(name)) element.removeAttribute(name);
    } else {
      var text = value === true && name.indexOf("aria-") !== 0 ? "" : String(value);
      if (element.getAttribute(name) !== text) element.setAttribute(name, text);
    }
  }
  function asyncBinding(value) {
    if (!value || typeof value.then !== "function") return false;
    value.then(function () { }, core.report);
    core.report(new TypeError("KitJS: bindings must return synchronously"));
    return true;
  }

  function prepareBoundary(current) {
    if (!current || current.disposed || !current.host || !current.host.isConnected) return [];
    core.initialize(current);
    var structuresChanged = core.reconcileStructures && core.reconcileStructures(current);
    core.prepareHooks.forEach(function (prepare) { prepare(current); });
    if (!structuresChanged) return [];
    return core.liveComponents(current.host).filter(function (candidate) {
      return candidate !== current && !candidate.rendered;
    });
  }
  function renderElement(element) {
    var current = core.scopeRecordFor(element);
    if (!current) return;
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
    if (element.hasAttribute("data-kit-bind")) {
      var entries = safeBind(element);
      if (entries) entries.forEach(function (entry) {
        var bound = entry.read(scope, core.localsFor ? core.localsFor(element) : null);
        if (asyncBinding(bound) || core.equal(entry.last, bound)) return;
        entry.last = bound;
        writeBound(element, entry.name, bound);
      });
    }
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
          if (ancestor.hasAttribute("data-kit-component")) depth++;
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
      if (!current || current.disposed || !current.host || !current.host.isConnected || pending.has(current)) return;
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
        core.ownedElements(current, BINDINGS).forEach(function (element) {
          try { renderElement(element); } catch (error) { core.report(error); }
        });
        core.renderHooks.forEach(function (renderHook) {
          try { renderHook(current); } catch (error) { core.report(error); }
        });
        current.rendered = true;
        children.forEach(enqueue);
      }
    } finally {
      core.renderPending = null;
    }
  }
  function executeAttribute(element, name, locals) {
    var boundary = core.ownerFor(element);
    var current = core.scopeRecordFor(element);
    if (boundary && !current) return false;
    var program = safeProgram(element, name, "action");
    if (!program) return false;
    if (current) core.initialize(current);
    try {
      if (core.localsFor) locals = core.localsFor(element, locals);
      program.read(current ? current.scope : EMPTY_SCOPE, locals, function (value, owner) {
        core.observe(value, owner);
      });
      return true;
    } catch (error) {
      core.report(error);
      return false;
    }
  }

  core.BINDINGS = BINDINGS;
  core.elementRecord = elementRecord;
  core.safeProgram = safeProgram;
  core.asyncBinding = asyncBinding;
  core.executeAttribute = executeAttribute;
  core.render = render;
  core.phase = "dom";
})(globalThis, document);
