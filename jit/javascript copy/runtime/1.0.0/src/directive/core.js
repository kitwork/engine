"use strict";

var expressionConstants = require("../expression/constants.js");
var errors = require("../core/errors.js");
var utils = require("../core/utils.js");
var MODES = expressionConstants.MODES;
var createRuntimeError = errors.createRuntimeError;
var normalizeScalar = utils.normalizeScalar;
var hasOwn = utils.hasOwn;

var HTML_BOOLEAN_ATTRIBUTES = new Set([
  "allowfullscreen", "async", "autofocus", "autoplay", "checked", "controls", "default",
  "defer", "disabled", "formnovalidate", "hidden", "inert", "ismap", "itemscope", "loop",
  "multiple", "muted", "nomodule", "novalidate", "open", "playsinline", "readonly", "required",
  "reversed", "selected"
]);

var BLOCKED_BIND_ATTRIBUTES = new Set([
  "class", "style", "srcdoc", "value", "checked", "selected"
]);

var URL_ATTRIBUTES = new Set([
  "href", "src", "action", "formaction", "poster", "xlink:href"
]);

function flattenClasses(value, output) {
  output = output || [];
  if (value == null || value === false || value === true) return output;
  if (typeof value === "string") {
    value.split(/\s+/).forEach(function (name) { if (name) output.push(name); });
    return output;
  }
  if (Array.isArray(value)) {
    value.forEach(function (item) { flattenClasses(item, output); });
    return output;
  }
  if (typeof value === "object") {
    Object.keys(value).forEach(function (key) {
      if (value[key]) flattenClasses(key, output);
    });
  }
  return output;
}

function classSetFromValue(value) {
  var set = new Set();
  if (Array.isArray(value) && value.length && value[0] && hasOwn(value[0], "key") && hasOwn(value[0], "value")) {
    value.forEach(function (entry) {
      if (entry.value) flattenClasses(entry.key, []).forEach(function (name) { set.add(name); });
    });
  } else {
    flattenClasses(value, []).forEach(function (name) { set.add(name); });
  }
  return set;
}

