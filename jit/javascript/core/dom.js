// KitJS directive compilation, live binding records and DOM diffing.
(function (window) {
  "use strict";

  var kit = window.kit;
  var core = kit && kit.__kitwork_core__;
  if (!core) throw new Error("KitJS core/global.js must be loaded before core/dom.js");
  if (core.reuse) return;
  if (core.phase !== "component") throw new Error("KitJS core fragment order error before core/dom.js");

  var runtime = core.runtime;
  var state = core.state;
  var warn = core.warn;
  var report = core.report;
  var UNSET = core.UNSET;
  var ASYNC_BINDING = core.ASYNC_BINDING;
  var EVENT_TYPES = core.EVENT_TYPES;
  var HTML_BOOLEAN = core.HTML_BOOLEAN;
  var BIND_DENY = core.BIND_DENY;
  var URL_ATTRIBUTES = core.URL_ATTRIBUTES;
  var compile = core.compile;
  var evaluate = core.evaluate;
  var splitTop = core.splitTop;
  var parseMap = core.parseMap;
  var classMap = core.classMap;
  var isElement = core.isElement;
  var inRoot = core.inRoot;
  var walk = core.walk;
  var insideInactiveComponent = core.insideInactiveComponent;
  var createComponent = core.createComponent;
  var seedScope = core.seedScope;
  var refreshRefs = core.refreshRefs;
  var resolverFor = core.resolverFor;
  var enqueue = core.enqueue;

  function ensureListener(eventType) {
    if (typeof core.ensureListener !== "function") throw new Error("KitJS core/lifecycle.js is not loaded");
    return core.ensureListener(eventType);
  }
  function flushInits() {
    if (typeof core.flushInits !== "function") throw new Error("KitJS core/lifecycle.js is not loaded");
    return core.flushInits();
  }
  function isThenable(value) {
    return typeof core.isThenable === "function" && core.isThenable(value);
  }

  function addBinding(element, attribute, kind, ast, key) {
    var elementState = state(element);
    elementState.compiled = elementState.compiled || Object.create(null);
    var signature = attribute + "\u0000" + (key || "");
    if (elementState.compiled[signature]) return;
    elementState.compiled[signature] = true;
    runtime.bindings.push({
      element: element,
      attribute: attribute,
      kind: kind,
      ast: ast,
      key: key || "",
      last: UNSET
    });
  }

  function compileProgram(source) {
    var program = [];
    splitTop(source, ";").forEach(function (statement) {
      statement = statement.trim();
      if (statement) program.push(compile(statement, "action"));
    });
    return program;
  }

  function addAction(element, attribute, source, eventType, modifiers) {
    var elementState = state(element);
    elementState.compiled = elementState.compiled || Object.create(null);
    var signature = attribute + "\u0000action";
    if (elementState.compiled[signature]) return;
    elementState.compiled[signature] = true;
    var action = {
      element: element,
      source: source,
      eventType: eventType,
      modifiers: modifiers,
      program: compileProgram(source),
      once: false,
      timer: null,
      pending: 0
    };
    var actions = runtime.actionsByElement.get(element);
    if (!actions) {
      actions = [];
      runtime.actionsByElement.set(element, actions);
      elementState.actions = actions;
    }
    actions.push(action);
    if (modifiers.indexOf("outside") !== -1) {
      (runtime.outsideActions[eventType] || (runtime.outsideActions[eventType] = [])).push(action);
    }
    ensureListener(eventType);
  }

  function supportedEventModifier(modifier) {
    if (modifier === "self" || modifier === "enter" || modifier === "escape" ||
        modifier === "prevent" || modifier === "stop" || modifier === "once" ||
        modifier === "outside" || modifier === "debounce") return true;
    if (modifier.indexOf("debounce(") !== 0 || modifier.charAt(modifier.length - 1) !== ")") return false;
    var delay = modifier.slice(9, -1);
    return /^[0-9]+$/.test(delay) && /[1-9]/.test(delay);
  }

  function metadataDirective(directive) {
    return directive === "scope" || directive === "component" || directive === "version" ||
      directive === "as" || directive === "ref" || directive === "app" ||
      directive === "hydrate" || directive === "drive" || directive === "ignore" ||
      directive === "key";
  }

  function rejectAttribute(element, attribute, detail) {
    var elementState = state(element);
    elementState.compiled = elementState.compiled || Object.create(null);
    var signature = attribute.name + "\u0000unsupported";
    if (elementState.compiled[signature]) return;
    elementState.compiled[signature] = true;
    var error = new Error("Unsupported KitJS attribute " + attribute.name + ": " + detail);
    error.code = "KIT_UNSUPPORTED_ATTRIBUTE";
    report(error, {
      element: element,
      directive: attribute.name,
      code: error.code
    });
  }

  function compileElement(element) {
    for (var index = 0; index < element.attributes.length; index++) {
      var attribute = element.attributes[index];
      if (attribute.name.indexOf("data-kit-") !== 0) continue;
      var directive = attribute.name.substring(9);
      var source = attribute.value;
      if (directive === "text" || directive === "show") {
        addBinding(element, attribute.name, directive, compile(source, "binding"));
      } else if (directive === "class") {
        if (classMap(source)) {
          var classEntries = parseMap(source).map(function (entry) {
            return { key: entry.key, ast: compile(entry.expression, "binding") };
          });
          addBinding(element, attribute.name, "class-map", classEntries);
        } else addBinding(element, attribute.name, "class", compile(source, "binding"));
      } else if (directive === "bind" || directive === "style") {
        parseMap(source).forEach(function (entry) {
          addBinding(element, attribute.name, directive, compile(entry.expression, "binding"), entry.key);
        });
      } else if (directive === "model") {
        var path = compile(source, "binding");
        if (path.type === "identifier" || path.type === "member") {
          addBinding(element, attribute.name, directive, path);
          state(element).model = path;
          ensureListener("input");
          ensureListener("change");
        } else warn("KIT_MODEL_NOT_WRITABLE", source);
      } else if (directive === "cloak") {
        addBinding(element, attribute.name, directive, null);
      } else {
        var parts = directive.split(":");
        var eventType = parts.shift();
        if (EVENT_TYPES[eventType]) {
          var invalidModifier = "";
          for (var modifierIndex = 0; modifierIndex < parts.length; modifierIndex++) {
            if (!supportedEventModifier(parts[modifierIndex])) {
              invalidModifier = parts[modifierIndex];
              break;
            }
          }
          if (invalidModifier) rejectAttribute(element, attribute, "unsupported event modifier " + invalidModifier);
          else addAction(element, attribute.name, source, eventType, parts);
        } else if (!metadataDirective(directive)) {
          rejectAttribute(element, attribute, "reserved data-kit-* name is not implemented");
        }
      }
    }
  }

  function hydrate(roots) {
    if (!runtime.started || !roots.length) return;
    var elements = [];
    var seen = new WeakSet();
    roots.forEach(function (root) {
      if (isElement(root) && inRoot(root)) walk(root, function (element) { elements.push(element); }, seen);
    });

    // Every component and alias exists before an outer binding is compiled.
    elements.forEach(function (element) {
      var spec = element.getAttribute("data-kit-component");
      if (spec !== null && !insideInactiveComponent(element, true)) {
        if (element.hasAttribute("data-kit-scope")) {
          state(element).componentInactive = true;
          warn("KIT_SCOPE_COMPONENT_CONFLICT", "data-kit-scope cannot be used on a component host");
        } else createComponent(element, spec);
      } else if (spec === null && element.hasAttribute("data-kit-version")) {
        warn("KIT_COMPONENT_VERSION_ORPHAN", "data-kit-version requires data-kit-component on the same element");
      }
    });
    elements.forEach(function (element) {
      if (!insideInactiveComponent(element, false)) seedScope(element);
    });
    refreshRefs();
    elements.forEach(function (element) {
      if (!insideInactiveComponent(element, false)) compileElement(element);
    });
    flushBindings();
    flushInits();
  }

  function scalar(value) {
    if (value === null || value === undefined) return "";
    if (typeof value === "string" || typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") return String(value);
    return null;
  }

  function safeAttribute(element, key, value) {
    key = String(key || "").toLowerCase();
    if (!key || key.indexOf("on") === 0 || BIND_DENY[key] || key.indexOf("data-kit") === 0) {
      warn("KIT_BIND_UNSAFE_ATTRIBUTE", key);
      return;
    }
    if (URL_ATTRIBUTES[key] && typeof value === "string") {
      var normalized = value.replace(/[\s\u0000-\u001f]/g, "").toLowerCase();
      if (normalized.indexOf("javascript:") === 0 || normalized.indexOf("vbscript:") === 0 || normalized.indexOf("data:text/html") === 0) {
        warn("KIT_BIND_UNSAFE_ATTRIBUTE", key);
        return;
      }
    }
    var dataOrAria = key.indexOf("data-") === 0 || key.indexOf("aria-") === 0;
    if (value === null || value === undefined) { element.removeAttribute(key); return; }
    if (value === false) {
      if (dataOrAria) element.setAttribute(key, "false");
      else element.removeAttribute(key);
      return;
    }
    if (value === true) {
      if (dataOrAria) element.setAttribute(key, "true");
      else if (HTML_BOOLEAN[key]) element.setAttribute(key, "");
      else element.setAttribute(key, "true");
      return;
    }
    element.setAttribute(key, String(value));
  }

  function collectClasses(value, output) {
    if (value === null || value === undefined || value === false) return;
    if (typeof value === "string") {
      value.split(/\s+/).forEach(function (name) { if (name) output[name] = true; });
    } else if (Array.isArray(value)) {
      value.forEach(function (item) { collectClasses(item, output); });
    } else if (typeof value === "object") {
      Object.keys(value).forEach(function (name) { if (value[name]) output[name] = true; });
    }
  }

  function classNames(record, value, expressionResolver) {
    var output = Object.create(null);
    var promises = [];
    if (record.kind === "class-map") {
      record.ast.forEach(function (entry) {
        var condition = evaluate(entry.ast, expressionResolver);
        if (isThenable(condition)) {
          if (promises.indexOf(condition) === -1) promises.push(condition);
        } else if (condition) collectClasses(entry.key, output);
      });
    } else collectClasses(value, output);
    if (promises.length) return { marker: ASYNC_BINDING, promises: promises };
    return Object.keys(output).sort();
  }

  function writeClasses(record, names) {
    var elementState = state(record.element);
    var next = Object.create(null);
    names.forEach(function (name) { next[name] = true; });
    if (!elementState.staticClasses) {
      elementState.staticClasses = Object.create(null);
      for (var staticIndex = 0; staticIndex < record.element.classList.length; staticIndex++) {
        var staticName = record.element.classList.item(staticIndex);
        if (!next[staticName]) elementState.staticClasses[staticName] = true;
      }
    }
    var previous = elementState.dynamicClasses || Object.create(null);
    Object.keys(previous).forEach(function (name) {
      if (!next[name] && !elementState.staticClasses[name]) record.element.classList.remove(name);
    });
    Object.keys(next).forEach(function (name) { if (!previous[name]) record.element.classList.add(name); });
    elementState.dynamicClasses = next;
  }

  function readControl(element) {
    var type = (element.type || "").toLowerCase();
    if (element.tagName === "SELECT" && element.multiple) {
      var selected = [];
      for (var index = 0; index < element.options.length; index++) {
        if (element.options[index].selected) selected.push(element.options[index].value);
      }
      return selected;
    }
    if (type === "checkbox") return !!element.checked;
    if (type === "number" || type === "range") {
      var number = parseFloat(element.value);
      return element.value === "" || isNaN(number) ? null : number;
    }
    return element.value;
  }

  function writeControl(element, value) {
    var type = (element.type || "").toLowerCase();
    if (type === "checkbox") { element.checked = !!value; return; }
    if (type === "radio") { element.checked = element.value === String(value); return; }
    var text = value === null || value === undefined ? "" : String(value);
    if (element.value !== text) element.value = text;
  }

  function readBinding(record, expressionResolver) {
    if (record.kind === "cloak") return false;
    if (record.kind === "class-map") {
      var mappedClasses = classNames(record, undefined, expressionResolver);
      return mappedClasses.marker === ASYNC_BINDING ? mappedClasses : mappedClasses.join("\u0000");
    }
    var value = evaluate(record.ast, expressionResolver);
    if (isThenable(value)) return { marker: ASYNC_BINDING, promises: [value] };
    if (record.kind === "show") return !!value;
    if (record.kind === "class") return classNames(record, value, expressionResolver).join("\u0000");
    return value;
  }

  function sameAsyncBinding(previous, next) {
    if (!previous || previous.marker !== ASYNC_BINDING || previous.promises.length !== next.promises.length) return false;
    for (var index = 0; index < next.promises.length; index++) {
      if (previous.promises[index] !== next.promises[index]) return false;
    }
    return true;
  }

  function writeBinding(record, value) {
    var element = record.element;
    if (record.kind === "text") {
      var text = scalar(value);
      if (text === null) { warn("KIT_TEXT_NON_SCALAR", record.attribute); return; }
      if (element.textContent !== text) element.textContent = text;
    } else if (record.kind === "show") element.hidden = !value;
    else if (record.kind === "bind") safeAttribute(element, record.key, value);
    else if (record.kind === "style") {
      if (value === null || value === undefined || value === false) element.style.removeProperty(record.key);
      else element.style.setProperty(record.key, String(value));
    } else if (record.kind === "class" || record.kind === "class-map") {
      writeClasses(record, value ? value.split("\u0000") : []);
    } else if (record.kind === "model") writeControl(element, value);
    else if (record.kind === "cloak") element.removeAttribute("data-kit-cloak");
  }

  function flushBindings() {
    if (!runtime.started || runtime.rendering) return;
    runtime.rendering = true;
    try {
      var live = [];
      runtime.bindings.forEach(function (record) {
        if (!inRoot(record.element)) return;
        live.push(record);
        try {
          var value = readBinding(record, resolverFor(record.element, {}));
          if (value && value.marker === ASYNC_BINDING) {
            if (!sameAsyncBinding(record.last, value)) {
              record.last = value;
              warn("KIT_ASYNC_BINDING", record.attribute);
              value.promises.forEach(function (promise) {
                Promise.resolve(promise).catch(function (error) {
                  if (!(error && error.name === "AbortError")) {
                    report(error, { element: record.element, directive: record.attribute });
                  }
                });
              });
            }
            return;
          }
          if (record.last !== UNSET && Object.is(record.last, value)) return;
          record.last = value;
          writeBinding(record, value);
        } catch (error) {
          report(error, { element: record.element, directive: record.attribute });
        }
      });
      runtime.bindings = live;
    } finally {
      runtime.rendering = false;
    }
  }

  function schedule() {
    if (!runtime.started || runtime.scheduled) return;
    runtime.scheduled = true;
    enqueue(function () {
      runtime.scheduled = false;
      flushBindings();
    });
  }

  core.addBinding = addBinding;
  core.compileProgram = compileProgram;
  core.addAction = addAction;
  core.supportedEventModifier = supportedEventModifier;
  core.metadataDirective = metadataDirective;
  core.rejectAttribute = rejectAttribute;
  core.compileElement = compileElement;
  core.hydrate = hydrate;
  core.scalar = scalar;
  core.safeAttribute = safeAttribute;
  core.collectClasses = collectClasses;
  core.classNames = classNames;
  core.writeClasses = writeClasses;
  core.readControl = readControl;
  core.writeControl = writeControl;
  core.readBinding = readBinding;
  core.sameAsyncBinding = sameAsyncBinding;
  core.writeBinding = writeBinding;
  core.flushBindings = flushBindings;
  core.schedule = schedule;
  core.phase = "dom";

})(window);
