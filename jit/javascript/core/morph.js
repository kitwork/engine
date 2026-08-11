// KitJS same-plan DOM morphing for Drive.
// Classic browser script: compose after core/lifecycle.js and before core/drive.js.
(function (window, document) {
  "use strict";

  var kit = window.kit;
  var core = kit && kit.__kitwork_core__;
  if (!core) throw new Error("KitJS core must be loaded before core/morph.js");
  if (core.reuse) return;
  if (core.phase !== "lifecycle") throw new Error("KitJS core fragment order error before core/morph.js");
  if (core.morph) throw new Error("KitJS morph is already installed");

  // Capture lifecycle hooks now. core/boot.js deliberately removes the capsule.
  var disposeTree = core.disposeTree;
  var hydrate = core.hydrate;
  var refreshRefs = core.refreshRefs;
  var schedule = core.schedule;

  var ELEMENT = 1;
  var TEXT = 3;
  var COMMENT = 8;
  var NAVIGATION_ATTRIBUTES = {
    "data-kit-app": true,
    "data-kit-hydrate": true,
    "data-kit-drive": true,
    "data-kit-ignore": true,
    "data-kit-key": true
  };

  function array(value) {
    return Array.prototype.slice.call(value || []);
  }

  function isElement(node) {
    return !!node && node.nodeType === ELEMENT;
  }

  function isScript(node) {
    return isElement(node) && String(node.localName || node.nodeName).toLowerCase() === "script";
  }

  function keyOf(node) {
    if (!isElement(node)) return "";
    var key = node.getAttribute("data-kit-key");
    if (key !== null && String(key).trim() !== "") return "key:" + String(key);
    if (node.id) return "id:" + String(node.id);
    return "";
  }

  function compatible(current, incoming) {
    if (!current || !incoming || current.nodeType !== incoming.nodeType) return false;
    if (current.nodeType !== ELEMENT) return true;
    return current.namespaceURI === incoming.namespaceURI &&
      String(current.localName || current.nodeName).toLowerCase() ===
      String(incoming.localName || incoming.nodeName).toLowerCase();
  }

  function ignored(node) {
    return isElement(node) && node.hasAttribute("data-kit-ignore");
  }

  function lifecycleAttribute(name) {
    name = String(name || "").toLowerCase();
    return name.indexOf("data-kit-") === 0 && !NAVIGATION_ATTRIBUTES[name];
  }

  function lifecycleSignature(element) {
    var values = [];
    array(element.attributes).forEach(function (attribute) {
      var name = String(attribute.name || "").toLowerCase();
      if (lifecycleAttribute(name)) values.push(name + "\u0000" + attribute.value);
    });
    values.sort();
    return values.join("\u0001");
  }

  function requiresReplacement(current, incoming) {
    if (!compatible(current, incoming)) return true;
    if (!isElement(current)) return false;
    if (ignored(current) !== ignored(incoming)) return true;
    return lifecycleSignature(current) !== lifecycleSignature(incoming);
  }

  function unsafeAttribute(attribute) {
    var name = String(attribute.name || "").toLowerCase();
    if (name.indexOf("on") === 0) return true;
    return name === "srcdoc";
  }

  function sanitizeElement(element) {
    array(element.attributes).forEach(function (attribute) {
      if (unsafeAttribute(attribute)) element.removeAttribute(attribute.name);
    });

    // template.content is outside the template element's ordinary child list.
    if (element.localName === "template" && element.content) sanitizeChildren(element.content);
    sanitizeChildren(element);
  }

  function sanitizeChildren(parent) {
    array(parent.childNodes).forEach(function (child) {
      if (isScript(child)) {
        parent.removeChild(child);
        return;
      }
      if (isElement(child)) sanitizeElement(child);
    });
  }

  function safeClone(node) {
    if (isScript(node)) return null;
    var clone = document.importNode ? document.importNode(node, true) : node.cloneNode(true);
    if (isElement(clone)) sanitizeElement(clone);
    return clone;
  }

  function dispose(node) {
    if (node && node.nodeType === ELEMENT) disposeTree(node);
  }

  function removeNode(node) {
    if (!node || !node.parentNode) return;
    dispose(node);
    node.parentNode.removeChild(node);
  }

  function replaceNode(current, incoming) {
    var parent = current.parentNode;
    if (!parent) return current;
    var replacement = safeClone(incoming);
    dispose(current);
    if (replacement) parent.replaceChild(replacement, current);
    else parent.removeChild(current);
    return replacement;
  }

  function patchAttributes(current, incoming) {
    array(current.attributes).forEach(function (attribute) {
      var name = attribute.name;
      if (!incoming.hasAttribute(name) || unsafeAttribute(attribute)) current.removeAttribute(name);
    });
    array(incoming.attributes).forEach(function (attribute) {
      if (unsafeAttribute(attribute)) {
        current.removeAttribute(attribute.name);
        return;
      }
      if (current.getAttribute(attribute.name) !== attribute.value) {
        current.setAttribute(attribute.name, attribute.value);
      }
    });
  }

  function morphNode(current, incoming) {
    if (requiresReplacement(current, incoming)) return replaceNode(current, incoming);

    if (current.nodeType === TEXT || current.nodeType === COMMENT) {
      if (current.nodeValue !== incoming.nodeValue) current.nodeValue = incoming.nodeValue;
      return current;
    }
    if (!isElement(current)) return current;

    // Ignored islands are deliberately opaque: preserve their live DOM exactly.
    if (ignored(current) && ignored(incoming)) return current;

    patchAttributes(current, incoming);
    morphChildren(current, incoming);
    return current;
  }

  function uniqueKeyMap(children) {
    var map = new Map();
    var duplicates = Object.create(null);
    children.forEach(function (child) {
      var key = keyOf(child);
      if (!key) return;
      if (map.has(key)) {
        map.delete(key);
        duplicates[key] = true;
      } else if (!duplicates[key]) {
        map.set(key, child);
      }
    });
    return map;
  }

  function morphChildren(current, incoming) {
    var oldChildren = array(current.childNodes);
    var incomingChildren = array(incoming.childNodes);
    var keyed = uniqueKeyMap(oldChildren);
    var used = new Set();
    var cursor = current.firstChild;

    incomingChildren.forEach(function (nextChild) {
      // Parsed response scripts never enter the live document. Same-plan runtime
      // assets have already been loaded by the current page.
      if (isScript(nextChild)) return;

      var nextKey = keyOf(nextChild);
      var candidate = nextKey ? keyed.get(nextKey) : null;

      if (candidate && (used.has(candidate) || !compatible(candidate, nextChild))) candidate = null;
      if (!candidate && !nextKey) {
        for (var index = 0; index < oldChildren.length; index++) {
          var possible = oldChildren[index];
          if (used.has(possible) || keyOf(possible) || !compatible(possible, nextChild)) continue;
          candidate = possible;
          break;
        }
      }

      if (candidate) {
        used.add(candidate);
        if (candidate !== cursor) current.insertBefore(candidate, cursor);
        var result = morphNode(candidate, nextChild);
        cursor = result ? result.nextSibling : cursor;
        return;
      }

      var clone = safeClone(nextChild);
      if (!clone) return;
      current.insertBefore(clone, cursor);
      cursor = clone.nextSibling;
    });

    oldChildren.forEach(function (child) {
      if (!used.has(child) && child.parentNode === current) removeNode(child);
    });
  }

  function controlIdentity(element, root) {
    var key = keyOf(element);
    if (key) return { kind: "key", value: key };
    var name = element.getAttribute && element.getAttribute("name");
    if (name) {
      return {
        kind: "name",
        value: String(element.localName || "") + "\u0000" + String(element.type || "") + "\u0000" + String(name),
        path: nodePath(element, root)
      };
    }
    return { kind: "path", value: nodePath(element, root) };
  }

  function nodePath(node, root) {
    var path = [];
    while (node && node !== root) {
      var parent = node.parentNode;
      if (!parent) return null;
      path.unshift(array(parent.childNodes).indexOf(node));
      node = parent;
    }
    return node === root ? path : null;
  }

  function nodeAtPath(root, path) {
    if (!path) return null;
    var node = root;
    for (var index = 0; index < path.length; index++) {
      if (!node || !node.childNodes || !node.childNodes[path[index]]) return null;
      node = node.childNodes[path[index]];
    }
    return node;
  }

  function findIdentity(root, identity) {
    if (!identity) return null;
    var candidates;
    if (identity.kind === "key") {
      candidates = [root].concat(root.querySelectorAll ? array(root.querySelectorAll("[data-kit-key],[id]")) : []);
      for (var keyedIndex = 0; keyedIndex < candidates.length; keyedIndex++) {
        if (keyOf(candidates[keyedIndex]) === identity.value) return candidates[keyedIndex];
      }
      return null;
    }
    if (identity.kind === "name") {
      candidates = root.querySelectorAll ? array(root.querySelectorAll("input[name],textarea[name],select[name],button[name]")) : [];
      for (var nameIndex = 0; nameIndex < candidates.length; nameIndex++) {
        var candidate = candidates[nameIndex];
        var value = String(candidate.localName || "") + "\u0000" + String(candidate.type || "") + "\u0000" + String(candidate.getAttribute("name"));
        if (value === identity.value) {
          // Repeated names (notably radios) use their structural path as a tie-breaker.
          var byPath = nodeAtPath(root, identity.path);
          if (byPath && isElement(byPath) && byPath.getAttribute("name") === candidate.getAttribute("name")) return byPath;
          return candidate;
        }
      }
    }
    return nodeAtPath(root, identity.value || identity.path);
  }

  function isDirty(control) {
    var name = String(control.localName || "").toLowerCase();
    var type = String(control.type || "").toLowerCase();
    if (name === "select") {
      return array(control.options).some(function (option) { return option.selected !== option.defaultSelected; });
    }
    if (type === "checkbox" || type === "radio") return control.checked !== control.defaultChecked;
    return control.value !== control.defaultValue;
  }

  function captureControl(control, root, active) {
    var name = String(control.localName || "").toLowerCase();
    var type = String(control.type || "").toLowerCase();
    var state = {
      node: control,
      identity: controlIdentity(control, root),
      name: name,
      type: type,
      active: control === active,
      scrollTop: control.scrollTop,
      scrollLeft: control.scrollLeft
    };
    if (name === "select") {
      state.values = array(control.options).filter(function (option) { return option.selected; }).map(function (option) { return option.value; });
    } else if (type === "checkbox" || type === "radio") {
      state.checked = control.checked;
    } else if (type !== "file") {
      state.value = control.value;
    }
    if (state.active && typeof control.selectionStart === "number") {
      state.selectionStart = control.selectionStart;
      state.selectionEnd = control.selectionEnd;
      state.selectionDirection = control.selectionDirection;
    }
    return state;
  }

  function captureState(root) {
    var active = document.activeElement;
    var focus = active && (active === root || (root.contains && root.contains(active))) ? {
      node: active,
      identity: controlIdentity(active, root)
    } : null;
    var controls = root.querySelectorAll ? array(root.querySelectorAll("input,textarea,select")) : [];
    if (isElement(root) && /^(input|textarea|select)$/i.test(root.localName || "")) controls.unshift(root);
    return {
      focus: focus,
      controls: controls.filter(function (control) {
        return control === active || control.hasAttribute("data-kit-model") || isDirty(control);
      }).map(function (control) { return captureControl(control, root, active); })
    };
  }

  function restoreControl(control, state) {
    if (!control) return;
    if (state.name === "select" && control.options) {
      var remaining = state.values.slice();
      array(control.options).forEach(function (option) {
        var index = remaining.indexOf(option.value);
        option.selected = index !== -1;
        if (index !== -1) remaining.splice(index, 1);
      });
    } else if (state.type === "checkbox" || state.type === "radio") {
      control.checked = state.checked;
    } else if (state.type !== "file" && state.value !== undefined) {
      control.value = state.value;
    }
    if (typeof state.scrollTop === "number") control.scrollTop = state.scrollTop;
    if (typeof state.scrollLeft === "number") control.scrollLeft = state.scrollLeft;
  }

  function restoreState(root, snapshot) {
    var activeControl = null;
    snapshot.controls.forEach(function (state) {
      var control = state.node && state.node.isConnected && (state.node === root || root.contains(state.node))
        ? state.node
        : findIdentity(root, state.identity);
      restoreControl(control, state);
      if (state.active) activeControl = { node: control, state: state };
    });

    var focus = activeControl && activeControl.node;
    if (!focus && snapshot.focus) {
      focus = snapshot.focus.node && snapshot.focus.node.isConnected &&
        (snapshot.focus.node === root || root.contains(snapshot.focus.node))
        ? snapshot.focus.node
        : findIdentity(root, snapshot.focus.identity);
    }
    if (!focus || typeof focus.focus !== "function") return;
    try { focus.focus({ preventScroll: true }); } catch (_) { focus.focus(); }
    if (activeControl && typeof activeControl.state.selectionStart === "number" && typeof focus.setSelectionRange === "function") {
      try {
        focus.setSelectionRange(
          activeControl.state.selectionStart,
          activeControl.state.selectionEnd,
          activeControl.state.selectionDirection || "none"
        );
      } catch (_) { /* Unsupported input type. */ }
    }
  }

  function reconcile(root) {
    hydrate([root]);
    refreshRefs();
    schedule();
  }

  function morph(current, incoming) {
    if (!current || !incoming) throw new TypeError("KitJS morph requires current and incoming roots");
    var snapshot = captureState(current);
    var result = morphNode(current, incoming);
    if (!result) throw new Error("KitJS morph cannot remove its root");
    reconcile(result);
    restoreState(result, snapshot);
    return result;
  }

  core.morph = morph;

})(window, document);
