// Kitwork client kernel.
//
// One root:
//   window.kitwork
//
// One registry.
// One set of delegated listeners.
// One observer.
//
// No eval.
// No new Function.
(function () {
  "use strict";

  var kitwork = window.kitwork || {};

  if (kitwork.runtime) {
    return;
  }

  window.kitwork = kitwork;

  kitwork.runtime = {
    name: "kitwork",
    version: "1.0.0",
    engine: "web",
    development: false,

    info: function () {
      return {
        name: this.name,
        version: this.version,
        engine: this.engine,
        development: this.development
      };
    }
  };

  kitwork.storage = {
    prefix: "kitwork:",

    key: function (key) {
      return this.prefix + key;
    },

    get: async function (key, options) {
      options = options || {};

      var value;

      try {
        value = window.localStorage.getItem(this.key(key));
      } catch (_) {
        return options.default;
      }

      if (value === null) {
        return options.default;
      }

      try {
        return JSON.parse(value);
      } catch (_) {
        return value;
      }
    },

    set: async function (key, value) {
      if (value === undefined) {
        throw new TypeError(
          "kitwork.storage.set: value cannot be undefined"
        );
      }

      var encoded = JSON.stringify(value);

      try {
        window.localStorage.setItem(
          this.key(key),
          encoded
        );

        return true;
      } catch (error) {
        throw error;
      }
    },

    remove: async function (key) {
      try {
        window.localStorage.removeItem(
          this.key(key)
        );

        return true;
      } catch (error) {
        throw error;
      }
    },

    clear: async function () {
      var prefix = this.prefix;
      var keys = [];

      try {
        for (
          var index = 0;
          index < window.localStorage.length;
          index++
        ) {
          var key = window.localStorage.key(index);

          if (
            key &&
            key.indexOf(prefix) === 0
          ) {
            keys.push(key);
          }
        }

        for (
          var removeIndex = 0;
          removeIndex < keys.length;
          removeIndex++
        ) {
          window.localStorage.removeItem(
            keys[removeIndex]
          );
        }

        return true;
      } catch (error) {
        throw error;
      }
    },

    keys: async function () {
      var prefix = this.prefix;
      var keys = [];

      try {
        for (
          var index = 0;
          index < window.localStorage.length;
          index++
        ) {
          var key = window.localStorage.key(index);

          if (
            key &&
            key.indexOf(prefix) === 0
          ) {
            keys.push(
              key.slice(prefix.length)
            );
          }
        }

        return keys;
      } catch (error) {
        throw error;
      }
    },

    has: async function (key) {
      try {
        return (
          window.localStorage.getItem(
            this.key(key)
          ) !== null
        );
      } catch (error) {
        throw error;
      }
    },

    namespace: function (name) {
      if (
        typeof name !== "string" ||
        name.trim() === ""
      ) {
        throw new TypeError(
          "kitwork.storage.namespace: name is required"
        );
      }

      var storage = this;
      var namespace = name.trim() + ":";

      return {
        get: function (key, options) {
          return storage.get(
            namespace + key,
            options
          );
        },

        set: function (key, value) {
          return storage.set(
            namespace + key,
            value
          );
        },

        remove: function (key) {
          return storage.remove(
            namespace + key
          );
        },

        has: function (key) {
          return storage.has(
            namespace + key
          );
        },

        keys: async function () {
          var keys = await storage.keys();
          var result = [];

          for (
            var index = 0;
            index < keys.length;
            index++
          ) {
            if (
              keys[index].indexOf(namespace) === 0
            ) {
              result.push(
                keys[index].slice(namespace.length)
              );
            }
          }

          return result;
        },

        clear: async function () {
          var keys = await this.keys();

          for (
            var index = 0;
            index < keys.length;
            index++
          ) {
            await this.remove(keys[index]);
          }

          return true;
        },

        namespace: function (child) {
          return storage.namespace(
            namespace + child
          );
        }
      };
    }
  };

  kitwork.theme = {
    get: function () {
      var saved = kitwork.storage.get("theme");

      if (saved === "dark" || saved === "light") {
        return saved;
      }

      return document.documentElement.classList.contains("dark")
        ? "dark"
        : "light";
    },

    set: function (value) {
      var theme = value === "dark" ? "dark" : "light";

      document.documentElement.classList.toggle(
        "dark",
        theme === "dark"
      );

      kitwork.storage.set("theme", theme);

      return theme;
    },

    toggle: function () {
      return this.set(
        this.get() === "dark" ? "light" : "dark"
      );
    }
  };

  kitwork.window = {
    supported: function () {
      return !!(
        kitwork.bridge &&
        typeof kitwork.bridge.call === "function"
      );
    },

    minimize: function () {
      return this.call("minimize");
    },

    maximize: function () {
      return this.call("maximize");
    },

    close: function () {
      return this.call("close");
    },

    drag: function () {
      return this.call("drag");
    },

    call: function (action) {
      if (!this.supported()) {
        return false;
      }

      return kitwork.bridge.call(
        "window." + action,
        {}
      );
    }
  };

  var DRAG = '[data-kit-drag="true"]';
  var UNDRAG = '[data-kit-drag="false"]';

  document.addEventListener("mousedown", function (e) {
    if (e.button !== 0) return;
    if (!e.target.closest) return;
    if (e.target.closest(UNDRAG)) return;

    if (e.target.closest(DRAG)) {
      kitwork.window.drag();
    }
  });

  document.addEventListener("dblclick", function (e) {
    if (!e.target.closest) return;
    if (e.target.closest(UNDRAG)) return;

    if (e.target.closest(DRAG)) {
      kitwork.window.maximize();
    }
  });

  kitwork.platform = (function () {
    var userAgent =
      window.navigator.userAgent || "";

    var platform =
      window.navigator.platform || "";

    function detectOS() {
      if (/Android/i.test(userAgent)) {
        return "android";
      }

      if (/iPhone|iPad|iPod/i.test(userAgent)) {
        return "ios";
      }

      if (/Win/i.test(platform)) {
        return "windows";
      }

      if (/Mac/i.test(platform)) {
        return "macos";
      }

      if (/Linux/i.test(platform)) {
        return "linux";
      }

      return "unknown";
    }

    function detectMobile() {
      return /Android|iPhone|iPad|iPod|Mobile/i.test(
        userAgent
      );
    }

    var os = detectOS();
    var mobile = detectMobile();

    return {
      name: "web",
      os: os,

      web: true,
      native: false,

      mobile: mobile,
      desktop: !mobile,

      touch:
        "ontouchstart" in window ||
        navigator.maxTouchPoints > 0,

      language:
        navigator.language || "en",

      online:
        navigator.onLine,

      info: function () {
        return {
          name: this.name,
          os: this.os,

          web: this.web,
          native: this.native,

          mobile: this.mobile,
          desktop: this.desktop,

          touch: this.touch,
          language: this.language,
          online: navigator.onLine
        };
      }
    };
  })();

  kitwork.clipboard = {
    write: function (value) {
      var text = value == null
        ? ""
        : String(value);

      if (
        navigator.clipboard &&
        typeof navigator.clipboard.writeText === "function"
      ) {
        return navigator.clipboard
          .writeText(text)
          .then(function () {
            return true;
          });
      }

      return new Promise(function (resolve, reject) {
        var area = document.createElement("textarea");

        area.value = text;
        area.setAttribute("readonly", "");
        area.style.position = "fixed";
        area.style.left = "-9999px";
        area.style.opacity = "0";

        document.body.appendChild(area);
        area.select();

        try {
          var copied = document.execCommand("copy");
          area.remove();

          if (!copied) {
            reject(
              new Error("kitwork.clipboard.write: copy failed")
            );

            return;
          }

          resolve(true);
        } catch (error) {
          area.remove();
          reject(error);
        }
      });
    },

    read: function () {
      if (
        navigator.clipboard &&
        typeof navigator.clipboard.readText === "function"
      ) {
        return navigator.clipboard.readText();
      }

      return Promise.reject(
        new Error(
          "kitwork.clipboard.read: clipboard reading is not supported"
        )
      );
    }
  };


  kitwork.share = {
    supported: function () {
      return typeof navigator.share === "function";
    },

    send: function (options) {
      options = options || {};

      var data = {};

      if (options.title != null) {
        data.title = String(options.title);
      }

      if (options.text != null) {
        data.text = String(options.text);
      }

      if (options.url != null) {
        data.url = String(options.url);
      }

      if (
        options.files &&
        options.files.length
      ) {
        data.files = options.files;
      }

      if (
        data.files &&
        typeof navigator.canShare === "function" &&
        !navigator.canShare({ files: data.files })
      ) {
        return Promise.reject(
          new Error(
            "kitwork.share.send: files are not supported"
          )
        );
      }

      if (!this.supported()) {
        return Promise.reject(
          new Error(
            "kitwork.share.send: not supported"
          )
        );
      }

      return navigator.share(data).then(function () {
        return true;
      });
    }
  };

  kitwork.notification = {
    supported: function () {
      return "Notification" in window;
    },

    permission: function () {
      if (!this.supported()) {
        return "unsupported";
      }

      return Notification.permission;
    },

    request: function () {
      if (!this.supported()) {
        return Promise.resolve("unsupported");
      }

      return Notification.requestPermission();
    },

    show: function (title, options) {
      if (!this.supported()) {
        return Promise.reject(
          new Error(
            "kitwork.notification.show: not supported"
          )
        );
      }

      if (Notification.permission !== "granted") {
        return Promise.reject(
          new Error(
            "kitwork.notification.show: permission not granted"
          )
        );
      }

      var notification = new Notification(
        String(title || ""),
        options || {}
      );

      return Promise.resolve(notification);
    }
  };

  kitwork.network = (function () {
    var listeners = [];

    function online() {
      return navigator.onLine;
    }

    function connection() {
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

      for (
        var index = 0;
        index < listeners.length;
        index++
      ) {
        try {
          listeners[index](value);
        } catch (_) { }
      }
    }

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
      nativeConnection.addEventListener(
        "change",
        emit
      );
    }

    return {
      isOnline: online,

      info: info,

      onChange: function (listener) {
        if (typeof listener !== "function") {
          throw new TypeError(
            "kitwork.network.onChange: listener must be a function"
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

  kitwork.network = (function () {
    var listeners = [];

    function online() {
      return navigator.onLine;
    }

    function connection() {
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

      for (
        var index = 0;
        index < listeners.length;
        index++
      ) {
        try {
          listeners[index](value);
        } catch (_) { }
      }
    }

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
      nativeConnection.addEventListener(
        "change",
        emit
      );
    }

    return {
      isOnline: online,

      info: info,

      onChange: function (listener) {
        if (typeof listener !== "function") {
          throw new TypeError(
            "kitwork.network.onChange: listener must be a function"
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
})();