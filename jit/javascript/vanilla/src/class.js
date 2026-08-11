;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "structure") throw new Error("KitJS: class loaded out of order");
  if (core.reuse) { core.phase = "class"; return; }

  var SELECTOR = "[data-kit-class]";

  function addTokens(output, source) {
    String(source).trim().split(/\s+/).forEach(function (token) {
      if (token) output[token] = true;
    });
  }

  function classTokens(value) {
    var output = Object.create(null);
    if (value === null || value === undefined || value === false) return output;
    if (typeof value === "string") {
      addTokens(output, value);
      return output;
    }
    if (typeof value !== "object" || Array.isArray(value)) {
      throw new TypeError("KitJS: class binding must return a string or object");
    }
    Object.keys(value).forEach(function (name) {
      if (value[name]) addTokens(output, name);
    });
    return output;
  }

  function classState(element) {
    var modules = core.elementRecord(element).modules;
    if (modules.classes) return modules.classes;
    var fixed = Object.create(null);
    element.classList.forEach(function (name) { fixed[name] = true; });
    modules.classes = { fixed: fixed, owned: Object.create(null) };
    return modules.classes;
  }

  function render(current) {
    core.ownedElements(current, SELECTOR).forEach(function (element) {
      try {
        var program = core.safeProgram(element, "data-kit-class", "binding");
        if (!program) return;
        var value = program.read(current.scope, core.localsFor ? core.localsFor(element) : null);
        if (core.asyncBinding(value)) return;
        var next = classTokens(value);
        var state = classState(element);
        Object.keys(state.owned).forEach(function (name) {
          if (next[name]) return;
          element.classList.remove(name);
          delete state.owned[name];
        });
        Object.keys(next).forEach(function (name) {
          if (state.fixed[name]) return;
          if (state.owned[name]) return;
          if (!element.classList.contains(name)) {
            element.classList.add(name);
            state.owned[name] = true;
          }
        });
      } catch (error) { core.report(error); }
    });
  }

  core.renderHooks.push(render);
  core.phase = "class";
})(document);
