// KitJS private lifecycle, delegated events and mutation ownership.
(function (window, document) {
  "use strict";

  var kit = window.kit;
  var core = kit && kit.__kitwork_core__;
  if (!core) throw new Error("KitJS core/global.js must be loaded before core/lifecycle.js");
  if (core.reuse) return;
  if (core.phase !== "dom") throw new Error("KitJS core fragment order error before core/lifecycle.js");

  var OWN = core.OWN;
  var INVALID_MEMBER = core.INVALID_MEMBER;
  var blueprints = core.blueprints;
  var runtime = core.runtime;
  var state = core.state;
  var peek = core.peek;
  var warn = core.warn;
  var report = core.report;
  var memberKey = core.memberKey;
  var evaluate = core.evaluate;
  var enqueue = core.enqueue;
  var isElement = core.isElement;
  var inRoot = core.inRoot;
  var depth = core.depth;
  var walk = core.walk;
  var closestComponent = core.closestComponent;
  var resolverFor = core.resolverFor;
  var refreshRefs = core.refreshRefs;
  var hydrate = core.hydrate;
  var readControl = core.readControl;
  var schedule = core.schedule;

  function isThenable(value) {
    return value !== null && value !== undefined &&
      (typeof value === "object" || typeof value === "function") &&
      typeof value.then === "function";
  }

  function observePromise(value, context, settled) {
    if (!isThenable(value)) return false;
    Promise.resolve(value).then(function (result) {
      if (settled) settled(result, false);
      schedule();
    }, function (error) {
      if (!(error && error.name === "AbortError")) report(error, context);
      if (settled) settled(undefined, true);
      schedule();
    });
    return true;
  }

  function runCleanup(record, cleanup) {
    try {
      observePromise(cleanup(), {
        component: record.instance,
        componentName: record.name,
        lifecycle: "cleanup"
      });
    } catch (error) {
      report(error, { component: record.instance, lifecycle: "cleanup" });
    }
  }

  function initialize(record) {
    if (!record || record.disposed || record.initialized) return;
    record.initialized = true;
    if (typeof record.instance.init !== "function") return;
    try {
      var result = record.instance.init();
      if (!observePromise(result, { component: record.instance, element: record.host, lifecycle: "init" }, function (cleanup, failed) {
        if (!failed && typeof cleanup === "function") {
          if (record.disposed) runCleanup(record, cleanup);
          else record.cleanup = cleanup;
        }
      }) && typeof result === "function") record.cleanup = result;
    } catch (error) {
      report(error, { component: record.instance, element: record.host, lifecycle: "init" });
    }
  }

  function flushInits() {
    var pending = runtime.pendingInits;
    runtime.pendingInits = [];
    pending.sort(function (left, right) { return depth(left.host) - depth(right.host); });
    pending.forEach(initialize);
  }

  function disposeComponent(record) {
    if (!record || record.disposed) return;
    var host = record.host;
    record.disposed = true;
    if (record.cleanup) {
      runCleanup(record, record.cleanup);
      record.cleanup = null;
    }
    if (record.alias && runtime.aliases[record.alias] === record.instance) delete runtime.aliases[record.alias];
    runtime.componentByHost.delete(host);
    Object.keys(record.refs).forEach(function (name) { delete record.refs[name]; });
    record.parent = null;
    record.host = null;
    record.alias = "";
    record.initialized = false;
  }

  function disposeTree(root) {
    var records = [];
    var elements = [];
    walk(root, function (element) {
      elements.push(element);
      var component = runtime.componentByHost.get(element);
      if (component) records.push(component);
      var elementState = peek(element);
      if (elementState && elementState.actions) {
        elementState.actions.forEach(function (action) {
          if (action.timer) window.clearTimeout(action.timer);
          action.disposed = true;
          action.element.removeAttribute("data-busy");
          action.element.removeAttribute("aria-busy");
        });
      }
      runtime.actionsByElement.delete(element);
    }, null, true);
    records.sort(function (left, right) { return depth(right.host) - depth(left.host); });
    records.forEach(disposeComponent);
    runtime.bindings = runtime.bindings.filter(function (binding) {
      return !(binding.element === root || root.contains(binding.element));
    });
    runtime.components = runtime.components.filter(function (component) { return !component.disposed; });
    runtime.pendingInits = runtime.pendingInits.filter(function (component) { return !component.disposed; });
    Object.keys(runtime.outsideActions).forEach(function (eventType) {
      runtime.outsideActions[eventType] = runtime.outsideActions[eventType].filter(function (action) { return !action.disposed; });
    });
    elements.forEach(core.deleteElementState);
  }

  function writeModel(element) {
    var ast = state(element).model;
    if (!ast) return;
    var expressionResolver = resolverFor(element, {});
    var value = readControl(element);
    if (ast.type === "identifier") expressionResolver.set(ast.name, value);
    else if (ast.type === "member") {
      var object = evaluate(ast.object, expressionResolver);
      var key = memberKey(ast.computed ? evaluate(ast.property, expressionResolver) : ast.property);
      if (object !== null && object !== undefined && key !== INVALID_MEMBER) object[key] = value;
      else warn("KIT_MODEL_NOT_WRITABLE", element.getAttribute("data-kit-model"));
    } else warn("KIT_MODEL_NOT_WRITABLE", element.getAttribute("data-kit-model"));
    schedule();
  }

  function hasModifier(action, name) { return action.modifiers.indexOf(name) !== -1; }
  function debounceDelay(action) {
    for (var index = 0; index < action.modifiers.length; index++) {
      var modifier = action.modifiers[index];
      if (modifier === "debounce") return 300;
      if (modifier.indexOf("debounce(") === 0) return parseInt(modifier.slice(9, -1), 10) || 300;
    }
    return 0;
  }

  function trustedFocusable(element) {
    if (!isElement(element) || element.isConnected === false || typeof element.focus !== "function") return false;
    if (element.disabled || element.getAttribute("aria-disabled") === "true" || element.getAttribute("aria-hidden") === "true") return false;
    if (element.hidden || element.closest("[hidden],[inert]")) return false;
    return element.tabIndex >= 0;
  }

  function safeFocus(element) {
    if (!trustedFocusable(element)) return false;
    try { element.focus({ preventScroll: true }); }
    catch (_) {
      try { element.focus(); }
      catch (_) { return false; }
    }
    return document.activeElement === element;
  }

  function restoreEscapeFocus(action) {
    var component = closestComponent(action.element);
    if (!component) return;
    enqueue(function () {
      if (component.disposed) return;
      safeFocus(component.refs.trigger);
    });
  }

  var overlayState = {
    owner: null,
    close: null,
    restore: null,
    handoff: 0,
    scrollLock: null
  };

  function focusTarget() {
    var current = document.activeElement;
    return current && current !== document.body && current !== document.documentElement ? current : null;
  }

  function focusConnected(element) {
    if (!isElement(element) || element.isConnected === false || typeof element.focus !== "function") return false;
    try { element.focus({ preventScroll: true }); }
    catch (_) {
      try { element.focus(); }
      catch (_) { return false; }
    }
    return document.activeElement === element;
  }

  function lockOverlayScroll() {
    if (overlayState.scrollLock || !document.body) return;
    var body = document.body;
    overlayState.scrollLock = {
      body: body,
      value: body.style.getPropertyValue("overflow"),
      priority: body.style.getPropertyPriority("overflow")
    };
    body.style.setProperty("overflow", "hidden");
  }

  function unlockOverlayScroll() {
    var lock = overlayState.scrollLock;
    overlayState.scrollLock = null;
    if (!lock || !lock.body) return;
    if (lock.value) lock.body.style.setProperty("overflow", lock.value, lock.priority);
    else lock.body.style.removeProperty("overflow");
  }

  function overlayIsOwner(owner) {
    return overlayState.owner === owner;
  }

  function overlayRelease(owner, restoreFocus) {
    if (overlayState.owner !== owner) return false;
    var restore = overlayState.restore;
    var releasedFocus = document.activeElement;
    overlayState.owner = null;
    overlayState.close = null;
    overlayState.restore = null;
    if (!overlayState.handoff) unlockOverlayScroll();
    if (restoreFocus === false) return true;
    enqueue(function () {
      var current = document.activeElement;
      if (overlayState.owner || (current !== releasedFocus && current !== document.body && current !== document.documentElement)) return;
      focusConnected(restore);
    });
    return true;
  }

  function overlayClaim(owner, close) {
    if (!owner || typeof close !== "function") throw new TypeError("KitJS overlay claim requires an owner and close callback");
    if (overlayState.owner === owner) {
      overlayState.close = close;
      lockOverlayScroll();
      return true;
    }

    var restore = focusTarget();
    if (overlayState.owner) {
      var priorOwner = overlayState.owner;
      var priorClose = overlayState.close;
      restore = overlayState.restore;
      overlayState.handoff++;
      try { priorClose(false); }
      finally { overlayState.handoff--; }
      if (overlayState.owner === priorOwner) {
        throw new Error("KitJS overlay owner did not release during handoff");
      }
    }

    overlayState.owner = owner;
    overlayState.close = close;
    overlayState.restore = restore;
    lockOverlayScroll();
    return true;
  }

  function resetOverlay() {
    overlayState.owner = null;
    overlayState.close = null;
    overlayState.restore = null;
    overlayState.handoff = 0;
    unlockOverlayScroll();
  }

  var overlay = Object.freeze({
    claim: overlayClaim,
    release: overlayRelease,
    isOwner: overlayIsOwner
  });

  function executeAction(action, event) {
    if (action.disposed) return;
    if (action.once && hasModifier(action, "once")) return;
    if (hasModifier(action, "self") && event.target !== action.element) return;
    if (hasModifier(action, "enter") && event.key !== "Enter") return;
    if (hasModifier(action, "escape") && event.key !== "Escape") return;
    if (hasModifier(action, "prevent")) event.preventDefault();
    if (hasModifier(action, "stop")) event.stopPropagation();
    if (hasModifier(action, "once")) action.once = true;

    var run = function () {
      if (action.disposed) return;
      var seenPromises = [];
      try {
        var expressionResolver = resolverFor(action.element, { event: event });
        action.program.forEach(function (ast) {
          var result = evaluate(ast, expressionResolver);
          if (!isThenable(result) || seenPromises.indexOf(result) !== -1) return;
          seenPromises.push(result);
          action.pending++;
          action.element.setAttribute("data-busy", "true");
          action.element.setAttribute("aria-busy", "true");
          observePromise(result, { element: action.element, event: action.eventType, source: action.source }, function () {
            if (action.disposed) return;
            action.pending = Math.max(0, action.pending - 1);
            if (!action.pending) {
              action.element.removeAttribute("data-busy");
              action.element.removeAttribute("aria-busy");
            }
          });
        });
      } catch (error) {
        report(error, { element: action.element, event: action.eventType, source: action.source });
        return;
      }
      schedule();
      if (action.eventType === "keydown" && hasModifier(action, "escape")) restoreEscapeFocus(action);
    };

    var delay = debounceDelay(action);
    if (delay) {
      if (action.timer) window.clearTimeout(action.timer);
      action.timer = window.setTimeout(run, delay);
    } else run();
  }

  function dispatch(eventType, event) {
    var target = event.target;
    if ((eventType === "input" || eventType === "change") && isElement(target) && state(target).model) writeModel(target);

    var current = isElement(target) ? target : target && target.parentElement;
    while (isElement(current) && inRoot(current)) {
      var actions = runtime.actionsByElement.get(current) || [];
      actions.forEach(function (action) {
        if (action.eventType === eventType && !hasModifier(action, "outside")) executeAction(action, event);
      });
      if (current === runtime.root || event.cancelBubble) break;
      current = current.parentElement;
    }

    (runtime.outsideActions[eventType] || []).forEach(function (action) {
      if (inRoot(action.element) && !action.element.contains(target)) executeAction(action, event);
    });
  }

  function ensureListener(eventType) {
    if (!runtime.started || runtime.listeners[eventType]) return;
    var target = runtime.root === document.documentElement ? document : runtime.root;
    var listener = function (event) { dispatch(eventType, event); };
    runtime.listeners[eventType] = { target: target, listener: listener };
    target.addEventListener(eventType, listener, true);
  }

  function removeListeners() {
    Object.keys(runtime.listeners).forEach(function (eventType) {
      var entry = runtime.listeners[eventType];
      entry.target.removeEventListener(eventType, entry.listener, true);
    });
    runtime.listeners = Object.create(null);
  }

  function processMutations() {
    runtime.mutationScheduled = false;
    var mutations = runtime.mutations;
    runtime.mutations = [];
    var additions = [];
    var removals = [];
    mutations.forEach(function (mutation) {
      for (var added = 0; added < mutation.addedNodes.length; added++) {
        if (isElement(mutation.addedNodes[added])) additions.push(mutation.addedNodes[added]);
      }
      for (var removed = 0; removed < mutation.removedNodes.length; removed++) {
        if (isElement(mutation.removedNodes[removed])) removals.push(mutation.removedNodes[removed]);
      }
    });
    removals.forEach(function (root) { if (!inRoot(root)) disposeTree(root); });
    hydrate(additions);
    refreshRefs();
    schedule();
  }

  function observeRoot() {
    if (typeof window.MutationObserver !== "function") return;
    runtime.observer = new window.MutationObserver(function (mutations) {
      runtime.mutations = runtime.mutations.concat(mutations);
      if (!runtime.mutationScheduled) {
        runtime.mutationScheduled = true;
        enqueue(processMutations);
      }
    });
    runtime.observer.observe(runtime.root, { childList: true, subtree: true });
  }

  function normalizeRoot(root) {
    if (!root) return document.documentElement;
    if (root.nodeType === 9) return root.documentElement;
    if (!isElement(root)) throw new TypeError("KitJS runtime root must be a Document or Element");
    return root;
  }

  kit.component = function (name, definition) {
    name = String(name || "").trim();
    if (!/^[A-Za-z][A-Za-z0-9_.-]*$/.test(name)) throw new TypeError("kit.component(name, definition) requires a valid name");
    if (arguments.length !== 2) throw new TypeError("kit.component(name, definition) requires exactly two arguments");
    if (!definition || typeof definition !== "object") throw new TypeError("Component '" + name + "' must be a plain object");
    var prototype = Object.getPrototypeOf(definition);
    if (prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("Component '" + name + "' must be a plain object");
    }
    if (OWN.call(blueprints, name)) {
      if (blueprints[name] === definition) return definition;
      throw new Error("Component already registered: " + name);
    }
    blueprints[name] = definition;
    if (runtime.started) enqueue(function () { hydrate([runtime.root]); });
    return definition;
  };

  function runRuntimeHooks(hooks, lifecycle) {
    hooks.slice().forEach(function (hook) {
      try { hook(); }
      catch (error) { report(error, { lifecycle: lifecycle }); }
    });
  }

  function startRuntime(root) {
    root = normalizeRoot(root);
    if (typeof window.Proxy !== "function") throw new Error("KitJS requires Proxy support");
    if (runtime.started && runtime.root === root) {
      hydrate([root]);
      return kit;
    }
    if (runtime.started) destroyRuntime();
    core.resetRuntime();
    runtime.root = root;
    runtime.started = true;
    observeRoot();
    hydrate([root]);
    runRuntimeHooks(core.startHooks, "runtime-start");
    return kit;
  }

  function destroyRuntime(root) {
    if (!runtime.started) return kit;
    if (root) {
      root = normalizeRoot(root);
      if (root !== runtime.root) {
        disposeTree(root);
        schedule();
        return kit;
      }
    }
    runRuntimeHooks(core.destroyHooks, "runtime-destroy");
    if (runtime.observer) runtime.observer.disconnect();
    removeListeners();
    disposeTree(runtime.root);
    resetOverlay();
    core.resetRuntime();
    core.resetElementStates();
    return kit;
  }

  // Optional classic-script modules and core/boot.js capture this lifecycle
  // before boot deletes the assembly capsule from the public namespace.
  core.isThenable = isThenable;
  core.observePromise = observePromise;
  core.runCleanup = runCleanup;
  core.initialize = initialize;
  core.flushInits = flushInits;
  core.disposeComponent = disposeComponent;
  core.disposeTree = disposeTree;
  core.writeModel = writeModel;
  core.hasModifier = hasModifier;
  core.debounceDelay = debounceDelay;
  core.trustedFocusable = trustedFocusable;
  core.safeFocus = safeFocus;
  core.restoreEscapeFocus = restoreEscapeFocus;
  core.overlay = overlay;
  core.executeAction = executeAction;
  core.dispatch = dispatch;
  core.ensureListener = ensureListener;
  core.removeListeners = removeListeners;
  core.processMutations = processMutations;
  core.observeRoot = observeRoot;
  core.normalizeRoot = normalizeRoot;
  core.startRuntime = startRuntime;
  core.destroyRuntime = destroyRuntime;
  core.phase = "lifecycle";

})(window, document);
