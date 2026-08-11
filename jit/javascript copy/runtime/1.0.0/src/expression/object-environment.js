"use strict";

const { createError } = require("./errors.js");
const {
  RESERVED_ASSIGNMENT_ROOTS,
  hasOwn,
  isBlockedMember
} = require("./constants.js");

function createObjectEnvironment(stateObject, options) {
  options = options || {};
  const state = stateObject || Object.create(null);
  const contexts = options.contexts || Object.create(null);
  const aliases = options.aliases || Object.create(null);
  const services = options.services || Object.create(null);
  const readonlyRoots = options.readonlyRoots || RESERVED_ASSIGNMENT_ROOTS;

  return {
    resolve(name) {
      if (hasOwn(contexts, name)) {
        return { found: true, value: contexts[name], owner: null, readonly: true, kind: "context" };
      }
      if (hasOwn(aliases, name)) {
        return { found: true, value: aliases[name], owner: aliases[name], readonly: true, kind: "alias" };
      }
      if (name === "kit") {
        return { found: true, value: services, owner: null, readonly: true, kind: "service" };
      }
      if (hasOwn(state, name)) {
        return { found: true, value: state[name], owner: state, readonly: false, kind: "state" };
      }
      return { found: false, value: undefined, owner: null, readonly: false, kind: "missing" };
    },

    assign(name, value) {
      if (!name || readonlyRoots[name] || name[0] === "$" || name === "kit") {
        throw createError("KIT_READONLY_CONTEXT", "Cannot assign to runtime context '" + name + "'", {
          name
        });
      }
      state[name] = value;
      if (typeof options.onMutation === "function") {
        options.onMutation({ type: "identifier", name, owner: state, value });
      }
      return value;
    },

    canWriteMember(reference) {
      if (!reference || !reference.owner || reference.key == null) return false;
      if (isBlockedMember(reference.key)) return false;
      if (readonlyRoots[reference.root]) return false;
      if (reference.owner === services || reference.owner === contexts) return false;
      if (typeof globalThis !== "undefined" && reference.owner === globalThis) return false;
      if (reference.owner && reference.owner.nodeType) return false;
      if (typeof Map !== "undefined" && reference.owner instanceof Map) return false;
      if (typeof Set !== "undefined" && reference.owner instanceof Set) return false;

      const descriptor = Object.getOwnPropertyDescriptor(reference.owner, reference.key);
      if (descriptor && (
        descriptor.writable === false ||
        typeof descriptor.value === "function" ||
        descriptor.get || descriptor.set
      )) return false;

      return true;
    },

    onMutation: options.onMutation || null,
    onEffect: options.onEffect || null,
    defaultThis: options.defaultThis || state,
    state
  };
}

module.exports = {
  createObjectEnvironment
};
