; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "scope") throw new Error("KitJS: component loaded out of order");
  if (core.reuse) { core.phase = "component"; return; }

  var OWN = core.OWN;
  var BOUNDARIES = "[data-kit-component],[data-kit-scope]";
  var METADATA = "[data-kit-component],[data-kit-version],[data-kit-local],[data-kit-scope]";
  var ALIASES = "[data-kit-as]";
  var aliases = new WeakMap();
  var metadata = new WeakMap();
  var localRegistry = new Map();
  var missingLocalReports = new WeakSet();
  var localAuditComplete = false;
  var componentCache = new Map();
  var componentHandoff = null;
  var componentRegistration = null;
  var componentRegistrationGraph = null;
  var cleanupObserver = null;
  var cleanupOwners = 0;
  var removedRoots = new Set();
  var removalQueued = false;
  var retainValidity = new WeakMap();
  var retainStructureValidity = new WeakMap();
  var retainReports = new WeakMap();
  var COMPONENT_NAME = /^[A-Za-z_$][A-Za-z0-9_$.-]*$/;
  var SERVICE_NAME = /^[A-Za-z][A-Za-z0-9_.-]*$/;
  var SERVICE_ACTION = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
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

  var NOOP_CANCEL = Object.freeze(function () { });

  function ensureCleanupOwner(current) {
    if (current.ownsCleanup) return;
    if (!connectedHere(current.host)) {
      queueRemovedRoot(current.host);
      return;
    }
    current.ownsCleanup = true;
    cleanupOwners++;
    startCleanupObserver();
  }

  function releaseCleanupOwner(current) {
    if (!current.ownsCleanup || current.cleanups.length || current.afterRenders.length) return;
    current.ownsCleanup = false;
    cleanupOwners--;
    stopCleanupObserver();
  }

  function removeLifecycleEntry(entries, entry) {
    var index = entries.indexOf(entry);
    if (index >= 0) entries.splice(index, 1);
  }

  function invokeLifecycle(current, callback) {
    try { callback.call(current.scope); }
    catch (error) { core.report(error); }
  }

  function ownCleanup(current, cleanup) {
    if (!current || current.disposed) return NOOP_CANCEL;
    var entry = { callback: cleanup, active: true };
    current.cleanups.push(entry);
    ensureCleanupOwner(current);
    return Object.freeze(function () {
      if (!entry.active) return;
      entry.active = false;
      removeLifecycleEntry(current.cleanups, entry);
      invokeLifecycle(current, cleanup);
      releaseCleanupOwner(current);
    });
  }

  function lifecycleContext(current) {
    function owned(selector) {
      if (typeof selector !== "string") throw new TypeError("KitJS: context.owned(selector) expects a string");
      if (current.disposed) return Object.freeze([]);
      return Object.freeze(ownedElements(current, selector));
    }
    function cleanup(callback) {
      if (typeof callback !== "function") throw new TypeError("KitJS: context.cleanup(fn) expects a function");
      return ownCleanup(current, callback);
    }
    function listen(target, type, callback, options) {
      if (!target || typeof target.addEventListener !== "function" ||
        typeof target.removeEventListener !== "function") {
        throw new TypeError("KitJS: context.listen(target, type, fn, options) expects an EventTarget");
      }
      if (typeof type !== "string" || typeof callback !== "function") {
        throw new TypeError("KitJS: context.listen(target, type, fn, options) expects a string and function");
      }
      if (current.disposed) return NOOP_CANCEL;
      var capture = typeof options === "boolean" ? options : !!(options && options.capture);
      target.addEventListener(type, callback, options);
      return ownCleanup(current, function () {
        target.removeEventListener(type, callback, capture);
      });
    }
    function afterRender(callback) {
      if (typeof callback !== "function") {
        throw new TypeError("KitJS: context.afterRender(fn) expects a function");
      }
      if (current.disposed) return NOOP_CANCEL;
      var entry = { callback: callback, active: true };
      current.afterRenders.push(entry);
      ensureCleanupOwner(current);
      return Object.freeze(function () {
        if (!entry.active) return;
        entry.active = false;
        removeLifecycleEntry(current.afterRenders, entry);
        releaseCleanupOwner(current);
      });
    }
    var context = {};
    Object.defineProperties(context, {
      host: { get: function () { return current.host; }, enumerable: true },
      owned: { value: Object.freeze(owned), enumerable: true },
      listen: { value: Object.freeze(listen), enumerable: true },
      cleanup: { value: Object.freeze(cleanup), enumerable: true },
      afterRender: { value: Object.freeze(afterRender), enumerable: true }
    });
    return Object.freeze(context);
  }

  function flushAfterRender(current) {
    if (!current || current.disposed || !current.afterRenders.length) return;
    if (!connectedHere(current.host)) {
      queueRemovedRoot(current.host);
      return;
    }
    var entries = current.afterRenders.slice();
    current.afterRenders.length = 0;
    entries.forEach(function (entry) {
      if (!entry.active || current.disposed) return;
      entry.active = false;
      invokeLifecycle(current, entry.callback);
    });
    releaseCleanupOwner(current);
  }

  function copyValue(value, seen, scopeData) {
    if (value === null || typeof value !== "object") return value;
    var prototype = Object.getPrototypeOf(value);
    if (!Array.isArray(value) && prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("KitJS: component state must contain only plain objects and arrays");
    }
    if (seen.has(value)) throw new TypeError("KitJS: circular component state is not supported");
    seen.add(value);
    var output = Array.isArray(value) ? [] : Object.create(prototype);
    Object.keys(value).forEach(function (name) {
      if (scopeData ? core.blockedScopeKey(name) : core.blocked(name)) {
        throw new TypeError("KitJS: blocked component state key \"" + name + "\"");
      }
      output[name] = copyValue(value[name], seen, scopeData);
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

  function plainObject(value) {
    var prototype = value && Object.getPrototypeOf(value);
    return !!value && (prototype === Object.prototype || prototype === null) &&
      Object.getOwnPropertySymbols(value).length === 0;
  }

  function normalizeComponentGraph(source) {
    var prototype = source && Object.getPrototypeOf(source);
    if (!source || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(source).length) graphError("expected a plain object");
    var id = source.id;
    var profile = source.profile;
    var artifact = source.artifact;
    var services = source.services;
    var components = source.components;
    var componentHashes = source.componentHashes;
    var actions = source.actions;
    var grants = source.grants;
    if (typeof id !== "string" || !GRAPH_ID.test(id)) graphError("invalid id");
    if (profile !== "kit" && profile !== "hydrate") graphError("invalid profile");
    if (artifact !== undefined && (typeof artifact !== "string" || !GRAPH_ID.test(artifact))) {
      graphError("invalid artifact");
    }
    if (profile !== core.profile) {
      graphError("profile does not match the assembled runtime");
    }
    if (!plainObject(services)) graphError("services must be a plain object");
    if (!plainObject(components)) graphError("components must be a plain object");
    if (!plainObject(actions)) graphError("actions must be a plain object");
    if (!plainObject(grants)) graphError("grants must be a plain object");

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

    var componentManifest = Object.create(null);
    Object.keys(components).forEach(function (name) {
      var version = components[name];
      if (!validComponentName(name)) graphError("invalid component name \"" + name + "\"");
      if (!validVersion(version)) graphError("invalid version for component \"" + name + "\"");
      componentManifest[name] = version;
    });
    var hashManifest;
    if (componentHashes !== undefined) {
      if (artifact === undefined) graphError("staged graph requires an artifact");
      if (!plainObject(componentHashes)) graphError("componentHashes must be a plain object");
      hashManifest = Object.create(null);
      Object.keys(componentHashes).forEach(function (name) {
        if (!OWN.call(componentManifest, name)) {
          graphError("componentHashes names undeclared component \"" + name + "\"");
        }
        if (typeof componentHashes[name] !== "string" || !GRAPH_ID.test(componentHashes[name])) {
          graphError("invalid source hash for component \"" + name + "\"");
        }
        hashManifest[name] = componentHashes[name];
      });
      Object.keys(componentManifest).forEach(function (name) {
        if (!OWN.call(hashManifest, name)) {
          graphError("missing source hash for component \"" + name + "\"");
        }
      });
      Object.freeze(hashManifest);
    }

    var actionManifest = Object.create(null);
    Object.keys(actions).forEach(function (serviceName) {
      if (!OWN.call(serviceManifest, serviceName)) {
        graphError("actions name undeclared service \"" + serviceName + "\"");
      }
      var members = actions[serviceName];
      var memberPrototype = members && Object.getPrototypeOf(members);
      if (!members || memberPrototype !== Object.prototype && memberPrototype !== null ||
        Object.getOwnPropertySymbols(members).length) {
        graphError("actions for service \"" + serviceName + "\" must be a plain object");
      }
      var normalized = Object.create(null);
      Object.keys(members).forEach(function (memberName) {
        if (!SERVICE_ACTION.test(memberName) || core.blocked(memberName) || members[memberName] !== true) {
          graphError("invalid authored action \"" + serviceName + "." + memberName + "\"");
        }
        normalized[memberName] = true;
      });
      actionManifest[serviceName] = Object.freeze(normalized);
    });
    Object.keys(serviceManifest).forEach(function (serviceName) {
      if (!OWN.call(actionManifest, serviceName)) {
        graphError("missing actions for service \"" + serviceName + "\"");
      }
    });

    var grantManifest = Object.create(null);
    Object.keys(grants).forEach(function (componentName) {
      if (!OWN.call(componentManifest, componentName)) {
        graphError("grants name undeclared component \"" + componentName + "\"");
      }
      var dependencies = grants[componentName];
      var dependencyPrototype = dependencies && Object.getPrototypeOf(dependencies);
      if (!dependencies || dependencyPrototype !== Object.prototype && dependencyPrototype !== null ||
        Object.getOwnPropertySymbols(dependencies).length) {
        graphError("grants for component \"" + componentName + "\" must be a plain object");
      }
      var normalized = Object.create(null);
      Object.keys(dependencies).forEach(function (serviceName) {
        if (!OWN.call(serviceManifest, serviceName) || dependencies[serviceName] !== serviceManifest[serviceName]) {
          graphError("invalid grant \"" + componentName + " -> " + serviceName + "\"");
        }
        normalized[serviceName] = dependencies[serviceName];
      });
      grantManifest[componentName] = Object.freeze(normalized);
    });
    Object.keys(componentManifest).forEach(function (componentName) {
      if (!OWN.call(grantManifest, componentName)) {
        graphError("missing grants for component \"" + componentName + "\"");
      }
    });
    Object.freeze(serviceManifest);
    Object.freeze(componentManifest);
    Object.freeze(actionManifest);
    Object.freeze(grantManifest);
    var graphSource = {
      id: id,
      profile: profile,
      services: serviceManifest,
      components: componentManifest,
      actions: actionManifest,
      grants: grantManifest
    };
    if (artifact !== undefined) graphSource.artifact = artifact;
    if (hashManifest) graphSource.componentHashes = hashManifest;
    return Object.freeze(graphSource);
  }

  function stagedGraph(graph) {
    return !!graph && OWN.call(graph, "componentHashes");
  }

  function installComponentGraph(source) {
    if (core.graph) throw new Error("KitJS: component graph is already installed");
    if (core.booted || core.phase !== "events" && core.phase !== "drive") {
      throw new Error("KitJS: component graph must be installed immediately before boot");
    }
    var graph = normalizeComponentGraph(source);
    if (core.serviceRegistry) {
      core.serviceRegistry.forEach(function (_, name) {
        if (!OWN.call(graph.services, name)) {
          graphError("registered service \"" + name + "\" is not declared");
        }
      });
    }
    core.registry.forEach(function (_, name) {
      if (!OWN.call(graph.components, name)) {
        graphError("registered component \"" + name + "\" is not declared");
      }
    });
    localRegistry.forEach(function (_, name) {
      if (OWN.call(graph.components, name)) {
        graphError("client component \"" + name + "\" conflicts with a managed component");
      }
    });
    core.graph = graph;
    if (stagedGraph(graph)) {
      Object.defineProperty(core.kit, Symbol.for("kitjs:graph"), {
        get: function () { return componentRegistrationGraph || core.graph; }
      });
    } else {
      Object.defineProperty(core.kit, Symbol.for("kitjs:graph"), { value: graph });
    }
  }

  function deliveryError(message) {
    throw new TypeError("KitJS: invalid staged delivery: " + message);
  }

  function normalizeDeliveryComponent(source) {
    if (!plainObject(source) || !validComponentName(source.name) || !validVersion(source.version) ||
      typeof source.sourceHash !== "string" || !GRAPH_ID.test(source.sourceHash)) {
      deliveryError("invalid component source identity");
    }
    return Object.freeze({
      name: source.name,
      version: source.version,
      sourceHash: source.sourceHash
    });
  }

  function normalizeStagedDelivery(source, graph) {
    if (!plainObject(source) || !stagedGraph(graph)) deliveryError("expected a staged graph contract");
    if (source.profile !== graph.profile || source.graphKey !== graph.id ||
      source.graphHash !== graph.artifact || typeof source.runtimeHash !== "string" ||
      !GRAPH_ID.test(source.runtimeHash) || source.hydrateHash !== null &&
      (typeof source.hydrateHash !== "string" || !GRAPH_ID.test(source.hydrateHash)) ||
      !Array.isArray(source.assets)) {
      deliveryError("graph identity does not match");
    }
    if (graph.profile === "hydrate" && source.hydrateHash === null ||
      graph.profile === "kit" && source.hydrateHash !== null) {
      deliveryError("profile assets do not match");
    }

    var assets = [];
    var roleCounts = Object.create(null);
    var serviceAssets = Object.create(null);
    var componentSources = Object.create(null);
    var graphAsset = null;
    var runtimeAsset = null;
    var hydrateAsset = null;
    source.assets.forEach(function (assetSource) {
      if (!plainObject(assetSource)) deliveryError("asset must be a plain object");
      var role = assetSource.role;
      var packageName = assetSource.package;
      var version = assetSource.version;
      var hash = assetSource.hash;
      var integrity = assetSource.integrity;
      var name = assetSource.name;
      var url = assetSource.url;
      if (["runtime", "hydrate", "graph", "service", "component", "components"].indexOf(role) < 0 ||
        typeof packageName !== "string" || typeof version !== "string" ||
        typeof hash !== "string" || !GRAPH_ID.test(hash) || typeof integrity !== "string" || !integrity ||
        typeof name !== "string" || name.indexOf(hash + ".") !== 0 || name.slice(-3) !== ".js" ||
        typeof url !== "string" || !url) {
        deliveryError("invalid asset identity");
      }

      var packages = null;
      if (assetSource.packages !== null && assetSource.packages !== undefined) {
        if (!Array.isArray(assetSource.packages)) deliveryError("asset packages must be an array");
        packages = assetSource.packages.map(function (value) {
          if (typeof value !== "string" || !value) deliveryError("invalid asset package member");
          return value;
        });
        Object.freeze(packages);
      }
      var components = null;
      if (assetSource.components !== null && assetSource.components !== undefined) {
        if (!Array.isArray(assetSource.components) || !assetSource.components.length) {
          deliveryError("asset components must be a non-empty array");
        }
        components = assetSource.components.map(normalizeDeliveryComponent);
        Object.freeze(components);
      }
      var sourceHash = assetSource.sourceHash === null || assetSource.sourceHash === undefined
        ? null : assetSource.sourceHash;
      if (sourceHash !== null && (typeof sourceHash !== "string" || !GRAPH_ID.test(sourceHash))) {
        deliveryError("invalid component asset source hash");
      }

      if (role === "component") {
        if (!validComponentName(packageName) || !validVersion(version) || !components ||
          components.length !== 1 || components[0].name !== packageName ||
          components[0].version !== version || components[0].sourceHash !== sourceHash) {
          deliveryError("individual component mapping does not match its asset");
        }
      } else if (role === "components") {
        if (packageName || version || !components || components.length < 2 || sourceHash !== null) {
          deliveryError("component bundle mapping is incomplete");
        }
      } else if (components || sourceHash !== null) {
        deliveryError("non-component asset contains a component mapping");
      }

      var asset = Object.freeze({
        role: role,
        package: packageName,
        version: version,
        hash: hash,
        integrity: integrity,
        name: name,
        url: url,
        packages: packages,
        components: components,
        sourceHash: sourceHash
      });
      assets.push(asset);
      roleCounts[role] = (roleCounts[role] || 0) + 1;
      if (role === "runtime") runtimeAsset = asset;
      else if (role === "hydrate") hydrateAsset = asset;
      else if (role === "graph") graphAsset = asset;
      else if (role === "service") {
        if (!validServiceName(packageName) || !validVersion(version) || serviceAssets[packageName]) {
          deliveryError("invalid or duplicate service asset");
        }
        serviceAssets[packageName] = asset;
      }
      if (components) components.forEach(function (identity) {
        if (componentSources[identity.name]) deliveryError("duplicate component source mapping");
        componentSources[identity.name] = identity;
      });
    });
    Object.freeze(assets);

    if (roleCounts.runtime !== 1 || roleCounts.graph !== 1 ||
      graph.profile === "hydrate" && roleCounts.hydrate !== 1 ||
      graph.profile === "kit" && roleCounts.hydrate || !runtimeAsset || !graphAsset ||
      runtimeAsset.hash !== source.runtimeHash || graphAsset.hash !== source.graphHash ||
      graphAsset.integrity !== source.graphIntegrity || graphAsset.name !== source.graphName ||
      graphAsset.url !== source.graphURL || hydrateAsset && hydrateAsset.hash !== source.hydrateHash) {
      deliveryError("required asset identities do not match");
    }
    Object.keys(graph.services).forEach(function (name) {
      var asset = serviceAssets[name];
      if (!asset || asset.version !== graph.services[name]) {
        deliveryError("missing service asset \"" + name + "\"");
      }
    });
    Object.keys(serviceAssets).forEach(function (name) {
      if (!OWN.call(graph.services, name)) deliveryError("undeclared service asset \"" + name + "\"");
    });
    Object.keys(graph.components).forEach(function (name) {
      var identity = componentSources[name];
      if (!identity || identity.version !== graph.components[name] ||
        identity.sourceHash !== graph.componentHashes[name]) {
        deliveryError("missing component source mapping \"" + name + "\"");
      }
    });
    Object.keys(componentSources).forEach(function (name) {
      if (!OWN.call(graph.components, name)) deliveryError("undeclared component source mapping \"" + name + "\"");
    });

    return Object.freeze({
      profile: source.profile,
      graphKey: source.graphKey,
      runtimeHash: source.runtimeHash,
      hydrateHash: source.hydrateHash,
      graphHash: source.graphHash,
      graphIntegrity: source.graphIntegrity,
      graphName: source.graphName,
      graphURL: source.graphURL,
      assets: assets
    });
  }

  function installStagedDelivery(source) {
    if (!core.graph || !stagedGraph(core.graph) || core.activeDelivery) {
      throw new Error("KitJS: staged delivery cannot be installed");
    }
    core.activeDelivery = normalizeStagedDelivery(source, core.graph);
    return core.activeDelivery;
  }

  function metadataError(element, entry, message, shouldReport) {
    entry.value = null;
    entry.error = message;
    if (shouldReport && !entry.reported) {
      entry.reported = true;
      core.report(new TypeError("KitJS: " + message));
    }
    if (entry.cacheable) metadata.set(element, entry);
    return null;
  }

  function storeMetadata(element, entry, value) {
    entry.value = value;
    if (entry.cacheable) metadata.set(element, entry);
    return value;
  }

  // Undefined means this element carries no component metadata. Null means its
  // metadata is invalid. A record means it is a valid component host.
  function componentMetadata(element, shouldReport, requestedGraph) {
    if (!element || element.nodeType !== 1 || !element.hasAttribute) return undefined;
    if (core.ignoredForRuntime(element)) return undefined;
    var hasComponent = element.hasAttribute("data-kit-component");
    var hasVersion = element.hasAttribute("data-kit-version");
    var hasLocal = element.hasAttribute("data-kit-local");
    if (!hasComponent && !hasVersion && !hasLocal) return undefined;
    var componentSource = hasComponent ? element.getAttribute("data-kit-component") : null;
    var versionSource = hasVersion ? element.getAttribute("data-kit-version") : null;
    var localSource = hasLocal ? element.getAttribute("data-kit-local") : null;
    var retainSource = element.hasAttribute("data-kit-retain") ? element.getAttribute("data-kit-retain") : null;
    var cacheable = arguments.length < 3;
    var graphIdentity = cacheable ? core.graph || null : requestedGraph || null;
    var entry = cacheable ? metadata.get(element) : null;
    if (entry && entry.componentSource === componentSource && entry.versionSource === versionSource &&
      entry.localSource === localSource && entry.retainSource === retainSource &&
      entry.graphIdentity === graphIdentity) {
      if (shouldReport && entry.error && !entry.reported) {
        entry.reported = true;
        core.report(new TypeError("KitJS: " + entry.error));
      }
      return entry.value;
    }
    entry = {
      componentSource: componentSource,
      versionSource: versionSource,
      localSource: localSource,
      retainSource: retainSource,
      graphIdentity: graphIdentity,
      value: undefined,
      error: "",
      reported: false,
      cacheable: cacheable
    };
    if (!hasComponent) {
      return metadataError(element, entry,
        hasLocal ? "data-kit-local requires a component host" :
          "data-kit-version requires a component host", shouldReport);
    }
    if (String(element.localName || "").toLowerCase() === "template") {
      return metadataError(element, entry,
        "data-kit-component cannot be used on a template; place the boundary inside template.content",
        shouldReport);
    }
    var componentSpec = String(componentSource || "");
    var spec = componentSpec.trim();
    var inlineSpec = componentSpec.trimStart();
    var separator = spec.indexOf("@");
    var name = separator < 0 ? spec : inlineSpec.slice(0, inlineSpec.indexOf("@"));
    var inlineVersion = separator < 0 ? null : inlineSpec.slice(inlineSpec.indexOf("@") + 1);
    if (inlineVersion !== null && !validVersion(inlineVersion)) {
      return metadataError(element, entry,
        "inline component version must be an exact semantic version", shouldReport);
    }
    if (!validComponentName(name)) {
      return metadataError(element, entry, "invalid component name \"" + name + "\"", shouldReport);
    }
    if (hasLocal && localSource !== "") {
      return metadataError(element, entry,
        "data-kit-local must be an empty presence marker", shouldReport);
    }
    if (inlineVersion !== null && hasVersion) {
      return metadataError(element, entry,
        "inline component versions cannot be combined with data-kit-version", shouldReport);
    }
    var version = inlineVersion;
    if (hasVersion) {
      version = String(versionSource || "").trim();
      if (!validVersion(version)) {
        return metadataError(element, entry,
          "data-kit-version must be an exact semantic version", shouldReport);
      }
    }
    var managed = version !== null;
    if (hasLocal && managed) {
      return metadataError(element, entry,
        hasVersion ? "data-kit-local components cannot use data-kit-version" :
          "data-kit-local cannot mark a versioned component", shouldReport);
    }
    if (!managed) {
      if (element.hasAttribute("data-kit-retain") && (hasLocal || graphIdentity)) {
        return metadataError(element, entry,
          "unversioned client components cannot use data-kit-retain", shouldReport);
      }
      if (graphIdentity && OWN.call(graphIdentity.components, name)) {
        return metadataError(element, entry,
          "client component \"" + name + "\" conflicts with the installed graph", shouldReport);
      }
      return storeMetadata(element, entry, {
        name: name, version: null, lane: "client"
      });
    }
    if (!graphIdentity || !OWN.call(graphIdentity.components, name)) {
      return metadataError(element, entry,
        "component \"" + name + "\" is not present in the installed graph", shouldReport);
    }
    if (graphIdentity.components[name] !== version) {
      return metadataError(element, entry,
        "component \"" + name + "\" requires " + version +
        " but the installed graph provides " + graphIdentity.components[name], shouldReport);
    }
    return storeMetadata(element, entry, {
      name: name, version: version, lane: "managed"
    });
  }

  function componentMetadataForGraph(element, graph) {
    return componentMetadata(element, false, graph);
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
    if (root.nodeType === 1 && core.ignoredForRuntime(root)) return;
    if (root.nodeType === 1 && root.matches(METADATA)) {
      componentMetadata(root, true);
      core.scopeSeed(root, true);
    }
    root.querySelectorAll(METADATA).forEach(function (element) {
      if (core.ignoredForRuntime(element)) return;
      componentMetadata(element, true);
      core.scopeSeed(element, true);
    });
    root.querySelectorAll("template").forEach(function (template) {
      if (!core.ignoredForRuntime(template) && template.content) prepareComponentTree(template.content, true);
    });
    if (!nested) inspectRetains(root, true, false);
  }

  function validateComponentTree(root) {
    if (!root || root.nodeType !== 1 && root.nodeType !== 9 && root.nodeType !== 11) return true;
    function visit(node) {
      if (!node) return;
      if (node.nodeType === 1 && core.ignoredForRuntime(node)) return;
      if (node.nodeType === 1 && String(node.localName || "").toLowerCase() === "template") {
        if (node.hasAttribute("data-kit-component")) {
          throw new TypeError(
            "KitJS: data-kit-component cannot be used on a template; place the boundary inside template.content"
          );
        }
        if (node.content) visit(node.content);
      }
      var child = node.firstChild;
      while (child) {
        visit(child);
        child = child.nextSibling;
      }
    }
    visit(root);
    return true;
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
    localRegistry.forEach(function (_, name) {
      if (OWN.call(core.graph.components, name)) {
        throw new Error("KitJS: client component \"" + name + "\" conflicts with the installed graph");
      }
    });
    Object.keys(core.graph.components).forEach(function (name) {
      if (!core.registry.has(name)) {
        throw new Error("KitJS: component graph is missing definition \"" + name + "\"");
      }
      if (stagedGraph(core.graph)) {
        var key = componentCacheKey(name, core.graph.components[name], core.graph.componentHashes[name]);
        var cached = componentCache.get(key);
        if (!cached || cached.descriptors !== core.registry.get(name)) {
          throw new Error("KitJS: component graph is missing exact package \"" + name + "\"");
        }
      }
    });
  }

  function componentDescriptors(name, definition, graph) {
    if (typeof name !== "string" || !COMPONENT_NAME.test(name)) {
      throw new TypeError("KitJS: invalid component name");
    }
    if (core.blocked(name)) throw new TypeError("KitJS: blocked component name");
    if (graph && !OWN.call(graph.components, name)) {
      throw new Error("KitJS: component \"" + name + "\" is not declared by the installed graph");
    }
    var prototype = definition && Object.getPrototypeOf(definition);
    if (!definition || prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("KitJS: component definition must be a plain object");
    }
    // Only the canonical app boundary projects service names into its authored
    // action alias. Other trusted components may use a same-named reactive
    // field even when their JavaScript package depends on that service.
    var granted = name === "app" && graph && graph.grants[name];
    if (granted) Object.keys(granted).forEach(function (serviceName) {
      if (OWN.call(definition, serviceName)) {
        throw new TypeError("KitJS: component field \"" + serviceName +
          "\" conflicts with a granted service");
      }
    });
    return snapshot(definition);
  }

  function assertDescriptorsForGraph(name, descriptors, graph) {
    var granted = name === "app" && graph && graph.grants[name];
    if (!granted) return true;
    Object.keys(granted).forEach(function (serviceName) {
      if (OWN.call(descriptors, serviceName)) {
        throw new TypeError("KitJS: cached component field \"" + serviceName +
          "\" conflicts with a granted service");
      }
    });
    return true;
  }

  function component(name, definition) {
    if (arguments.length !== 2) throw new TypeError("KitJS: component(name, definition) expects two arguments");
    if (componentRegistration) return componentRegistration(name, definition);
    if (typeof name !== "string" || !COMPONENT_NAME.test(name)) {
      throw new TypeError("KitJS: invalid component name");
    }
    if (core.blocked(name)) throw new TypeError("KitJS: blocked component name");
    var managed = !core.graph || OWN.call(core.graph.components, name);
    if (!managed && componentHandoff && OWN.call(componentHandoff.graph.components, name)) {
      throw new Error("KitJS: client component \"" + name + "\" conflicts with the pending graph");
    }
    var registry = managed ? core.registry : localRegistry;
    if (core.registry.has(name) || localRegistry.has(name)) {
      throw new Error("KitJS: component \"" + name + "\" already exists");
    }
    if (!managed && localRegistry.size >= core.cacheLimit) {
      throw new Error("KitJS: client component registry limit exceeded");
    }
    registry.set(name, componentDescriptors(name, definition, managed ? core.graph : null));
    if (core.booted) core.invalidate();
  }

  function componentCacheKey(name, version, sourceHash) {
    return name + "\u0000" + version + "\u0000" + sourceHash;
  }

  function recordStagedComponentPackage(name, version, sourceHash) {
    if (!stagedGraph(core.graph) || !validComponentName(name) || !validVersion(version) ||
      typeof sourceHash !== "string" || !GRAPH_ID.test(sourceHash) ||
      core.graph.components[name] !== version || core.graph.componentHashes[name] !== sourceHash ||
      !core.registry.has(name)) {
      throw new Error("KitJS: staged component package does not match the active graph");
    }
    var key = componentCacheKey(name, version, sourceHash);
    if (componentCache.has(key)) throw new Error("KitJS: staged component package is already cached");
    componentCache.forEach(function (entry) {
      if (entry.name === name && entry.version === version && entry.sourceHash !== sourceHash) {
        throw new Error("KitJS: staged component version has conflicting source bytes");
      }
    });
    if (componentCache.size >= core.cacheLimit) {
      throw new Error("KitJS: staged component cache limit exceeded");
    }
    componentCache.set(key, Object.freeze({
      name: name,
      version: version,
      sourceHash: sourceHash,
      descriptors: core.registry.get(name)
    }));
  }

  function sameStringManifest(left, right) {
    var leftNames = Object.keys(left);
    var rightNames = Object.keys(right);
    return leftNames.length === rightNames.length && leftNames.every(function (name) {
      return OWN.call(right, name) && left[name] === right[name];
    });
  }

  function sameActionManifest(left, right) {
    var leftNames = Object.keys(left);
    var rightNames = Object.keys(right);
    return leftNames.length === rightNames.length && leftNames.every(function (name) {
      return OWN.call(right, name) && sameStringManifest(left[name], right[name]);
    });
  }

  function serviceAssetMap(delivery) {
    var output = Object.create(null);
    delivery.assets.forEach(function (asset) {
      if (asset.role !== "service") return;
      output[asset.package] = [asset.version, asset.hash, asset.integrity, asset.name].join("\u0000");
    });
    return output;
  }

  function beginComponentHandoff(graphSource, deliverySource) {
    if (!core.booted || !stagedGraph(core.graph) || !core.activeDelivery) {
      throw new Error("KitJS: component handoff requires an active staged runtime");
    }
    if (componentHandoff) throw new Error("KitJS: another component handoff is already active");
    var targetGraph = normalizeComponentGraph(graphSource);
    var targetDelivery = normalizeStagedDelivery(deliverySource, targetGraph);
    var currentDelivery = core.activeDelivery;
    if (!stagedGraph(targetGraph) || targetGraph.profile !== core.graph.profile ||
      targetDelivery.profile !== currentDelivery.profile ||
      targetDelivery.runtimeHash !== currentDelivery.runtimeHash ||
      targetDelivery.hydrateHash !== currentDelivery.hydrateHash ||
      !sameStringManifest(targetGraph.services, core.graph.services) ||
      !sameActionManifest(targetGraph.actions, core.graph.actions) ||
      !sameStringManifest(serviceAssetMap(targetDelivery), serviceAssetMap(currentDelivery))) {
      throw new Error("KitJS: component handoff requires the exact active runtime and services");
    }
    Object.keys(targetGraph.components).forEach(function (name) {
      if (localRegistry.has(name)) {
        throw new Error("KitJS: component handoff conflicts with client component \"" + name + "\"");
      }
      if (OWN.call(core.graph.components, name) &&
        (targetGraph.components[name] !== core.graph.components[name] ||
          targetGraph.componentHashes[name] !== core.graph.componentHashes[name] ||
          !sameStringManifest(targetGraph.grants[name], core.graph.grants[name]))) {
        throw new Error("KitJS: component handoff cannot replace overlapping component \"" + name + "\"");
      }
    });

    var pending = new Map();
    var missingEntries = [];
    Object.keys(targetGraph.components).forEach(function (name) {
      var version = targetGraph.components[name];
      var sourceHash = targetGraph.componentHashes[name];
      var key = componentCacheKey(name, version, sourceHash);
      var cached = componentCache.get(key);
      componentCache.forEach(function (entry) {
        if (entry.name === name && entry.version === version && entry.sourceHash !== sourceHash) {
          throw new Error("KitJS: component handoff found conflicting source bytes for \"" + name + "\"");
        }
      });
      if (cached) {
        assertDescriptorsForGraph(name, cached.descriptors, targetGraph);
      } else {
        missingEntries.push(Object.freeze({ name: name, version: version, sourceHash: sourceHash }));
      }
    });
    Object.freeze(missingEntries);
    if (componentCache.size + missingEntries.length > core.cacheLimit) {
      throw new Error("KitJS: component handoff exceeds the component cache limit");
    }
    var closed = false;
    var committed = false;
    var rollbackUsed = false;
    var tx;

    function assertOpen() {
      if (closed || componentHandoff !== tx) throw new Error("KitJS: component handoff is closed");
    }

    function abort() {
      if (closed) return false;
      closed = true;
      pending.clear();
      if (componentHandoff === tx) componentHandoff = null;
      return true;
    }

    function missing() {
      assertOpen();
      return missingEntries;
    }

    function register(name, version, sourceHash, installer) {
      assertOpen();
      try {
        if (!validComponentName(name) || !validVersion(version) ||
          typeof sourceHash !== "string" || !GRAPH_ID.test(sourceHash) ||
          typeof installer !== "function" || !Object.isFrozen(installer) ||
          targetGraph.components[name] !== version ||
          targetGraph.componentHashes[name] !== sourceHash) {
          throw new Error("KitJS: component handoff package does not match the target graph");
        }
        var key = componentCacheKey(name, version, sourceHash);
        if (componentCache.has(key) || pending.has(key)) {
          throw new Error("KitJS: component handoff package is duplicate or already cached");
        }
        pending.set(key, Object.freeze({
          name: name,
          version: version,
          sourceHash: sourceHash,
          installer: installer
        }));
        return true;
      } catch (error) {
        abort();
        throw error;
      }
    }

    function ready() {
      assertOpen();
      try {
        if (pending.size !== missingEntries.length || !missingEntries.every(function (identity) {
          return pending.has(componentCacheKey(identity.name, identity.version, identity.sourceHash));
        })) {
          throw new Error("KitJS: component handoff has missing or partial registration");
        }
        return true;
      } catch (error) {
        abort();
        throw error;
      }
    }

    function commit() {
      ready();
      Object.keys(targetGraph.components).forEach(function (name) {
        if (localRegistry.has(name)) {
          abort();
          throw new Error("KitJS: component handoff conflicts with client component \"" + name + "\"");
        }
      });
      var previousGraph = core.graph;
      var previousDelivery = core.activeDelivery;
      var previousRegistry = core.registry;
      var previousCompiled = core.compiled;
      var previousMetadata = metadata;
      var previousRetainValidity = retainValidity;
      var previousRetainStructureValidity = retainStructureValidity;
      var previousRetainReports = retainReports;
      var nextRegistry = new Map();
      var materialized = new Map();
      var added = [];
      try {
        missingEntries.forEach(function (identity) {
          var key = componentCacheKey(identity.name, identity.version, identity.sourceHash);
          var staged = pending.get(key);
          if (!staged) throw new Error("KitJS: component handoff package disappeared before commit");
          var registered = 0;
          var descriptors = null;
          function registrar(registeredName, definition) {
            registered++;
            if (registered !== 1 || registeredName !== identity.name) {
              throw new Error("KitJS: component handoff package registered an undeclared component");
            }
            descriptors = componentDescriptors(registeredName, definition, targetGraph);
          }
          if (componentRegistration || componentRegistrationGraph) {
            throw new Error("KitJS: component registration transaction is already active");
          }
          componentRegistration = registrar;
          componentRegistrationGraph = targetGraph;
          try {
            staged.installer(core.kit);
          } finally {
            componentRegistration = null;
            componentRegistrationGraph = null;
          }
          if (registered !== 1 || !descriptors) {
            throw new Error("KitJS: component handoff package must register exactly once");
          }
          materialized.set(key, Object.freeze({
            name: identity.name,
            version: identity.version,
            sourceHash: identity.sourceHash,
            descriptors: descriptors
          }));
        });
        Object.keys(targetGraph.components).forEach(function (name) {
          var key = componentCacheKey(name, targetGraph.components[name], targetGraph.componentHashes[name]);
          var entry = materialized.get(key) || componentCache.get(key);
          if (!entry) throw new Error("KitJS: component handoff cache is incomplete");
          assertDescriptorsForGraph(name, entry.descriptors, targetGraph);
          nextRegistry.set(name, entry.descriptors);
        });
        materialized.forEach(function (entry, key) {
          if (componentCache.has(key)) throw new Error("KitJS: component handoff cache changed before commit");
          componentCache.set(key, entry);
          added.push(Object.freeze({ key: key, entry: entry }));
        });
        core.registry = nextRegistry;
        core.graph = targetGraph;
        core.activeDelivery = targetDelivery;
        core.compiled = new Map();
        metadata = new WeakMap();
        retainValidity = new WeakMap();
        retainStructureValidity = new WeakMap();
        retainReports = new WeakMap();
        closed = true;
        committed = true;
        componentHandoff = null;
        pending.clear();
      } catch (error) {
        added.forEach(function (item) {
          if (componentCache.get(item.key) === item.entry) componentCache.delete(item.key);
        });
        core.registry = previousRegistry;
        core.graph = previousGraph;
        core.activeDelivery = previousDelivery;
        core.compiled = previousCompiled;
        metadata = previousMetadata;
        retainValidity = previousRetainValidity;
        retainStructureValidity = previousRetainStructureValidity;
        retainReports = previousRetainReports;
        abort();
        throw error;
      }

      function rollback() {
        if (rollbackUsed) return false;
        rollbackUsed = true;
        if (!committed || core.graph !== targetGraph || core.activeDelivery !== targetDelivery ||
          core.registry !== nextRegistry) {
          throw new Error("KitJS: component handoff can no longer be rolled back");
        }
        added.forEach(function (item) {
          if (componentCache.get(item.key) === item.entry) componentCache.delete(item.key);
        });
        core.registry = previousRegistry;
        core.graph = previousGraph;
        core.activeDelivery = previousDelivery;
        core.compiled = previousCompiled;
        metadata = previousMetadata;
        retainValidity = previousRetainValidity;
        retainStructureValidity = previousRetainStructureValidity;
        retainReports = previousRetainReports;
        return true;
      }
      return Object.freeze(rollback);
    }

    tx = Object.freeze({
      graph: targetGraph,
      delivery: targetDelivery,
      missing: Object.freeze(missing),
      register: Object.freeze(register),
      ready: Object.freeze(ready),
      commit: Object.freeze(commit),
      abort: Object.freeze(abort)
    });
    componentHandoff = tx;
    return tx;
  }

  function supportsAppLoader(version) {
    return version === "1.1.0";
  }

  function releaseAppLoaderLinks(current) {
    if (current.loaderSources) {
      current.loaderSources.forEach(function (source) {
        if (source.loaderDependents) source.loaderDependents.delete(current);
      });
      current.loaderSources.clear();
      current.loaderSources = null;
    }
    if (current.loaderDependents) {
      current.loaderDependents.forEach(function (dependent) {
        if (dependent.loaderSources) dependent.loaderSources.delete(current);
      });
      current.loaderDependents.clear();
      current.loaderDependents = null;
    }
  }

  function trackAppLoaderDependency(source, dependent) {
    if (!source || !dependent || source === dependent || source.disposed || dependent.disposed) return;
    if (!source.loaderDependents) source.loaderDependents = new Set();
    if (!dependent.loaderSources) dependent.loaderSources = new Set();
    source.loaderDependents.add(dependent);
    dependent.loaderSources.add(source);
  }

  function invalidateAppLoaderDependents(source) {
    if (!source.loaderDependents) return;
    source.loaderDependents.forEach(function (dependent) {
      if (!dependent || dependent.disposed || !connectedHere(dependent.host)) {
        source.loaderDependents.delete(dependent);
        if (dependent && dependent.loaderSources) dependent.loaderSources.delete(source);
        return;
      }
      core.invalidate(dependent);
    });
  }

  function createInstance(descriptors, host, request, seed) {
    var own = {};
    Object.keys(descriptors).forEach(function (name) {
      own[name] = Object.assign({}, descriptors[name]);
      if (name !== "init" && OWN.call(own[name], "value")) {
        own[name].value = copyValue(own[name].value, new WeakSet());
      }
    });
    if (seed !== undefined) Object.keys(seed).forEach(function (name) {
      if (request) {
        var seeded = own[name];
        if (!seeded) {
          throw new TypeError("KitJS: data-kit-scope field \"" + name +
            "\" is not declared by component \"" + request.name + "\"");
        }
        if (!OWN.call(seeded, "value") || typeof seeded.value === "function" || name === "init") {
          throw new TypeError("KitJS: data-kit-scope cannot seed non-data component field \"" + name + "\"");
        }
        if (!seeded.writable) {
          throw new TypeError("KitJS: data-kit-scope cannot seed read-only component field \"" + name + "\"");
        }
        seeded.value = copyValue(seed[name], new WeakSet(), true);
      } else {
        own[name] = {
          value: copyValue(seed[name], new WeakSet(), true),
          writable: true,
          enumerable: true,
          configurable: true
        };
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
      cleanups: [],
      afterRenders: [],
      context: null,
      ownsCleanup: false,
      initialized: false,
      rendered: false,
      disposed: false,
      structures: undefined,
      observations: null,
      captures: new WeakMap(),
      loaderSources: null,
      loaderDependents: null,
      componentIdentity: request ? Object.freeze({
        name: request.name,
        version: request.version,
        lane: request.lane,
        alias: host.hasAttribute("data-kit-as") ? aliasName(host) : null
      }) : null
    };
    var scope = new Proxy(target, {
      set: function (object, name, value, receiver) {
        if (core.blocked(String(name))) return false;
        var before = Reflect.get(object, name, receiver);
        var success = Reflect.set(object, name, value, receiver);
        if (success && !core.equal(before, Reflect.get(object, name, receiver))) {
          if (String(name) === "loader") invalidateAppLoaderDependents(current);
          core.invalidate(current);
        }
        return success;
      },
      deleteProperty: function (object, name) {
        if (core.blocked(String(name))) return false;
        var had = OWN.call(object, name);
        var success = Reflect.deleteProperty(object, name);
        if (success && had) {
          if (String(name) === "loader") invalidateAppLoaderDependents(current);
          core.invalidate(current);
        }
        return success;
      }
    });
    current.scope = scope;
    current.context = lifecycleContext(current);
    core.scopeRecords.set(scope, current);
    return current;
  }

  function nearest(element) {
    while (element) {
      if (element.nodeType === 1) {
        if (element.hasAttribute("data-kit-ignore")) return null;
        if (element.hasAttribute("data-kit-component") || element.hasAttribute("data-kit-scope")) return element;
      }
      element = element.parentElement;
    }
    return null;
  }
  function reportMissingLocal(element, name) {
    if (!element || missingLocalReports.has(element)) return;
    missingLocalReports.add(element);
    core.report(new ReferenceError("KitJS: client component \"" + name +
      "\" has no registered definition"));
  }
  function auditLocalComponents() {
    localAuditComplete = true;
    var definitions = core.graph ? localRegistry : core.registry;
    document.querySelectorAll("[data-kit-component]").forEach(function (element) {
      if (core.ignoredForRuntime(element)) return;
      var request = componentMetadata(element, false);
      if (request && request.lane === "client" && !definitions.has(request.name)) {
        reportMissingLocal(element, request.name);
      }
    });
  }

  function registryForRequest(request) {
    return request && request.lane === "client" && core.graph ? localRegistry : core.registry;
  }

  function hasComponentDefinition(request) {
    var registry = registryForRequest(request);
    return !!request && !!registry && registry.has(request.name);
  }
  function invalidRetainHost(element) {
    if (!element || !element.hasAttribute("data-kit-retain")) return false;
    if (!retainValidity.has(element)) {
      inspectRetains(connectedHere(element) ? document : element, true, false);
    }
    return retainValidity.get(element) === true;
  }
  function ensureComponent(element) {
    if (!element || core.ignoredForRuntime(element)) return null;
    var current = core.scopes.get(element);
    if (current) return current.failed ? null : current;
    if (invalidRetainHost(element)) {
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      if (element.hasAttribute("data-kit-scope") && core.releaseScopeSeed) core.releaseScopeSeed(element);
      return null;
    }
    var hasScope = element.hasAttribute("data-kit-scope");
    var seed = core.scopeSeed(element, true);
    var request = componentMetadata(element, true);
    var invalidAlias = request === undefined && hasScope && element.hasAttribute("data-kit-as");
    if (invalidAlias) aliasName(element);
    if (request === null || hasScope && seed === null || request === undefined && !hasScope || invalidAlias) {
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      if (hasScope && core.releaseScopeSeed) core.releaseScopeSeed(element);
      return null;
    }
    var descriptors = request ? registryForRequest(request).get(request.name) : Object.create(null);
    if (request && !descriptors) {
      if (request.lane === "client" && localAuditComplete) reportMissingLocal(element, request.name);
      return null;
    }
    try {
      current = createInstance(descriptors, element, request, seed);
      core.scopes.set(element, current);
      return current;
    } catch (error) {
      core.report(error);
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      return null;
    } finally {
      if (hasScope && core.releaseScopeSeed) core.releaseScopeSeed(element);
    }
  }
  function scopeRecordFor(element) {
    var boundary = nearest(element);
    return boundary ? ensureComponent(boundary) : null;
  }
  function ownedElements(current, selector) {
    var output = [];
    var host = current && current.host;
    if (!host || current.disposed || !host.isConnected || core.ignoredForRuntime(host)) return output;
    if (host.matches(selector)) output.push(host);
    var walker = document.createTreeWalker(host, 1, {
      acceptNode: function (element) {
        if (element.hasAttribute("data-kit-ignore")) return 2;
        if (element.hasAttribute("data-kit-component") || element.hasAttribute("data-kit-scope")) return 2;
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
      var initialized = current.init.call(current.scope, current.context);
      if (typeof initialized === "function") ownCleanup(current, initialized);
      else core.observe(initialized, current);
    }
    catch (error) { core.report(error); }
  }
  function componentElements(root) {
    var output = [];
    root = root && root.querySelectorAll ? root : document;
    if (root.nodeType === 1 && core.ignoredForRuntime(root)) return output;
    if (root.nodeType === 1 && root.matches(BOUNDARIES)) output.push(root);
    root.querySelectorAll(BOUNDARIES).forEach(function (element) {
      if (!core.ignoredForRuntime(element)) output.push(element);
    });
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
    if (current.failed) {
      current.host = null;
      core.dirtyRecords.delete(current);
      if (core.renderPending) core.renderPending.delete(current);
      core.scopes.delete(element);
      aliases.delete(element);
      metadata.delete(element);
      retainValidity.delete(element);
      retainStructureValidity.delete(element);
      retainReports.delete(element);
      return;
    }
    var scope = current.scope;
    var cleanups = current.cleanups.slice().reverse();
    current.cleanups.length = 0;
    current.afterRenders.forEach(function (entry) { entry.active = false; });
    current.afterRenders.length = 0;
    if (current.ownsCleanup) {
      current.ownsCleanup = false;
      cleanupOwners--;
      stopCleanupObserver();
    }
    cleanups.forEach(function (entry) {
      if (!entry.active) return;
      entry.active = false;
      try { entry.callback.call(scope); }
      catch (error) { core.report(error); }
    });
    releaseAppLoaderLinks(current);
    if (current.observations) current.observations.clear();
    if (current.scope) core.scopeRecords.delete(current.scope);
    current.host = null;
    current.scope = null;
    current.init = null;
    current.context = null;
    current.cleanups = null;
    current.afterRenders = null;
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
      if (core.ignoredForRuntime(element)) return;
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

  function resolveAppService(alias, current, serviceName) {
    if (alias !== "$app" || !current || !current.componentIdentity ||
      current.componentIdentity.lane !== "managed" || current.componentIdentity.name !== "app" ||
      current.componentIdentity.alias !== "$app" ||
      !core.graph || !core.graph.grants || !core.serviceRegistry) return undefined;
    var granted = core.graph.grants.app;
    if (!granted || !OWN.call(granted, serviceName) ||
      granted[serviceName] !== core.graph.services[serviceName]) return undefined;
    return core.serviceRegistry.get(serviceName);
  }

  function resolveAppLoader(dependent, leaf) {
    if (leaf !== "visible" && leaf !== "value" || !dependent || dependent.disposed ||
      !connectedHere(dependent.host)) {
      throw new ReferenceError("KitJS: $app.loader is unavailable");
    }
    var current = resolveAlias("$app");
    var identity = current.componentIdentity;
    if (!identity || identity.lane !== "managed" || identity.name !== "app" || identity.alias !== "$app" ||
      !supportsAppLoader(identity.version) || !core.graph ||
      core.graph.components.app !== identity.version || !current.host.contains(dependent.host)) {
      throw new ReferenceError("KitJS: $app.loader requires the canonical app@1.1.0 ancestor");
    }
    var granted = core.graph.grants && core.graph.grants.app;
    if (granted && OWN.call(granted, "loader")) {
      throw new TypeError("KitJS: $app.loader cannot expose a granted service");
    }
    var loader = Reflect.get(current.scope, "loader", current.scope);
    if (!loader || typeof loader !== "object" || !Object.isFrozen(loader) || !OWN.call(loader, leaf) ||
      typeof core.serviceName === "function" && core.serviceName(loader)) {
      throw new TypeError("KitJS: invalid $app.loader snapshot");
    }
    var value = loader[leaf];
    var invalidValue = leaf === "visible" ? typeof value !== "boolean" :
      value !== null && (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > 100);
    if (invalidValue ||
      typeof core.serviceName === "function" && core.serviceName(value)) {
      throw new TypeError("KitJS: invalid $app.loader snapshot");
    }
    trackAppLoaderDependency(current, dependent);
    return value;
  }

  core.startHooks.push(function () {
    function scheduleAudit() {
      var schedule = document.defaultView && document.defaultView.setTimeout;
      if (typeof schedule === "function") schedule.call(document.defaultView, auditLocalComponents, 0);
      else enqueue(auditLocalComponents);
    }
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", scheduleAudit, { once: true });
    } else scheduleAudit();
  });

  core.activeDelivery = null;
  Object.defineProperty(core, "delivery", {
    get: function () { return core.activeDelivery; }
  });
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
  core.installStagedDelivery = installStagedDelivery;
  core.recordStagedComponentPackage = recordStagedComponentPackage;
  core.beginComponentHandoff = beginComponentHandoff;
  core.componentMetadata = componentMetadata;
  core.componentMetadataForGraph = componentMetadataForGraph;
  core.hasComponentDefinition = hasComponentDefinition;
  core.inspectRetains = function (root) { return inspectRetains(root, false, true); };
  core.invalidRetainStructure = function (element) {
    if (!retainStructureValidity.has(element)) {
      inspectRetains(connectedHere(element) ? document : element, true, false);
    }
    return retainStructureValidity.get(element) === true;
  };
  core.prepareComponentTree = prepareComponentTree;
  core.validateComponentTree = validateComponentTree;
  core.assertComponentGraph = assertComponentGraph;
  core.ensureComponent = ensureComponent;
  core.ownerFor = nearest;
  core.scopeRecordFor = scopeRecordFor;
  core.ownedElements = ownedElements;
  core.initialize = initialize;
  core.flushAfterRender = flushAfterRender;
  core.liveComponents = liveComponents;
  core.disposeComponent = disposeComponent;
  core.validAlias = validAlias;
  core.validServiceName = validServiceName;
  core.resolveAlias = resolveAlias;
  core.resolveAppLoader = resolveAppLoader;
  core.resolveAppService = resolveAppService;
  core.phase = "component";
})(document);
