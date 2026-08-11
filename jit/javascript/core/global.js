// KitJS global and assembly capsule.
// Classic browser script: load before every other core fragment.
(function (window, document) {
  "use strict";

  var RUNTIME_VERSION = "0.1.0-preview.1";
  var kit = window.kit;
  var reuse = false;

  if (kit && typeof kit.component === "function") {
    if (kit.version !== RUNTIME_VERSION) {
      throw new Error("KitJS runtime conflict: found " + (kit.version || "an unknown version") + ", expected " + RUNTIME_VERSION);
    }
    reuse = true;
  }
  if (kit !== undefined && (kit === null || (typeof kit !== "object" && typeof kit !== "function"))) {
    throw new Error("KitJS cannot install because globalThis.kit is not an object");
  }

  kit = kit || {};
  window.kit = kit;
  if (!reuse) {
    Object.defineProperty(kit, "version", {
      value: RUNTIME_VERSION,
      enumerable: true,
      configurable: false,
      writable: false
    });
  }
  if (Object.prototype.hasOwnProperty.call(kit, "__kitwork_core__")) {
    throw new Error("KitJS core assembly is already in progress");
  }

  var core = {
    phase: "global",
    reuse: reuse,
    version: RUNTIME_VERSION,
    startHooks: [],
    destroyHooks: []
  };
  Object.defineProperty(kit, "__kitwork_core__", {
    value: core,
    enumerable: false,
    configurable: true,
    writable: false
  });
  if (reuse) return;

  var OWN = Object.prototype.hasOwnProperty;
  var elementStates = new WeakMap();

  function words(source) {
    var output = Object.create(null);
    source.split(" ").forEach(function (word) { if (word) output[word] = true; });
    return output;
  }

  var BLOCKED = words(
    "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
    "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
    "window globalThis top parent self"
  );
  var FORBIDDEN = words(
    "var let const function class return if else for while do switch case new " +
    "delete void typeof instanceof in await yield throw try catch finally import export"
  );
  var RESERVED = words("kit $element $host $event $refs $component $parent $error $alias $invalidate");
  var INSTANCE_RESERVED = words("kit $host $refs $parent $alias $invalidate");
  var EVENT_TYPES = words("click dblclick submit input change keydown keyup pointerdown pointerup focusin focusout");
  var HTML_BOOLEAN = words("disabled checked selected readonly required multiple hidden open autofocus autoplay controls loop muted novalidate formnovalidate itemscope default reversed");
  var BIND_DENY = words("class style srcdoc");
  var URL_ATTRIBUTES = words("href src action formaction poster xlink:href");
  var UNSET = {};
  var INVALID_MEMBER = {};
  var ASYNC_BINDING = {};

  function blocked(key) {
    return typeof key === "string" && BLOCKED[key] === true;
  }

  function blockedMember(key) {
    return blocked(key) || (typeof key === "string" && INSTANCE_RESERVED[key] === true);
  }

  function memberKey(value) {
    if (typeof value === "number") {
      if (!Number.isFinite(value)) return INVALID_MEMBER;
      value = String(value);
    } else if (typeof value !== "string") return INVALID_MEMBER;
    return blockedMember(value) ? INVALID_MEMBER : value;
  }

  function state(element) {
    var value = elementStates.get(element);
    if (!value) {
      value = {};
      elementStates.set(element, value);
    }
    return value;
  }

  function peek(element) {
    return elementStates.get(element) || null;
  }

  function resetElementStates() {
    elementStates = new WeakMap();
  }

  function deleteElementState(element) {
    elementStates.delete(element);
  }

  function warn(code, detail) {
    if (window.console && typeof window.console.warn === "function") {
      window.console.warn("[kit] " + code, detail || "");
    }
  }

  function report(error, context) {
    context = context || {};
    var handled = false;
    if (context.component && typeof context.component.error === "function") {
      try { handled = context.component.error(error, context) === true; }
      catch (componentError) { error = componentError; }
    }
    if (!handled && window.console && typeof window.console.error === "function") {
      window.console.error("[kit:error]", error, context);
    }
    try {
      document.dispatchEvent(new CustomEvent("kit:error", {
        detail: { error: error, context: context }
      }));
    } catch (_) {}
  }

  core.OWN = OWN;
  core.BLOCKED = BLOCKED;
  core.FORBIDDEN = FORBIDDEN;
  core.RESERVED = RESERVED;
  core.INSTANCE_RESERVED = INSTANCE_RESERVED;
  core.EVENT_TYPES = EVENT_TYPES;
  core.HTML_BOOLEAN = HTML_BOOLEAN;
  core.BIND_DENY = BIND_DENY;
  core.URL_ATTRIBUTES = URL_ATTRIBUTES;
  core.UNSET = UNSET;
  core.INVALID_MEMBER = INVALID_MEMBER;
  core.ASYNC_BINDING = ASYNC_BINDING;
  core.blocked = blocked;
  core.blockedMember = blockedMember;
  core.memberKey = memberKey;
  core.state = state;
  core.peek = peek;
  core.resetElementStates = resetElementStates;
  core.deleteElementState = deleteElementState;
  core.warn = warn;
  core.report = report;

})(window, document);
