// ============================================================================
// Kitwork Client Runtime Service: Announcer (1.0.0)
// ============================================================================
// Location: engine/jit/javascript/service/announce/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.announce) return;

  function getRegionElement(assertive) {
    var id = assertive ? "kit-announcer-assertive" : "kit-announcer-polite";
    var el = document.getElementById(id);
    if (!el && typeof document !== "undefined" && document.body) {
      el = document.createElement("div");
      el.id = id;
      el.setAttribute("data-kit-keep", "true");
      el.setAttribute("aria-live", assertive ? "assertive" : "polite");
      el.setAttribute("aria-atomic", "true");
      el.style.position = "absolute";
      el.style.width = "1px";
      el.style.height = "1px";
      el.style.padding = "0";
      el.style.overflow = "hidden";
      el.style.clip = "rect(0,0,0,0)";
      el.style.whiteSpace = "nowrap";
      el.style.border = "0";
      document.body.appendChild(el);
    }
    return el;
  }

  kit.announce = {
    say: function (message, mode) {
      message = String(message || "").trim();
      if (!message) return Promise.resolve(false);

      var isAssertive = mode === "assertive" || mode === "urgent";
      var el = getRegionElement(isAssertive);

      if (el) {
        el.textContent = "";
        setTimeout(function () {
          el.textContent = message;
        }, 50);
        return Promise.resolve(true);
      }

      return Promise.reject("Announcer DOM unavailable");
    },

    polite: function (message) {
      return this.say(message, "polite");
    },

    assertive: function (message) {
      return this.say(message, "assertive");
    }
  };

  if (typeof document !== "undefined") {
    document.addEventListener("click", function (e) {
      var target = e.target && e.target.closest ? e.target.closest("[data-kit-announce]") : null;
      if (target) {
        var msg = target.getAttribute("data-kit-announce");
        var mode = target.getAttribute("data-kit-announce-mode") || "polite";
        if (msg) {
          kit.announce.say(msg, mode);
        }
      }
    }, true);
  }

})(typeof window !== "undefined" ? window : globalThis);
