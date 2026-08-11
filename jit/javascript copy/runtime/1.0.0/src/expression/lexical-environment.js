/*
 * Kitwork Client Runtime — Lexical Environment Adapter M1
 *
 * Bridges the expression engine to the runtime ownership model without making
 * the expression package depend on DOM traversal, component registries, or the
 * render scheduler.
 */
(function (root, factory) {
  "use strict";

  var expressionApi = null;
  if (typeof module === "object" && module && module.exports) {
    expressionApi = require("./index.js");
    module.exports = factory(expressionApi);
    return;
  }

  expressionApi = root.KitworkExpression;
  var api = factory(expressionApi);
  try {
    Object.defineProperty(root, "KitworkLexicalEnvironment", {
      value: api,
      configurable: true,
      enumerable: false,
      writable: false
    });
  } catch (_) {
    root.KitworkLexicalEnvironment = api;
  }
})(typeof globalThis !== "undefined" ? globalThis : this, function (expressionApi) {
  "use strict";

  if (!expressionApi) throw new Error("KitworkExpression must be loaded first");

  var KitworkExpressionError = expressionApi.KitworkExpressionError;

  var BLOCKED = Object.create(null);
  (
    "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
    "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
    "window globalThis top parent self"
  ).split(/\s+/).forEach(function (key) {
    if (key) BLOCKED[key] = true;
  });

  var READONLY_MEMBER_ROOTS = Object.create(null);
  "$element $host $event $refs $parent $index kit".split(/\s+/).forEach(function (key) {
    READONLY_MEMBER_ROOTS[key] = true;
  });

  var RESERVED_INSTANCE_KEYS = Object.create(null);
  "$host $refs $parent $alias $app".split(/\s+/).forEach(function (key) {
    RESERVED_INSTANCE_KEYS[key] = true;
  });

  function hasOwn(object, key) {
    return object != null && Object.prototype.hasOwnProperty.call(object, key);
  }

  function fail(code, message, details) {
    throw new KitworkExpressionError(code, message, details || null);
  }

  function aliasGet(aliases, name) {
    if (!aliases) return undefined;
    if (typeof aliases.get === "function") return aliases.get(name);
    return hasOwn(aliases, name) ? aliases[name] : undefined;
  }

  function aliasHas(aliases, name) {
    if (!aliases) return false;
    if (typeof aliases.has === "function") return aliases.has(name);
    return hasOwn(aliases, name);
  }

  function propertyDescriptor(object, key) {
    var current = object;
    while (current) {
      var descriptor = Object.getOwnPropertyDescriptor(current, key);
      if (descriptor) return descriptor;
      current = Object.getPrototypeOf(current);
    }
    return null;
  }

  function isBlocked(key) {
    return typeof key === "string" && BLOCKED[key] === true;
  }

  function normalizeScopeEntry(entry, fallbackBoundary) {
    if (!entry) return null;
    if (entry.scope) {
      return {
        scope: entry.scope,
        boundary: entry.boundary || entry.element || fallbackBoundary || null
      };
    }
    return { scope: entry, boundary: fallbackBoundary || null };
  }

  function createLexicalEnvironment(options) {
    options = options || {};

    var contexts = options.contexts || Object.create(null);
    var aliases = options.aliases || null;
    var loopFrames = options.loopFrames || [];
    var component = options.component || null;
    var componentBoundary = options.componentBoundary || options.host || null;
    var appScope = options.appScope || Object.create(null);
    var appBoundary = options.appBoundary || options.appRoot || null;
    var kitSurface = options.kit || Object.create(null);
    var localScopes = [];

    (options.localScopes || []).forEach(function (entry) {
      var normalized = normalizeScopeEntry(entry, null);
      if (normalized) localScopes.push(normalized);
    });

    function dirty(boundary, mutation) {
      if (typeof options.onDirty === "function") {
        options.onDirty(boundary || componentBoundary || appBoundary, mutation || null);
      }
    }

    function resolve(name) {
      if (hasOwn(contexts, name)) {
        return {
          found: true,
          value: contexts[name],
          owner: null,
          readonly: true,
          kind: "context",
          boundary: null
        };
      }

      if (name === "kit") {
        return {
          found: true,
          value: kitSurface,
          owner: null,
          readonly: true,
          kind: "service",
          boundary: null
        };
      }

      if (name && name[0] === "$" && aliasHas(aliases, name)) {
        var alias = aliasGet(aliases, name);
        var instance = alias && alias.instance ? alias.instance : alias;
        return {
          found: true,
          value: instance,
          owner: instance,
          readonly: true,
          kind: "alias",
          boundary: alias && alias.host ? alias.host : null
        };
      }

      for (var i = 0; i < loopFrames.length; i++) {
        if (hasOwn(loopFrames[i], name)) {
          return {
            found: true,
            value: loopFrames[i][name],
            owner: loopFrames[i],
            readonly: name === "$index",
            kind: "loop",
            boundary: null
          };
        }
      }

      for (var j = 0; j < localScopes.length; j++) {
        if (hasOwn(localScopes[j].scope, name)) {
          return {
            found: true,
            value: localScopes[j].scope[name],
            owner: localScopes[j].scope,
            readonly: false,
            kind: "local",
            boundary: localScopes[j].boundary
          };
        }
      }

      if (component && name in component) {
        return {
          found: true,
          value: component[name],
          owner: component,
          readonly: false,
          kind: "component",
          boundary: componentBoundary
        };
      }

      if (hasOwn(appScope, name)) {
        return {
          found: true,
          value: appScope[name],
          owner: appScope,
          readonly: false,
          kind: "app",
          boundary: appBoundary
        };
      }

      return {
        found: false,
        value: undefined,
        owner: null,
        readonly: false,
        kind: "missing",
        boundary: null
      };
    }

    function assertWritableComponentKey(key) {
      if (!component) return;
      if (RESERVED_INSTANCE_KEYS[key]) {
        fail("KIT_COMPONENT_STATE_COLLISION", "Cannot overwrite runtime metadata '" + key + "'", {
          key: key
        });
      }
      var descriptor = propertyDescriptor(component, key);
      if (descriptor && (
        typeof descriptor.value === "function" ||
        descriptor.get || descriptor.set || descriptor.writable === false
      )) {
        fail("KIT_COMPONENT_STATE_COLLISION", "Cannot overwrite component method/accessor '" + key + "'", {
          key: key
        });
      }
    }

    function assign(name, value) {
      if (!name || name === "kit" || name[0] === "$" || hasOwn(contexts, name)) {
        fail("KIT_READONLY_CONTEXT", "Cannot assign to runtime context '" + name + "'", {
          name: name
        });
      }

      for (var i = 0; i < localScopes.length; i++) {
        if (hasOwn(localScopes[i].scope, name)) {
          localScopes[i].scope[name] = value;
          dirty(localScopes[i].boundary, { type: "identifier", name: name, value: value });
          return value;
        }
      }

      if (component && name in component) {
        assertWritableComponentKey(name);
        component[name] = value;
        dirty(componentBoundary, { type: "identifier", name: name, value: value });
        return value;
      }

      if (hasOwn(appScope, name)) {
        appScope[name] = value;
        dirty(appBoundary, { type: "identifier", name: name, value: value });
        return value;
      }

      if (localScopes.length) {
        localScopes[0].scope[name] = value;
        dirty(localScopes[0].boundary, { type: "identifier", name: name, value: value });
        return value;
      }

      if (component) {
        assertWritableComponentKey(name);
        component[name] = value;
        dirty(componentBoundary, { type: "identifier", name: name, value: value });
        return value;
      }

      appScope[name] = value;
      dirty(appBoundary, { type: "identifier", name: name, value: value });
      return value;
    }

    function canWriteMember(reference) {
      if (!reference || !reference.owner || reference.key == null) return false;
      if (isBlocked(reference.key)) return false;
      if (READONLY_MEMBER_ROOTS[reference.root]) return false;
      if (reference.owner === kitSurface || reference.owner === contexts) return false;
      if (typeof globalThis !== "undefined" && reference.owner === globalThis) return false;
      if (reference.owner && reference.owner.nodeType) return false;
      if (typeof Map !== "undefined" && reference.owner instanceof Map) return false;
      if (typeof Set !== "undefined" && reference.owner instanceof Set) return false;

      if (reference.owner === component) {
        assertWritableComponentKey(String(reference.key));
      }

      var descriptor = propertyDescriptor(reference.owner, reference.key);
      if (descriptor && (
        descriptor.writable === false ||
        typeof descriptor.value === "function" ||
        descriptor.get || descriptor.set
      )) return false;

      return true;
    }

    function onMutation(mutation) {
      if (!mutation || mutation.type !== "member") return;
      var rootResolution = mutation.root ? resolve(mutation.root) : null;
      dirty(rootResolution && rootResolution.boundary, mutation);
    }

    return {
      resolve: resolve,
      assign: assign,
      canWriteMember: canWriteMember,
      onMutation: onMutation,
      onEffect: typeof options.onEffect === "function" ? options.onEffect : null,
      defaultThis: component || appScope,

      // Runtime integration/debug metadata, not authored-expression surface.
      internal: {
        contexts: contexts,
        aliases: aliases,
        loopFrames: loopFrames,
        localScopes: localScopes,
        component: component,
        appScope: appScope,
        appBoundary: appBoundary,
        componentBoundary: componentBoundary
      }
    };
  }

  return Object.freeze({
    createLexicalEnvironment: createLexicalEnvironment
  });
});
