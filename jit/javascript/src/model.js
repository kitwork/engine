;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "style") throw new Error("KitJS: model loaded out of order");
  if (core.reuse) { core.phase = "model"; return; }

  var OWN = core.OWN;
  var SELECTOR = "[data-kit-model]";
  var composing = new WeakSet();

  function controlKind(element) {
    var tag = element.tagName && element.tagName.toLowerCase();
    if (tag === "textarea") return "text";
    if (tag === "select") return element.multiple ? "select-multiple" : "select";
    if (tag !== "input") return "";
    var type = (element.type || "text").toLowerCase();
    if (type === "checkbox" || type === "radio" || type === "number" || type === "range") return type;
    if (["button", "submit", "reset", "image", "file", "hidden"].indexOf(type) >= 0) return "";
    return "text";
  }

  function modelState(element) {
    var modules = core.elementRecord(element).modules;
    if (OWN.call(modules, "model")) return modules.model;
    var source = (element.getAttribute("data-kit-model") || "").trim();
    if (source.charAt(0) === "$") {
      core.report(new SyntaxError("KitJS: model cannot use the reserved $ namespace"));
      modules.model = null;
      return null;
    }
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(source) || core.blocked(source)) {
      core.report(new SyntaxError("KitJS: model must name one component field"));
      modules.model = null;
      return null;
    }
    if (!controlKind(element)) {
      core.report(new TypeError("KitJS: model requires a supported form control"));
      modules.model = null;
      return null;
    }
    modules.model = { name: source, failed: false };
    return modules.model;
  }

  function writable(scope, state) {
    if (state.failed) return null;
    var descriptor = Object.getOwnPropertyDescriptor(scope, state.name);
    if (!descriptor || !OWN.call(descriptor, "value") || !descriptor.writable) {
      state.failed = true;
      core.report(new TypeError("KitJS: model field \"" + state.name + "\" is not writable"));
      return null;
    }
    return descriptor;
  }

  function setValue(element, kind, value) {
    if (kind === "checkbox") {
      var checked = Array.isArray(value) ? value.some(function (item) {
        return String(item) === element.value;
      }) : !!value;
      if (element.checked !== checked) element.checked = checked;
      return;
    }
    if (kind === "radio") {
      var selected = value !== null && value !== undefined && String(value) === element.value;
      if (element.checked !== selected) element.checked = selected;
      return;
    }
    if (kind === "select-multiple") {
      var selectedValues = Array.isArray(value) ? value.map(String) : [];
      Array.prototype.forEach.call(element.options, function (option) {
        var selected = selectedValues.indexOf(option.value) >= 0;
        if (option.selected !== selected) option.selected = selected;
      });
      return;
    }
    var text = value === null || value === undefined ? "" : String(value);
    if (element.value !== text) element.value = text;
  }

  function eventValue(element, kind, current) {
    if (kind === "checkbox") {
      if (!Array.isArray(current)) return !!element.checked;
      var next = current.slice();
      var found = -1;
      next.some(function (item, index) {
        if (String(item) !== element.value) return false;
        found = index;
        return true;
      });
      if (element.checked && found < 0) next.push(element.value);
      else if (!element.checked && found >= 0) next.splice(found, 1);
      return next;
    }
    if (kind === "radio") return element.checked ? element.value : current;
    if (kind === "select-multiple") {
      return Array.prototype.filter.call(element.options, function (option) {
        return option.selected;
      }).map(function (option) { return option.value; });
    }
    if (kind === "number" || kind === "range") {
      if (element.value === "") return null;
      var number = Number(element.value);
      return Number.isFinite(number) ? number : null;
    }
    return element.value;
  }

  function expectedEvent(kind) {
    return kind === "checkbox" || kind === "radio" || kind === "select" || kind === "select-multiple" ?
      "change" : "input";
  }

  function update(element, eventType, force) {
    if (!element || !element.hasAttribute || !element.hasAttribute("data-kit-model")) return false;
    if (core.ignoredForRuntime(element)) return false;
    var state = modelState(element);
    if (!state || state.failed) return false;
    var kind = controlKind(element);
    if (!force && expectedEvent(kind) !== eventType) return false;
    if (kind === "text" && composing.has(element)) return false;
    var current = core.scopeRecordFor(element);
    if (!current || !writable(current.scope, state)) return false;
    core.initialize(current);
    var before = Reflect.get(current.scope, state.name, current.scope);
    var value = eventValue(element, kind, before);
    if (kind === "radio" && !element.checked) return false;
    if (!Reflect.set(current.scope, state.name, value, current.scope)) {
      core.report(new TypeError("KitJS: model field \"" + state.name + "\" rejected a write"));
      return false;
    }
    return true;
  }

  function render(current) {
    core.ownedElements(current, SELECTOR).forEach(function (element) {
      try {
        var state = modelState(element);
        if (!state || !writable(current.scope, state)) return;
        setValue(element, controlKind(element), Reflect.get(current.scope, state.name, current.scope));
      } catch (error) { core.report(error); }
    });
  }

  core.renderHooks.push(render);
  core.updateModel = update;
  core.modelCompositionStart = function (element) {
    if (element && element.hasAttribute && element.hasAttribute("data-kit-model") &&
      !core.ignoredForRuntime(element)) composing.add(element);
  };
  core.modelCompositionEnd = function (element) {
    if (!element || core.ignoredForRuntime(element)) return;
    composing.delete(element);
    update(element, "input", true);
  };
  core.phase = "model";
})(document);
