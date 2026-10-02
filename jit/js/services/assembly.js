;(function (global, document) {
  "use strict";
  // What the native services (services/<name>/v<x>.js) read before they register: the object KitJS
  // used to build, under the same key, holding exactly what they take from it.
  //   nativeHost  — the adapter the shell seeds at document-start (native/bridge), call(action, params);
  //                 null in a plain browser, where every service answers UNAVAILABLE
  //   nativeFiles — the shell's binary file channel, send(frame), when it has one
  //   graph       — the services this engine carries and their versions, so one service can check a
  //                 sibling it hands something to (files ↔ network, camera)
  // Services also leave private hand-offs on it (an upload boundary, a file adopter), so it stays
  // an ordinary object. Installed once; a page Drive swaps in keeps the first.
  var ASSEMBLY = Symbol.for("kitjs:assembly");
  if (Object.prototype.hasOwnProperty.call(document, ASSEMBLY)) return;
  function seeded(key, method) {
    try {
      var value = document[Symbol.for(key)];
      return value && value.version === "1.0.0" && typeof value[method] === "function" ? value : null;
    } catch (_) { return null; }
  }
  Object.defineProperty(document, ASSEMBLY, {
    value: {
      nativeHost: seeded("kitwork:native:host:v1", "call"),
      nativeFiles: seeded("kitwork:native:files:v1", "send"),
      graph: { services: __SERVICES__ }
    },
    configurable: false,
    enumerable: false,
    writable: false
  });
})(globalThis, document);
