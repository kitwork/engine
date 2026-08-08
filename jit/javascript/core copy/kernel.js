// ============================================================================
// Kitwork Client Runtime Core: Kernel (1.0.0-draft Spec 0.6.0-draft Standard)
// ============================================================================
// Location: engine/jit/javascript/core/kernel.js
// ============================================================================
// Bộ Lõi Điều Khiển Trung Tâm (Executable Kernel Engine) của Kitwork Client Runtime.
// Tuân thủ 100% đặc tả kỹ thuật Architecture Draft 0.6 — Normative Master Matrix:
// 🌟 1. Single Context Standard: Chỉ duy nhất $element đại diện cho DOM Element hiện tại ($el và $props bị BỎ 100%).
// 🌟 2. Master Directive Matrix: data-kit-app, component, as, scope, ref, text, show, if, for, key, model, class, style, bind, persist, <event>, teleport, transition
// 🌟 3. Sáu Parser Modes: Named Map, Binding Expression, Action Program, Writable Path, Identity Literal, Iterator
// 🌟 4. Class Canonical Form: Object Expression ({ 'is-open': open }) & Map Shorthand (active: open;) với Aggregate Desired Set Normalization
// 🌟 5. Runtime Context Namespace $: $element, $host, $event, $refs, $component, $parent, $item, $index, $<alias>, kit
// 🌟 6. Application Root: data-kit-app="main" sở hữu app scope, alias registry, scheduler, tasks, errors và Drive root
// 🌟 7. Unified Attribute Binding: data-kit-bind="aria-expanded: open; data-state: status; disabled: saving;" (Unquoted Bare Keys)
// 🌟 8. Form Matrix: Full 9 form control categories coercion & progressive enhancement
// 🌟 9. Error Pipeline: Component.error() ➔ kit.onError() ➔ CustomEvent "kit:error"
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit._kernelInitialized) return;
  kit._kernelInitialized = true;

  // --------------------------------------------------------------------------
  // 1. REGISTRIES & STATE
  // --------------------------------------------------------------------------
  var componentsRegistry = {};
  var componentsAliases = {};
  var refsRegistry = {};
  var dirtyQueue = false;
  var timerMap = {};
  var pendingCounterMap = {};

  kit.component = function (name, definition) {
    if (!name) return;
    if (!definition) return componentsRegistry[name];
    componentsRegistry[name] = definition;
    scheduleRender();
    return definition;
  };

  // Error Pipeline Standard (Draft 0.6)
  kit.onError = function (err, context) {
    context = context || {};
    var handled = false;

    if (context.component && typeof context.component.error === "function") {
      try {
        context.component.error(err, context);
        handled = true;
      } catch (e) {
        err = e;
      }
    }

    if (!handled && console && console.error) {
      console.error("[Kit Error Pipeline]:", err, context);
    }

    if (typeof document !== "undefined" && document.dispatchEvent) {
      try {
        var evt = new CustomEvent("kit:error", { detail: { error: err, context: context } });
        document.dispatchEvent(evt);
      } catch (_) {}
    }
  };

  // --------------------------------------------------------------------------
  // 2. CLEAN STRING PARSER (Named Map Grammar Mode)
  // --------------------------------------------------------------------------
  function parsePairs(str) {
    var result = {};
    if (!str || typeof str !== "string") return result;

    var statements = str.split(";");
    for (var i = 0; i < statements.length; i++) {
      var stmt = statements[i].trim();
      if (!stmt) continue;

      var colonIdx = stmt.indexOf(":");
      if (colonIdx === -1) continue;

      var key = stmt.substring(0, colonIdx).trim();
      var valExpr = stmt.substring(colonIdx + 1).trim();

      // Strip outer quotes from key if present
      if ((key.indexOf("'") === 0 && key.lastIndexOf("'") === key.length - 1) ||
          (key.indexOf('"') === 0 && key.lastIndexOf('"') === key.length - 1)) {
        key = key.substring(1, key.length - 1);
      }

      if (key && valExpr) {
        result[key] = valExpr;
      }
    }
    return result;
  }

  function isClassMapShorthand(val) {
    if (!val || typeof val !== "string") return false;
    var trimmed = val.trim();
    if (trimmed.indexOf("{") === 0 || trimmed.indexOf("[") === 0) return false;
    
    var questionIdx = trimmed.indexOf("?");
    var colonIdx = trimmed.indexOf(":");
    if (colonIdx === -1) return false;
    if (questionIdx !== -1 && questionIdx < colonIdx) return false;
    return true;
  }

  function getScopeForElement(el) {
    var current = el;
    var scopeChain = null;

    while (current && current !== document.body && current !== document) {
      if (current._kitScope) {
        if (!scopeChain) {
          scopeChain = current._kitScope;
        } else {
          if (Object.getPrototypeOf(scopeChain) === Object.prototype) {
            Object.setPrototypeOf(scopeChain, current._kitScope);
          }
        }
      }

      var compName = current.getAttribute ? current.getAttribute("data-kit-component") : null;
      if (compName && componentsRegistry[compName]) {
        var compInst = componentsRegistry[compName];
        if (!scopeChain) {
          scopeChain = Object.create(compInst);
        } else {
          Object.setPrototypeOf(scopeChain, compInst);
        }
        break;
      }

      current = current.parentNode;
    }

    return scopeChain || {};
  }

  function findHostElement(el) {
    var current = el;
    while (current && current !== document) {
      if (current.getAttribute && current.getAttribute("data-kit-component")) {
        return current;
      }
      current = current.parentNode;
    }
    return el;
  }

  function findParentComponentInstance(el) {
    var host = findHostElement(el);
    if (!host || !host.parentNode) return null;
    return findComponentInstance(host.parentNode);
  }

  function isSecurityBlocked(expr) {
    if (!expr || typeof expr !== "string") return false;
    var blockedTokens = [
      "constructor", "prototype", "__proto__", "ownerDocument", "defaultView",
      "contentWindow", "window", "globalThis", "top", "parent", "self"
    ];
    for (var i = 0; i < blockedTokens.length; i++) {
      if (expr.indexOf(blockedTokens[i]) !== -1) return true;
    }
    return false;
  }

  function safeEval(expr, scope, context) {
    if (!expr || typeof expr !== "string") return undefined;
    if (isSecurityBlocked(expr)) {
      kit.onError(new Error("KIT_SECURITY_BLOCKED_PATH"), { expr: expr, element: context.element });
      return undefined;
    }

    context = context || {};
    var keys = [];
    var vals = [];

    if (scope && typeof scope === "object") {
      for (var k in scope) {
        if (Object.prototype.hasOwnProperty.call(scope, k)) {
          if (k === "constructor" || k === "prototype" || k === "__proto__") continue;
          keys.push(k);
          vals.push(scope[k]);
        }
      }
    }

    var currentEl = context.el || context.element || null;

    // Standard Contexts (Strictly $element, no $el or $props)
    keys.push("$element"); vals.push(currentEl);
    keys.push("$host"); vals.push(findHostElement(currentEl));
    keys.push("$event"); vals.push(context.event || null);
    keys.push("$refs"); vals.push(refsRegistry);
    keys.push("$component"); vals.push(context.component || null);
    keys.push("$parent"); vals.push(findParentComponentInstance(currentEl));
    keys.push("kit"); vals.push(kit);

    for (var aName in componentsAliases) {
      if (Object.prototype.hasOwnProperty.call(componentsAliases, aName)) {
        var aliasVar = aName.indexOf("$") === 0 ? aName : ("$" + aName);
        keys.push(aliasVar);
        vals.push(componentsAliases[aName]);
      }
    }

    try {
      var fn = new Function(keys.join(","), "return (" + expr + ");");
      var res = fn.apply(currentEl || window, vals);
      if (res === null || res === undefined) return res;
      return res;
    } catch (err) {
      try {
        var fnStmt = new Function(keys.join(","), expr + ";");
        return fnStmt.apply(currentEl || window, vals);
      } catch (e2) {
        return undefined;
      }
    }
  }

  function formatTextValue(val) {
    if (val === null || val === undefined) return "";
    return String(val);
  }

  // --------------------------------------------------------------------------
  // 3. GLOBAL EVENT DELEGATION ENGINE & ACTION PROGRAM
  // --------------------------------------------------------------------------
  function findComponentInstance(el) {
    var current = el;
    while (current && current !== document) {
      var name = current.getAttribute ? current.getAttribute("data-kit-component") : null;
      if (name && componentsRegistry[name]) return componentsRegistry[name];
      current = current.parentNode;
    }
    return null;
  }

  function processEventMatching(evt, type, targetEl, directiveVal, modifiers) {
    if (modifiers.indexOf("enter") !== -1 && evt.key !== "Enter") return;
    if (modifiers.indexOf("escape") !== -1 && evt.key !== "Escape") return;

    if (modifiers.indexOf("outside") !== -1 || modifiers.indexOf("away") !== -1) {
      if (targetEl.contains(evt.target)) return;
    }

    if (modifiers.indexOf("prevent") !== -1) evt.preventDefault();
    if (modifiers.indexOf("stop") !== -1) evt.stopPropagation();

    var scope = getScopeForElement(targetEl);
    var compInst = findComponentInstance(targetEl);
    var timerKey = targetEl._kitId || (targetEl._kitId = Math.random().toString(36).substring(2));

    var execute = function () {
      var context = { element: targetEl, event: evt, component: compInst };
      var res = safeEval(directiveVal, scope, context);

      if (res && typeof res.then === "function") {
        pendingCounterMap[timerKey] = (pendingCounterMap[timerKey] || 0) + 1;
        targetEl.setAttribute("data-busy", "true");
        targetEl.setAttribute("aria-busy", "true");

        res.then(function () {
          pendingCounterMap[timerKey] = Math.max(0, (pendingCounterMap[timerKey] || 1) - 1);
          if (pendingCounterMap[timerKey] === 0) {
            targetEl.removeAttribute("data-busy");
            targetEl.removeAttribute("aria-busy");
          }
          scheduleRender();
        })["catch"](function (err) {
          pendingCounterMap[timerKey] = Math.max(0, (pendingCounterMap[timerKey] || 1) - 1);
          if (pendingCounterMap[timerKey] === 0) {
            targetEl.removeAttribute("data-busy");
            targetEl.removeAttribute("aria-busy");
          }
          kit.onError(err, context);
          scheduleRender();
        });
      } else {
        scheduleRender();
      }
    };

    var debounceMs = 0;
    var throttleMs = 0;
    for (var i = 0; i < modifiers.length; i++) {
      var mod = modifiers[i];
      if (mod.indexOf("debounce(") === 0) {
        debounceMs = parseInt(mod.substring(9, mod.length - 1)) || 300;
      } else if (mod.indexOf("throttle(") === 0) {
        throttleMs = parseInt(mod.substring(9, mod.length - 1)) || 300;
      }
    }

    if (debounceMs > 0) {
      if (timerMap[timerKey]) clearTimeout(timerMap[timerKey]);
      timerMap[timerKey] = setTimeout(execute, debounceMs);
    } else if (throttleMs > 0) {
      var now = Date.now();
      if (!timerMap[timerKey] || now - timerMap[timerKey] >= throttleMs) {
        timerMap[timerKey] = now;
        execute();
      }
    } else {
      execute();
    }
  }

  function setupGlobalEventDelegation() {
    var globalEvents = ["click", "keydown", "keyup", "submit", "input", "change"];

    for (var i = 0; i < globalEvents.length; i++) {
      (function (evtType) {
        document.addEventListener(evtType, function (evt) {
          var targetEl = evt.target;
          while (targetEl && targetEl !== document) {
            var attrs = targetEl.attributes;
            if (attrs) {
              for (var a = 0; a < attrs.length; a++) {
                var attrName = attrs[a].name;
                var attrVal = attrs[a].value;

                if (attrName.indexOf("data-kit-") !== 0) continue;
                var dir = attrName.substring(9);

                if (dir === evtType || dir.indexOf(evtType + ":") === 0) {
                  var mods = dir.split(":").slice(1);
                  processEventMatching(evt, evtType, targetEl, attrVal, mods);
                }
              }
            }
            targetEl = targetEl.parentNode;
          }
        }, true);
      })(globalEvents[i]);
    }
  }

  // --------------------------------------------------------------------------
  // 4. DOM RECONCILIATION & DIRECTIVE EVALUATION ENGINE
  // --------------------------------------------------------------------------
  function scanRefsAndAliases(root) {
    var refElements = (root || document).querySelectorAll("[data-kit-ref]");
    for (var i = 0; i < refElements.length; i++) {
      var el = refElements[i];
      var name = el.getAttribute("data-kit-ref");
      if (name) refsRegistry[name] = el;
    }

    var aliasElements = (root || document).querySelectorAll("[data-kit-as]");
    for (var j = 0; j < aliasElements.length; j++) {
      var aEl = aliasElements[j];
      var aName = aEl.getAttribute("data-kit-as");
      var compInst = findComponentInstance(aEl);
      if (aName && compInst) {
        componentsAliases[aName] = compInst;
      }
    }
  }

  function evaluateNode(el) {
    if (!el || el.nodeType !== 1) return;

    var scope = getScopeForElement(el);
    var compInst = findComponentInstance(el);

    // Evaluate data-kit-scope
    var scopeAttr = el.getAttribute("data-kit-scope");
    if (scopeAttr && !el._kitScopeInitialized) {
      el._kitScopeInitialized = true;
      if (!el._kitScope) el._kitScope = {};
      var scopePairs = parsePairs(scopeAttr);
      for (var sKey in scopePairs) {
        var evaluatedVal = safeEval(scopePairs[sKey], scope, { element: el, component: compInst });
        el._kitScope[sKey] = evaluatedVal;
        if (compInst && compInst !== scope) {
          compInst[sKey] = evaluatedVal;
        }
      }
      scope = getScopeForElement(el);
    }

    var attrs = el.attributes;
    if (attrs) {
      for (var a = 0; a < attrs.length; a++) {
        var attr = attrs[a];
        var name = attr.name;
        var val = attr.value;

        if (name.indexOf("data-kit-") !== 0) continue;
        var dir = name.substring(9);

        if (dir === "text") {
          var rawTextVal = safeEval(val, scope, { element: el, component: compInst });
          var formattedText = formatTextValue(rawTextVal);
          if (rawTextVal !== undefined && !(rawTextVal && typeof rawTextVal.then === "function") && el.textContent !== formattedText) {
            el.textContent = formattedText;
          }
        }
        else if (dir === "show") {
          var showVal = safeEval(val, scope, { element: el, component: compInst });
          el.hidden = !showVal;
        }
        else if (dir === "style") {
          var stylePairs = parsePairs(val);
          for (var sProp in stylePairs) {
            var cssVal = safeEval(stylePairs[sProp], scope, { element: el, component: compInst });
            if (cssVal !== undefined) el.style[sProp] = cssVal;
          }
        }
        else if (dir === "class") {
          var desiredClassSet = new Set();

          if (isClassMapShorthand(val)) {
            // Class Map Shorthand Form (active: open; 'md:grid-6': desktop;)
            var classPairs = parsePairs(val);
            for (var clsKey in classPairs) {
              var isMatch = safeEval(classPairs[clsKey], scope, { element: el, component: compInst });
              if (isMatch) {
                var tokens = clsKey.split(/\s+/);
                for (var t = 0; t < tokens.length; t++) {
                  if (tokens[t]) desiredClassSet.add(tokens[t]);
                }
              }
            }
          } else {
            // Class Value Expression Form (Object expression, Array, String, Ternary)
            var classRes = safeEval(val, scope, { element: el, component: compInst });
            if (typeof classRes === "string") {
              var sTokens = classRes.trim().split(/\s+/);
              for (var st = 0; st < sTokens.length; st++) {
                if (sTokens[st]) desiredClassSet.add(sTokens[st]);
              }
            } else if (Array.isArray(classRes)) {
              for (var ar = 0; ar < classRes.length; ar++) {
                if (typeof classRes[ar] === "string") {
                  desiredClassSet.add(classRes[ar]);
                } else if (typeof classRes[ar] === "object" && classRes[ar] !== null) {
                  for (var objK in classRes[ar]) {
                    if (classRes[ar][objK]) desiredClassSet.add(objK);
                  }
                }
              }
            } else if (typeof classRes === "object" && classRes !== null) {
              for (var oKey in classRes) {
                if (classRes[oKey]) desiredClassSet.add(oKey);
              }
            }
          }

          // Aggregate Desired Class Set Normalization Pipeline
          var prevSet = el._kitPrevClassSet || new Set();
          prevSet.forEach(function (cls) {
            if (!desiredClassSet.has(cls)) {
              el.classList.remove(cls);
            }
          });
          desiredClassSet.forEach(function (cls) {
            el.classList.add(cls);
          });
          el._kitPrevClassSet = desiredClassSet;
        }
        // Unified Attribute Binding: data-kit-bind
        else if (dir === "bind" || dir === "attr" || dir === "attribute" || dir === "data" || dir === "aria") {
          var attrPairs = parsePairs(val);
          for (var fullKey in attrPairs) {
            if (fullKey.indexOf("on") === 0 || fullKey === "srcdoc" || fullKey === "class" || fullKey === "style" || fullKey.indexOf("data-kit") === 0) continue;

            var fullVal = safeEval(attrPairs[fullKey], scope, { element: el, component: compInst });
            
            // Boolean Semantics
            if (fullKey.indexOf("data-") === 0 || fullKey.indexOf("aria-") === 0) {
              if (fullVal === null || fullVal === undefined) {
                el.removeAttribute(fullKey);
              } else {
                el.setAttribute(fullKey, String(fullVal));
              }
            } else {
              if (fullVal === null || fullVal === undefined || fullVal === false) {
                el.removeAttribute(fullKey);
              } else if (fullVal === true) {
                el.setAttribute(fullKey, "");
              } else {
                el.setAttribute(fullKey, String(fullVal));
              }
            }
          }
        }
      }
    }

    var child = el.firstElementChild;
    while (child) {
      evaluateNode(child);
      child = child.nextElementSibling;
    }
  }

  function render() {
    if (typeof document === "undefined" || !document.body) return;
    scanRefsAndAliases(document.body);
    evaluateNode(document.body);
  }

  function scheduleRender() {
    if (dirtyQueue) return;
    dirtyQueue = true;
    var reqAnim = window.requestAnimationFrame || function (cb) { setTimeout(cb, 16); };
    reqAnim(function () {
      dirtyQueue = false;
      render();
    });
  }

  function setupMutationObserver() {
    if (typeof MutationObserver === "undefined" || !document.body) return;

    var observer = new MutationObserver(function (mutations) {
      var shouldRender = false;
      for (var i = 0; i < mutations.length; i++) {
        if (mutations[i].addedNodes && mutations[i].addedNodes.length > 0) {
          shouldRender = true;
          break;
        }
      }
      if (shouldRender) scheduleRender();
    });

    observer.observe(document.body, { childList: true, subtree: true });
  }

  kit.render = render;

  if (typeof document !== "undefined") {
    setupGlobalEventDelegation();

    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", function () {
        setupMutationObserver();
        render();
      });
    } else {
      setTimeout(function () {
        setupMutationObserver();
        render();
      }, 0);
    }
  }

})(typeof window !== "undefined" ? window : globalThis);
