"use strict";

var expressionApi = require("../expression/index.js");
var lexicalApi = require("../expression/lexical-environment.js");
var utils = require("./utils.js");
var errors = require("./errors.js");
var records = require("./records.js");
var schedulerModule = require("./scheduler.js");
var directiveRegistryModule = require("../directive/registry.js");
var serviceRegistryModule = require("../service/registry.js");
var componentModule = require("../component/manager.js");
var coreDirectivesModule = require("../directive/core.js");
var modelModule = require("../directive/model.js");
var eventModule = require("../directive/events.js");
var structuralModule = require("../directive/structural.js");
var taskServiceModule = require("../service/task.js");
var requestServiceModule = require("../service/request.js");
var taskServiceModule = require("../service/task.js");
var requestServiceModule = require("../service/request.js");

var MODES = expressionApi.MODES;
var createExpressionEngine = expressionApi.createExpressionEngine;
var createLexicalEnvironment = lexicalApi.createLexicalEnvironment;
var hasOwn = utils.hasOwn;
var enqueueMicrotask = utils.enqueueMicrotask;
var nodeContains = utils.nodeContains;
var nodeDepth = utils.nodeDepth;
var isElement = utils.isElement;
var createNullObject = utils.createNullObject;
var createAppRecord = records.createAppRecord;
var createNodeRecord = records.createNodeRecord;
var createBindingRecord = records.createBindingRecord;
var createRuntimeError = errors.createRuntimeError;
var normalizeRuntimeError = errors.normalizeRuntimeError;

var RESERVED_ATTRIBUTES = new Set([
  "data-kit-app", "data-kit-component", "data-kit-as", "data-kit-scope", "data-kit-ref",
  "data-kit-if", "data-kit-for", "data-kit-key", "data-kit-persist"
]);

var PHASE_ORDER = { content: 10, form: 20 };

