"use strict";

var errors = require("../core/errors.js");
var createRuntimeError = errors.createRuntimeError;

function createDirectiveRegistry() {
  var directives = new Map();

  function register(name, contract) {
    name = String(name || "").trim();
    if (!/^[a-z][a-z0-9-]*$/.test(name)) {
      throw createRuntimeError("KIT_DIRECTIVE_NAME", "Invalid directive name '" + name + "'", {
        directive: name
      });
    }
    if (!contract || typeof contract !== "object") {
      throw createRuntimeError("KIT_DIRECTIVE_CONTRACT", "Directive '" + name + "' requires a contract");
    }
    directives.set(name, Object.assign({ name: name, phase: "content" }, contract));
    return contract;
  }

  return {
    register: register,
    get: function (name) { return directives.get(name); },
    has: function (name) { return directives.has(name); },
    names: function () { return Array.from(directives.keys()); },
    entries: function () { return Array.from(directives.entries()); }
  };
}

module.exports = {
  createDirectiveRegistry: createDirectiveRegistry
};
