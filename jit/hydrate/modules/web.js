// Browser-backed platform services exposed through the canonical `kit.*` namespaces.
(function (window, document) {
  "use strict";

  var kit = window.kit || window.kitwork;
  if (!kit || !kit.module || !kit.service || kit.has("web")) return;

  var native = kit.module("native");
  var navigator = window.navigator || {};
  var hasOwn = Object.prototype.hasOwnProperty;
  var capabilityIDs = Object.create(null);

  [
    "capabilities.supports",
    "dialog.alert",
    "dialog.confirm",
    "dialog.prompt",
    "share.open",
    "clipboard.writeText",
    "clipboard.readText",
    "clipboard.copy",
    "camera.capture",
    "theme.mode",
    "theme.resolved",
    "theme.set",
    "theme.toggle",
    "navigation.back",
    "navigation.forward",
    "navigation.reload",
    "host.info",
    "host.exit",
    "host.restart",
    "permissions.check",
    "permissions.request",
    "secureStorage.get",
    "secureStorage.set",
    "secureStorage.remove",
    "cache.get",
    "cache.set",
    "database.open",
    "files.read",
    "files.write",
    "files.exists",
    "media.resize",
    "audio.record",
    "audio.play",
    "screen.capture",
    "screen.keepAwake",
    "location.current",
    "device.info",
    "device.vibrate",
    "ai.chat",
    "ai.transcribe",
    "auth.login",
    "auth.logout",
    "session.get",
    "session.set",
    "session.clear",
    "logs.info",
    "logs.error",
    "shell.open",
    "window.drag",
    "window.minimize",
    "window.maximize",
    "window.restore",
    "window.close",
    "storage.get",
    "storage.set",
    "storage.remove",
    "storage.clear"
  ].forEach(function (id) {
    capabilityIDs[id] = true;
  });

  function unsupported(moduleName, actionName) {
    return kit.KitworkError(
      moduleName + "." + actionName + " is unsupported",
      "UNSUPPORTED",
      moduleName,
      actionName
    );
  }

  function localStorageAvailable() {
    try {
      return !!window.localStorage;
    } catch (_) {
      return false;
    }
  }

  function browserSupports(id) {
    switch (id) {
      case "capabilities.supports":
        return true;
      case "dialog.alert":
        return typeof window.alert === "function";
      case "dialog.confirm":
        return typeof window.confirm === "function";
      case "dialog.prompt":
        return typeof window.prompt === "function";
      case "share.open":
        return typeof navigator.share === "function";
      case "clipboard.writeText":
      case "clipboard.copy":
        return !!(
          navigator.clipboard &&
          typeof navigator.clipboard.writeText === "function"
        ) || typeof document.execCommand === "function";
      case "clipboard.readText":
        return !!(
          navigator.clipboard &&
          typeof navigator.clipboard.readText === "function"
        );
      case "camera.capture":
        return (
          typeof document.createElement === "function" &&
          typeof window.FileReader !== "undefined"
        );
      case "theme.mode":
      case "theme.resolved":
      case "theme.set":
      case "theme.toggle":
        return !!document.documentElement;
      case "navigation.back":
      case "navigation.forward":
        return !!window.history;
      case "navigation.reload":
        return !!window.location;
      case "storage.get":
      case "storage.set":
      case "storage.remove":
      case "storage.clear":
        return localStorageAvailable();
      default:
        return false;
    }
  }

  var capabilities = {
    supports: function (id) {
      id = String(id || "");
      if (!hasOwn.call(capabilityIDs, id)) return Promise.resolve(false);
      if (
        id === "capabilities.supports" ||
        id.indexOf("theme.") === 0 ||
        id.indexOf("navigation.") === 0
      ) {
        return Promise.resolve(browserSupports(id));
      }
      if (native.available) {
        return native.call("capabilities.supports", { id: id }).then(function (value) {
          return !!value;
        });
      }
      return Promise.resolve(browserSupports(id));
    }
  };

  function dialogMessage(options) {
    if (options && typeof options === "object") {
      return options.message == null ? "" : String(options.message);
    }
    return options == null ? "" : String(options);
  }

  var dialog = {
    alert: function (options) {
      if (native.available) return native.call("dialog.alert", options);
      if (typeof window.alert !== "function") {
        return Promise.reject(unsupported("dialog", "alert"));
      }
      return Promise.resolve().then(function () {
        window.alert(dialogMessage(options));
        return true;
      });
    },
    confirm: function (options) {
      if (native.available) return native.call("dialog.confirm", options);
      if (typeof window.confirm !== "function") {
        return Promise.reject(unsupported("dialog", "confirm"));
      }
      return Promise.resolve().then(function () {
        return window.confirm(dialogMessage(options));
      });
    },
    prompt: function (options) {
      if (native.available) return native.call("dialog.prompt", options);
      if (typeof window.prompt !== "function") {
        return Promise.reject(unsupported("dialog", "prompt"));
      }
      return Promise.resolve().then(function () {
        var title = options && typeof options === "object" ?
          (options.title || options.message || "") :
          (options || "");
        var placeholder = options && typeof options === "object" ?
          (options.placeholder || options.default || "") :
          "";
        return window.prompt(String(title), String(placeholder));
      });
    }
  };

  var share = {
    open: function (options) {
      if (native.available) return native.call("share.open", options);
      if (typeof navigator.share !== "function") {
        return Promise.reject(unsupported("share", "open"));
      }
      return Promise.resolve().then(function () {
        return navigator.share(options || {});
      });
    }
  };

  function writeClipboard(text) {
    text = text == null ? "" : String(text);
    if (native.available) {
      return native.call("clipboard.writeText", { text: text }).then(function () { });
    }
    if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
      return Promise.resolve().then(function () {
        return navigator.clipboard.writeText(text);
      }).then(function () { });
    }
    if (typeof document.execCommand !== "function") {
      return Promise.reject(unsupported("clipboard", "writeText"));
    }
    return Promise.resolve().then(function () {
      var area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.left = "-9999px";
      document.body.appendChild(area);
      try {
        area.select();
        if (document.execCommand("copy") === false) {
          throw unsupported("clipboard", "writeText");
        }
      } finally {
        area.remove();
      }
    });
  }

  var clipboard = {
    writeText: writeClipboard,
    readText: function () {
      if (native.available) return native.call("clipboard.readText");
      if (navigator.clipboard && typeof navigator.clipboard.readText === "function") {
        return Promise.resolve().then(function () {
          return navigator.clipboard.readText();
        });
      }
      return Promise.reject(unsupported("clipboard", "readText"));
    },
    copy: writeClipboard
  };

  function captureCamera(options) {
    if (
      typeof document.createElement !== "function" ||
      typeof window.FileReader === "undefined"
    ) {
      return Promise.reject(unsupported("camera", "capture"));
    }
    return new Promise(function (resolve, reject) {
      var input = document.createElement("input");
      input.type = "file";
      input.accept = (options && options.accept) || "image/*";
      input.setAttribute("capture", (options && options.facingMode) || "environment");
      input.style.display = "none";
      input.addEventListener("change", function () {
        var file = input.files && input.files[0];
        input.remove();
        if (!file) {
          resolve(null);
          return;
        }
        var reader = new window.FileReader();
        reader.onload = function () { resolve(reader.result); };
        reader.onerror = function () { reject(new Error("Camera file read failed")); };
        reader.readAsDataURL(file);
      }, { once: true });
      input.addEventListener("cancel", function () {
        input.remove();
        resolve(null);
      }, { once: true });
      document.body.appendChild(input);
      input.click();
    });
  }

  var camera = {
    capture: function (options) {
      return native.available ?
        native.call("camera.capture", options) :
        captureCamera(options);
    }
  };

  function themeMode() {
    try {
      var saved = window.localStorage.getItem("theme");
      if (saved === "light" || saved === "dark") return saved;
    } catch (_) { }
    return document.documentElement.classList.contains("dark") ? "dark" : "light";
  }

  function resolvedTheme() {
    return document.documentElement.classList.contains("dark") ? "dark" : "light";
  }

  function setTheme(mode) {
    mode = String(mode || "").toLowerCase();
    if (mode !== "light" && mode !== "dark") {
      throw kit.KitworkError(
        "Theme mode must be light or dark",
        "INVALID_ARGUMENT",
        "theme",
        "set",
        { mode: mode }
      );
    }
    try {
      window.localStorage.setItem("theme", mode);
    } catch (_) { }
    document.documentElement.classList.toggle("dark", mode === "dark");
    return mode;
  }

  var theme = {
    set: setTheme,
    toggle: function () {
      return setTheme(resolvedTheme() === "dark" ? "light" : "dark");
    }
  };
  Object.defineProperties(theme, {
    mode: {
      get: themeMode,
      enumerable: true
    },
    resolved: {
      get: function () { return resolvedTheme(); },
      enumerable: true
    }
  });

  var navigation = {
    back: function () {
      window.history.back();
      return true;
    },
    forward: function () {
      window.history.forward();
      return true;
    },
    reload: function () {
      window.location.reload();
      return true;
    }
  };

  kit.service("capabilities", capabilities, {
    expression: ["supports"]
  });
  kit.service("dialog", dialog, {
    expression: ["alert", "confirm", "prompt"]
  });
  kit.service("share", share, {
    expression: ["open"]
  });
  kit.service("clipboard", clipboard, {
    expression: ["writeText", "readText", "copy"]
  });
  kit.service("camera", camera, {
    expression: ["capture"]
  });
  kit.service("theme", theme, {
    expression: ["mode", "resolved", "set", "toggle"]
  });
  kit.service("navigation", navigation, {
    expression: ["back", "forward", "reload"]
  });

  function compatibility(name, value) {
    Object.defineProperty(kit, name, {
      value: value,
      configurable: true,
      writable: true,
      enumerable: false
    });
  }

  compatibility("runtimeInfo", function () {
    return native.available ?
      native.call("runtime.info") :
      Promise.resolve(kit.runtime.info());
  });
  compatibility("supports", function (id) {
    return capabilities.supports(id);
  });
  compatibility("toggleTheme", function () {
    return theme.toggle();
  });
  compatibility("back", function () {
    return navigation.back();
  });
  compatibility("forward", function () {
    return navigation.forward();
  });
  compatibility("reload", function () {
    return navigation.reload();
  });

  kit.module("web", {
    supports: browserSupports,
    captureCamera: captureCamera
  });
})(window, document);
