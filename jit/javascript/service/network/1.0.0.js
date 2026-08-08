// ============================================================================
// Kitwork Platform Service Façade: Network (1.0.0)
// ============================================================================
// Location: engine/jit/javascript/service/network/1.0.0.js
// ============================================================================
// Pure State Service Façade nhận biết mạng thời gian thực.
// Đăng ký trên namespace `kit.network`. Hỗ trợ vendor prefixes, saveData & unsubscribe.
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.network = (function () {
    var listeners = [];

    function online() {
      return typeof navigator !== "undefined" ? navigator.onLine : true;
    }

    function connection() {
      if (typeof navigator === "undefined") return null;

      var value =
        navigator.connection ||
        navigator.mozConnection ||
        navigator.webkitConnection ||
        null;

      if (!value) {
        return null;
      }

      return {
        type: value.type || null,
        effectiveType: value.effectiveType || null,
        downlink: value.downlink || null,
        rtt: value.rtt || null,
        saveData: !!value.saveData
      };
    }

    function info() {
      return {
        online: online(),
        connection: connection()
      };
    }

    function emit() {
      var value = info();

      for (var index = 0; index < listeners.length; index++) {
        try {
          listeners[index](value);
        } catch (_) {}
      }

      if (kit.render) kit.render();
    }

    if (typeof window !== "undefined") {
      window.addEventListener("online", emit);
      window.addEventListener("offline", emit);

      var nativeConnection =
        navigator.connection ||
        navigator.mozConnection ||
        navigator.webkitConnection;

      if (
        nativeConnection &&
        typeof nativeConnection.addEventListener === "function"
      ) {
        nativeConnection.addEventListener("change", emit);
      }
    }

    return {
      isOnline: online,

      info: info,

      onChange: function (listener) {
        if (typeof listener !== "function") {
          throw new TypeError(
            "kit.network.onChange: listener must be a function"
          );
        }

        listeners.push(listener);

        return function () {
          var index = listeners.indexOf(listener);

          if (index >= 0) {
            listeners.splice(index, 1);
          }
        };
      }
    };
  })();

})(typeof window !== "undefined" ? window : globalThis);
