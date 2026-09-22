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
  // The modifiers of ideaship-final §4, run in its fixed order whatever order the author wrote:
  // target (window document) → filter (outside escape enter, and self) → prevent → stop →
  // timing (debounce(n) throttle(n)) → once → run.
  "self prevent stop once outside enter escape window document".split(" ").forEach(function (name) {
    MODIFIERS[name] = true;
  });
  "component scope version alias element retain drive ignore text show bind seed class style model if for key error".split(" ").forEach(function (name) {
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
      // data-kit-bind:<name> / data-kit-seed:<name> carry their target after the colon; not events.
      if (type === "bind" || type === "seed") return null;
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
      target: "self",
      self: false,
      prevent: false,
      stop: false,
      once: false,
      outside: false,
      key: "",
      delay: 0,
      throttle: 0
    };

    parts.forEach(function (modifier) {
      if (!modifier) directiveError("empty event modifier", name);
      var canonical = modifier;
      var timing = /^(debounce|throttle)\(([0-9]+)\)$/.exec(modifier);
      if (timing) canonical = timing[1];
      else if (!MODIFIERS[modifier]) directiveError("unsupported event modifier \"" + modifier + "\"", name);
      if (seen[canonical]) directiveError("duplicate event modifier \"" + canonical + "\"", name);
      seen[canonical] = true;

      if (canonical === "debounce" || canonical === "throttle") {
        var delay = Number(timing[2]);
        if (!Number.isInteger(delay) || delay < 1 || delay > 60000) {
          directiveError(canonical + " delay must be between 1 and 60000", name);
        }
        if (canonical === "debounce") descriptor.delay = delay; else descriptor.throttle = delay;
      } else if (canonical === "window" || canonical === "document") {
        if (descriptor.target !== "self") directiveError("event cannot use both window and document", name);
        descriptor.target = canonical;
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
    if (descriptor.self && descriptor.target !== "self") {
      directiveError("self and " + descriptor.target + " cannot be combined", name);
    }
    if (descriptor.outside && descriptor.target !== "self") {
      directiveError("outside already listens beyond the element; " + descriptor.target + " is redundant", name);
    }
    if (descriptor.delay && descriptor.throttle) {
      directiveError("debounce and throttle cannot both time one handler", name);
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
