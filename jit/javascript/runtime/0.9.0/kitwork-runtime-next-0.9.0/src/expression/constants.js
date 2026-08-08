"use strict";

const MODES = Object.freeze({
  NAMED_MAP: "named-map",
  BINDING: "binding",
  CLASS_VALUE: "class-value",
  ACTION: "action",
  WRITABLE_PATH: "writable-path",
  IDENTITY: "identity",
  ITERATOR: "iterator"
});

const MODE_ALIASES = Object.freeze({
  map: MODES.NAMED_MAP,
  namedMap: MODES.NAMED_MAP,
  binding: MODES.BINDING,
  class: MODES.CLASS_VALUE,
  classValue: MODES.CLASS_VALUE,
  action: MODES.ACTION,
  writable: MODES.WRITABLE_PATH,
  writablePath: MODES.WRITABLE_PATH,
  identity: MODES.IDENTITY,
  iterator: MODES.ITERATOR
});

function createWordSet(source) {
  const output = Object.create(null);
  String(source || "").split(/\s+/).forEach((word) => {
    if (word) output[word] = true;
  });
  return output;
}

const BLOCKED_MEMBERS = createWordSet(
  "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
  "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
  "window globalThis top parent self"
);

const FORBIDDEN_KEYWORDS = createWordSet(
  "var let const function class return if else for while do switch case new " +
  "delete void typeof instanceof in await yield throw try catch finally import export"
);

const RESERVED_ASSIGNMENT_ROOTS = createWordSet(
  "$element $host $event $refs $parent $index kit"
);

function hasOwn(object, key) {
  return object != null && Object.prototype.hasOwnProperty.call(object, key);
}

function isThenable(value) {
  return value != null &&
    (typeof value === "object" || typeof value === "function") &&
    typeof value.then === "function";
}

function isIdentifierStart(character) {
  return character === "$" || character === "_" ||
    (character >= "A" && character <= "Z") ||
    (character >= "a" && character <= "z");
}

function isIdentifierPart(character) {
  return isIdentifierStart(character) ||
    (character >= "0" && character <= "9");
}

function isBlockedMember(key) {
  return typeof key === "string" && BLOCKED_MEMBERS[key] === true;
}

function rootIdentifier(ast) {
  let current = ast;
  while (current && current.type === "MemberExpression") current = current.object;
  return current && current.type === "Identifier" ? current.name : "";
}

function normalizeMode(mode) {
  mode = mode || MODES.BINDING;
  return MODE_ALIASES[mode] || mode;
}

module.exports = {
  MODES,
  MODE_ALIASES,
  BLOCKED_MEMBERS,
  FORBIDDEN_KEYWORDS,
  RESERVED_ASSIGNMENT_ROOTS,
  createWordSet,
  hasOwn,
  isThenable,
  isIdentifierStart,
  isIdentifierPart,
  isBlockedMember,
  rootIdentifier,
  normalizeMode
};