function createRuntime(globalObject, options) {
  options = Object.assign({
    development: false,
    autoStart: true,
    evaluationBudget: 10000,
    callDepthLimit: 64
  }, options || {});

  var global = globalObject || (typeof window !== "undefined" ? window : globalThis);
  var document = global.document || null;
  var kit = global.kit = global.kit || {};

  var runtime = {
    global: global,
    document: document,
    kit: kit,
    options: options,
    apps: new Set(),
    appsByRoot: new WeakMap(),
    nodeRecords: new WeakMap(),
    logicalOwners: new WeakMap(),
    modelBindings: new Set(),
    globalCleanups: [],
    warningKeys: new Set(),
    started: false,
    destroyed: false,
    publicApi: null
  };

  runtime.expression = createExpressionEngine({
    evaluationBudget: options.evaluationBudget,
    callDepthLimit: options.callDepthLimit
  });
  runtime.directives = directiveRegistryModule.createDirectiveRegistry();
  runtime.services = serviceRegistryModule.createServiceRegistry(kit);
  runtime.scheduler = schedulerModule.createScheduler(runtime);
  runtime.components = componentModule.createComponentManager(runtime);
  runtime.model = modelModule.createModelManager(runtime);
  runtime.events = eventModule.createEventManager(runtime);
  runtime.structural = structuralModule.createStructuralManager(runtime);

  // Core async services are ordinary service façades. Trusted component code sees
  // the concrete objects on `kit`; authored expressions see only explicitly
  // granted members through the curated service surface.
  runtime.task = taskServiceModule.createTaskService({
    onTask: function (taskRecord) {
      var owner = taskRecord && taskRecord.owner;
      if (!owner) return;
      runtime.apps.forEach(function (app) {
        app.components.forEach(function (componentRecord) {
          if (componentRecord.instance === owner) componentRecord.tasks.add(taskRecord);
        });
      });
      function releaseTask() {
        runtime.apps.forEach(function (app) {
          app.components.forEach(function (componentRecord) {
            componentRecord.tasks.delete(taskRecord);
          });
        });
      }
      Promise.resolve(taskRecord.promise).then(releaseTask, releaseTask);
    }
  });
  runtime.request = requestServiceModule.createRequestService(global);

  runtime.nodeRecord = function (node, app) {
    var record = runtime.nodeRecords.get(node);
    if (!record) {
      record = createNodeRecord(node, app || runtime.appForNode(node));
      runtime.nodeRecords.set(node, record);
    } else if (app && record.app !== app) {
      record.app = app;
    }
    return record;
  };
  runtime.peekNodeRecord = function (node) { return runtime.nodeRecords.get(node) || null; };
  runtime.logicalParent = function (node) { return runtime.logicalOwners.get(node) || null; };

  runtime.listen = function (target, type, handler, listenerOptions) {
    if (!target || !target.addEventListener) return function () {};
    target.addEventListener(type, handler, listenerOptions);
    var cleanup = function () { target.removeEventListener(type, handler, listenerOptions); };
    runtime.globalCleanups.push(cleanup);
    return cleanup;
  };

  runtime.warn = function (code, message, context) {
    if (!options.development) return;
    var key = code + "\u0000" + message;
    if (runtime.warningKeys.has(key)) return;
    runtime.warningKeys.add(key);
    if (global.console && typeof global.console.warn === "function") {
      global.console.warn("kitwork [" + code + "]: " + message, context || "");
    }
  };

  runtime.contextFor = function (node, directive, source, phase, event) {
    var app = runtime.appForNode(node);
    var component = app ? runtime.components.nearest(node && node.nodeType === 1 ? node : node && node.parentNode, app, true) : null;
    return {
      app: app,
      phase: phase || "runtime",
      directive: directive || "",
      source: source || "",
      element: node && node.nodeType === 1 ? node : null,
      node: node || null,
      host: component ? component.host : null,
      component: component ? component.instance : null,
      componentRecord: component || null,
      event: event || null
    };
  };

  runtime.reportError = function (error, context) {
    var normalized = normalizeRuntimeError(error, null, null, context || null);
    var componentRecord = context && context.componentRecord;
    var handled = false;
    var app = context && context.app;

    if (app && app.errorDepth > 4) return normalized;
    if (app) app.errorDepth++;
    try {
      if (componentRecord && componentRecord.instance && typeof componentRecord.instance.error === "function") {
        try { handled = componentRecord.instance.error(normalized, context || {}) === true; }
        catch (hookError) {
          normalized = normalizeRuntimeError(hookError, "KIT_ERROR_HOOK", "Component error hook failed", context);
        }
      }
      if (!handled && typeof kit.onError === "function") {
        try { handled = kit.onError(normalized, context || {}) === true; }
        catch (hookError2) {
          normalized = normalizeRuntimeError(hookError2, "KIT_ERROR_HOOK", "Global error hook failed", context);
        }
      }
    } finally {
      if (app) app.errorDepth--;
    }

    var root = app ? app.root : document;
    if (!handled && root && root.dispatchEvent && typeof global.CustomEvent === "function") {
      try {
        root.dispatchEvent(new global.CustomEvent("kitwork:error", {
          bubbles: true,
          detail: { error: normalized, context: context || {} }
        }));
      } catch (_) { /* older DOM */ }
    }
    if (!handled && options.development && global.console && typeof global.console.error === "function") {
      global.console.error("kitwork [" + normalized.code + "]: " + normalized.message, context || {}, normalized.cause || "");
    }
    return normalized;
  };

  runtime.appForNode = function (node) {
    if (!node) return runtime.apps.size === 1 ? Array.from(runtime.apps)[0] : null;
    var current = node.nodeType === 1 ? node : node.parentNode;
    while (current) {
      var app = runtime.appsByRoot.get(current);
      if (app && !app.destroyed) return app;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return null;
  };

  runtime.boundaryFor = function (node) {
    var app = runtime.appForNode(node);
    if (!app) return null;
    var component = runtime.components.nearest(node && node.nodeType === 1 ? node : node && node.parentNode, app, true);
    if (component) return component.host;
    var current = node && node.nodeType === 1 ? node : node && node.parentNode;
    while (current && current !== app.root.parentNode) {
      var record = runtime.peekNodeRecord(current);
      if (record && record.scope) return current;
      if (current === app.root) break;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return app.root;
  };

  function collectLoopFrames(node, app, extra) {
    var frames = [];
    (extra || []).forEach(function (frame) { frames.push(frame); });
    var current = node && node.nodeType === 1 ? node : node && node.parentNode;
    var seen = new Set(frames);
    while (current && current !== app.root.parentNode) {
      var record = runtime.peekNodeRecord(current);
      if (record && record.loopFrame && !seen.has(record.loopFrame)) {
        frames.push(record.loopFrame);
        seen.add(record.loopFrame);
      }
      if (current === app.root) break;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return frames;
  }

  function collectLocalScopes(node, app, componentRecord, prepended) {
    var scopes = [];
    (prepended || []).forEach(function (entry) {
      scopes.push(entry.scope ? entry : { scope: entry, boundary: node && node.nodeType === 1 ? node : null });
    });
    var current = node && node.nodeType === 1 ? node : node && node.parentNode;
    var stop = componentRecord ? componentRecord.host : app.root.parentNode;
    while (current && current !== stop) {
      var record = runtime.peekNodeRecord(current);
      if (record && record.scope) scopes.push({ scope: record.scope, boundary: current });
      if (current === app.root) break;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return scopes;
  }

  runtime.environmentFor = function (node, event, extra) {
    extra = extra || {};
    var app = extra.app || runtime.appForNode(node);
    if (!app) throw createRuntimeError("KIT_APP_MISSING", "No Kitwork application owns this node", { node: node });
    var element = extra.element || (node && node.nodeType === 1 ? node : node && node.parentNode);
    var componentRecord;
    if (hasOwn(extra, "componentRecord")) componentRecord = extra.componentRecord;
    else componentRecord = runtime.components.nearest(element, app, true);

    var contexts = createNullObject();
    contexts.$element = element || undefined;
    contexts.$host = componentRecord ? componentRecord.host : undefined;
    contexts.$event = event || undefined;
    contexts.$refs = componentRecord ? componentRecord.refs : undefined;
    contexts.$component = componentRecord ? componentRecord.instance : undefined;
    contexts.$parent = componentRecord && componentRecord.parent ? componentRecord.parent.instance : undefined;

    return createLexicalEnvironment({
      contexts: contexts,
      aliases: app.aliases,
      loopFrames: collectLoopFrames(node, app, extra.loopFrames),
      localScopes: collectLocalScopes(node, app, componentRecord, extra.prependScopes),
      component: componentRecord ? componentRecord.instance : null,
      componentBoundary: componentRecord ? componentRecord.host : null,
      appScope: app.scope,
      appBoundary: app.root,
      kit: runtime.services.publicSurface,
      onDirty: function (boundary, mutation) {
        runtime.scheduler.invalidate(app, boundary || runtime.boundaryFor(node), mutation);
      }
    });
  };

  runtime.evaluateNamedMapInto = function (compiled, node, app, target, extra) {
    if (!compiled || compiled.mode !== MODES.NAMED_MAP) {
      throw createRuntimeError("KIT_MAP_COMPILED", "Expected a named-map compiled expression");
    }
    var staged = target || createNullObject();
    var entries = compiled.ast.entries || [];
    for (var i = 0; i < entries.length; i++) {
      var entry = entries[i];
      var environment = runtime.environmentFor(node, null, Object.assign({}, extra || {}, {
        app: app,
        prependScopes: [{ scope: staged, boundary: node && node.nodeType === 1 ? node : app.root }].concat(extra && extra.prependScopes || [])
      }));
      var value = runtime.expression.evaluate({ mode: MODES.BINDING, source: entry.source, ast: entry.ast }, environment);
      staged[entry.key] = value;
    }
    return staged;
  };

  runtime.markFresh = function (node) {
    var app = runtime.appForNode(node);
    if (!app) return;
    var record = runtime.nodeRecord(node, app);
    record.fresh = true;
    enqueueMicrotask(function () { if (record) record.fresh = false; });
  };
  runtime.isFresh = function (node) {
    var record = runtime.peekNodeRecord(node);
    return !!(record && record.fresh);
  };

  runtime.registerPersist = function (element, app) {
    var record = runtime.nodeRecord(element, app);
    var next = String(element.getAttribute("data-kit-persist") || "").trim();
    if (record.persistKey && record.persistKey !== next && app.persisted.get(record.persistKey) === element) {
      app.persisted.delete(record.persistKey);
    }
    record.persistKey = "";
    if (!next) return;
    var existing = app.persisted.get(next);
    if (existing && existing !== element) {
      throw createRuntimeError("KIT_DUPLICATE_PERSIST_KEY", "Duplicate data-kit-persist key '" + next + "'", {
        key: next,
        element: element,
        existing: existing
      });
    }
    record.persistKey = next;
    app.persisted.set(next, element);
  };

  function initializeAppScope(app) {
    if (app.scopeInitialized) return;
    app.scopeInitialized = true;
    var source = app.root.getAttribute && app.root.getAttribute("data-kit-scope");
    if (!source) return;
    try {
      var compiled = runtime.expression.compile(MODES.NAMED_MAP, source);
      runtime.evaluateNamedMapInto(compiled, app.root, app, app.scope, { componentRecord: null });
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(app.root, "scope", source, "app-scope"));
    }
  }

  function initializeLocalScope(element, app, record) {
    if (record.scopeInitialized) return;
    record.scopeInitialized = true;
    var source = element.getAttribute("data-kit-scope");
    if (!source) return;
    try {
      var target = createNullObject();
      var compiled = runtime.expression.compile(MODES.NAMED_MAP, source);
      runtime.evaluateNamedMapInto(compiled, element, app, target);
      record.scope = target;
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(element, "scope", source, "scope"));
    }
  }

  function evaluateHostSeed(element, app, parentComponent) {
    var source = element.getAttribute("data-kit-scope");
    if (!source) return createNullObject();
    var target = createNullObject();
    var compiled = runtime.expression.compile(MODES.NAMED_MAP, source);
    runtime.evaluateNamedMapInto(compiled, element, app, target, {
      componentRecord: parentComponent || null
    });
    return target;
  }

  function reconcileRef(element, app, componentRecord, isComponentHost) {
    var record = runtime.nodeRecord(element, app);
    var nextName = String(element.getAttribute("data-kit-ref") || "").trim();
    var owner = isComponentHost ? (componentRecord && componentRecord.parent) : componentRecord;
    if (record.refName && (record.refName !== nextName || record.refOwner !== owner)) {
      runtime.components.removeRef(record.refOwner, record.refName, element);
      record.refName = "";
      record.refOwner = null;
    }
    if (!nextName) return;
    try {
      runtime.components.registerRef(owner, nextName, element);
      record.refName = nextName;
      record.refOwner = owner;
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(element, "ref", nextName, "reconcile"));
    }
  }

  function cleanupBinding(binding) {
    if (!binding || binding.destroyed) return;
    binding.destroyed = true;
    if (binding.contract && typeof binding.contract.unmount === "function") {
      try { binding.contract.unmount(binding, runtime); }
      catch (error) { runtime.reportError(error, runtime.contextFor(binding.element, binding.attributeName, binding.source, "binding-unmount")); }
    }
    runtime.events.unregister(binding);
    if (typeof binding.cleanup === "function") {
      try { binding.cleanup(); } catch (error2) { runtime.reportError(error2, runtime.contextFor(binding.element, binding.attributeName, binding.source, "binding-cleanup")); }
    }
    binding.app.bindings.delete(binding);
    var nodeRecord = runtime.peekNodeRecord(binding.element);
    if (nodeRecord) nodeRecord.bindings.delete(binding.attributeName);
  }
  runtime.cleanupBinding = cleanupBinding;

  function createDirectiveBinding(element, app, attribute, directiveName, contract) {
    var source = attribute.value;
    var compiled = runtime.expression.compile(contract.mode, source);
    var binding = createBindingRecord(app, element, attribute.name, directiveName, contract, source, compiled);
    if (typeof contract.validate === "function") contract.validate(binding, runtime);
    if (typeof contract.mount === "function") contract.mount(binding, runtime);
    runtime.nodeRecord(element, app).bindings.set(attribute.name, binding);
    app.bindings.add(binding);
    return binding;
  }

  function createEventBinding(element, app, attribute, eventSpec) {
    var contract = { name: eventSpec.type, mode: MODES.ACTION, phase: "event" };
    var compiled = runtime.expression.compile(MODES.ACTION, attribute.value);
    var binding = createBindingRecord(app, element, attribute.name, eventSpec.type, contract, attribute.value, compiled);
    runtime.nodeRecord(element, app).bindings.set(attribute.name, binding);
    app.bindings.add(binding);
    runtime.events.register(binding, eventSpec);
    return binding;
  }

  runtime.reconcileBindings = function (element, app) {
    var record = runtime.nodeRecord(element, app);
    var active = new Set();
    var attributes = Array.prototype.slice.call(element.attributes || []);

    attributes.forEach(function (attribute) {
      if (attribute.name.indexOf("data-kit-") !== 0 || RESERVED_ATTRIBUTES.has(attribute.name)) return;
      var directiveName = attribute.name.slice(9);
      var contract = runtime.directives.get(directiveName);
      var eventSpec = null;
      if (!contract) {
        try { eventSpec = runtime.events.parseAttribute(attribute.name); }
        catch (error) {
          runtime.reportError(error, runtime.contextFor(element, attribute.name, attribute.value, "event-parse"));
          return;
        }
      }
      if (!contract && !eventSpec) {
        if (directiveName === "teleport" || directiveName === "transition") {
          runtime.warn("KIT_CAPABILITY_DEFERRED", "Directive '" + directiveName + "' requires an optional capability", { element: element });
        } else {
          runtime.warn("KIT_UNKNOWN_DIRECTIVE", "Unknown Kitwork directive '" + attribute.name + "'", { element: element });
        }
        return;
      }

      active.add(attribute.name);
      var existing = record.bindings.get(attribute.name);
      if (existing && existing.source === attribute.value) return;
      if (existing) cleanupBinding(existing);
      try {
        if (contract) createDirectiveBinding(element, app, attribute, directiveName, contract);
        else createEventBinding(element, app, attribute, eventSpec);
      } catch (error2) {
        runtime.reportError(error2, runtime.contextFor(element, attribute.name, attribute.value, "binding-create"));
      }
    });

    Array.from(record.bindings.entries()).forEach(function (entry) {
      if (!active.has(entry[0])) cleanupBinding(entry[1]);
    });
  };

  runtime.hydrateTree = function hydrateTree(node, app) {
    if (!node || !app || app.destroyed) return;
    if (node.nodeType !== 1) {
      runtime.nodeRecord(node, app).hydrated = true;
      return;
    }

    if (node !== app.root && node.hasAttribute("data-kit-app")) return;

    if (node.hasAttribute("data-kit-if") || node.hasAttribute("data-kit-for")) {
      try { runtime.structural.collect(node, app); }
      catch (error) { runtime.reportError(error, runtime.contextFor(node, "structure", "", "hydrate")); }
      return;
    }

    var record = runtime.nodeRecord(node, app);
    if (record.destroyed) return;

    var componentName = String(node.getAttribute("data-kit-component") || "").trim();
    var componentRecord = null;
    if (componentName) {
      if (node === app.root) {
        runtime.reportError(createRuntimeError("KIT_APP_COMPONENT_CONFLICT", "data-kit-app and data-kit-component cannot share one element in Runtime 1.0", {
          element: node
        }), runtime.contextFor(node, "component", componentName, "hydrate"));
      }
      var parentComponent = runtime.components.nearest(node, app, false);
      var seed = createNullObject();
      try { seed = evaluateHostSeed(node, app, parentComponent); }
      catch (error2) { runtime.reportError(error2, runtime.contextFor(node, "scope", node.getAttribute("data-kit-scope") || "", "component-seed")); }
      componentRecord = runtime.components.ensure(app, node, componentName, parentComponent, seed);
      record.component = componentRecord;
      record.scopeInitialized = true;
    } else {
      record.component = runtime.components.nearest(node, app, true);
      if (node !== app.root && node.hasAttribute("data-kit-scope")) initializeLocalScope(node, app, record);
    }

    reconcileRef(node, app, componentRecord || record.component, !!componentName);
    if (node.hasAttribute("data-kit-persist")) {
      try { runtime.registerPersist(node, app); }
      catch (error3) { runtime.reportError(error3, runtime.contextFor(node, "persist", node.getAttribute("data-kit-persist"), "hydrate")); }
    }
    runtime.reconcileBindings(node, app);
    record.hydrated = true;

    var children = Array.prototype.slice.call(node.childNodes || []);
    for (var i = 0; i < children.length; i++) runtime.hydrateTree(children[i], app);
  };

  runtime.cleanupTree = function cleanupTree(node, expectedApp) {
    if (!node) return;
    var children = Array.prototype.slice.call(node.childNodes || []);
    for (var i = children.length - 1; i >= 0; i--) cleanupTree(children[i], expectedApp);

    var record = runtime.peekNodeRecord(node);
    if (!record || record.destroyed || expectedApp && record.app !== expectedApp) return;
    record.destroyed = true;

    Array.from(record.bindings.values()).forEach(cleanupBinding);
    if (record.refName) runtime.components.removeRef(record.refOwner, record.refName, node);
    if (record.persistKey && record.app.persisted.get(record.persistKey) === node) record.app.persisted.delete(record.persistKey);
    if (record.structure) runtime.structural.cleanup(record.structure);
    // Descendant node records point at their owning component for context
    // resolution. Only the actual component host owns the instance lifecycle.
    if (record.component && record.component.host === node) {
      runtime.components.unmount(record.component);
    }
    while (record.cleanups.length) {
      try { record.cleanups.pop()(); }
      catch (error) { runtime.reportError(error, runtime.contextFor(node, "cleanup", "", "cleanup")); }
    }
    runtime.nodeRecords.delete(node);
  };

  function renderBinding(binding) {
    if (!binding || binding.disabled || !binding.element.isConnected) return;
    try {
      var environment = runtime.environmentFor(binding.element, null);
      var result = runtime.expression.execute(binding.compiled, environment);
      binding.ownerBoundary = runtime.boundaryFor(binding.element);
      if (binding.contract && typeof binding.contract.update === "function") binding.contract.update(binding, result, runtime);
      binding.initialized = true;
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(binding.element, binding.attributeName, binding.source, "render"));
    }
  }

  runtime.renderBoundaries = function (app, boundaries) {
    app.renderCount++;
    boundaries.forEach(function (boundary) {
      var structurePass = 0;
      while (structurePass++ < 8 && runtime.structural.render(app, boundary)) { /* settle nested structure */ }

      var bindings = Array.from(app.bindings).filter(function (binding) {
        return !binding.destroyed && binding.phase !== "event" && binding.element.isConnected &&
          (boundary === app.root || nodeContains(boundary, binding.element));
      });
      bindings.sort(function (left, right) {
        var phase = (PHASE_ORDER[left.phase] || 50) - (PHASE_ORDER[right.phase] || 50);
        if (phase) return phase;
        return nodeDepth(left.element) - nodeDepth(right.element);
      });
      bindings.forEach(renderBinding);
      runtime.components.mountPending(app, boundary);
    });
  };

  function setupObserver(app) {
    if (!global.MutationObserver || !app.root) return;
    app.observer = new global.MutationObserver(function (mutations) {
      mutations.forEach(function (mutation) {
        if (mutation.type === "childList") {
          Array.prototype.slice.call(mutation.addedNodes || []).forEach(function (node) {
            if (!node.isConnected) return;
            var owner = runtime.appForNode(node) || app;
            if (owner !== app) return;

            // Moving a hydrated subtree across application roots changes every
            // ownership contract (scope, aliases, tasks, scheduler and errors).
            // Tear down the old app record before hydrating it into the new app.
            var previousRecord = runtime.peekNodeRecord(node);
            if (previousRecord && previousRecord.app && previousRecord.app !== app) {
              runtime.cleanupTree(node, previousRecord.app);
            }
            runtime.hydrateTree(node, app);
            var boundaryNode = node.nodeType === 1 ? node : node.parentNode;
            runtime.scheduler.invalidate(app, runtime.boundaryFor(boundaryNode || app.root), {
              type: "mutation-add"
            });
          });
          Array.prototype.slice.call(mutation.removedNodes || []).forEach(function (node) {
            app.removedNodes.add(node);
          });
          return;
        }

        // The runtime owns class/style/aria/data/hidden output mutations. Re-rendering
        // because of those mutations creates a feedback loop. Only authored Kitwork
        // directive changes are reconciled through the observer.
        if (mutation.type === "attributes" && mutation.attributeName &&
            mutation.attributeName.indexOf("data-kit-") === 0) {
          var element = mutation.target;
          if (element.isConnected && runtime.appForNode(element) === app) {
            runtime.hydrateTree(element, app);
            runtime.scheduler.invalidate(app, runtime.boundaryFor(element), {
              type: "directive-attribute",
              name: mutation.attributeName
            });
          }
        }
      });

      if (app.removedNodes.size) enqueueMicrotask(function () {
        Array.from(app.removedNodes).forEach(function (node) {
          app.removedNodes.delete(node);
          var currentApp = node.isConnected ? runtime.appForNode(node) : null;
          // A DOM move inside the same application keeps logical ownership and must
          // not unmount. A real removal, or a move across app roots, is cleaned up.
          if (!node.isConnected || currentApp !== app) runtime.cleanupTree(node, app);
        });
      });
    });
    app.observer.observe(app.root, { childList: true, subtree: true, attributes: true });
  }

  function createApp(root, id) {
    if (runtime.appsByRoot.has(root)) return runtime.appsByRoot.get(root);
    var app = createAppRecord(root, id || root.getAttribute && root.getAttribute("data-kit-app") || "main");
    runtime.apps.add(app);
    runtime.appsByRoot.set(root, app);
    initializeAppScope(app);
    setupObserver(app);
    runtime.hydrateTree(root, app);
    app.initialized = true;
    runtime.scheduler.invalidate(app, root, { type: "app-start" });
    runtime.scheduler.flush(app);
    return app;
  }

  function discoverRoots() {
    var roots = Array.prototype.slice.call(document.querySelectorAll("[data-kit-app]"));
    if (!roots.length) return [document.documentElement];
    roots.forEach(function (root) {
      var parent = root.parentElement && root.parentElement.closest("[data-kit-app]");
      if (parent) throw createRuntimeError("KIT_APP_NESTED", "Nested data-kit-app roots are not allowed", {
        root: root,
        parent: parent
      });
    });
    return roots;
  }

  runtime.start = function (root) {
    if (!document || runtime.destroyed) return runtime.publicApi;
    if (root) createApp(root, root.getAttribute && root.getAttribute("data-kit-app") || "main");
    else discoverRoots().forEach(function (candidate) { createApp(candidate); });
    runtime.started = true;
    if (document && document.dispatchEvent && typeof global.CustomEvent === "function") {
      document.dispatchEvent(new global.CustomEvent("kitwork:ready", {
        detail: { runtime: kit.runtime, apps: runtime.apps.size }
      }));
    }
    return runtime.publicApi;
  };

  runtime.destroyApp = function (app) {
    if (!app || app.destroyed) return;
    app.destroyed = true;
    if (runtime.task && typeof runtime.task.abort === "function") {
      try {
        runtime.task.abort(app, undefined, "app-destroy");
        runtime.task.abort(app.scope, undefined, "app-destroy");
      } catch (error) {
        runtime.reportError(error, { app: app, phase: "task-abort", directive: "data-kit-app", source: app.name });
      }
    }
    if (app.observer) app.observer.disconnect();
    runtime.cleanupTree(app.root, app);
    app.aliases.clear();
    app.bindings.clear();
    app.structures.clear();
    app.persisted.clear();
    app.dirtyBoundaries.clear();
    runtime.apps.delete(app);
    runtime.appsByRoot.delete(app.root);
  };

  runtime.destroy = function (root) {
    if (root) runtime.destroyApp(runtime.appsByRoot.get(root));
    else Array.from(runtime.apps).forEach(runtime.destroyApp);
    if (!root) {
      while (runtime.globalCleanups.length) {
        try { runtime.globalCleanups.pop()(); } catch (_) { /* cleanup */ }
      }
      runtime.destroyed = true;
      runtime.started = false;
    }
  };

  runtime.render = function (target) {
    if (!target) {
      runtime.apps.forEach(function (app) { runtime.scheduler.invalidate(app, app.root, { type: "manual" }); });
      return;
    }
    if (target.nodeType) {
      var app = runtime.appForNode(target);
      if (app) runtime.scheduler.invalidate(app, runtime.boundaryFor(target), { type: "manual" });
      return;
    }
    runtime.apps.forEach(function (app) {
      app.components.forEach(function (record) {
        if (record.instance === target) runtime.scheduler.invalidate(app, record.host, { type: "manual" });
      });
    });
  };

  runtime.inspect = function (element) {
    var app = runtime.appForNode(element);
    var record = runtime.peekNodeRecord(element);
    var component = app ? runtime.components.nearest(element, app, true) : null;
    return {
      app: app,
      node: record,
      component: component,
      scope: record && record.scope,
      refs: component && component.refs,
      aliases: app ? Array.from(app.aliases.keys()) : [],
      bindings: record ? Array.from(record.bindings.keys()) : [],
      dirtyBoundaries: app ? Array.from(app.dirtyBoundaries) : []
    };
  };

  coreDirectivesModule.installCoreDirectives(runtime);
  runtime.model.install(document);

  // Built-in runtime services are installed through the same public service
  // registry as application capabilities. Trusted JavaScript always receives
  // the concrete service. Authored expressions see only the explicitly granted
  // request members; task orchestration remains a component-method concern.
  runtime.services.register("task", runtime.task, { expression: [] });
  runtime.services.register("request", runtime.request, {
    expression: ["get", "post", "submit", "abort"]
  });

  kit.runtime = Object.assign(kit.runtime && typeof kit.runtime === "object" ? kit.runtime : {}, {
    name: "kitwork",
    version: "3.0.0-draft.m2",
    specification: "0.7.0-draft",
    development: !!options.development,
    loaded: true,
    booted: false
  });

  kit.component = function (name, definition) {
    if (arguments.length === 1) return runtime.components.get(name);
    return runtime.components.register(name, definition);
  };
  kit.service = function (name, implementation, serviceOptions) {
    if (arguments.length === 1) return runtime.services.get(name);
    return runtime.services.register(name, implementation, serviceOptions);
  };
  kit.start = function (root) {
    var result = runtime.start(root);
    kit.runtime.booted = true;
    return result;
  };
  kit.destroy = function (root) {
    runtime.destroy(root);
    if (!root) kit.runtime.booted = false;
  };
  kit.render = runtime.render;
  kit.onError = typeof kit.onError === "function" ? kit.onError : function () { return false; };
  kit.dev = kit.dev || {};
  kit.dev.inspect = runtime.inspect;

  runtime.publicApi = kit;
  kit.internal = Object.freeze({
    runtime: runtime,
    persist: Object.freeze({
      find: function (root, key) {
        var app = root && root.root ? root : runtime.appsByRoot.get(root);
        return app ? app.persisted.get(String(key)) || null : null;
      }
    }),
    hydrate: runtime.hydrateTree,
    cleanupTree: runtime.cleanupTree,
    invalidate: function (node) {
      var app = runtime.appForNode(node);
      if (app) runtime.scheduler.invalidate(app, runtime.boundaryFor(node), { type: "internal" });
    }
  });

  if (document && options.autoStart !== false) {
    if (document.readyState === "loading") {
      runtime.listen(document, "DOMContentLoaded", function () { if (!runtime.started) kit.start(); }, { once: true });
    } else enqueueMicrotask(function () { if (!runtime.started) kit.start(); });
  }

  return runtime;
}

module.exports = {
  createRuntime: createRuntime
};
