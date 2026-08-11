// KitJS runtime ownership, scopes, component instances, aliases and refs.
(function (window) {
  "use strict";

  var kit = window.kit;
  var core = kit && kit.__kitwork_core__;
  if (!core) throw new Error("KitJS core/global.js must be loaded before core/component.js");
  if (core.reuse) return;
  if (core.phase !== "expression") throw new Error("KitJS core fragment order error before core/component.js");

  var OWN = core.OWN;
  var RESERVED = core.RESERVED;
  var INSTANCE_RESERVED = core.INSTANCE_RESERVED;
  var blocked = core.blocked;
  var blockedMember = core.blockedMember;
  var state = core.state;
  var peek = core.peek;
  var warn = core.warn;
  var report = core.report;
  var compile = core.compile;
  var evaluate = core.evaluate;
  var parseMap = core.parseMap;
  var blueprints = Object.create(null);

  function freshRuntime() {
    return {
      root: null,
      started: false,
      scheduled: false,
      rendering: false,
      aliases: Object.create(null),
      page: Object.create(null),
      bindings: [],
      components: [],
      pendingInits: [],
      componentByHost: new WeakMap(),
      actionsByElement: new WeakMap(),
      outsideActions: Object.create(null),
      listeners: Object.create(null),
      observer: null,
      mutations: [],
      mutationScheduled: false
    };
  }

  // Keep this object identity stable: later classic-script fragments capture it.
  var runtime = freshRuntime();
  function resetRuntime() {
    var next = freshRuntime();
    Object.keys(runtime).forEach(function (key) { delete runtime[key]; });
    Object.keys(next).forEach(function (key) { runtime[key] = next[key]; });
    return runtime;
  }
  Object.defineProperty(core, "runtime", {
    get: function () { return runtime; },
    enumerable: false,
    configurable: false
  });

  function schedule() {
    if (typeof core.schedule !== "function") throw new Error("KitJS core/dom.js is not loaded");
    return core.schedule();
  }

  function enqueue(callback) {
    if (typeof window.queueMicrotask === "function") window.queueMicrotask(callback);
    else Promise.resolve().then(callback);
  }

  function isElement(value) { return !!value && value.nodeType === 1; }
  function inRoot(element) { return !!runtime.root && (element === runtime.root || runtime.root.contains(element)); }
  function depth(element) {
    var value = 0;
    while (element && element.parentElement) { value++; element = element.parentElement; }
    return value;
  }
  function ignored(element) {
    while (isElement(element)) {
      if (element.hasAttribute("data-kit-ignore")) return true;
      if (element === runtime.root) break;
      element = element.parentElement;
    }
    return false;
  }
  function walk(root, callback, seen, includeIgnored) {
    if (!isElement(root) || (!includeIgnored && ignored(root))) return;
    if (seen) {
      if (seen.has(root)) return;
      seen.add(root);
    }
    callback(root);
    var child = root.firstElementChild;
    while (child) {
      var next = child.nextElementSibling;
      walk(child, callback, seen, includeIgnored);
      child = next;
    }
  }

  function validComponentVersion(version) {
    var match = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(version);
    if (!match) return false;
    if (match[4]) {
      var identifiers = match[4].split(".");
      for (var index = 0; index < identifiers.length; index++) {
        if (/^[0-9]+$/.test(identifiers[index]) && identifiers[index].length > 1 && identifiers[index].charAt(0) === "0") return false;
      }
    }
    return true;
  }

  function normalizeComponentVersion(version) {
    version = String(version || "").trim();
    if (version.charAt(0) === "v") version = version.substring(1);
    return validComponentVersion(version) ? version : null;
  }

  function parseComponentSpec(spec) {
    spec = String(spec || "").trim();
    if (!spec || spec.indexOf("=") !== -1) return null;
    var at = spec.lastIndexOf("@");
    if (at === 0 || spec.indexOf("@") !== at) return null;
    var name = at >= 0 ? spec.substring(0, at) : spec;
    var version = at >= 0 ? normalizeComponentVersion(spec.substring(at + 1)) : "";
    if (!/^[A-Za-z][A-Za-z0-9_.-]*$/.test(name)) return null;
    if (at >= 0 && version === null) return null;
    return { name: name, version: version };
  }

  function resolveComponent(spec, separateVersion) {
    var parsed = parseComponentSpec(spec);
    if (!parsed) return { code: "KIT_COMPONENT_INVALID", detail: spec };
    if (separateVersion !== null) {
      if (parsed.version) return { code: "KIT_COMPONENT_VERSION_CONFLICT", detail: spec };
      parsed.version = normalizeComponentVersion(separateVersion);
      if (parsed.version === null) return { code: "KIT_COMPONENT_VERSION_INVALID", detail: separateVersion };
    }
    var registered = blueprints[parsed.name];
    if (!registered) return { name: parsed.name, code: "KIT_COMPONENT_NOT_FOUND", detail: spec };
    return { name: parsed.name, version: parsed.version, definition: registered };
  }

  function closestComponent(element) {
    while (isElement(element)) {
      var record = runtime.componentByHost.get(element);
      if (record && !record.disposed) return record;
      if (element === runtime.root) break;
      element = element.parentElement;
    }
    return null;
  }

  function parentComponent(host) {
    return closestComponent(host.parentElement);
  }

  function insideInactiveComponent(element, excludeSelf) {
    if (excludeSelf && isElement(element)) element = element.parentElement;
    while (isElement(element)) {
      var elementState = peek(element);
      if (elementState && elementState.componentInactive) return true;
      if (element === runtime.root) break;
      element = element.parentElement;
    }
    return false;
  }

  function scopesFor(element) {
    var scopes = [];
    while (isElement(element)) {
      var elementState = peek(element);
      if (elementState && elementState.scope) scopes.push(elementState.scope);
      var component = runtime.componentByHost.get(element);
      if (component && !component.disposed) {
        scopes.push(component.instance);
        break;
      }
      if (element === runtime.root) break;
      element = element.parentElement;
    }
    scopes.push(runtime.page);
    return scopes;
  }

  function elementFacade(element) {
    if (!isElement(element)) return null;
    var facade = Object.create(null);
    facade.tagName = element.tagName ? element.tagName.toLowerCase() : "";
    facade.id = element.id || "";
    facade.name = element.name || "";
    facade.type = element.type || "";
    facade.value = typeof element.value === "string" ? element.value : "";
    facade.checked = Boolean(element.checked);
    facade.disabled = Boolean(element.disabled);
    return Object.freeze(facade);
  }

  function refsFacade(refs) {
    var facade = Object.create(null);
    Object.keys(refs || {}).forEach(function (name) {
      if (!blockedMember(name)) facade[name] = elementFacade(refs[name]);
    });
    return Object.freeze(facade);
  }

  function eventFacade(event, currentElement) {
    if (!event) return undefined;
    var facade = Object.create(null);
    facade.type = String(event.type || "");
    facade.key = String(event.key || "");
    facade.code = String(event.code || "");
    facade.repeat = Boolean(event.repeat);
    facade.button = typeof event.button === "number" ? event.button : 0;
    facade.buttons = typeof event.buttons === "number" ? event.buttons : 0;
    facade.clientX = typeof event.clientX === "number" ? event.clientX : 0;
    facade.clientY = typeof event.clientY === "number" ? event.clientY : 0;
    facade.altKey = Boolean(event.altKey);
    facade.ctrlKey = Boolean(event.ctrlKey);
    facade.metaKey = Boolean(event.metaKey);
    facade.shiftKey = Boolean(event.shiftKey);
    facade.defaultPrevented = Boolean(event.defaultPrevented);
    facade.target = elementFacade(event.target);
    facade.currentTarget = elementFacade(currentElement);
    facade.preventDefault = function () { event.preventDefault(); };
    facade.stopPropagation = function () { event.stopPropagation(); };
    return Object.freeze(facade);
  }

  function errorFacade(error) {
    if (!error) return undefined;
    var facade = Object.create(null);
    facade.name = String(error.name || "Error");
    facade.message = String(error.message || error);
    return Object.freeze(facade);
  }

  function expressionContext(element, extra) {
    extra = extra || {};
    var component = closestComponent(element);
    return {
      $element: elementFacade(element),
      $host: component ? elementFacade(component.host) : null,
      $event: eventFacade(extra.event, element),
      $refs: refsFacade(component ? component.refs : null),
      $component: component ? component.instance : null,
      $parent: component && component.parent ? component.parent.instance : null,
      $error: errorFacade(extra.error)
    };
  }

  function resolver(scopes, context) {
    return {
      get: function (name) {
        if (name === "kit" || blocked(name)) return undefined;
        if (context && OWN.call(context, name)) return context[name];
        if (name.charAt(0) === "$" && OWN.call(runtime.aliases, name)) return runtime.aliases[name];
        for (var index = 0; index < scopes.length; index++) {
          if (scopes[index] && name in scopes[index]) {
            var value = scopes[index][name];
            return typeof value === "function" ? value.bind(scopes[index]) : value;
          }
        }
        return undefined;
      },
      set: function (name, value) {
        if (RESERVED[name] || blocked(name) || OWN.call(runtime.aliases, name)) {
          warn("KIT_MODEL_NOT_WRITABLE", name);
          return;
        }
        for (var index = 0; index < scopes.length; index++) {
          if (scopes[index] && name in scopes[index]) {
            scopes[index][name] = value;
            return;
          }
        }
        (scopes[0] || runtime.page)[name] = value;
        schedule();
      }
    };
  }

  function resolverFor(element, extra) {
    return resolver(scopesFor(element), expressionContext(element, extra));
  }

  function cloneState(value, seen) {
    if (value === null || typeof value !== "object") return value;
    if (typeof window.structuredClone === "function") {
      try { return window.structuredClone(value); } catch (_) {}
    }
    seen = seen || new Map();
    if (seen.has(value)) return seen.get(value);
    if (value instanceof Date) return new Date(value.getTime());
    if (value instanceof RegExp) return new RegExp(value.source, value.flags);
    if (Array.isArray(value)) {
      var array = [];
      seen.set(value, array);
      for (var index = 0; index < value.length; index++) array[index] = cloneState(value[index], seen);
      return array;
    }
    var prototype = Object.getPrototypeOf(value);
    if (prototype !== Object.prototype && prototype !== null) return value;
    var object = Object.create(prototype);
    seen.set(value, object);
    Object.keys(value).forEach(function (key) { object[key] = cloneState(value[key], seen); });
    return object;
  }

  function assertInstanceKey(record, key) {
    if (INSTANCE_RESERVED[key] || blocked(key)) throw new Error("Component '" + record.name + "' uses reserved key '" + key + "'");
  }

  function registerAlias(record) {
    var alias = (record.host.getAttribute("data-kit-as") || "").trim();
    if (!alias) return;
    if (!/^\$[A-Za-z][A-Za-z0-9_]*$/.test(alias) || RESERVED[alias]) {
      warn("KIT_INVALID_ALIAS", alias);
      return;
    }
    if (OWN.call(runtime.aliases, alias) && runtime.aliases[alias] !== record.instance) {
      warn("KIT_DUPLICATE_ALIAS", alias);
      return;
    }
    runtime.aliases[alias] = record.instance;
    record.alias = alias;
  }

  function createComponent(host, spec) {
    var current = runtime.componentByHost.get(host);
    if (current && !current.disposed) {
      current.parent = parentComponent(host);
      return current;
    }
    var resolved = resolveComponent(spec, host.getAttribute("data-kit-version"));
    if (!resolved.name || !resolved.definition) {
      state(host).componentInactive = true;
      warn(resolved.code || "KIT_COMPONENT_NOT_FOUND", resolved.detail || spec);
      return null;
    }

    var record = {
      name: resolved.name,
      version: resolved.version,
      host: host,
      parent: parentComponent(host),
      refs: Object.create(null),
      alias: "",
      target: Object.create(null),
      instance: null,
      initialized: false,
      disposed: false,
      cleanup: null
    };

    record.instance = new Proxy(record.target, {
      get: function (target, key, receiver) {
        if (key === "$host") return record.host;
        if (key === "$refs") return record.refs;
        if (key === "$parent") return record.parent ? record.parent.instance : null;
        if (key === "$alias") return record.alias;
        if (key === "$invalidate") return function () { if (!record.disposed) schedule(); };
        return Reflect.get(target, key, receiver);
      },
      set: function (target, key, value, receiver) {
        key = String(key);
        assertInstanceKey(record, key);
        if (runtime.rendering) throw new Error("Component '" + record.name + "' mutated state during render");
        var previous = Reflect.get(target, key, receiver);
        var result = Reflect.set(target, key, value, receiver);
        if (!record.disposed && !Object.is(previous, value)) schedule();
        return result;
      },
      defineProperty: function (target, key, descriptor) {
        key = String(key);
        assertInstanceKey(record, key);
        if (runtime.rendering) throw new Error("Component '" + record.name + "' defined state during render");
        var result = Reflect.defineProperty(target, key, descriptor);
        if (!record.disposed) schedule();
        return result;
      },
      deleteProperty: function (target, key) {
        key = String(key);
        assertInstanceKey(record, key);
        if (runtime.rendering) throw new Error("Component '" + record.name + "' deleted state during render");
        var existed = OWN.call(target, key);
        var result = Reflect.deleteProperty(target, key);
        if (!record.disposed && existed) schedule();
        return result;
      }
    });

    Reflect.ownKeys(resolved.definition).forEach(function (key) {
      if (typeof key !== "string") return;
      assertInstanceKey(record, key);
      var descriptor = Object.getOwnPropertyDescriptor(resolved.definition, key);
      if (!descriptor) return;
      if (typeof descriptor.value === "function" || descriptor.get || descriptor.set) {
        Object.defineProperty(record.target, key, descriptor);
      } else {
        Object.defineProperty(record.target, key, {
          configurable: descriptor.configurable !== false,
          enumerable: descriptor.enumerable !== false,
          writable: descriptor.writable !== false,
          value: cloneState(descriptor.value)
        });
      }
    });

    runtime.componentByHost.set(host, record);
    runtime.components.push(record);
    runtime.pendingInits.push(record);
    state(host).component = record;
    state(host).componentInactive = false;
    registerAlias(record);
    return record;
  }

  function seedScope(element) {
    var source = element.getAttribute("data-kit-scope");
    if (source === null) return;
    if (element.hasAttribute("data-kit-component")) {
      warn("KIT_SCOPE_COMPONENT_CONFLICT", "data-kit-scope cannot be used on a component host");
      return;
    }
    var elementState = state(element);
    if (elementState.scopeReady) return;
    elementState.scopeReady = true;
    elementState.scope = Object.create(null);
    var scopeResolver = resolver([elementState.scope].concat(scopesFor(element.parentElement || element)), expressionContext(element, {}));
    parseMap(source).forEach(function (entry) {
      if (!entry.key || entry.key.charAt(0) === "$" || entry.key === "kit") {
        warn("KIT_SCOPE_KEY_RESERVED", entry.key);
        return;
      }
      try { elementState.scope[entry.key] = evaluate(compile(entry.expression, "binding"), scopeResolver); }
      catch (error) { report(error, { element: element, directive: "data-kit-scope" }); }
    });
  }

  function refreshRefs() {
    runtime.components.forEach(function (record) {
      if (record.disposed) return;
      Object.keys(record.refs).forEach(function (key) { delete record.refs[key]; });
    });
    Object.keys(runtime.aliases).forEach(function (key) {
      var target = runtime.aliases[key];
      if (target && target.nodeType === 1 && !target.hasAttribute("data-kit-component")) {
        delete runtime.aliases[key];
      }
    });
    walk(runtime.root, function (element) {
      if (insideInactiveComponent(element, false)) return;
      var refName = element.getAttribute("data-kit-ref");
      if (refName) {
        var component = closestComponent(element);
        if (component) component.refs[refName] = element;
      }
      var aliasName = (element.getAttribute("data-kit-alias") || "").trim();
      if (aliasName && /^\$[A-Za-z][A-Za-z0-9_]*$/.test(aliasName)) {
        runtime.aliases[aliasName] = element;
      }
    });
  }


  core.blueprints = blueprints;
  core.freshRuntime = freshRuntime;
  core.resetRuntime = resetRuntime;
  core.enqueue = enqueue;
  core.isElement = isElement;
  core.inRoot = inRoot;
  core.depth = depth;
  core.ignored = ignored;
  core.walk = walk;
  core.validComponentVersion = validComponentVersion;
  core.normalizeComponentVersion = normalizeComponentVersion;
  core.parseComponentSpec = parseComponentSpec;
  core.resolveComponent = resolveComponent;
  core.closestComponent = closestComponent;
  core.parentComponent = parentComponent;
  core.insideInactiveComponent = insideInactiveComponent;
  core.scopesFor = scopesFor;
  core.elementFacade = elementFacade;
  core.refsFacade = refsFacade;
  core.eventFacade = eventFacade;
  core.errorFacade = errorFacade;
  core.expressionContext = expressionContext;
  core.resolver = resolver;
  core.resolverFor = resolverFor;
  core.cloneState = cloneState;
  core.assertInstanceKey = assertInstanceKey;
  core.registerAlias = registerAlias;
  core.createComponent = createComponent;
  core.seedScope = seedScope;
  core.refreshRefs = refreshRefs;
  core.phase = "component";

})(window);
