;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "model") throw new Error("KitJS: events fragment loaded out of order");
  if (core.reuse) { core.phase = "events"; return; }

  var OWN = core.OWN;
  var outsideActive = Object.create(null);
  var prepared = false;

  function validMetadata(element) {
    if (core.ignoredForRuntime(element)) return false;
    function valid(candidate) {
      return (!core.componentMetadata || core.componentMetadata(candidate, true) !== null) &&
        (!core.scopeSeed || core.scopeSeed(candidate, true) !== null);
    }
    if (!valid(element)) return false;
    var boundary = core.ownerFor && core.ownerFor(element);
    return !boundary || boundary === element || valid(boundary);
  }

  function eventElement(event) {
    var target = event.target;
    return target && target.nodeType === 1 ? target : target && target.parentElement;
  }

  function safeEvent(element, name) {
    if (core.ignoredForRuntime(element)) return null;
    if (!validMetadata(element)) return null;
    var events = core.elementRecord(element).events;
    if (OWN.call(events, name)) return events[name];
    try {
      var descriptor = core.parseEventAttribute(name);
      if (!descriptor) return null;
      var program = core.safeProgram(element, name, "action");
      events[name] = program ? {
        descriptor: descriptor,
        program: program,
        onceDone: false,
        timer: 0,
        generation: 0
      } : null;
      if (events[name] && descriptor.outside) {
        outsideActive[descriptor.type] = (outsideActive[descriptor.type] || 0) + 1;
      }
    } catch (error) {
      core.report(error);
      events[name] = null;
    }
    return events[name];
  }

  function eventStates(element, type) {
    if (core.ignoredForRuntime(element)) return [];
    var prefix = "data-kit-" + type;
    var output = [];
    element.getAttributeNames().forEach(function (name) {
      if (name !== prefix && name.indexOf(prefix + ":") !== 0) return;
      var state = safeEvent(element, name);
      if (state) output.push(state);
    });
    return output;
  }

  function validateElement(element) {
    if (core.ignoredForRuntime(element)) return;
    var record = core.records.get(element);
    if (!validMetadata(element)) {
      if (!record) record = core.elementRecord(element);
      if (element.hasAttribute("data-kit-component")) record.invalid["data-kit-component"] = true;
      if (element.hasAttribute("data-kit-version")) record.invalid["data-kit-version"] = true;
      if (element.hasAttribute("data-kit-scope")) record.invalid["data-kit-scope"] = true;
      return;
    }
    element.getAttributeNames().forEach(function (name) {
      if (name.indexOf("data-kit-") !== 0 || record && OWN.call(record.invalid, name)) return;
      try {
        var descriptor = core.parseEventAttribute(name);
        if (descriptor) safeEvent(element, name);
      } catch (error) {
        core.report(error);
        if (!record) record = core.elementRecord(element);
        record.invalid[name] = true;
      }
    });
  }

  function prepare() {
    if (prepared) return;
    prepared = true;
    if (core.prepareComponentTree) core.prepareComponentTree(document);
    document.querySelectorAll("*").forEach(validateElement);
  }

  function prepareTree(root) {
    if (!root || root.nodeType === 1 && core.ignoredForRuntime(root)) return;
    if (core.prepareComponentTree) core.prepareComponentTree(root);
    if (root.nodeType === 1) validateElement(root);
    if (root.querySelectorAll) root.querySelectorAll("*").forEach(validateElement);
  }

  function disposeElement(element) {
    var record = core.records.get(element);
    if (!record) return;
    Object.keys(record.events).forEach(function (name) {
      var state = record.events[name];
      if (!state) return;
      if (state.timer) clearTimeout(state.timer);
      state.timer = 0;
      state.generation++;
      if (state.descriptor.outside && outsideActive[state.descriptor.type]) {
        outsideActive[state.descriptor.type]--;
      }
    });
  }

  function snapshot(event, target) {
    var value = null;
    if (target && "value" in target) {
      var candidate = target.value;
      if (candidate === null || typeof candidate === "string" || typeof candidate === "boolean" ||
          typeof candidate === "number" && Number.isFinite(candidate)) value = candidate;
    }
    var checked = target && "checked" in target ? !!target.checked : false;
    var output = Object.create(null);
    Object.assign(output, {
      type: String(event.type || ""),
      key: typeof event.key === "string" ? event.key : "",
      code: typeof event.code === "string" ? event.code : "",
      button: typeof event.button === "number" ? event.button : 0,
      buttons: typeof event.buttons === "number" ? event.buttons : 0,
      clientX: typeof event.clientX === "number" ? event.clientX : 0,
      clientY: typeof event.clientY === "number" ? event.clientY : 0,
      detail: typeof event.detail === "number" ? event.detail : 0,
      ctrlKey: !!event.ctrlKey,
      shiftKey: !!event.shiftKey,
      altKey: !!event.altKey,
      metaKey: !!event.metaKey,
      repeat: !!event.repeat,
      isComposing: !!event.isComposing,
      value: value,
      checked: checked
    });
    return Object.freeze(output);
  }

  function locals(eventSnapshot) {
    var output = Object.create(null);
    output.$event = eventSnapshot;
    return output;
  }

  function matches(state, element, target, event) {
    var descriptor = state.descriptor;
    if (state.onceDone) return false;
    if (descriptor.self && target !== element) return false;
    if (descriptor.key && (event.isComposing || event.keyCode === 229 || event.key !== descriptor.key)) {
      return false;
    }
    return true;
  }

  function connectedOwner(state) {
    var owner = null;
    Array.prototype.some.call(document.querySelectorAll("*"), function (element) {
      if (core.ignoredForRuntime(element)) return false;
      var record = core.records.get(element);
      if (!record || record.events[state.descriptor.name] !== state ||
          !element.hasAttribute(state.descriptor.name)) return false;
      owner = element;
      return true;
    });
    return owner;
  }

  function scheduleDebounce(state, eventSnapshot) {
    if (state.timer) clearTimeout(state.timer);
    var generation = ++state.generation;
    state.timer = setTimeout(function () {
      state.timer = 0;
      if (generation !== state.generation || state.onceDone) return;
      var owner = connectedOwner(state);
      if (!owner) return;
      if (core.executeAttribute(owner, state.descriptor.name, locals(eventSnapshot)) &&
          state.descriptor.once) state.onceDone = true;
    }, state.descriptor.delay);
  }

  function execute(state, element, event, eventSnapshot) {
    var descriptor = state.descriptor;
    if (descriptor.prevent && event.cancelable) event.preventDefault();
    if (descriptor.stop) event.stopPropagation();

    if (descriptor.delay) {
      scheduleDebounce(state, eventSnapshot);
      return true;
    }

    var success = core.executeAttribute(element, descriptor.name, locals(eventSnapshot));
    if (success && descriptor.once) state.onceDone = true;
    return success;
  }

  function direct(event, target, eventSnapshot) {
    var element = target;
    while (element && element !== document) {
      var states = eventStates(element, event.type);
      var stopped = false;
      for (var index = 0; index < states.length; index++) {
        var state = states[index];
        if (state.descriptor.outside || !matches(state, element, target, event)) continue;
        execute(state, element, event, eventSnapshot);
        if (state.descriptor.stop) stopped = true;
      }
      if (stopped) return true;
      element = element.parentElement;
    }
    return false;
  }

  function outside(event, target, eventSnapshot) {
    return Array.prototype.some.call(document.querySelectorAll("*"), function (element) {
      if (core.ignoredForRuntime(element)) return false;
      if (element.contains(target)) return false;
      var states = eventStates(element, event.type);
      var stopped = false;
      for (var index = 0; index < states.length; index++) {
        var state = states[index];
        if (!state.descriptor.outside || !matches(state, element, target, event)) continue;
        execute(state, element, event, eventSnapshot);
        if (state.descriptor.stop) stopped = true;
      }
      return stopped;
    });
  }

  function dispatch(event) {
    try {
      var target = eventElement(event);
      if (!target || core.ignoredForRuntime(target)) return;
      if (event.type === "input" || event.type === "change") core.updateModel(target, event.type, false);
      var eventSnapshot = snapshot(event, target);
      if (!direct(event, target, eventSnapshot) && outsideActive[event.type]) {
        outside(event, target, eventSnapshot);
      }
    } catch (error) { core.report(error); }
  }

  core.prepareHooks.push(prepare);
  core.prepareEventTree = prepareTree;
  core.disposeElementEvents = disposeElement;
  core.installEvents = function () {
    core.eventTypes.forEach(function (type) { document.addEventListener(type, dispatch); });
    document.addEventListener("compositionstart", function (event) {
      core.modelCompositionStart(eventElement(event));
    });
    document.addEventListener("compositionend", function (event) {
      core.modelCompositionEnd(eventElement(event));
    });
  };
  core.phase = "events";
})(document);
