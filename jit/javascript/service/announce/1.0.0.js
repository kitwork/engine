// KitJS service: announce@1.0.0
;(function (global) {
  "use strict";

  var kit = global.kit;
  var version = "1.0.0";
  var OWN = Object.prototype.hasOwnProperty;
  var pending = { polite: null, assertive: null };

  if (!kit || !OWN.call(kit, "component") || typeof kit.component !== "function") {
    throw new Error("KitJS core must be loaded before service:announce");
  }
  if (OWN.call(kit, "announce")) {
    if (kit.announce.version === version) return;
    throw new Error("KitJS service conflict: announce");
  }

  function modeOf(value) {
    return value === "assertive" || value === "urgent" ? "assertive" : "polite";
  }

  function region(mode) {
    var document = global.document;
    if (!document || !document.body) throw new Error("Announcement region is unavailable");
    var id = "kit-announce-" + mode;
    var element = document.getElementById(id);
    if (element) return element;

    element = document.createElement("div");
    element.id = id;
    element.setAttribute("role", mode === "assertive" ? "alert" : "status");
    element.setAttribute("aria-live", mode);
    element.setAttribute("aria-atomic", "true");
    element.style.position = "fixed";
    element.style.width = "1px";
    element.style.height = "1px";
    element.style.padding = "0";
    element.style.margin = "-1px";
    element.style.overflow = "hidden";
    element.style.clipPath = "inset(50%)";
    element.style.whiteSpace = "nowrap";
    element.style.border = "0";
    document.body.appendChild(element);
    return element;
  }

  function cancel(mode) {
    var current = pending[mode];
    if (!current) return;
    global.clearTimeout(current.timer);
    current.resolve(false);
    pending[mode] = null;
  }

  function say(message, mode) {
    message = message === null || message === undefined ? "" : String(message).trim();
    if (!message) return Promise.resolve(false);
    mode = modeOf(mode);

    var element;
    try { element = region(mode); }
    catch (error) { return Promise.reject(error); }

    cancel(mode);
    element.textContent = "";
    return new Promise(function (resolve) {
      pending[mode] = {
        resolve: resolve,
        timer: global.setTimeout(function () {
          pending[mode] = null;
          element.textContent = message;
          resolve(true);
        }, 20)
      };
    });
  }

  function clear(mode) {
    var modes = mode ? [modeOf(mode)] : ["polite", "assertive"];
    modes.forEach(function (name) {
      cancel(name);
      var element = global.document && global.document.getElementById("kit-announce-" + name);
      if (element) element.textContent = "";
    });
    return true;
  }

  var announce = {
    say: say,
    polite: function (message) { return say(message, "polite"); },
    assertive: function (message) { return say(message, "assertive"); },
    clear: clear
  };
  Object.defineProperty(announce, "version", { value: version, enumerable: false });
  Object.freeze(announce);
  Object.defineProperty(kit, "announce", {
    value: announce,
    enumerable: true,
    configurable: false,
    writable: false
  });
})(globalThis);
