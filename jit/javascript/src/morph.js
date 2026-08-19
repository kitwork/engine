;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "events") throw new Error("KitJS: Morph loaded out of order");
  if (core.reuse) { core.phase = "morph"; return; }

  var ELEMENT = 1;
  var TEXT = 3;
  var COMMENT = 8;
  var RETAIN = "data-kit-retain";
  var IGNORE = "data-kit-ignore";
  var COMPONENT_METADATA = {
    "data-kit-component": true,
    "data-kit-version": true,
    "data-kit-local": true
  };
  var URL_ATTRIBUTES = {
    action: true,
    formaction: true,
    href: true,
    poster: true,
    src: true,
    "xlink:href": true
  };
  var ACTIVE_ELEMENTS = {
    applet: true,
    embed: true,
    fencedframe: true,
    frame: true,
    iframe: true,
    object: true,
    portal: true
  };

  function unsafeURL(value) {
    var text = String(value || "").replace(/[\u0000-\u0020]+/g, "").toLowerCase();
    return text.indexOf("javascript:") === 0 || text.indexOf("vbscript:") === 0 ||
      text.indexOf("data:text/html") === 0;
  }

  function sanitizeAttributes(element) {
    element.getAttributeNames().forEach(function (name) {
      var lower = name.toLowerCase();
      if (lower.indexOf("on") === 0 || lower === "srcdoc" ||
        URL_ATTRIBUTES[lower] && unsafeURL(element.getAttribute(name))) {
        element.removeAttribute(name);
      }
    });
  }

  function activeContent(element) {
    var name = String(element.localName || "").toLowerCase();
    if (ACTIVE_ELEMENTS[name]) return true;
    return name === "meta" &&
      String(element.getAttribute("http-equiv") || "").trim().toLowerCase() === "refresh";
  }

  function sanitizeContainer(container) {
    Array.prototype.slice.call(container.childNodes).forEach(function (node) {
      if (node.nodeType !== ELEMENT) return;
      if ((node.localName && node.localName.toLowerCase() === "script") || activeContent(node)) {
        container.removeChild(node);
        return;
      }
      sanitizeAttributes(node);
      if (node.localName && node.localName.toLowerCase() === "template" && node.content) {
        sanitizeContainer(node.content);
      }
      sanitizeContainer(node);
    });
  }

  function sanitizedClone(element) {
    var clone = element.cloneNode(true);
    if ((clone.localName && clone.localName.toLowerCase() === "script") || activeContent(clone)) {
      throw new TypeError("KitJS: Morph root cannot contain active document content");
    }
    sanitizeAttributes(clone);
    if (clone.localName && clone.localName.toLowerCase() === "template" && clone.content) {
      sanitizeContainer(clone.content);
    }
    sanitizeContainer(clone);
    return clone;
  }

  function directiveIdentity(element, normalizeComponent) {
    var attributes = [];
    element.getAttributeNames().forEach(function (name) {
      var lower = name.toLowerCase();
      if (lower.indexOf("data-kit-") !== 0 || normalizeComponent && COMPONENT_METADATA[lower]) return;
      attributes.push(lower + "\u0000" + element.getAttribute(name));
    });
    attributes.sort();
    return attributes.join("\u0001");
  }

  function retainKey(element) {
    if (!element || element.nodeType !== ELEMENT || element.hasAttribute(IGNORE) ||
      !element.hasAttribute(RETAIN)) return "";
    return element.getAttribute(RETAIN);
  }

  function parentElement(node) {
    var parent = node && node.parentNode;
    return parent && parent.nodeType === ELEMENT ? parent : null;
  }

  function canonicalAlias(element) {
    return element.hasAttribute("data-kit-as") ? element.getAttribute("data-kit-as") : null;
  }

  function retainCompatible(currentEntry, incomingEntry) {
    var current = currentEntry.element;
    var incoming = incomingEntry.element;
    var mounted = currentEntry.mounted;
    return !currentEntry.blocked && (!mounted || mounted.name === currentEntry.request.name &&
        mounted.version === currentEntry.request.version && mounted.lane === currentEntry.request.lane &&
        mounted.alias === canonicalAlias(current)) &&
      current.namespaceURI === incoming.namespaceURI && current.localName === incoming.localName &&
      currentEntry.request.name === incomingEntry.request.name &&
      currentEntry.request.version === incomingEntry.request.version &&
      currentEntry.request.lane === incomingEntry.request.lane &&
      canonicalAlias(current) === canonicalAlias(incoming);
  }

  function retainContext(currentRoot, incomingRoot) {
    if (typeof core.inspectRetains !== "function") {
      throw new Error("KitJS: incomplete retain metadata assembly");
    }
    var current = core.inspectRetains(currentRoot);
    var incoming = core.inspectRetains(incomingRoot);
    var context = {
      current: current,
      incoming: incoming,
      pairs: new Map(),
      incomingPairs: new Map(),
      protected: new Set(),
      used: new Set(),
      incomingAncestors: new WeakSet(),
      parking: document.createDocumentFragment()
    };
    incoming.forEach(function (incomingEntry, key) {
      var currentEntry = current.get(key);
      if (!currentEntry || !retainCompatible(currentEntry, incomingEntry)) return;
      context.pairs.set(currentEntry.element, incomingEntry.element);
      context.incomingPairs.set(incomingEntry.element, currentEntry.element);
      context.protected.add(currentEntry.element);
      var ancestor = parentElement(incomingEntry.element);
      while (ancestor) {
        context.incomingAncestors.add(ancestor);
        if (ancestor === incomingRoot) break;
        ancestor = parentElement(ancestor);
      }
    });
    return context;
  }

  function componentCompatible(current, incoming) {
    var currentHasComponent = current.hasAttribute("data-kit-component");
    var incomingHasComponent = incoming.hasAttribute("data-kit-component");
    var currentScope = current.getAttribute("data-kit-scope");
    var incomingScope = incoming.getAttribute("data-kit-scope");
    if (!currentHasComponent && !incomingHasComponent) {
      if (currentScope === null && incomingScope === null) return true;
      return currentScope !== null && incomingScope !== null && currentScope === incomingScope &&
        current.getAttribute("data-kit-as") === incoming.getAttribute("data-kit-as") &&
        current.getAttribute("data-kit-version") === incoming.getAttribute("data-kit-version");
    }
    if (!currentHasComponent || !incomingHasComponent || typeof core.componentMetadata !== "function") return false;
    var currentRequest = core.componentMetadata(current, false);
    var incomingRequest = core.componentMetadata(incoming, false);
    return !!currentRequest && !!incomingRequest &&
      currentRequest.name === incomingRequest.name &&
      currentRequest.version === incomingRequest.version &&
      currentRequest.lane === incomingRequest.lane &&
      current.getAttribute("data-kit-as") === incoming.getAttribute("data-kit-as") &&
      directiveIdentity(current, true) === directiveIdentity(incoming, true);
  }

  function compatible(current, incoming, context) {
    if (!current || !incoming || current.nodeType !== incoming.nodeType) return false;
    if (current.nodeType === TEXT || current.nodeType === COMMENT) return true;
    if (current.nodeType !== ELEMENT) return false;
    if (current.namespaceURI !== incoming.namespaceURI || current.localName !== incoming.localName) return false;
    var currentIgnored = current.hasAttribute(IGNORE);
    var incomingIgnored = incoming.hasAttribute(IGNORE);
    if (currentIgnored || incomingIgnored) return currentIgnored && incomingIgnored;
    if (context && context.pairs.get(current) === incoming) return true;
    if (context && (retainKey(current) || retainKey(incoming))) return false;
    if (!componentCompatible(current, incoming)) return false;
    if (current.localName === "input" &&
      (current.getAttribute("type") || "text").toLowerCase() !==
      (incoming.getAttribute("type") || "text").toLowerCase()) return false;
    if (current.localName === "input" &&
      (current.getAttribute("type") || "text").toLowerCase() === "file") return false;
    if ((current.localName === "input" || current.localName === "textarea" || current.localName === "select") &&
      !stableFormIdentity(current, incoming)) return false;
    return true;
  }

  function identity(node) {
    if (!node || node.nodeType !== ELEMENT) return "";
    var retained = retainKey(node);
    if (retained) return "retain\u0000" + retained;
    var id = node.getAttribute("id");
    return id ? "id\u0000" + id : "";
  }

  function resetElement(element) {
    if (core.disposeElementEvents) core.disposeElementEvents(element);
    core.records.delete(element);
  }

  function disposeNode(node) {
    if (!node || node.nodeType !== ELEMENT) return;
    if (core.disposeTree) core.disposeTree(node);
  }

  function stableFormIdentity(current, incoming) {
    var name = current.localName;
    if (name !== "input" && name !== "textarea" && name !== "select") return false;
    var id = current.getAttribute("id");
    return !!id && id === incoming.getAttribute("id");
  }

  function formState(element) {
    var name = element.localName;
    if (name === "input") {
      var type = (element.type || "text").toLowerCase();
      var checked = type === "checkbox" || type === "radio";
      if (checked && element.checked !== element.defaultChecked) {
        return { kind: "checked", checked: element.checked };
      }
      if (type !== "file" && element.value !== element.defaultValue) {
        return { kind: "value", value: element.value };
      }
      return null;
    }
    if (name === "textarea") {
      return element.value !== element.defaultValue ? { kind: "value", value: element.value } : null;
    }
    if (name !== "select") return null;
    var options = Array.prototype.slice.call(element.options);
    if (element.multiple) {
      var dirty = options.some(function (option) { return option.selected !== option.defaultSelected; });
      var values = options.filter(function (option) { return option.selected; }).map(function (option) {
        return option.value;
      });
      return dirty ? { kind: "selectedValues", values: values } : null;
    }
    var defaultIndex = -1;
    for (var index = 0; index < options.length; index++) {
      if (options[index].defaultSelected) { defaultIndex = index; break; }
    }
    if (defaultIndex < 0 && options.length) defaultIndex = 0;
    return element.selectedIndex !== defaultIndex ? {
      kind: "selectedValue",
      value: element.selectedIndex < 0 ? null : options[element.selectedIndex].value
    } : null;
  }

  function applyIncomingFormState(element, incoming) {
    var name = element.localName;
    if (name === "input") {
      var type = (element.type || "text").toLowerCase();
      if (type === "checkbox" || type === "radio") {
        element.checked = incoming.checked;
        element.indeterminate = incoming.indeterminate;
      }
      if (type !== "file") element.value = incoming.value;
      return;
    }
    if (name === "textarea") {
      element.value = incoming.value;
      return;
    }
    if (name !== "select") return;
    if (!element.multiple) {
      element.selectedIndex = incoming.selectedIndex;
      return;
    }
    Array.prototype.forEach.call(element.options, function (option, index) {
      option.selected = !!incoming.options[index] && incoming.options[index].selected;
    });
  }

  function restoreFormState(element, state) {
    if (!state) return;
    if (state.kind === "checked") element.checked = state.checked;
    else if (state.kind === "value") element.value = state.value;
    else if (state.kind === "selectedValue") {
      if (state.value === null) element.selectedIndex = -1;
      else Array.prototype.some.call(element.options, function (option, index) {
        if (option.value !== state.value) return false;
        element.selectedIndex = index;
        return true;
      });
    } else if (state.kind === "selectedValues") {
      var remaining = state.values.slice();
      Array.prototype.forEach.call(element.options, function (option) {
        var index = remaining.indexOf(option.value);
        option.selected = index >= 0;
        if (index >= 0) remaining.splice(index, 1);
      });
    }
  }

  function patchAttributes(current, incoming) {
    current.getAttributeNames().forEach(function (name) {
      if (!incoming.hasAttribute(name)) current.removeAttribute(name);
    });
    incoming.getAttributeNames().forEach(function (name) {
      var value = incoming.getAttribute(name);
      if (current.getAttribute(name) !== value) current.setAttribute(name, value);
    });
  }

  function uniqueIdentities(nodes) {
    var output = new Map();
    nodes.forEach(function (node) {
      var key = identity(node);
      if (!key) return;
      output.set(key, output.has(key) ? null : node);
    });
    return output;
  }

  function positionalMatch(cursor) {
    return cursor && !identity(cursor) ? cursor : null;
  }

  function parkRetainedDescendants(node, context) {
    if (!context || !node || node.nodeType !== ELEMENT || !node.querySelectorAll) return;
    Array.prototype.forEach.call(node.querySelectorAll("[data-kit-retain]"), function (element) {
      if (!context.protected.has(element) || context.used.has(element) || !node.contains(element)) return;
      context.parking.appendChild(element);
    });
  }

  function morphChildren(current, incoming, context) {
    var original = Array.prototype.slice.call(current.childNodes);
    var keyed = uniqueIdentities(original);
    var used = new Set();
    var cursor = current.firstChild;
    Array.prototype.slice.call(incoming.childNodes).forEach(function (incomingChild) {
      while (cursor && used.has(cursor)) cursor = cursor.nextSibling;
      var retained = retainKey(incomingChild);
      var key = identity(incomingChild);
      var match = retained && context ? context.incomingPairs.get(incomingChild) :
        key ? keyed.get(key) : positionalMatch(cursor);
      if (!match || used.has(match) || context && context.used.has(match) ||
        !compatible(match, incomingChild, context)) match = null;
      if (!match) {
        var shallow = incomingChild.nodeType === ELEMENT && context &&
          context.incomingAncestors.has(incomingChild);
        var inserted = incomingChild.cloneNode(!shallow);
        current.insertBefore(inserted, cursor);
        if (shallow) morphElement(inserted, incomingChild, context);
        return;
      }
      if (match !== cursor) current.insertBefore(match, cursor);
      used.add(match);
      if (context) context.used.add(match);
      morphNode(match, incomingChild, context);
      cursor = match.nextSibling;
    });
    original.forEach(function (node) {
      if (used.has(node) || node.parentNode !== current) return;
      if (context && context.protected.has(node) && !context.used.has(node)) return;
      parkRetainedDescendants(node, context);
      disposeNode(node);
      current.removeChild(node);
    });
  }

  function morphElement(current, incoming, context) {
    var state = stableFormIdentity(current, incoming) ? formState(current) : null;
    resetElement(current);
    patchAttributes(current, incoming);
    if (current.localName === "template" && current.content && incoming.content) {
      morphChildren(current.content, incoming.content, context);
    } else morphChildren(current, incoming, context);
    applyIncomingFormState(current, incoming);
    restoreFormState(current, state);
    return current;
  }

  function morphNode(current, incoming, context) {
    if (!compatible(current, incoming, context)) {
      var shallow = incoming.nodeType === ELEMENT && context &&
        context.incomingAncestors.has(incoming);
      var replacement = incoming.cloneNode(!shallow);
      if (shallow) parkRetainedDescendants(current, context);
      disposeNode(current);
      current.parentNode.replaceChild(replacement, current);
      if (shallow) morphElement(replacement, incoming, context);
      return replacement;
    }
    if (current.nodeType === ELEMENT && current.hasAttribute(IGNORE) && incoming.hasAttribute(IGNORE)) {
      return current;
    }
    if (current.nodeType === TEXT || current.nodeType === COMMENT) {
      if (current.data !== incoming.data) current.data = incoming.data;
      return current;
    }
    if (context && context.pairs.get(current) === incoming) context.used.add(current);
    return morphElement(current, incoming, context);
  }

  function focusState(root) {
    var active = document.activeElement;
    if (!active || active === document.body || active !== root && !root.contains(active)) return null;
    var selection = null;
    try {
      if (typeof active.selectionStart === "number") {
        selection = [active.selectionStart, active.selectionEnd, active.selectionDirection];
      }
    } catch (_) { selection = null; }
    return { element: active, id: active.id || "", selection: selection };
  }

  function findIdentity(root, state) {
    var output = null;
    if (state.id && root.querySelectorAll) {
      Array.prototype.some.call(root.querySelectorAll("[id]"), function (element) {
        if (element.id !== state.id) return false;
        output = element;
        return true;
      });
    }
    return output;
  }

  function invalidateBoundaries(root) {
    var records = core.liveComponents(root);
    var owner = core.scopeRecordFor(root);
    if (owner && records.indexOf(owner) < 0) records.unshift(owner);
    records.forEach(function (record) { core.invalidate(record); });
  }

  function restoreFocus(root, state) {
    if (!state) return;
    var element = state.element && state.element.isConnected ? state.element :
      findIdentity(root, state);
    if (!element || typeof element.focus !== "function") return;
    if (document.activeElement !== element) {
      try { element.focus({ preventScroll: true }); }
      catch (_) { element.focus(); }
    }
    if (state.selection && typeof element.setSelectionRange === "function") {
      try { element.setSelectionRange(state.selection[0], state.selection[1], state.selection[2]); }
      catch (_) { /* The replacement input type may reject selection. */ }
    }
  }

  function morph(currentRoot, incomingRoot) {
    if (!currentRoot || currentRoot.nodeType !== ELEMENT ||
      !incomingRoot || incomingRoot.nodeType !== ELEMENT) {
      throw new TypeError("KitJS: Morph expects two element roots");
    }
    var focus = focusState(currentRoot);
    var incoming = sanitizedClone(incomingRoot);
    if (core.validateScopeTree) core.validateScopeTree(incoming);
    if (core.validateComponentTree) core.validateComponentTree(incoming);
    var context = retainContext(currentRoot, incoming);
    if (core.resetStructures) core.resetStructures(currentRoot);
    var result = morphNode(currentRoot, incoming, context);
    if (core.prepareEventTree) core.prepareEventTree(result);
    restoreFocus(result, focus);
    invalidateBoundaries(result);
    return result;
  }

  core.validateMorphRetains = function (currentRoot, incomingRoot) {
    if (!currentRoot || currentRoot.nodeType !== ELEMENT ||
      !incomingRoot || incomingRoot.nodeType !== ELEMENT) {
      throw new TypeError("KitJS: Morph retain validation expects two element roots");
    }
    if (core.validateScopeTree) core.validateScopeTree(incomingRoot);
    if (core.validateComponentTree) core.validateComponentTree(incomingRoot);
    retainContext(currentRoot, incomingRoot);
    return true;
  };
  core.morph = morph;
  core.phase = "morph";
})(document);
