"use strict";

var constants = require("../expression/constants.js");
var errors = require("../core/errors.js");
var utils = require("../core/utils.js");
var MODES = constants.MODES;
var createRuntimeError = errors.createRuntimeError;
var nodeContains = utils.nodeContains;

function createStructuralManager(runtime) {
  function collect(element, app) {
    var ifSource = element.getAttribute("data-kit-if");
    var forSource = element.getAttribute("data-kit-for");
    if (ifSource != null && forSource != null) {
      throw createRuntimeError("KIT_STRUCTURE_CONFLICT", "data-kit-if and data-kit-for cannot own the same element", {
        element: element
      });
    }
    if (ifSource == null && forSource == null) return null;

    var parent = element.parentNode;
    if (!parent) return null;
    var type = ifSource != null ? "if" : "for";
    var anchor = runtime.document.createComment("kit-" + type);
    var template = element.cloneNode(true);
    template.removeAttribute(type === "if" ? "data-kit-if" : "data-kit-for");
    if (type === "for") template.removeAttribute("data-kit-key");

    var structure = {
      type: type,
      app: app,
      anchor: anchor,
      template: template,
      source: type === "if" ? ifSource : forSource,
      compiled: runtime.expression.compile(type === "if" ? MODES.BINDING : MODES.ITERATOR, type === "if" ? ifSource : forSource),
      keySource: type === "for" ? (element.getAttribute("data-kit-key") || "") : "",
      keyCompiled: null,
      mounted: null,
      rows: new Map(),
      destroyed: false
    };
    if (type === "for" && structure.keySource) {
      structure.keyCompiled = runtime.expression.compile(MODES.BINDING, structure.keySource);
    }

    parent.replaceChild(anchor, element);
    var record = runtime.nodeRecord(anchor, app);
    record.structure = structure;
    record.hydrated = true;
    app.structures.add(structure);
    return structure;
  }

  function renderIf(structure) {
    var environment = runtime.environmentFor(structure.anchor, null);
    var visible = !!runtime.expression.evaluate(structure.compiled, environment);
    var changed = false;
    if (visible && !structure.mounted) {
      var node = structure.template.cloneNode(true);
      structure.anchor.parentNode.insertBefore(node, structure.anchor.nextSibling);
      structure.mounted = node;
      runtime.markFresh(node);
      runtime.hydrateTree(node, structure.app);
      changed = true;
    } else if (!visible && structure.mounted) {
      runtime.cleanupTree(structure.mounted, structure.app);
      if (structure.mounted.parentNode) structure.mounted.parentNode.removeChild(structure.mounted);
      structure.mounted = null;
      changed = true;
    }
    return changed;
  }

  function normalizeKey(value, index) {
    if (typeof value !== "string" && typeof value !== "number") return "index:" + index;
    return typeof value + ":" + String(value);
  }

  function renderFor(structure) {
    var environment = runtime.environmentFor(structure.anchor, null);
    var iterator = runtime.expression.evaluate(structure.compiled, environment);
    var collection = iterator && iterator.collection;
    if (!Array.isArray(collection)) collection = [];

    var used = new Set();
    var ordered = [];
    var duplicate = false;

    for (var i = 0; i < collection.length; i++) {
      var frame = Object.create(null);
      frame[iterator.itemName] = collection[i];
      if (iterator.indexName) frame[iterator.indexName] = i;

      var rawKey = i;
      if (structure.keyCompiled) {
        var keyEnvironment = runtime.environmentFor(structure.anchor, null, { loopFrames: [frame] });
        rawKey = runtime.expression.evaluate(structure.keyCompiled, keyEnvironment);
        if (typeof rawKey !== "string" && typeof rawKey !== "number") {
          if (runtime.options.development) runtime.warn("KIT_LIST_KEY_TYPE", "data-kit-key must resolve to a string or number; index fallback used", {
            value: rawKey,
            index: i,
            anchor: structure.anchor
          });
          rawKey = i;
        }
      } else if (runtime.options.development && i === 0) {
        runtime.warn("KIT_LIST_INDEX_KEY", "data-kit-for has no data-kit-key; index fallback does not preserve identity across reorders", {
          anchor: structure.anchor
        });
      }

      var key = normalizeKey(rawKey, i);
      if (used.has(key)) {
        duplicate = true;
        runtime.reportError(createRuntimeError("KIT_DUPLICATE_LIST_KEY", "Duplicate list key '" + rawKey + "'", {
          key: rawKey,
          index: i,
          anchor: structure.anchor
        }), runtime.contextFor(structure.anchor, "for", structure.source, "structure"));
        continue;
      }
      used.add(key);

      var row = structure.rows.get(key);
      if (!row) {
        var node = structure.template.cloneNode(true);
        var record = runtime.nodeRecord(node, structure.app);
        record.loopFrame = frame;
        row = { key: key, rawKey: rawKey, node: node, frame: frame };
        structure.rows.set(key, row);
      } else {
        row.frame[iterator.itemName] = collection[i];
        if (iterator.indexName) row.frame[iterator.indexName] = i;
        row.rawKey = rawKey;
      }
      ordered.push(row);
    }

    var changed = false;
    var parent = structure.anchor.parentNode;
    if (!parent) return false;
    var cursor = structure.anchor.nextSibling;
    ordered.forEach(function (row) {
      if (!row.node.isConnected) {
        parent.insertBefore(row.node, cursor);
        runtime.markFresh(row.node);
        runtime.hydrateTree(row.node, structure.app);
        changed = true;
      } else if (row.node !== cursor) {
        parent.insertBefore(row.node, cursor);
        changed = true;
      }
      cursor = row.node.nextSibling;
    });

    Array.from(structure.rows.entries()).forEach(function (entry) {
      if (used.has(entry[0])) return;
      var row = entry[1];
      runtime.cleanupTree(row.node, structure.app);
      if (row.node.parentNode) row.node.parentNode.removeChild(row.node);
      structure.rows.delete(entry[0]);
      changed = true;
    });
    return changed || duplicate;
  }

  function render(app, boundary) {
    var changed = false;
    Array.from(app.structures).forEach(function (structure) {
      if (structure.destroyed || !structure.anchor.isConnected) return;
      if (boundary !== app.root && !nodeContains(boundary, structure.anchor)) return;
      try {
        if (structure.type === "if") changed = renderIf(structure) || changed;
        else changed = renderFor(structure) || changed;
      } catch (error) {
        runtime.reportError(error, runtime.contextFor(structure.anchor, structure.type, structure.source, "structure"));
      }
    });
    return changed;
  }

  function cleanup(structure) {
    if (!structure || structure.destroyed) return;
    structure.destroyed = true;
    if (structure.mounted) {
      runtime.cleanupTree(structure.mounted, structure.app);
      structure.mounted = null;
    }
    structure.rows.forEach(function (row) { runtime.cleanupTree(row.node, structure.app); });
    structure.rows.clear();
    structure.app.structures.delete(structure);
  }

  return {
    collect: collect,
    render: render,
    cleanup: cleanup
  };
}

module.exports = {
  createStructuralManager: createStructuralManager
};
