"use strict";

var utils = require("../core/utils.js");
var errors = require("../core/errors.js");
var hasOwn = utils.hasOwn;
var createNullObject = utils.createNullObject;
var createRuntimeError = errors.createRuntimeError;

var BLOCKED_MEMBERS = createNullObject();
(
  "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
  "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
  "window globalThis top parent self"
).split(/\s+/).forEach(function (name) {
  if (name) BLOCKED_MEMBERS[name] = true;
});

var RESERVED_NAMES = createNullObject();
(
  "runtime component service start destroy render onError internal dev " +
  "module modules has get set scope scopeFor compile run"
).split(/\s+/).forEach(function (name) {
  if (name) RESERVED_NAMES[name] = true;
});

function validName(name) {
  return /^[A-Za-z][A-Za-z0-9]*$/.test(name) && !RESERVED_NAMES[name] && !BLOCKED_MEMBERS[name];
}

function createServiceRegistry(kit) {
  var services = new Map();
  var grants = new Map();
  var surfaces = new Map();

  function createSurface(name) {
    if (surfaces.has(name)) return surfaces.get(name);
    var surface = new Proxy(createNullObject(), {
      get: function (_, member) {
        var allowed = grants.get(name);
        if (!allowed || !allowed.has(member)) return undefined;
        var service = services.get(name);
        if (service == null) return undefined;
        var value = service[member];
        return typeof value === "function" ? value.bind(service) : value;
      },
      has: function (_, member) {
        var allowed = grants.get(name);
        return !!allowed && allowed.has(member);
      },
      ownKeys: function () {
        var allowed = grants.get(name);
        return allowed ? Array.from(allowed) : [];
      },
      getOwnPropertyDescriptor: function (_, member) {
        var allowed = grants.get(name);
        if (!allowed || !allowed.has(member)) return undefined;
        return { configurable: true, enumerable: true };
      },
      set: function () { return false; },
      defineProperty: function () { return false; },
      deleteProperty: function () { return false; },
      setPrototypeOf: function () { return false; },
      getPrototypeOf: function () { return null; }
    });
    surfaces.set(name, surface);
    return surface;
  }

  var publicSurface = new Proxy(createNullObject(), {
    get: function (_, name) {
      if (typeof name !== "string" || !grants.has(name)) return undefined;
      return createSurface(name);
    },
    has: function (_, name) { return grants.has(name); },
    ownKeys: function () { return Array.from(grants.keys()); },
    getOwnPropertyDescriptor: function (_, name) {
      if (!grants.has(name)) return undefined;
      return { configurable: true, enumerable: true };
    },
    set: function () { return false; },
    defineProperty: function () { return false; },
    deleteProperty: function () { return false; },
    setPrototypeOf: function () { return false; },
    getPrototypeOf: function () { return null; }
  });

  function register(name, implementation, options) {
    name = String(name || "");
    if (!validName(name)) {
      throw createRuntimeError("KIT_SERVICE_NAME", "Invalid or reserved service name '" + name + "'", {
        service: name
      });
    }

    services.set(name, implementation);
    kit[name] = implementation;

    var allowed = new Set();
    var members = options && options.expression;
    if (Array.isArray(members)) {
      members.forEach(function (member) {
        member = String(member || "");
        if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(member) && !BLOCKED_MEMBERS[member]) {
          allowed.add(member);
        }
      });
    }

    if (allowed.size) grants.set(name, allowed);
    else grants.delete(name);
    surfaces.delete(name);
    return implementation;
  }

  function service(name) {
    return services.get(String(name || ""));
  }

  return {
    register: register,
    get: service,
    publicSurface: publicSurface,
    services: services,
    grants: grants
  };
}

module.exports = {
  createServiceRegistry: createServiceRegistry
};
