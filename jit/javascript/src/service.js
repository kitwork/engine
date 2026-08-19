; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || ["events", "drive"].indexOf(core.phase) < 0) {
    throw new Error("KitJS: service registrar loaded out of order");
  }
  if (core.reuse) return;
  if (!core.kit || typeof core.validServiceName !== "function" ||
    typeof core.sealKit !== "function" || core.serviceRegistry) {
    throw new Error("KitJS: service registrar cannot be installed");
  }

  var OWN = core.OWN;
  var registry = new Map();
  var identities = new WeakMap();
  var kit = core.kit;
  var sealed = false;

  function snapshot(name, namespace) {
    var prototype = namespace && Object.getPrototypeOf(namespace);
    if (!namespace || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(namespace).length) {
      throw new TypeError("KitJS: service namespace must be a plain object");
    }
    var descriptors = Object.getOwnPropertyDescriptors(namespace);
    var output = Object.create(null);
    Object.keys(descriptors).forEach(function (member) {
      if (member === "version" || core.blocked(member)) {
        throw new TypeError("KitJS: invalid service member \"" + member + "\"");
      }
      var descriptor = descriptors[member];
      if (descriptor.set || !OWN.call(descriptor, "value") && typeof descriptor.get !== "function") {
        throw new TypeError("KitJS: service members must be values or readonly getters");
      }
      if (OWN.call(descriptor, "value")) {
        Object.defineProperty(output, member, {
          value: descriptor.value,
          enumerable: descriptor.enumerable !== false
        });
      } else {
        Object.defineProperty(output, member, {
          get: descriptor.get,
          enumerable: descriptor.enumerable !== false
        });
      }
    });
    Object.defineProperty(output, "version", {
      value: core.graph.services[name]
    });
    return Object.freeze(output);
  }

  function service(name, namespace) {
    if (arguments.length !== 2) {
      throw new TypeError("KitJS: service(name, namespace) expects two arguments");
    }
    if (sealed) throw new Error("KitJS: service registrar is sealed");
    if (!core.graph) throw new Error("KitJS: services must register after the graph is installed");
    if (!core.validServiceName(name)) throw new TypeError("KitJS: invalid service name");
    if (!OWN.call(core.graph.services, name)) {
      throw new Error("KitJS: service \"" + name + "\" is not declared by the installed graph");
    }
    if (registry.has(name)) throw new Error("KitJS: service \"" + name + "\" already exists");
    var value = snapshot(name, namespace);
    Object.defineProperty(kit, name, {
      value: value,
      enumerable: true
    });
    registry.set(name, value);
    identities.set(value, name);
  }

  Object.defineProperty(kit, "service", {
    value: service,
    configurable: true
  });

  core.serviceRegistry = registry;
  core.sealServices = function () {
    if (sealed) throw new Error("KitJS: services are already sealed");
    if (!core.graph) throw new Error("KitJS: service graph is not installed");
    Object.keys(core.graph.services).forEach(function (name) {
      if (!registry.has(name)) {
        throw new Error("KitJS: service graph is missing definition \"" + name + "\"");
      }
      Object.keys(core.graph.actions[name]).forEach(function (member) {
        if (typeof registry.get(name)[member] !== "function") {
          throw new Error("KitJS: authored action \"" + name + "." + member + "\" is not callable");
        }
      });
    });
    sealed = true;
    if (!delete kit.service) throw new Error("KitJS: service registrar could not be removed");
    core.servicesSealed = true;
    return core.sealKit();
  };
  core.serviceName = function (value) { return identities.get(value) || null; };
})(document);