function installCoreDirectives(runtime) {
  var registry = runtime.directives;

  registry.register("text", {
    mode: MODES.BINDING,
    phase: "content",
    update: function (binding, result) {
      var text = normalizeScalar(result.value);
      if (text == null) {
        throw createRuntimeError("KIT_TEXT_NON_SCALAR", "data-kit-text must resolve to a scalar value", {
          value: result.value,
          element: binding.element
        });
      }
      if (binding.element.textContent !== text) binding.element.textContent = text;
      binding.lastValue = text;
    }
  });

  registry.register("show", {
    mode: MODES.BINDING,
    phase: "content",
    mount: function (binding) {
      binding.authorHidden = binding.element.hasAttribute("hidden");
    },
    update: function (binding, result) {
      var nextHidden = !result.value;
      if (binding.element.hidden !== nextHidden) binding.element.hidden = nextHidden;
      binding.lastValue = !!result.value;
    },
    unmount: function (binding) {
      binding.element.hidden = !!binding.authorHidden;
    }
  });

  registry.register("class", {
    mode: MODES.CLASS_VALUE,
    phase: "content",
    mount: function (binding) {
      binding.initialClasses = new Set(Array.prototype.slice.call(binding.element.classList || []));
    },
    update: function (binding, result) {
      var wanted = classSetFromValue(result.value);
      binding.ownedClasses.forEach(function (name) {
        if (!wanted.has(name)) {
          binding.element.classList.remove(name);
          binding.ownedClasses.delete(name);
        }
      });
      wanted.forEach(function (name) {
        if (!binding.element.classList.contains(name)) binding.element.classList.add(name);
        if (!binding.initialClasses.has(name)) binding.ownedClasses.add(name);
      });
      binding.lastValue = wanted;
    },
    unmount: function (binding) {
      binding.ownedClasses.forEach(function (name) { binding.element.classList.remove(name); });
      binding.ownedClasses.clear();
    }
  });

  registry.register("style", {
    mode: MODES.NAMED_MAP,
    phase: "content",
    mount: function (binding) {
      binding.styleOriginals = new Map();
    },
    update: function (binding, result) {
      var seen = new Set();
      function setAttribute(name, value) {
        var next = String(value);
        if (!binding.element.hasAttribute(name) || binding.element.getAttribute(name) !== next) {
          binding.element.setAttribute(name, next);
        }
      }
      function setBooleanAttribute(name) {
        if (!binding.element.hasAttribute(name) || binding.element.getAttribute(name) !== "") {
          binding.element.setAttribute(name, "");
        }
      }
      result.value.forEach(function (entry) {
        var key = entry.key;
        seen.add(key);
        if (!binding.styleOriginals.has(key)) {
          binding.styleOriginals.set(key, {
            value: binding.element.style.getPropertyValue(key),
            priority: binding.element.style.getPropertyPriority(key)
          });
        }
        if (entry.value == null || entry.value === false) {
          if (binding.element.style.getPropertyValue(key)) binding.element.style.removeProperty(key);
        } else {
          var nextStyle = String(entry.value);
          if (binding.element.style.getPropertyValue(key) !== nextStyle) {
            binding.element.style.setProperty(key, nextStyle);
          }
        }
      });
      binding.ownedStyles.forEach(function (_, key) {
        if (!seen.has(key)) binding.element.style.removeProperty(key);
      });
      binding.ownedStyles = new Map();
      seen.forEach(function (key) { binding.ownedStyles.set(key, true); });
    },
    unmount: function (binding) {
      if (!binding.styleOriginals) return;
      binding.styleOriginals.forEach(function (original, key) {
        if (original.value) binding.element.style.setProperty(key, original.value, original.priority || "");
        else binding.element.style.removeProperty(key);
      });
    }
  });

  registry.register("bind", {
    mode: MODES.NAMED_MAP,
    phase: "content",
    validate: function (binding) {
      var entries = binding.compiled.ast && binding.compiled.ast.entries || [];
      entries.forEach(function (entry) {
        var name = String(entry.key);
        var lower = name.toLowerCase();
        if (BLOCKED_BIND_ATTRIBUTES.has(lower) || lower.indexOf("on") === 0 ||
            lower.indexOf("data-kit-") === 0 || lower.indexOf("data-kitwork-") === 0) {
          throw createRuntimeError("KIT_UNSAFE_ATTRIBUTE", "data-kit-bind cannot own attribute '" + name + "'", {
            attribute: name,
            element: binding.element
          });
        }
      });
    },
    mount: function (binding) {
      binding.attributeOriginals = new Map();
    },
    update: function (binding, result) {
      var seen = new Set();
      function setAttribute(name, value) {
        var next = String(value);
        if (!binding.element.hasAttribute(name) || binding.element.getAttribute(name) !== next) {
          binding.element.setAttribute(name, next);
        }
      }
      function setBooleanAttribute(name) {
        if (!binding.element.hasAttribute(name) || binding.element.getAttribute(name) !== "") {
          binding.element.setAttribute(name, "");
        }
      }
      result.value.forEach(function (entry) {
        var name = entry.key;
        var lower = name.toLowerCase();
        var value = entry.value;
        seen.add(name);
        if (!binding.attributeOriginals.has(name)) {
          binding.attributeOriginals.set(name, {
            present: binding.element.hasAttribute(name),
            value: binding.element.getAttribute(name)
          });
        }

        if (URL_ATTRIBUTES.has(lower) && value != null) {
          var normalized = String(value).trim().toLowerCase();
          if (normalized.indexOf("javascript:") === 0 || normalized.indexOf("vbscript:") === 0) {
            throw createRuntimeError("KIT_UNSAFE_URL", "Unsafe URL scheme in attribute '" + name + "'", {
              attribute: name,
              value: value,
              element: binding.element
            });
          }
        }

        if (lower.indexOf("data-") === 0 || lower.indexOf("aria-") === 0) {
          if (value == null) {
            if (binding.element.hasAttribute(name)) binding.element.removeAttribute(name);
          } else if (value === true) setAttribute(name, "true");
          else if (value === false) setAttribute(name, "false");
          else setAttribute(name, value);
        } else if (HTML_BOOLEAN_ATTRIBUTES.has(lower)) {
          if (value === true) setBooleanAttribute(name);
          else if (value == null || value === false) {
            if (binding.element.hasAttribute(name)) binding.element.removeAttribute(name);
          } else setAttribute(name, value);
        } else {
          if (value == null || value === false) {
            if (binding.element.hasAttribute(name)) binding.element.removeAttribute(name);
          } else if (value === true) setAttribute(name, "true");
          else setAttribute(name, value);
        }
      });

      binding.ownedAttributes.forEach(function (_, name) {
        if (!seen.has(name)) binding.element.removeAttribute(name);
      });
      binding.ownedAttributes = new Map();
      seen.forEach(function (name) { binding.ownedAttributes.set(name, true); });
    },
    unmount: function (binding) {
      if (!binding.attributeOriginals) return;
      binding.attributeOriginals.forEach(function (original, name) {
        if (original.present) binding.element.setAttribute(name, original.value == null ? "" : original.value);
        else binding.element.removeAttribute(name);
      });
    }
  });

  registry.register("model", {
    mode: MODES.WRITABLE_PATH,
    phase: "form",
    mount: function (binding) {
      runtime.model.mount(binding);
    },
    update: function (binding, result) {
      runtime.model.update(binding, result.value);
    },
    unmount: function (binding) {
      runtime.model.unmount(binding);
    }
  });
}

module.exports = {
  installCoreDirectives: installCoreDirectives,
  flattenClasses: flattenClasses,
  classSetFromValue: classSetFromValue,
  HTML_BOOLEAN_ATTRIBUTES: HTML_BOOLEAN_ATTRIBUTES
};
