"use strict";

function hasOwn(object, key) {
  return object != null && Object.prototype.hasOwnProperty.call(object, key);
}

function isThenable(value) {
  return value != null && (typeof value === "object" || typeof value === "function") &&
    typeof value.then === "function";
}

function enqueueMicrotask(callback) {
  if (typeof queueMicrotask === "function") return queueMicrotask(callback);
  Promise.resolve().then(callback);
}

function createNullObject() {
  return Object.create(null);
}

function nodeContains(parent, child) {
  if (!parent || !child) return false;
  if (parent === child) return true;
  if (parent.nodeType === 1 && typeof parent.contains === "function") return parent.contains(child);
  var current = child.parentNode;
  while (current) {
    if (current === parent) return true;
    current = current.parentNode;
  }
  return false;
}

function nodeDepth(node) {
  var depth = 0;
  var current = node;
  while (current) {
    depth++;
    current = current.parentNode;
  }
  return depth;
}

function isElement(value) {
  return !!value && value.nodeType === 1;
}

function isNode(value) {
  return !!value && typeof value.nodeType === "number";
}

function toArray(value) {
  return Array.prototype.slice.call(value || []);
}

function cloneState(value, seen) {
  if (value == null || typeof value !== "object") return value;
  if (typeof structuredClone === "function") {
    try { return structuredClone(value); } catch (_) { /* fall through */ }
  }

  seen = seen || new Map();
  if (seen.has(value)) return seen.get(value);

  if (value instanceof Date) return new Date(value.getTime());
  if (value instanceof RegExp) return new RegExp(value.source, value.flags);
  if (Array.isArray(value)) {
    var array = [];
    seen.set(value, array);
    for (var i = 0; i < value.length; i++) array[i] = cloneState(value[i], seen);
    return array;
  }

  var prototype = Object.getPrototypeOf(value);
  if (prototype !== Object.prototype && prototype !== null) return value;

  var object = Object.create(prototype);
  seen.set(value, object);
  Object.keys(value).forEach(function (key) {
    object[key] = cloneState(value[key], seen);
  });
  return object;
}

function normalizeScalar(value) {
  if (value == null) return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean" ||
      typeof value === "bigint") return String(value);
  return null;
}

function cssEscape(value) {
  if (typeof CSS !== "undefined" && CSS && typeof CSS.escape === "function") return CSS.escape(value);
  return String(value).replace(/[^A-Za-z0-9_-]/g, function (character) {
    return "\\" + character.charCodeAt(0).toString(16) + " ";
  });
}

function eventPath(event) {
  if (event && typeof event.composedPath === "function") return event.composedPath();
  var path = [];
  var current = event && event.target;
  while (current) {
    path.push(current);
    current = current.parentNode || current.host || null;
  }
  if (typeof window !== "undefined") path.push(window);
  return path;
}

module.exports = {
  hasOwn: hasOwn,
  isThenable: isThenable,
  enqueueMicrotask: enqueueMicrotask,
  createNullObject: createNullObject,
  nodeContains: nodeContains,
  nodeDepth: nodeDepth,
  isElement: isElement,
  isNode: isNode,
  toArray: toArray,
  cloneState: cloneState,
  normalizeScalar: normalizeScalar,
  cssEscape: cssEscape,
  eventPath: eventPath
};
