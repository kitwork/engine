;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "model") throw new Error("KitJS: events fragment loaded out of order");
  if (core.reuse) { core.phase = "events"; return; }

  var OWN = core.OWN;
  // Handlers that listen beyond their own element — :outside, :window, :document — are found by a
  // document walk, so the walk only happens while such a handler exists for the event type.
  var elsewhereActive = Object.create(null);
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
        generation: 0,
        ownsRemoval: false
      } : null;
      if (events[name] && listensElsewhere(descriptor)) {
        elsewhereActive[descriptor.type] = (elsewhereActive[descriptor.type] || 0) + 1;
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
      if (state.ownsRemoval) {
        state.ownsRemoval = false;
        if (core.releaseRemovalOwner) core.releaseRemovalOwner();
      }
      if (listensElsewhere(state.descriptor) && elsewhereActive[state.descriptor.type]) {
        elsewhereActive[state.descriptor.type]--;
      }
    });
  }

  // `$event` is a still picture of the native event — the fields an action reads, frozen at
  // dispatch so a debounced handler sees what happened, not what the browser has since reused the
  // object for. The elements it points at (target, submitter, relatedTarget) are the real ones,
  // read through the same closed element table as `$refs` (ideaship-final §3 names `$event` native;
  // this is the native event as the closed grammar can see it).
  function elementOrNull(value) {
    return value && value.nodeType === 1 ? value : null;
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
      checked: checked,
      target: elementOrNull(target),
      submitter: elementOrNull(event.submitter),
      relatedTarget: elementOrNull(event.relatedTarget)
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

  function connectedOwner(state, element) {
    if (!element || element.ownerDocument !== document || !document.contains(element) ||
      core.ignoredForRuntime(element)) return null;
    var record = core.records.get(element);
    if (!record || record.events[state.descriptor.name] !== state ||
        !element.hasAttribute(state.descriptor.name)) return null;
    return element;
  }

  function scheduleDebounce(state, element, eventSnapshot) {
    if (state.timer) clearTimeout(state.timer);
    else if (!state.ownsRemoval) {
      state.ownsRemoval = true;
      if (core.retainRemovalOwner) core.retainRemovalOwner();
    }
    var generation = ++state.generation;
    var timer = setTimeout(function () {
      if (state.timer !== timer) return;
      state.timer = 0;
      if (state.ownsRemoval) {
        state.ownsRemoval = false;
        if (core.releaseRemovalOwner) core.releaseRemovalOwner();
      }
      if (generation !== state.generation || state.onceDone) return;
      var owner = connectedOwner(state, element);
      if (!owner) return;
      if (core.executeAttribute(owner, state.descriptor.name, locals(eventSnapshot)) &&
          state.descriptor.once) state.onceDone = true;
    }, state.descriptor.delay);
    state.timer = timer;
  }

  function listensElsewhere(descriptor) {
    return descriptor.outside || descriptor.target !== "self";
  }

  // execute is the tail of the fixed pipeline (ideaship-final §4) once the filters in matches()
  // have passed: prevent → stop → timing (debounce or throttle) → run → once.
  function execute(state, element, event, eventSnapshot) {
    var descriptor = state.descriptor;
    if (descriptor.prevent && event.cancelable) event.preventDefault();
    if (descriptor.stop) event.stopPropagation();

    if (descriptor.delay) {
      scheduleDebounce(state, element, eventSnapshot);
      return true;
    }
    if (descriptor.throttle) {
      var now = Date.now();
      if (state.lastRun && now - state.lastRun < descriptor.throttle) return true;
      state.lastRun = now;
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
        if (listensElsewhere(state.descriptor) || !matches(state, element, target, event)) continue;
        execute(state, element, event, eventSnapshot);
        if (state.descriptor.stop) stopped = true;
      }
      if (stopped) return true;
      element = element.parentElement;
    }
    return false;
  }

  // elsewhere runs the handlers that listen beyond their element: :outside when the event landed
  // anywhere but inside it, :window/:document wherever it landed.
  function elsewhere(event, target, eventSnapshot) {
    return Array.prototype.some.call(document.querySelectorAll("*"), function (element) {
      if (core.ignoredForRuntime(element)) return false;
      var inside = element.contains(target);
      var states = eventStates(element, event.type);
      var stopped = false;
      for (var index = 0; index < states.length; index++) {
        var state = states[index];
        if (!listensElsewhere(state.descriptor) || !matches(state, element, target, event)) continue;
        if (state.descriptor.outside && inside) continue;
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
      if (!direct(event, target, eventSnapshot) && elsewhereActive[event.type]) {
        elsewhere(event, target, eventSnapshot);
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
