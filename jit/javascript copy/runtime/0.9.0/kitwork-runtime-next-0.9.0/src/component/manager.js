"use strict";

var utils = require("../core/utils.js");
var errors = require("../core/errors.js");
var records = require("../core/records.js");
var hasOwn = utils.hasOwn;
var cloneState = utils.cloneState;
var nodeDepth = utils.nodeDepth;
var isThenable = utils.isThenable;
var createRuntimeError = errors.createRuntimeError;
var createComponentRecord = records.createComponentRecord;

var RESERVED_ALIAS_NAMES = new Set([
  "$element", "$host", "$event", "$refs", "$component", "$parent", "$item", "$index"
]);
var RESERVED_INSTANCE_KEYS = new Set([
  "$host", "$refs", "$parent", "$alias", "$app", "$invalidate", "$runtime"
]);

function descriptorIn(object, key) {
  var current = object;
  while (current) {
    var descriptor = Object.getOwnPropertyDescriptor(current, key);
    if (descriptor) return descriptor;
    current = Object.getPrototypeOf(current);
  }
  return null;
}

function createComponentManager(runtime) {
  var definitions = new Map();
  var recordsByHost = new WeakMap();

  function validComponentName(name) {
    return /^[A-Za-z][A-Za-z0-9_.-]*$/.test(name);
  }

  function validAlias(alias) {
    return /^\$[A-Za-z][A-Za-z0-9_]*$/.test(alias) && !RESERVED_ALIAS_NAMES.has(alias);
  }

  function assertStateKey(record, key) {
    if (RESERVED_INSTANCE_KEYS.has(key)) {
      throw createRuntimeError("KIT_COMPONENT_STATE_COLLISION", "Cannot overwrite runtime metadata '" + key + "'", {
        component: record.name,
        key: key,
        host: record.host
      });
    }
    var descriptor = descriptorIn(record.target, key);
    if (descriptor && (typeof descriptor.value === "function" || descriptor.get || descriptor.set || descriptor.writable === false)) {
      throw createRuntimeError("KIT_COMPONENT_STATE_COLLISION", "Cannot overwrite component method/accessor '" + key + "'", {
        component: record.name,
        key: key,
        host: record.host
      });
    }
  }

  function createInstance(record) {
    var target = record.target;
    var proxy = new Proxy(target, {
      get: function (object, key, receiver) {
        if (key === "$host") return record.host;
        if (key === "$refs") return record.refs;
        if (key === "$parent") return record.parent ? record.parent.instance : undefined;
        if (key === "$alias") return record.alias || "";
        if (key === "$app") return record.app.root;
        if (key === "$runtime") return runtime.publicApi;
        if (key === "$invalidate") return function () {
          runtime.scheduler.invalidate(record.app, record.host, { type: "component-manual", component: record.name });
        };
        return Reflect.get(object, key, receiver);
      },
      set: function (object, key, value, receiver) {
        key = String(key);
        assertStateKey(record, key);
        var previous = object[key];
        var changed = previous !== value;
        var result = Reflect.set(object, key, value, receiver);
        if (changed) {
          runtime.scheduler.invalidate(record.app, record.host, {
            type: "component-state",
            component: record.name,
            key: key,
            value: value
          });
        }
        return result;
      },
      defineProperty: function (object, key, descriptor) {
        key = String(key);
        assertStateKey(record, key);
        var result = Reflect.defineProperty(object, key, descriptor);
        runtime.scheduler.invalidate(record.app, record.host, {
          type: "component-define",
          component: record.name,
          key: key
        });
        return result;
      },
      deleteProperty: function (object, key) {
        key = String(key);
        assertStateKey(record, key);
        var existed = hasOwn(object, key);
        var result = Reflect.deleteProperty(object, key);
        if (existed) runtime.scheduler.invalidate(record.app, record.host, {
          type: "component-delete",
          component: record.name,
          key: key
        });
        return result;
      }
    });
    record.instance = proxy;
    return proxy;
  }

  function installDefinition(record, definition) {
    if (!definition || record.activated) return record.instance;
    record.definition = definition;

    var keys = Reflect.ownKeys(definition);
    for (var i = 0; i < keys.length; i++) {
      var key = keys[i];
      if (typeof key !== "string") continue;
      if (RESERVED_INSTANCE_KEYS.has(key)) {
        throw createRuntimeError("KIT_COMPONENT_STATE_COLLISION", "Component definition uses reserved key '" + key + "'", {
          component: record.name,
          key: key
        });
      }
      var descriptor = Object.getOwnPropertyDescriptor(definition, key);
      if (!descriptor) continue;

      if (hasOwn(record.hostSeed, key) && (typeof descriptor.value === "function" || descriptor.get || descriptor.set)) {
        throw createRuntimeError("KIT_COMPONENT_STATE_COLLISION", "SSR scope cannot override component method/accessor '" + key + "'", {
          component: record.name,
          key: key,
          host: record.host
        });
      }

      if (typeof descriptor.value === "function" || descriptor.get || descriptor.set) {
        Object.defineProperty(record.target, key, descriptor);
      } else if (!hasOwn(record.target, key)) {
        Object.defineProperty(record.target, key, {
          configurable: descriptor.configurable !== false,
          enumerable: descriptor.enumerable !== false,
          writable: descriptor.writable !== false,
          value: cloneState(descriptor.value)
        });
      }
    }

    Object.keys(record.hostSeed).forEach(function (key) {
      assertStateKey(record, key);
      record.target[key] = record.hostSeed[key];
    });

    record.activated = true;
    record.app.pendingComponents.add(record);
    runtime.scheduler.invalidate(record.app, record.host, { type: "component-activated", component: record.name });
    return record.instance;
  }

  function register(name, definition) {
    name = String(name || "").trim();
    if (!validComponentName(name)) {
      throw createRuntimeError("KIT_COMPONENT_NAME", "Invalid component name '" + name + "'", { component: name });
    }
    if (arguments.length === 1) return definitions.get(name);
    if (!definition || typeof definition !== "object") {
      throw createRuntimeError("KIT_COMPONENT_DEFINITION", "Component '" + name + "' requires a plain object definition", {
        component: name
      });
    }
    definitions.set(name, definition);

    runtime.apps.forEach(function (app) {
      app.components.forEach(function (record) {
        if (record.name === name && !record.activated && !record.destroyed) {
          try { installDefinition(record, definition); }
          catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", name, "mount")); }
        }
      });
    });
    return definition;
  }

  function setAlias(record, alias) {
    alias = String(alias || "").trim();
    if (record.alias && record.app.aliases.get(record.alias) === record) {
      record.app.aliases.delete(record.alias);
    }
    record.alias = "";
    if (!alias) return;
    if (!validAlias(alias)) {
      throw createRuntimeError("KIT_ALIAS_INVALID", "Invalid or reserved component alias '" + alias + "'", {
        alias: alias,
        host: record.host
      });
    }
    var existing = record.app.aliases.get(alias);
    if (existing && existing !== record) {
      throw createRuntimeError("KIT_DUPLICATE_ALIAS", "Component alias '" + alias + "' is already registered in this app", {
        alias: alias,
        host: record.host,
        existingHost: existing.host
      });
    }
    record.alias = alias;
    record.app.aliases.set(alias, record);
  }

  function ensure(app, host, name, parent, hostSeed) {
    var record = recordsByHost.get(host);
    if (record) {
      record.parent = parent || null;
      if (hostSeed) record.hostSeed = hostSeed;
      var aliasNow = host.getAttribute("data-kit-as") || "";
      if (aliasNow !== record.alias) setAlias(record, aliasNow);
      return record;
    }

    record = createComponentRecord(app, host, name, parent);
    record.hostSeed = hostSeed || Object.create(null);
    createInstance(record);
    recordsByHost.set(host, record);
    app.components.add(record);
    runtime.nodeRecord(host, app).component = record;

    try { setAlias(record, host.getAttribute("data-kit-as") || ""); }
    catch (error) { runtime.reportError(error, runtime.contextFor(host, "as", host.getAttribute("data-kit-as"), "mount")); }

    var definition = definitions.get(name);
    if (definition) {
      try { installDefinition(record, definition); }
      catch (error) { runtime.reportError(error, runtime.contextFor(host, "component", name, "mount")); }
    } else if (runtime.options.development) {
      runtime.warn("KIT_COMPONENT_MISSING", "Component '" + name + "' is not registered yet", {
        component: name,
        host: host
      });
    }

    return record;
  }

  function nearest(node, app, includeSelf) {
    var current = includeSelf === false ? node && node.parentNode : node;
    while (current && current !== app.root.parentNode) {
      var record = recordsByHost.get(current);
      if (record && !record.destroyed && record.app === app) return record;
      if (current === app.root) break;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return null;
  }

  function registerRef(record, name, element) {
    if (!record) {
      if (runtime.options.development) runtime.warn("KIT_REF_NO_COMPONENT", "data-kit-ref requires an owning component", {
        ref: name,
        element: element
      });
      return;
    }
    name = String(name || "").trim();
    if (!/^[A-Za-z][A-Za-z0-9_]*$/.test(name)) {
      throw createRuntimeError("KIT_REF_INVALID", "Invalid ref name '" + name + "'", { ref: name, element: element });
    }
    var existing = record.refs[name];
    if (existing && existing !== element) {
      throw createRuntimeError("KIT_DUPLICATE_REF", "Ref '" + name + "' is already registered in component '" + record.name + "'", {
        ref: name,
        component: record.name,
        element: element,
        existing: existing
      });
    }
    record.refs[name] = element;
  }

  function removeRef(record, name, element) {
    if (record && name && record.refs[name] === element) delete record.refs[name];
  }

  function runMount(record) {
    if (!record || record.destroyed || record.mounted || record.mounting || !record.activated) return;
    var mount = record.instance && record.instance.mount;
    record.mounted = true;
    if (typeof mount !== "function") return;
    record.mounting = true;
    var result;
    try {
      result = mount.call(record.instance);
    } catch (error) {
      record.mounting = false;
      runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "mount"));
      return;
    }

    if (isThenable(result)) {
      record.pendingEffects.add(result);
      result.then(function (cleanup) {
        record.pendingEffects.delete(result);
        record.mounting = false;
        if (record.destroyed) {
          if (typeof cleanup === "function") {
            try { cleanup(); } catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "cleanup")); }
          }
          return;
        }
        if (typeof cleanup === "function") record.mountCleanup = cleanup;
      }, function (error) {
        record.pendingEffects.delete(result);
        record.mounting = false;
        runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "mount"));
      });
    } else {
      record.mounting = false;
      if (typeof result === "function") record.mountCleanup = result;
    }
  }

  function mountPending(app, boundary) {
    var pending = Array.from(app.pendingComponents).filter(function (record) {
      return !record.destroyed && record.host.isConnected &&
        (!boundary || boundary === app.root || (boundary.contains && boundary.contains(record.host)) || boundary === record.host);
    });
    pending.sort(function (left, right) { return nodeDepth(right.host) - nodeDepth(left.host); });
    pending.forEach(function (record) {
      app.pendingComponents.delete(record);
      runMount(record);
    });
  }

  function unmount(record) {
    if (!record || record.destroyed) return;
    record.destroyed = true;

    // Every task started with this component instance as owner is aborted
    // before lifecycle cleanup, preventing stale async work from mutating a
    // component that has already left its application.
    if (runtime.task && typeof runtime.task.abort === "function") {
      try { runtime.task.abort(record.instance, undefined, "component-unmount"); }
      catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "task-abort")); }
    }

    var children = Array.from(record.app.components).filter(function (candidate) {
      return candidate.parent === record && !candidate.destroyed;
    }).sort(function (left, right) { return nodeDepth(right.host) - nodeDepth(left.host); });
    children.forEach(unmount);

    // Async work is owned by the component instance, not by its current DOM
    // position. A real unmount aborts it; a DOM move inside the same app never
    // reaches this path.
    if (runtime.task && record.instance) {
      try { runtime.task.abort(record.instance); }
      catch (taskError) { runtime.reportError(taskError, runtime.contextFor(record.host, "component", record.name, "task-abort")); }
    }

    if (record.mounted && record.instance && typeof record.instance.unmount === "function") {
      try { record.instance.unmount.call(record.instance); }
      catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "unmount")); }
    }

    if (typeof record.mountCleanup === "function") {
      try { record.mountCleanup(); }
      catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "cleanup")); }
      record.mountCleanup = null;
    }

    while (record.cleanups.length) {
      try { record.cleanups.pop()(); }
      catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "cleanup")); }
    }

    if (record.alias && record.app.aliases.get(record.alias) === record) record.app.aliases.delete(record.alias);
    Object.keys(record.refs).forEach(function (key) { delete record.refs[key]; });
    record.app.components.delete(record);
    record.app.pendingComponents.delete(record);
    recordsByHost.delete(record.host);
  }

  return {
    register: register,
    get: function (name) { return definitions.get(name); },
    ensure: ensure,
    nearest: nearest,
    byHost: function (host) { return recordsByHost.get(host) || null; },
    setAlias: setAlias,
    registerRef: registerRef,
    removeRef: removeRef,
    mountPending: mountPending,
    unmount: unmount,
    definitions: definitions,
    recordsByHost: recordsByHost
  };
}

module.exports = {
  createComponentManager: createComponentManager
};
