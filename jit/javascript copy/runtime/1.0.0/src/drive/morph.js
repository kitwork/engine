"use strict";

var utils = require("../core/utils.js");
var errors = require("../core/errors.js");
var toArray = utils.toArray;
var cssEscape = utils.cssEscape;
var createRuntimeError = errors.createRuntimeError;

var FORM_CONTROL_SELECTOR = "input,textarea,select";

function createMorphEngine(runtime) {
  var document = runtime.document;

  function persistKey(node) {
    return node && node.nodeType === 1
      ? String(node.getAttribute("data-kit-persist") || "").trim()
      : "";
  }

  function stableKey(node) {
    if (!node || node.nodeType !== 1) return "";
    var persist = persistKey(node);
    if (persist) return "persist:" + persist;
    var id = String(node.id || "").trim();
    if (id) return "id:" + id;
    var key = String(node.getAttribute("data-kit-key") || "").trim();
    if (key) return "key:" + key;
    return "";
  }

  function compatible(oldNode, incomingNode) {
    if (!oldNode || !incomingNode || oldNode.nodeType !== incomingNode.nodeType) return false;
    if (oldNode.nodeType === 1) {
      if (oldNode.tagName !== incomingNode.tagName) return false;
      var oldComponent = String(oldNode.getAttribute("data-kit-component") || "").trim();
      var nextComponent = String(incomingNode.getAttribute("data-kit-component") || "").trim();
      if (oldComponent !== nextComponent) return false;
      var oldApp = String(oldNode.getAttribute("data-kit-app") || "").trim();
      var nextApp = String(incomingNode.getAttribute("data-kit-app") || "").trim();
      if (oldApp !== nextApp && (oldApp || nextApp)) return false;
    }
    return true;
  }

  function controlIdentity(element, root) {
    if (!element || element.nodeType !== 1) return "";
    var persistent = persistKey(element);
    if (persistent) return "persist:" + persistent;
    if (element.id) return "id:" + element.id;
    var key = element.getAttribute("data-kit-key");
    if (key) return "key:" + key;
    var name = element.getAttribute("name");
    if (name) {
      var type = String(element.type || element.tagName || "").toLowerCase();
      return "name:" + type + ":" + name;
    }
    var parts = [];
    var current = element;
    while (current && current !== root && current.parentElement) {
      var index = 1;
      var sibling = current.previousElementSibling;
      while (sibling) { index++; sibling = sibling.previousElementSibling; }
      parts.unshift(String(current.tagName || "node").toLowerCase() + ":" + index);
      current = current.parentElement;
    }
    return parts.length ? "path:" + parts.join("/") : "";
  }

  function isDirtyControl(element) {
    var tag = String(element.tagName || "").toLowerCase();
    var type = String(element.type || "").toLowerCase();
    if (type === "file") return false;
    if (type === "checkbox" || type === "radio") return element.checked !== element.defaultChecked;
    if (tag === "select") {
      var options = toArray(element.options);
      for (var i = 0; i < options.length; i++) {
        if (options[i].selected !== options[i].defaultSelected) return true;
      }
      return false;
    }
    return element.value !== element.defaultValue;
  }

  function captureControl(element, root, activeElement) {
    var tag = String(element.tagName || "").toLowerCase();
    var type = String(element.type || "").toLowerCase();
    var active = element === activeElement;
    var owned = element.hasAttribute("data-kit-model");
    if (!active && !owned && !isDirtyControl(element)) return null;

    var state = {
      identity: controlIdentity(element, root),
      element: element,
      tag: tag,
      type: type,
      active: active,
      scrollTop: element.scrollTop || 0,
      scrollLeft: element.scrollLeft || 0
    };

    if (type === "checkbox" || type === "radio") state.checked = !!element.checked;
    else if (tag === "select") {
      state.values = toArray(element.options).filter(function (option) {
        return option.selected;
      }).map(function (option) { return String(option.value); });
    } else if (type !== "file") state.value = element.value;

    if (active && typeof element.selectionStart === "number") {
      state.selectionStart = element.selectionStart;
      state.selectionEnd = element.selectionEnd;
      state.selectionDirection = element.selectionDirection || "none";
    }
    return state;
  }

  function captureDomState(root) {
    var activeElement = document.activeElement && root.contains(document.activeElement)
      ? document.activeElement
      : null;
    var controls = new Map();
    toArray(root.querySelectorAll(FORM_CONTROL_SELECTOR)).forEach(function (element) {
      var state = captureControl(element, root, activeElement);
      if (state && state.identity && !controls.has(state.identity)) controls.set(state.identity, state);
    });

    var scroll = new Map();
    toArray(root.querySelectorAll("[data-kit-scroll-preserve],[data-kit-persist]")).forEach(function (element) {
      var identity = stableKey(element) || controlIdentity(element, root);
      if (identity) scroll.set(identity, { top: element.scrollTop, left: element.scrollLeft, element: element });
    });

    return {
      activeElement: activeElement,
      activeIdentity: activeElement ? controlIdentity(activeElement, root) : "",
      controls: controls,
      scroll: scroll
    };
  }

  function findByIdentity(root, identity, fallbackElement) {
    if (fallbackElement && fallbackElement.isConnected && root.contains(fallbackElement)) return fallbackElement;
    if (!identity) return null;
    var value;
    if (identity.indexOf("persist:") === 0) {
      value = identity.slice(8);
      return root.querySelector('[data-kit-persist="' + cssEscape(value) + '"]');
    }
    if (identity.indexOf("id:") === 0) {
      value = identity.slice(3);
      var byId = document.getElementById(value);
      return byId && root.contains(byId) ? byId : null;
    }
    if (identity.indexOf("key:") === 0) {
      value = identity.slice(4);
      return root.querySelector('[data-kit-key="' + cssEscape(value) + '"]');
    }
    if (identity.indexOf("name:") === 0) {
      var pieces = identity.split(":");
      var name = pieces.slice(2).join(":");
      return root.querySelector('[name="' + cssEscape(name) + '"]');
    }
    return null;
  }

  function restoreControl(root, state) {
    var element = findByIdentity(root, state.identity, state.element);
    if (!element) return;
    if (state.type === "checkbox" || state.type === "radio") element.checked = state.checked;
    else if (state.tag === "select") {
      var selected = new Set(state.values || []);
      toArray(element.options).forEach(function (option) {
        option.selected = selected.has(String(option.value));
      });
    } else if (state.type !== "file" && state.value !== undefined) element.value = state.value;

    element.scrollTop = state.scrollTop || 0;
    element.scrollLeft = state.scrollLeft || 0;
    if (state.active && typeof element.focus === "function") {
      try { element.focus({ preventScroll: true }); } catch (_) { try { element.focus(); } catch (_) {} }
      if (typeof state.selectionStart === "number" && typeof element.setSelectionRange === "function") {
        try { element.setSelectionRange(state.selectionStart, state.selectionEnd, state.selectionDirection); } catch (_) {}
      }
    }
  }

  function restoreDomState(root, snapshot) {
    snapshot.controls.forEach(function (state) { restoreControl(root, state); });
    snapshot.scroll.forEach(function (state, identity) {
      var element = findByIdentity(root, identity, state.element);
      if (element) {
        element.scrollTop = state.top || 0;
        element.scrollLeft = state.left || 0;
      }
    });
    if (snapshot.activeIdentity && (!document.activeElement || document.activeElement === document.body)) {
      var active = findByIdentity(root, snapshot.activeIdentity, snapshot.activeElement);
      if (active && typeof active.focus === "function") {
        try { active.focus({ preventScroll: true }); } catch (_) { try { active.focus(); } catch (_) {} }
      }
    }
  }

  function prepareElement(element, app) {
    var record = runtime.peekNodeRecord(element);
    if (!record || record.app !== app) return;
    Array.from(record.bindings.values()).forEach(function (binding) {
      // Event bindings may own pending asynchronous actions. Keep an unchanged
      // event alive through the swap; reconcileBindings will replace it when
      // its authored attribute changes.
      if (binding.phase !== "event") runtime.cleanupBinding(binding);
    });
  }

  function syncAttributes(oldElement, incomingElement, app) {
    prepareElement(oldElement, app);
    var incoming = new Map();
    toArray(incomingElement.attributes).forEach(function (attribute) {
      incoming.set(attribute.name, attribute.value);
    });

    toArray(oldElement.attributes).forEach(function (attribute) {
      if (!incoming.has(attribute.name)) oldElement.removeAttribute(attribute.name);
    });
    incoming.forEach(function (value, name) {
      if (!oldElement.hasAttribute(name) || oldElement.getAttribute(name) !== value) {
        oldElement.setAttribute(name, value);
      }
    });
  }

  function cloneIncoming(node) {
    if (node.nodeType === 1 && String(node.tagName).toLowerCase() === "script") {
      var script = document.createElement("script");
      toArray(node.attributes).forEach(function (attribute) { script.setAttribute(attribute.name, attribute.value); });
      script.setAttribute("data-kit-drive-type", node.getAttribute("type") || "");
      script.setAttribute("type", "application/kitwork-pending");
      script.setAttribute("data-kit-drive-pending", "");
      script.text = node.textContent || "";
      return script;
    }
    var clone = document.importNode ? document.importNode(node, true) : node.cloneNode(true);
    if (clone && clone.nodeType === 1 && clone.querySelectorAll) {
      toArray(clone.querySelectorAll("script")).forEach(function (script) {
        script.setAttribute("data-kit-drive-type", script.getAttribute("type") || "");
        script.setAttribute("type", "application/kitwork-pending");
        script.setAttribute("data-kit-drive-pending", "");
      });
    }
    return clone;
  }

  function removeNode(node, app) {
    runtime.cleanupTree(node, app);
    if (node.parentNode) node.parentNode.removeChild(node);
  }

  function morphNode(oldNode, incomingNode, context) {
    if (oldNode === incomingNode) return oldNode;
    if (!compatible(oldNode, incomingNode)) {
      var replacement = cloneIncoming(incomingNode);
      oldNode.parentNode.replaceChild(replacement, oldNode);
      runtime.cleanupTree(oldNode, context.app);
      return replacement;
    }

    if (oldNode.nodeType === 3 || oldNode.nodeType === 8) {
      if (oldNode.nodeValue !== incomingNode.nodeValue) oldNode.nodeValue = incomingNode.nodeValue;
      return oldNode;
    }

    var incomingPersist = persistKey(incomingNode);
    if (incomingPersist && oldNode === context.app.persisted.get(incomingPersist)) {
      context.usedPersist.add(incomingPersist);
      return oldNode;
    }

    syncAttributes(oldNode, incomingNode, context.app);

    var tag = String(oldNode.tagName || "").toLowerCase();
    if (tag === "textarea") {
      if (oldNode.defaultValue !== incomingNode.defaultValue) oldNode.defaultValue = incomingNode.defaultValue;
      oldNode.textContent = incomingNode.textContent || "";
      return oldNode;
    }
    if (tag === "script") {
      var scriptReplacement = cloneIncoming(incomingNode);
      oldNode.parentNode.replaceChild(scriptReplacement, oldNode);
      runtime.cleanupTree(oldNode, context.app);
      return scriptReplacement;
    }

    morphChildren(oldNode, incomingNode, context);
    return oldNode;
  }

  function findCompatibleUnkeyed(oldChildren, used, incoming, startIndex) {
    for (var i = startIndex; i < oldChildren.length; i++) {
      var candidate = oldChildren[i];
      if (used.has(candidate) || stableKey(candidate)) continue;
      if (compatible(candidate, incoming)) return candidate;
    }
    return null;
  }

  function morphChildren(oldParent, incomingParent, context) {
    var oldChildren = toArray(oldParent.childNodes);
    var incomingChildren = toArray(incomingParent.childNodes);
    var keyed = new Map();
    oldChildren.forEach(function (node) {
      var key = stableKey(node);
      if (key && !keyed.has(key)) keyed.set(key, node);
    });

    var used = new Set();
    var cursor = oldParent.firstChild;
    for (var i = 0; i < incomingChildren.length; i++) {
      var incoming = incomingChildren[i];
      var match = null;
      var incomingPersist = persistKey(incoming);
      if (incomingPersist) {
        var persisted = context.app.persisted.get(incomingPersist);
        if (persisted && persisted.isConnected && runtime.appForNode(persisted) === context.app && !used.has(persisted)) {
          match = persisted;
          context.usedPersist.add(incomingPersist);
        }
      }
      if (!match) {
        var key = stableKey(incoming);
        if (key && keyed.has(key) && !used.has(keyed.get(key))) match = keyed.get(key);
      }
      if (!match && cursor && !used.has(cursor) && !stableKey(cursor) && compatible(cursor, incoming)) match = cursor;
      if (!match) match = findCompatibleUnkeyed(oldChildren, used, incoming, i);

      if (!match) {
        match = cloneIncoming(incoming);
        oldParent.insertBefore(match, cursor);
      } else {
        if (match !== cursor) oldParent.insertBefore(match, cursor);
        match = morphNode(match, incoming, context);
      }
      used.add(match);
      cursor = match.nextSibling;
    }

    oldChildren.forEach(function (node) {
      if (!used.has(node) && node.parentNode === oldParent) removeNode(node, context.app);
    });
  }

  function morphRoot(app, incomingRoot, options) {
    options = options || {};
    if (!app || !app.root || !incomingRoot) {
      throw createRuntimeError("KIT_DRIVE_MORPH_ROOT", "Drive morph requires current and incoming roots");
    }
    if (!compatible(app.root, incomingRoot)) {
      throw createRuntimeError("KIT_DRIVE_ROOT_MISMATCH", "Incoming Drive root is incompatible with the current app root", {
        current: app.root,
        incoming: incomingRoot
      });
    }

    var snapshot = captureDomState(app.root);
    var context = { app: app, usedPersist: new Set(), options: options };
    runtime.pauseObserver(app);
    try {
      syncAttributes(app.root, incomingRoot, app);
      if (String(app.root.tagName || "").toLowerCase() === "html") {
        var currentBody = document.body;
        var incomingBody = incomingRoot.querySelector("body");
        if (!currentBody || !incomingBody) {
          throw createRuntimeError("KIT_DRIVE_BODY_MISSING", "Drive HTML response must contain a body element");
        }
        morphNode(currentBody, incomingBody, context);
      } else {
        morphChildren(app.root, incomingRoot, context);
      }
      runtime.hydrateTree(app.root, app);
      runtime.scheduler.invalidate(app, app.root, { type: "drive-morph" });
      runtime.scheduler.flush(app);
    } finally {
      runtime.resumeObserver(app);
    }
    restoreDomState(app.root, snapshot);
    return app.root;
  }

  return {
    morphRoot: morphRoot,
    morphNode: morphNode,
    captureDomState: captureDomState,
    restoreDomState: restoreDomState,
    stableKey: stableKey
  };
}

module.exports = {
  createMorphEngine: createMorphEngine
};
