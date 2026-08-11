"use strict";

var utils = require("../core/utils.js");
var hasOwn = utils.hasOwn;

function createModelManager(runtime) {
  function inputType(element) {
    return String(element.type || "").toLowerCase();
  }

  function readValue(binding, event) {
    var element = binding.element;
    var tag = String(element.tagName || "").toLowerCase();
    var type = inputType(element);

    if (type === "file") return element.files;
    if (type === "number" || type === "range") {
      if (element.value === "") return null;
      var number = Number(element.value);
      return Number.isFinite(number) ? number : null;
    }
    if (type === "checkbox") {
      var environment = runtime.environmentFor(element, event || null);
      var current;
      try { current = runtime.expression.evaluate(binding.compiled, environment); }
      catch (_) { current = undefined; }
      if (Array.isArray(current)) {
        var next = current.slice();
        var index = next.indexOf(element.value);
        if (element.checked && index < 0) next.push(element.value);
        if (!element.checked && index >= 0) next.splice(index, 1);
        return next;
      }
      return !!element.checked;
    }
    if (type === "radio") return element.checked ? element.value : undefined;
    if (tag === "select" && element.multiple) {
      return Array.prototype.slice.call(element.options || []).filter(function (option) {
        return option.selected;
      }).map(function (option) { return option.value; });
    }
    return element.value == null ? "" : String(element.value);
  }

  function writeValue(element, value) {
    var tag = String(element.tagName || "").toLowerCase();
    var type = inputType(element);

    if (type === "file") return;
    if (type === "checkbox") {
      if (Array.isArray(value)) element.checked = value.indexOf(element.value) >= 0;
      else element.checked = !!value;
      return;
    }
    if (type === "radio") {
      element.checked = value != null && String(value) === String(element.value);
      return;
    }
    if (tag === "select" && element.multiple) {
      var selected = Array.isArray(value) ? value.map(String) : [];
      Array.prototype.slice.call(element.options || []).forEach(function (option) {
        option.selected = selected.indexOf(String(option.value)) >= 0;
      });
      return;
    }

    var next = value == null ? "" : String(value);
    if (element.value !== next) element.value = next;
  }

  function seedIfMissing(binding) {
    if (binding.modelSeeded) return;
    binding.modelSeeded = true;
    var environment = runtime.environmentFor(binding.element, null);
    var current;
    try { current = runtime.expression.evaluate(binding.compiled, environment); }
    catch (_) { current = undefined; }
    if (current !== undefined) return;
    var value = readValue(binding, null);
    if (value === undefined) return;
    try {
      runtime.expression.assign(binding.compiled, environment, value);
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(binding.element, "model", binding.source, "model-seed"));
    }
  }

  function mount(binding) {
    seedIfMissing(binding);
    runtime.modelBindings.add(binding);
  }

  function update(binding, value) {
    var record = runtime.nodeRecord(binding.element, binding.app);
    if (record.composing) return;
    writeValue(binding.element, value);
  }

  function unmount(binding) {
    runtime.modelBindings.delete(binding);
  }

  function commit(element, event) {
    var record = runtime.peekNodeRecord(element);
    if (!record || record.composing) return;
    var binding = null;
    record.bindings.forEach(function (candidate) {
      if (candidate.directiveName === "model") binding = candidate;
    });
    if (!binding || binding.disabled) return;

    var value = readValue(binding, event);
    if (value === undefined) return;
    try {
      var environment = runtime.environmentFor(element, event || null);
      runtime.expression.assign(binding.compiled, environment, value);
      runtime.scheduler.invalidate(binding.app, runtime.boundaryFor(element), {
        type: "model",
        source: binding.source,
        value: value
      });
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(element, "model", binding.source, "input", event));
    }
  }

  function handleInput(event) {
    var element = event.target;
    if (!element || !element.getAttribute) return;
    var record = runtime.peekNodeRecord(element);
    if (!record) return;
    var hasModel = false;
    record.bindings.forEach(function (binding) {
      if (binding.directiveName === "model") hasModel = true;
    });
    if (!hasModel) return;
    commit(element, event);
  }

  function install(document) {
    runtime.listen(document, "compositionstart", function (event) {
      var record = runtime.peekNodeRecord(event.target);
      if (record) record.composing = true;
    }, true);
    runtime.listen(document, "compositionend", function (event) {
      var record = runtime.peekNodeRecord(event.target);
      if (record) record.composing = false;
      commit(event.target, event);
    }, true);
    runtime.listen(document, "input", handleInput, true);
    runtime.listen(document, "change", handleInput, true);
  }

  return {
    mount: mount,
    update: update,
    unmount: unmount,
    commit: commit,
    install: install,
    readValue: readValue,
    writeValue: writeValue
  };
}

module.exports = {
  createModelManager: createModelManager
};
