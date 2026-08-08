"use strict";

var expressionConstants = require("../expression/constants.js");
var errors = require("../core/errors.js");
var utils = require("../core/utils.js");
var MODES = expressionConstants.MODES;
var createRuntimeError = errors.createRuntimeError;
var eventPath = utils.eventPath;
var isThenable = utils.isThenable;

var EVENT_TYPES = new Set((
  "click dblclick contextmenu mousedown mouseup mousemove mouseover mouseout mouseenter mouseleave " +
  "pointerdown pointerup pointermove pointerover pointerout pointerenter pointerleave pointercancel " +
  "keydown keyup keypress input change submit reset focus blur focusin focusout scroll resize wheel " +
  "drag dragstart dragend dragenter dragleave dragover drop touchstart touchmove touchend touchcancel " +
  "animationstart animationend animationiteration transitionstart transitionend transitioncancel"
).split(/\s+/));

var DELEGATED_EVENTS = new Set((
  "click dblclick contextmenu mousedown mouseup mousemove mouseover mouseout " +
  "pointerdown pointerup pointermove pointerover pointerout pointercancel " +
  "keydown keyup keypress input change submit reset focusin focusout wheel " +
  "drag dragstart dragend dragenter dragleave dragover drop touchstart touchmove touchend touchcancel " +
  "animationstart animationend animationiteration transitionstart transitionend transitioncancel"
).split(/\s+/));

var KEYBOARD_EVENTS = new Set(["keydown", "keyup", "keypress"]);
var OUTSIDE_EVENTS = new Set(["click", "mousedown", "mouseup", "pointerdown", "pointerup", "touchstart"]);

