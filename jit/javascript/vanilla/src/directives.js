;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "component") throw new Error("KitJS: directives loaded out of order");
  if (core.reuse) { core.phase = "directives"; return; }

  var EVENTS = Object.create(null);
  var MODIFIERS = Object.create(null);
  var RESERVED = Object.create(null);
  var OUTSIDE = Object.create(null);
  var EVENT_NAMES = (
    "click dblclick submit input change keydown keyup pointerdown pointerup focusin focusout"
  ).split(" ");

  EVENT_NAMES.forEach(function (name) { EVENTS[name] = true; });
  "self prevent stop once outside enter escape".split(" ").forEach(function (name) {
    MODIFIERS[name] = true;
  });
  "component version as retain ignore text show bind class model if for key".split(" ").forEach(function (name) {
    RESERVED[name] = true;
  });
  "click dblclick pointerdown pointerup focusin".split(" ").forEach(function (name) {
    OUTSIDE[name] = true;
  });

  function directiveError(message, name) {
    throw new SyntaxError("KitJS: " + message + " in attribute \"" + name + "\"");
  }

  function parseEventAttribute(name) {
    if (typeof name !== "string" || name.indexOf("data-kit-") !== 0) return null;
    var source = name.slice(9);
    var parts = source.split(":");
    var type = parts.shift();
    if (!EVENTS[type]) {
      if (RESERVED[type]) {
        if (parts.length) directiveError("directive does not accept modifiers", name);
        return null;
      }
      directiveError("unsupported directive", name);
    }

    var seen = Object.create(null);
    var descriptor = {
      name: name,
      type: type,
      self: false,
      prevent: false,
      stop: false,
      once: false,
      outside: false,
      key: "",
      delay: 0
    };

    parts.forEach(function (modifier) {
      if (!modifier) directiveError("empty event modifier", name);
      var canonical = modifier;
      var debounce = /^debounce\(([0-9]+)\)$/.exec(modifier);
      if (debounce) canonical = "debounce";
      else if (!MODIFIERS[modifier]) directiveError("unsupported event modifier \"" + modifier + "\"", name);
      if (seen[canonical]) directiveError("duplicate event modifier \"" + canonical + "\"", name);
      seen[canonical] = true;

      if (canonical === "debounce") {
        var delay = Number(debounce[1]);
        if (!Number.isInteger(delay) || delay < 1 || delay > 60000) {
          directiveError("debounce delay must be between 1 and 60000", name);
        }
        descriptor.delay = delay;
      } else if (canonical === "enter" || canonical === "escape") {
        if (type !== "keydown" && type !== "keyup") {
          directiveError("keyboard modifier requires keydown or keyup", name);
        }
        if (descriptor.key) directiveError("event cannot use both enter and escape", name);
        descriptor.key = canonical === "enter" ? "Enter" : "Escape";
      } else descriptor[canonical] = true;
    });

    if (descriptor.outside && !OUTSIDE[type]) {
      directiveError("outside is not supported for this event", name);
    }
    if (descriptor.outside && descriptor.self) {
      directiveError("outside and self cannot be combined", name);
    }
    return descriptor;
  }

  core.eventTypes = EVENT_NAMES;
  core.outsideEventTypes = OUTSIDE;
  core.parseEventAttribute = parseEventAttribute;
  core.prepareHooks = [];
  core.renderHooks = [];
  core.phase = "directives";
})(document);
