"use strict";

var utils = require("../core/utils.js");
var toArray = utils.toArray;

function createHeadManager(runtime) {
  var document = runtime.document;
  var loadedScripts = new Set();
  var seededScripts = false;

  function absoluteUrl(value) {
    try { return new runtime.global.URL(value, runtime.global.location && runtime.global.location.href).href; }
    catch (_) { return String(value || ""); }
  }

  function selectorKey(node) {
    if (!node || node.nodeType !== 1) return "";
    var tag = String(node.tagName || "").toLowerCase();
    if (node.hasAttribute("data-kit-head")) {
      var key = node.getAttribute("data-kit-head");
      return "managed:" + (key || node.outerHTML);
    }
    if (tag === "meta") {
      if (node.getAttribute("name")) return "meta:name:" + node.getAttribute("name");
      if (node.getAttribute("property")) return "meta:property:" + node.getAttribute("property");
      if (node.getAttribute("http-equiv")) return "meta:http:" + node.getAttribute("http-equiv");
    }
    if (tag === "link") {
      var rel = String(node.getAttribute("rel") || "").toLowerCase();
      if (rel === "canonical") return "link:canonical";
      if (rel === "stylesheet" || rel === "modulepreload" || rel === "preload") {
        return "link:" + rel + ":" + absoluteUrl(node.getAttribute("href"));
      }
    }
    return "";
  }

  function cloneNode(node) {
    return document.importNode ? document.importNode(node, true) : node.cloneNode(true);
  }

  function reconcile(incomingDocument) {
    if (!incomingDocument) return;
    if (incomingDocument.title !== undefined && document.title !== incomingDocument.title) {
      document.title = incomingDocument.title;
    }

    var incoming = new Map();
    toArray(incomingDocument.head && incomingDocument.head.children).forEach(function (node) {
      var key = selectorKey(node);
      if (key) incoming.set(key, node);
    });
    var current = new Map();
    toArray(document.head && document.head.children).forEach(function (node) {
      var key = selectorKey(node);
      if (key && !current.has(key)) current.set(key, node);
    });

    incoming.forEach(function (node, key) {
      var existing = current.get(key);
      if (existing) {
        if (existing.outerHTML !== node.outerHTML) existing.replaceWith(cloneNode(node));
      } else document.head.appendChild(cloneNode(node));
    });

    current.forEach(function (node, key) {
      // Only managed metadata is removed. Stylesheets/preloads without
      // data-kit-head remain cumulative to avoid breaking already-mounted apps.
      if (!incoming.has(key) && (node.hasAttribute("data-kit-head") || key === "link:canonical" || key.indexOf("meta:") === 0)) {
        node.remove();
      }
    });
  }

  function scriptKey(script) {
    var src = script.getAttribute("src");
    if (src) return "src:" + absoluteUrl(src);
    var explicit = script.getAttribute("data-kit-script");
    if (explicit) return "key:" + explicit;
    return "inline:" + (script.textContent || "");
  }

  function executable(script) {
    var authoredType = script.hasAttribute("data-kit-drive-type")
      ? script.getAttribute("data-kit-drive-type")
      : script.getAttribute("type");
    var type = String(authoredType || "").trim().toLowerCase();
    return !type || type === "text/javascript" || type === "application/javascript" || type === "module";
  }

  function activateScripts(root) {
    if (!root || !root.querySelectorAll) return [];
    if (!seededScripts) {
      seededScripts = true;
      toArray(document.querySelectorAll("script[src]")).forEach(function (script) {
        loadedScripts.add(scriptKey(script));
      });
    }
    var pending = [];
    toArray(root.querySelectorAll("script[data-kit-drive-pending]")).forEach(function (script) {
      if (!executable(script)) return;
      var key = scriptKey(script);
      var reload = script.hasAttribute("data-kit-reload");
      var once = script.hasAttribute("data-kit-once") || !!script.getAttribute("src");
      if (!reload && once && loadedScripts.has(key)) {
        script.remove();
        return;
      }
      var fresh = document.createElement("script");
      var authoredType = script.getAttribute("data-kit-drive-type") || "";
      toArray(script.attributes).forEach(function (attribute) {
        if (attribute.name !== "data-kit-drive-pending" && attribute.name !== "data-kit-drive-type" && attribute.name !== "type") {
          fresh.setAttribute(attribute.name, attribute.value);
        }
      });
      if (authoredType) fresh.setAttribute("type", authoredType);
      if (!script.getAttribute("src")) fresh.text = script.textContent || "";
      var promise = new Promise(function (resolve) {
        if (script.getAttribute("src")) {
          fresh.addEventListener("load", resolve, { once: true });
          fresh.addEventListener("error", resolve, { once: true });
        } else resolve();
      });
      script.replaceWith(fresh);
      if (once) loadedScripts.add(key);
      pending.push(promise);
    });
    return pending;
  }

  return {
    reconcile: reconcile,
    activateScripts: activateScripts,
    loadedScripts: loadedScripts
  };
}

module.exports = {
  createHeadManager: createHeadManager
};