function createEventManager(runtime) {
  var delegatedInstalled = new Set();
  var delegatedBindings = new Map();
  var outsideBindings = new Map();
  var documentBindings = new Map();
  var windowBindings = new Map();

  function parseAttribute(attributeName) {
    if (attributeName.indexOf("data-kit-") !== 0) return null;
    var raw = attributeName.slice(9);
    var pieces = raw.split(":");
    var type = pieces.shift();
    if (!EVENT_TYPES.has(type)) return null;

    var spec = {
      type: type,
      target: "element",
      outside: false,
      enter: false,
      escape: false,
      prevent: false,
      stop: false,
      once: false,
      debounce: 0,
      throttle: 0
    };

    pieces.forEach(function (modifier) {
      if (modifier === "window" || modifier === "document") {
        if (spec.target !== "element") throw createRuntimeError("KIT_INVALID_MODIFIER", "Only one event target modifier is allowed", {
          attribute: attributeName
        });
        spec.target = modifier;
      } else if (modifier === "outside") spec.outside = true;
      else if (modifier === "enter") spec.enter = true;
      else if (modifier === "escape") spec.escape = true;
      else if (modifier === "prevent") spec.prevent = true;
      else if (modifier === "stop") spec.stop = true;
      else if (modifier === "once") spec.once = true;
      else if (/^debounce\(\d+\)$/.test(modifier)) {
        if (spec.throttle) throw createRuntimeError("KIT_INVALID_MODIFIER", "debounce() and throttle() cannot be combined", { attribute: attributeName });
        spec.debounce = parseInt(modifier.slice(9, -1), 10);
      } else if (/^throttle\(\d+\)$/.test(modifier)) {
        if (spec.debounce) throw createRuntimeError("KIT_INVALID_MODIFIER", "debounce() and throttle() cannot be combined", { attribute: attributeName });
        spec.throttle = parseInt(modifier.slice(9, -1), 10);
      } else {
        throw createRuntimeError("KIT_INVALID_MODIFIER", "Unknown event modifier '" + modifier + "'", {
          attribute: attributeName,
          modifier: modifier
        });
      }
    });

    if ((spec.enter || spec.escape) && !KEYBOARD_EVENTS.has(type)) {
      throw createRuntimeError("KIT_INVALID_MODIFIER", "Keyboard filters are only valid on keyboard events", {
        attribute: attributeName
      });
    }
    if (spec.outside && !OUTSIDE_EVENTS.has(type)) {
      throw createRuntimeError("KIT_INVALID_MODIFIER", ":outside is only valid on pointer/click events", {
        attribute: attributeName
      });
    }
    if (spec.outside && spec.target !== "element") {
      throw createRuntimeError("KIT_INVALID_MODIFIER", ":outside cannot be combined with :window or :document", {
        attribute: attributeName
      });
    }
    return spec;
  }

  function setFor(map, type) {
    var set = map.get(type);
    if (!set) { set = new Set(); map.set(type, set); }
    return set;
  }

  function trackEffect(binding, promise, event) {
    if (!isThenable(promise)) return;
    binding.pendingCount++;
    if (binding.pendingCount === 1) {
      binding.busySnapshot = {
        dataBusyPresent: binding.element.hasAttribute("data-busy"),
        dataBusy: binding.element.getAttribute("data-busy"),
        ariaBusyPresent: binding.element.hasAttribute("aria-busy"),
        ariaBusy: binding.element.getAttribute("aria-busy")
      };
      binding.element.setAttribute("data-busy", "true");
      binding.element.setAttribute("aria-busy", "true");
    }

    Promise.resolve(promise).then(function () {
      settle(binding);
    }, function (error) {
      runtime.reportError(error, runtime.contextFor(binding.element, binding.attributeName, binding.source, "async-action", event));
      settle(binding);
    });
  }

  function settle(binding) {
    binding.pendingCount = Math.max(0, binding.pendingCount - 1);
    if (binding.pendingCount === 0 && binding.busySnapshot) {
      var snapshot = binding.busySnapshot;
      if (snapshot.dataBusyPresent) binding.element.setAttribute("data-busy", snapshot.dataBusy == null ? "" : snapshot.dataBusy);
      else binding.element.removeAttribute("data-busy");
      if (snapshot.ariaBusyPresent) binding.element.setAttribute("aria-busy", snapshot.ariaBusy == null ? "" : snapshot.ariaBusy);
      else binding.element.removeAttribute("aria-busy");
      binding.busySnapshot = null;
    }
    runtime.scheduler.invalidate(binding.app, runtime.boundaryFor(binding.element), {
      type: "async-settle",
      directive: binding.attributeName
    });
  }

  function action(binding, event) {
    if (binding.disabled || binding.consumed && binding.modifiers.once) return { stop: false };
    var spec = binding.modifiers;

    if (spec.enter && event.key !== "Enter") return { stop: false };
    if (spec.escape && event.key !== "Escape" && event.key !== "Esc" && event.keyCode !== 27) return { stop: false };
    if (spec.outside && (binding.element === event.target || binding.element.contains(event.target))) return { stop: false };
    if (spec.outside && runtime.isFresh(binding.element)) return { stop: false };

    if (spec.prevent && event && event.preventDefault) event.preventDefault();
    if (spec.stop && event && event.stopPropagation) event.stopPropagation();
    if (spec.once) binding.consumed = true;

    function execute() {
      try {
        var environment = runtime.environmentFor(binding.element, event || null);
        var result = runtime.expression.execute(binding.compiled, environment);
        runtime.scheduler.invalidate(binding.app, runtime.boundaryFor(binding.element), {
          type: "action",
          directive: binding.attributeName,
          mutations: result.mutations
        });
        result.effects.forEach(function (effect) { trackEffect(binding, effect, event); });
      } catch (error) {
        runtime.reportError(error, runtime.contextFor(binding.element, binding.attributeName, binding.source, "action", event));
      }
    }

    if (spec.debounce > 0) {
      if (binding.timer) clearTimeout(binding.timer);
      binding.timer = setTimeout(function () {
        binding.timer = null;
        execute();
      }, spec.debounce);
    } else if (spec.throttle > 0) {
      var now = Date.now();
      if (!binding.throttleAt || now - binding.throttleAt >= spec.throttle) {
        binding.throttleAt = now;
        execute();
      }
    } else execute();

    return { stop: !!spec.stop };
  }

  function dispatchDelegated(type, event) {
    var path = eventPath(event);
    for (var i = 0; i < path.length; i++) {
      var node = path[i];
      if (!node || node.nodeType !== 1) continue;
      var record = runtime.peekNodeRecord(node);
      if (!record) continue;
      var list = record.eventBindings.get(type);
      if (!list) continue;
      for (var j = 0; j < list.length; j++) {
        var result = action(list[j], event);
        if (result.stop) return;
      }
    }
  }

  function installDelegated(type) {
    if (delegatedInstalled.has(type)) return;
    delegatedInstalled.add(type);
    runtime.listen(runtime.document, type, function (event) {
      dispatchDelegated(type, event);
      var outside = outsideBindings.get(type);
      if (outside) Array.from(outside).forEach(function (binding) { action(binding, event); });
      var documentSet = documentBindings.get(type);
      if (documentSet) Array.from(documentSet).forEach(function (binding) { action(binding, event); });
    }, false);
  }

  function installWindow(type) {
    var key = "window:" + type;
    if (delegatedInstalled.has(key)) return;
    delegatedInstalled.add(key);
    runtime.listen(runtime.global, type, function (event) {
      var set = windowBindings.get(type);
      if (set) Array.from(set).forEach(function (binding) { action(binding, event); });
    }, false);
  }

  function register(binding, spec) {
    binding.mode = MODES.ACTION;
    binding.eventType = spec.type;
    binding.modifiers = spec;

    if (spec.target === "window") {
      setFor(windowBindings, spec.type).add(binding);
      installWindow(spec.type);
      return;
    }
    if (spec.target === "document") {
      setFor(documentBindings, spec.type).add(binding);
      installDelegated(spec.type);
      return;
    }
    if (spec.outside) {
      setFor(outsideBindings, spec.type).add(binding);
      installDelegated(spec.type);
      return;
    }
    if (DELEGATED_EVENTS.has(spec.type)) {
      var record = runtime.nodeRecord(binding.element, binding.app);
      var list = record.eventBindings.get(spec.type);
      if (!list) { list = []; record.eventBindings.set(spec.type, list); }
      list.push(binding);
      setFor(delegatedBindings, spec.type).add(binding);
      installDelegated(spec.type);
      return;
    }

    var listener = function (event) { action(binding, event); };
    binding.element.addEventListener(spec.type, listener, false);
    binding.directListenerCleanup = function () {
      binding.element.removeEventListener(spec.type, listener, false);
    };
  }

  function unregister(binding) {
    if (binding.timer) clearTimeout(binding.timer);
    binding.timer = null;
    if (binding.directListenerCleanup) binding.directListenerCleanup();
    binding.directListenerCleanup = null;

    var spec = binding.modifiers;
    if (!spec) return;
    [delegatedBindings, outsideBindings, documentBindings, windowBindings].forEach(function (map) {
      var set = map.get(spec.type);
      if (set) set.delete(binding);
    });
    var record = runtime.peekNodeRecord(binding.element);
    if (record) {
      var list = record.eventBindings.get(spec.type);
      if (list) {
        var index = list.indexOf(binding);
        if (index >= 0) list.splice(index, 1);
        if (!list.length) record.eventBindings.delete(spec.type);
      }
    }
  }

  return {
    parseAttribute: parseAttribute,
    register: register,
    unregister: unregister,
    action: action,
    eventTypes: EVENT_TYPES
  };
}

module.exports = {
  createEventManager: createEventManager,
  EVENT_TYPES: EVENT_TYPES
};
