; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "evaluator") throw new Error("KitJS: component loaded out of order");
  if (core.reuse) { core.phase = "component"; return; }

  var OWN = core.OWN;
  var COMPONENTS = "[data-kit-component]";
  var VERSIONED = "[data-kit-component],[data-kit-version]";
  var ALIASES = "[data-kit-as]";
  var aliases = new WeakMap();
  var metadata = new WeakMap();
  var cleanupObserver = null;
  var cleanupOwners = 0;
  var removedRoots = new Set();
  var removalQueued = false;
  var retainValidity = new WeakMap();
  var retainStructureValidity = new WeakMap();
  var retainReports = new WeakMap();
  var COMPONENT_NAME = /^[A-Za-z_$][A-Za-z0-9_$.-]*$/;
  var SERVICE_NAME = /^[A-Za-z][A-Za-z0-9_.-]*$/;
  var GRAPH_ID = /^[0-9a-f]{64}$/;
  var RETAIN_KEY = /^[A-Za-z][A-Za-z0-9._:-]{0,127}$/;
  var EXACT_SEMVER = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;
  var RESERVED_ALIASES = {
    $element: true, $host: true, $event: true, $refs: true, $component: true,
    $parent: true, $error: true, $alias: true, $invalidate: true
  };

  function enqueue(callback) {
    var schedule = document.defaultView && document.defaultView.queueMicrotask;
    if (typeof schedule === "function") schedule.call(document.defaultView, callback);
    else Promise.resolve().then(callback);
  }

  function connectedHere(element) {
    return !!element && element.isConnected === true && element.ownerDocument === document;
  }

  function flushRemovedRoots() {
    removalQueued = false;
    var roots = Array.from(removedRoots);
    removedRoots.clear();
    roots.forEach(function (root) {
      if (connectedHere(root) || typeof core.disposeTree !== "function") return;
      try { core.disposeTree(root); }
      catch (error) { core.report(error); }
    });
  }

  function queueRemovedRoot(root) {
    if (!root || root.nodeType !== 1) return;
    removedRoots.add(root);
    if (removalQueued) return;
    removalQueued = true;
    enqueue(flushRemovedRoots);
  }

  function startCleanupObserver() {
    if (cleanupObserver || cleanupOwners < 1) return;
    var Observer = document.defaultView && document.defaultView.MutationObserver;
    if (typeof Observer !== "function") return;
    cleanupObserver = new Observer(function (mutations) {
      mutations.forEach(function (mutation) {
        Array.prototype.forEach.call(mutation.removedNodes || [], queueRemovedRoot);
      });
    });
    cleanupObserver.observe(document, { childList: true, subtree: true });
  }

  function stopCleanupObserver() {
    if (cleanupOwners > 0 || !cleanupObserver) return;
    cleanupObserver.disconnect();
    cleanupObserver = null;
  }

  function ownCleanup(current, cleanup) {
    current.cleanup = cleanup;
    if (!connectedHere(current.host)) {
      queueRemovedRoot(current.host);
      return;
    }
    current.ownsCleanup = true;
    cleanupOwners++;
    startCleanupObserver();
  }

  function copyValue(value, seen) {
    if (value === null || typeof value !== "object") return value;
    var prototype = Object.getPrototypeOf(value);
    if (!Array.isArray(value) && prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("KitJS: component state must contain only plain objects and arrays");
    }
    if (seen.has(value)) throw new TypeError("KitJS: circular component state is not supported");
    seen.add(value);
    var output = Array.isArray(value) ? [] : Object.create(prototype);
    Object.keys(value).forEach(function (name) {
      if (core.blocked(name)) throw new TypeError("KitJS: blocked component state key \"" + name + "\"");
      output[name] = copyValue(value[name], seen);
    });
    seen.delete(value);
    return output;
  }

  function snapshot(definition) {
    var descriptors = Object.getOwnPropertyDescriptors(definition);
    if (Object.getOwnPropertySymbols(definition).length) {
      throw new TypeError("KitJS: component definitions cannot contain symbol fields");
    }
    Object.keys(descriptors).forEach(function (name) {
      if (name.charAt(0) === "$") {
        throw new TypeError("KitJS: component fields cannot use the reserved $ namespace");
      }
      if (core.blocked(name)) throw new TypeError("KitJS: blocked component field \"" + name + "\"");
      var descriptor = descriptors[name];
      if (OWN.call(descriptor, "value") && typeof descriptor.value === "object" && descriptor.value !== null) {
        descriptor.value = copyValue(descriptor.value, new WeakSet());
      }
    });
    return descriptors;
  }

  function validComponentName(name) {
    return typeof name === "string" && COMPONENT_NAME.test(name) && !core.blocked(name);
  }

  function validServiceName(name) {
    return typeof name === "string" && SERVICE_NAME.test(name) &&
      name !== "version" && name !== "component" && name !== "service" &&
      !core.blocked(name);
  }

  function validVersion(version) {
    return typeof version === "string" && EXACT_SEMVER.test(version);
  }

  function graphError(message) {
    throw new TypeError("KitJS: invalid component graph: " + message);
  }

  function installComponentGraph(source) {
    if (core.graph) throw new Error("KitJS: component graph is already installed");
    if (core.booted || core.phase !== "events" && core.phase !== "drive") {
      throw new Error("KitJS: component graph must be installed immediately before boot");
    }
    var prototype = source && Object.getPrototypeOf(source);
    if (!source || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(source).length) graphError("expected a plain object");
    var id = source.id;
    var profile = source.profile;
    var services = source.services;
    var components = source.components;
    if (typeof id !== "string" || !GRAPH_ID.test(id)) graphError("invalid id");
    if (profile !== "kit" && profile !== "hydrate") graphError("invalid profile");
    if (profile !== (core.phase === "drive" ? "hydrate" : "kit")) {
      graphError("profile does not match the assembled runtime");
    }
    prototype = services && Object.getPrototypeOf(services);
    if (!services || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(services).length) graphError("services must be a plain object");
    prototype = components && Object.getPrototypeOf(components);
    if (!components || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(components).length) graphError("components must be a plain object");

    var serviceManifest = Object.create(null);
    Object.keys(services).forEach(function (name) {
      var version = services[name];
      if (!validServiceName(name)) graphError("invalid service name \"" + name + "\"");
      if (!validVersion(version)) graphError("invalid version for service \"" + name + "\"");
      serviceManifest[name] = version;
    });
    if (Object.keys(serviceManifest).length && !(core.serviceRegistry instanceof Map)) {
      graphError("services require the service registrar");
    }
    if (core.serviceRegistry) {
      core.serviceRegistry.forEach(function (_, name) {
        if (!OWN.call(serviceManifest, name)) {
          graphError("registered service \"" + name + "\" is not declared");
        }
      });
    }

    var componentManifest = Object.create(null);
    Object.keys(components).forEach(function (name) {
      var version = components[name];
      if (!validComponentName(name)) graphError("invalid component name \"" + name + "\"");
      if (!validVersion(version)) graphError("invalid version for component \"" + name + "\"");
      componentManifest[name] = version;
    });
    core.registry.forEach(function (_, name) {
      if (!OWN.call(componentManifest, name)) {
        graphError("registered component \"" + name + "\" is not declared");
      }
    });
    Object.freeze(serviceManifest);
    Object.freeze(componentManifest);
    var graph = Object.freeze({
      id: id,
      profile: profile,
      services: serviceManifest,
      components: componentManifest
    });
    Object.defineProperty(core.kit, Symbol.for("kitjs:graph"), { value: graph });
    core.graph = graph;
  }

  function metadataError(element, entry, message, shouldReport) {
    entry.value = null;
    entry.error = message;
    if (shouldReport && !entry.reported) {
      entry.reported = true;
      core.report(new TypeError("KitJS: " + message));
    }
    metadata.set(element, entry);
    return null;
  }

  // Undefined means this element carries no component metadata. Null means its
  // metadata is invalid. A record means it is a valid component host.
  function componentMetadata(element, shouldReport) {
    if (!element || element.nodeType !== 1 || !element.hasAttribute) return undefined;
    var hasComponent = element.hasAttribute("data-kit-component");
    var hasVersion = element.hasAttribute("data-kit-version");
    if (!hasComponent && !hasVersion) return undefined;
    var componentSource = hasComponent ? element.getAttribute("data-kit-component") : null;
    var versionSource = hasVersion ? element.getAttribute("data-kit-version") : null;
    var entry = metadata.get(element);
    if (entry && entry.componentSource === componentSource && entry.versionSource === versionSource) {
      if (shouldReport && entry.error && !entry.reported) {
        entry.reported = true;
        core.report(new TypeError("KitJS: " + entry.error));
      }
      return entry.value;
    }
    entry = {
      componentSource: componentSource,
      versionSource: versionSource,
      value: undefined,
      error: "",
      reported: false
    };
    if (!hasComponent) {
      return metadataError(element, entry, "data-kit-version requires a component host", shouldReport);
    }
    var name = String(componentSource || "").trim();
    if (name.indexOf("@") >= 0) {
      return metadataError(element, entry,
        "inline component versions are not supported; use data-kit-version", shouldReport);
    }
    if (!validComponentName(name)) {
      return metadataError(element, entry, "invalid component name \"" + name + "\"", shouldReport);
    }
    var version = null;
    if (hasVersion) {
      version = String(versionSource || "").trim();
      if (!validVersion(version)) {
        return metadataError(element, entry,
          "data-kit-version must be an exact semantic version", shouldReport);
      }
      if (!core.graph || !OWN.call(core.graph.components, name)) {
        return metadataError(element, entry,
          "component \"" + name + "\" is not present in the installed graph", shouldReport);
      }
      if (core.graph.components[name] !== version) {
        return metadataError(element, entry,
          "component \"" + name + "\" requires " + version +
          " but the installed graph provides " + core.graph.components[name], shouldReport);
      }
    }
    entry.value = { name: name, version: version };
    metadata.set(element, entry);
    return entry.value;
  }

  function retainIssue(entry, message) {
    if (entry.errors.indexOf(message) < 0) entry.errors.push(message);
  }

  function inspectRetains(root, shouldReport, strict) {
    root = root && (root.nodeType === 1 || root.nodeType === 9 || root.nodeType === 11) ? root : document;
    var entries = [];

    function rememberStructural(element) {
      retainStructureValidity.set(element, false);
    }

    function visit(node, templates, structures, retainedAncestor) {
      if (!node) return;
      if (node.nodeType === 1) {
        if (node.hasAttribute("data-kit-ignore")) return;
        var name = String(node.localName || "").toLowerCase();
        var nextTemplates = templates;
        var nextStructures = structures;
        if (name === "template") {
          rememberStructural(node);
          nextTemplates = templates.concat(node);
        }
        if (node.hasAttribute("data-kit-if") || node.hasAttribute("data-kit-for")) {
          rememberStructural(node);
          nextStructures = structures.concat(node);
        }

        var nextRetained = retainedAncestor;
        if (node.hasAttribute("data-kit-retain")) {
          var key = node.getAttribute("data-kit-retain");
          var entry = {
            key: key,
            element: node,
            request: null,
            mounted: null,
            blocked: false,
            errors: []
          };
          retainValidity.set(node, false);
          if (!RETAIN_KEY.test(key)) retainIssue(entry, "invalid key \"" + key + "\"");
          if (!node.hasAttribute("data-kit-component")) {
            retainIssue(entry, "key \"" + key + "\" requires a component host");
          } else {
            entry.request = componentMetadata(node, false);
            if (!entry.request) retainIssue(entry, "key \"" + key + "\" has invalid component metadata");
          }
          var mounted = core.scopes.get(node);
          if (core.scopes.has(node)) {
            if (mounted && !mounted.failed && !mounted.disposed && mounted.componentIdentity) {
              entry.mounted = mounted.componentIdentity;
            } else entry.blocked = true;
          }
          if (nextTemplates.length) {
            retainIssue(entry, "key \"" + key + "\" cannot be used inside a template");
            nextTemplates.forEach(function (template) { retainStructureValidity.set(template, true); });
          }
          if (nextStructures.length) {
            retainIssue(entry, "key \"" + key + "\" cannot be used in a structural region");
            nextStructures.forEach(function (structure) { retainStructureValidity.set(structure, true); });
          }
          if (retainedAncestor) {
            retainIssue(entry, "key \"" + key + "\" cannot be nested below retained key \"" +
              retainedAncestor.key + "\"");
            retainIssue(retainedAncestor, "retained key \"" + retainedAncestor.key +
              "\" cannot contain retained key \"" + key + "\"");
          }
          entries.push(entry);
          nextRetained = entry;
        }

        var child = node.firstChild;
        while (child) {
          visit(child, nextTemplates, nextStructures, nextRetained);
          child = child.nextSibling;
        }
        if (name === "template" && node.content) {
          child = node.content.firstChild;
          while (child) {
            visit(child, nextTemplates, nextStructures, nextRetained);
            child = child.nextSibling;
          }
        }
        return;
      }
      var descendant = node.firstChild;
      while (descendant) {
        visit(descendant, templates, structures, retainedAncestor);
        descendant = descendant.nextSibling;
      }
    }

    visit(root, [], [], null);
    var duplicates = new Map();
    entries.forEach(function (entry) {
      if (!duplicates.has(entry.key)) duplicates.set(entry.key, []);
      duplicates.get(entry.key).push(entry);
    });
    duplicates.forEach(function (matches, key) {
      if (matches.length < 2) return;
      matches.forEach(function (entry) { retainIssue(entry, "duplicate key \"" + key + "\""); });
    });

    var firstError = "";
    var byKey = new Map();
    entries.forEach(function (entry) {
      var invalid = entry.errors.length > 0;
      retainValidity.set(entry.element, invalid);
      if (!invalid) {
        retainReports.delete(entry.element);
        byKey.set(entry.key, entry);
        return;
      }
      var message = entry.errors[0];
      if (!firstError) firstError = message;
      if (shouldReport && retainReports.get(entry.element) !== message) {
        retainReports.set(entry.element, message);
        core.report(new TypeError("KitJS: invalid data-kit-retain: " + message));
      }
    });
    if (strict && firstError) throw new TypeError("KitJS: invalid data-kit-retain: " + firstError);
    return firstError ? null : byKey;
  }

  function prepareComponentTree(root, nested) {
    root = root && root.querySelectorAll ? root : document;
    if (root.nodeType === 1 && root.matches(VERSIONED)) componentMetadata(root, true);
    root.querySelectorAll(VERSIONED).forEach(function (element) {
      componentMetadata(element, true);
    });
    root.querySelectorAll("template").forEach(function (template) {
      if (template.content) prepareComponentTree(template.content, true);
    });
    if (!nested) inspectRetains(root, true, false);
  }

  function assertComponentGraph() {
    if (!core.graph) return;
    if (core.serviceRegistry) {
      core.serviceRegistry.forEach(function (_, name) {
        if (!OWN.call(core.graph.services, name)) {
          throw new Error("KitJS: service \"" + name + "\" is not declared by the installed graph");
        }
      });
    }
    Object.keys(core.graph.services).forEach(function (name) {
      if (!core.serviceRegistry || !core.serviceRegistry.has(name)) {
        throw new Error("KitJS: service graph is missing definition \"" + name + "\"");
      }
    });
    core.registry.forEach(function (_, name) {
      if (!OWN.call(core.graph.components, name)) {
        throw new Error("KitJS: component \"" + name + "\" is not declared by the installed graph");
      }
    });
    Object.keys(core.graph.components).forEach(function (name) {
      if (!core.registry.has(name)) {
        throw new Error("KitJS: component graph is missing definition \"" + name + "\"");
      }
    });
  }

  function component(name, definition) {
    if (arguments.length !== 2) throw new TypeError("KitJS: component(name, definition) expects two arguments");
    if (typeof name !== "string" || !COMPONENT_NAME.test(name)) {
      throw new TypeError("KitJS: invalid component name");
    }
    if (core.blocked(name)) throw new TypeError("KitJS: blocked component name");
    if (core.graph && !OWN.call(core.graph.components, name)) {
      throw new Error("KitJS: component \"" + name + "\" is not declared by the installed graph");
    }
    var prototype = definition && Object.getPrototypeOf(definition);
    if (!definition || prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("KitJS: component definition must be a plain object");
    }
    if (core.registry.has(name)) throw new Error("KitJS: component \"" + name + "\" already exists");
    core.registry.set(name, snapshot(definition));
    if (core.booted) core.invalidate();
  }

  function createInstance(descriptors, host, request) {
    var own = {};
    Object.keys(descriptors).forEach(function (name) {
      own[name] = Object.assign({}, descriptors[name]);
      if (name !== "init" && OWN.call(own[name], "value")) {
        own[name].value = copyValue(own[name].value, new WeakSet());
      }
    });
    var init = own.init && own.init.value;
    if (own.init && (typeof init !== "function" || own.init.get || own.init.set)) {
      throw new TypeError("KitJS: init must be a method");
    }
    delete own.init;
    var target = Object.defineProperties(Object.create(null), own);
    var current = {
      host: host,
      scope: null,
      init: init,
      cleanup: null,
      ownsCleanup: false,
      initialized: false,
      rendered: false,
      disposed: false,
      structures: undefined,
      observations: null,
      captures: new WeakMap(),
      componentIdentity: Object.freeze({
        name: request.name,
        version: request.version,
        alias: host.hasAttribute("data-kit-as") ? host.getAttribute("data-kit-as") : null
      })
    };
    var scope = new Proxy(target, {
      set: function (object, name, value, receiver) {
        if (core.blocked(String(name))) return false;
        var before = Reflect.get(object, name, receiver);
        var success = Reflect.set(object, name, value, receiver);
        if (success && !core.equal(before, Reflect.get(object, name, receiver))) core.invalidate(current);
        return success;
      },
      deleteProperty: function (object, name) {
        if (core.blocked(String(name))) return false;
        var success = Reflect.deleteProperty(object, name);
        if (success) core.invalidate(current);
        return success;
      }
    });
    current.scope = scope;
    core.scopeRecords.set(scope, current);
    return current;
  }

  function nearest(element) {
    while (element) {
      if (element.nodeType === 1 && element.hasAttribute("data-kit-component")) return element;
      element = element.parentElement;
    }
    return null;
  }
  function invalidRetainHost(element) {
    if (!element || !element.hasAttribute("data-kit-retain")) return false;
    if (!retainValidity.has(element)) {
      inspectRetains(connectedHere(element) ? document : element, true, false);
    }
    return retainValidity.get(element) === true;
  }
  function ensureComponent(element) {
    var current = core.scopes.get(element);
    if (current) return current.failed ? null : current;
    if (invalidRetainHost(element)) {
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      return null;
    }
    var request = componentMetadata(element, true);
    if (!request) {
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      return null;
    }
    var descriptors = core.registry.get(request.name);
    if (!descriptors) return null;
    try {
      current = createInstance(descriptors, element, request);
      core.scopes.set(element, current);
      return current;
    } catch (error) {
      core.report(error);
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      return null;
    }
  }
  function scopeRecordFor(element) {
    var boundary = nearest(element);
    return boundary ? ensureComponent(boundary) : null;
  }
  function ownedElements(current, selector) {
    var output = [];
    var host = current && current.host;
    if (!host || current.disposed || !host.isConnected) return output;
    if (host.matches(selector)) output.push(host);
    var walker = document.createTreeWalker(host, 1, {
      acceptNode: function (element) {
        if (element.hasAttribute("data-kit-component")) return 2;
        return element.matches(selector) ? 1 : 3;
      }
    });
    var element;
    while ((element = walker.nextNode())) output.push(element);
    return output;
  }
  function initialize(current) {
    if (!current || current.initialized || current.disposed) return;
    current.initialized = true;
    if (!current.init) return;
    try {
      var initialized = current.init.call(current.scope);
      if (typeof initialized === "function") ownCleanup(current, initialized);
      else core.observe(initialized, current);
    }
    catch (error) { core.report(error); }
  }
  function componentElements(root) {
    var output = [];
    root = root && root.querySelectorAll ? root : document;
    if (root.nodeType === 1 && root.matches(COMPONENTS)) output.push(root);
    root.querySelectorAll(COMPONENTS).forEach(function (element) { output.push(element); });
    return output;
  }
  function liveComponents(root) {
    var output = [];
    componentElements(root).forEach(function (element) {
      if (element.hasAttribute("data-kit-as")) aliasName(element);
      var current = ensureComponent(element);
      if (current) output.push(current);
    });
    return output;
  }
  function disposeComponent(element) {
    var current = core.scopes.get(element);
    if (!current || current.disposed) return;
    current.disposed = true;
    var cleanup = current.cleanup;
    var scope = current.scope;
    current.cleanup = null;
    if (current.ownsCleanup) {
      current.ownsCleanup = false;
      cleanupOwners--;
      stopCleanupObserver();
    }
    if (typeof cleanup === "function") {
      try { cleanup.call(scope); }
      catch (error) { core.report(error); }
    }
    if (current.observations) current.observations.clear();
    if (current.scope) core.scopeRecords.delete(current.scope);
    current.host = null;
    current.scope = null;
    current.init = null;
    current.captures = null;
    current.componentIdentity = null;
    core.dirtyRecords.delete(current);
    if (core.renderPending) core.renderPending.delete(current);
    core.scopes.delete(element);
    aliases.delete(element);
  }
  function validAlias(name) {
    return /^\$[A-Za-z][A-Za-z0-9_]*$/.test(name) && !RESERVED_ALIASES[name];
  }
  function aliasName(element) {
    if (aliases.has(element)) return aliases.get(element);
    var name = (element.getAttribute("data-kit-as") || "").trim();
    if (!element.hasAttribute("data-kit-component") || !validAlias(name)) {
      core.report(new TypeError("KitJS: data-kit-as requires a component host and a valid $alias"));
      name = null;
    }
    aliases.set(element, name);
    return name;
  }
  function resolveAlias(name) {
    if (!validAlias(name)) throw new ReferenceError("KitJS: unknown component alias \"" + name + "\"");
    var matches = [];
    document.querySelectorAll(ALIASES).forEach(function (element) {
      if (aliasName(element) === name) matches.push(element);
    });
    if (matches.length > 1) throw new TypeError("KitJS: duplicate component alias \"" + name + "\"");
    if (!matches.length) throw new ReferenceError("KitJS: unknown component alias \"" + name + "\"");
    var current = ensureComponent(matches[0]);
    if (!current || current.disposed || !current.host || !current.host.isConnected) {
      throw new ReferenceError("KitJS: unavailable component alias \"" + name + "\"");
    }
    initialize(current);
    return current;
  }

  core.component = component;
  var kit = {};
  Object.defineProperties(kit, {
    version: { value: core.version, enumerable: true },
    component: { value: component, enumerable: true },
    [core.install]: { value: core.version }
  });
  core.kit = kit;
  core.sealKit = function () {
    if (OWN.call(kit, "service")) {
      throw new Error("KitJS: service registrar must be removed before publication");
    }
    if (!Object.isFrozen(kit)) Object.freeze(kit);
    return kit;
  };
  core.installComponentGraph = installComponentGraph;
  core.componentMetadata = componentMetadata;
  core.inspectRetains = function (root) { return inspectRetains(root, false, true); };
  core.invalidRetainStructure = function (element) {
    if (!retainStructureValidity.has(element)) {
      inspectRetains(connectedHere(element) ? document : element, true, false);
    }
    return retainStructureValidity.get(element) === true;
  };
  core.prepareComponentTree = prepareComponentTree;
  core.assertComponentGraph = assertComponentGraph;
  core.ensureComponent = ensureComponent;
  core.ownerFor = nearest;
  core.scopeRecordFor = scopeRecordFor;
  core.ownedElements = ownedElements;
  core.initialize = initialize;
  core.liveComponents = liveComponents;
  core.disposeComponent = disposeComponent;
  core.validAlias = validAlias;
  core.validServiceName = validServiceName;
  core.resolveAlias = resolveAlias;
  core.phase = "component";
})(document);
