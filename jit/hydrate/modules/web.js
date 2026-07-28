// Browser-backed capabilities that share the same $app surface as native shells.
(function (window, document) {
  "use strict";

  var kitwork = window.kitwork;
  if (!kitwork || !kitwork.module || kitwork.has("web")) return;

  var native = kitwork.module("native");

  function supports(capability) {
    var name = String(capability || "");
    if (name === "storage" || name === "dialog" || name === "theme") return true;
    if (name === "clipboard") return !!(navigator.clipboard && navigator.clipboard.writeText);
    if (name === "share") return !!navigator.share;
    if (name === "camera") return typeof FileReader !== "undefined";
    return false;
  }

  kitwork.runtimeInfo = function () {
    return native.available ?
      native.call("runtime.info") :
      Promise.resolve(kitwork.runtime.info());
  };
  kitwork.supports = function (capability) {
    return native.available ?
      native.call("runtime.supports", { capability: capability }) :
      Promise.resolve(supports(capability));
  };

  kitwork.dialog = {
    alert: function (options) {
      if (native.available) return native.call("dialog.alert", options || {});
      alert((options && options.message) || options || "");
      return Promise.resolve(true);
    },
    confirm: function (options) {
      if (native.available) return native.call("dialog.confirm", options || {});
      return Promise.resolve(confirm((options && options.message) || options || ""));
    },
    prompt: function (options) {
      if (native.available) return native.call("dialog.prompt", options || {});
      return Promise.resolve(prompt(
        (options && options.title) || "",
        (options && options.placeholder) || ""
      ));
    }
  };
  kitwork.share = {
    open: function (options) {
      if (navigator.share) return navigator.share(options);
      return native.call("share.open", options);
    }
  };

  kitwork.clipboard = function (text) {
    text = text == null ? "" : String(text);
    if (native.available) {
      native.call("clipboard.write", { text: text }).catch(function () { });
      return true;
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text);
      return true;
    }
    var area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.left = "-9999px";
    document.body.appendChild(area);
    area.select();
    try { document.execCommand("copy"); } catch (_) { }
    area.remove();
    return true;
  };
  kitwork.clipboard.writeText = function (text) {
    return kitwork.clipboard(text);
  };
  kitwork.clipboard.readText = function () {
    return navigator.clipboard ?
      navigator.clipboard.readText() :
      Promise.reject(kitwork.KitworkError(
        "Clipboard read is unsupported",
        "UNSUPPORTED",
        "clipboard",
        "read"
      ));
  };

  function captureCamera(options) {
    return new Promise(function (resolve, reject) {
      var input = document.createElement("input");
      input.type = "file";
      input.accept = (options && options.accept) || "image/*";
      input.setAttribute("capture", (options && options.facingMode) || "environment");
      input.style.display = "none";
      input.addEventListener("change", function () {
        var file = input.files && input.files[0];
        input.remove();
        if (!file) { resolve(null); return; }
        var reader = new FileReader();
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

  kitwork.camera = function (key) {
    var request = native.available ? native.call("camera.capture") : captureCamera();
    request.then(function (uri) {
      if (uri) kitwork.set(key, uri);
    }).catch(function (error) {
      kitwork.set(key + "_error", String((error && error.message) || error));
    });
    return true;
  };
  kitwork.camera.capture = function (options) {
    return native.available ? native.call("camera.capture", options) : captureCamera(options);
  };

  Object.defineProperty(kitwork, "theme", {
    get: function () {
      var theme = localStorage.getItem("theme");
      if (theme) return theme;
      return document.documentElement.classList.contains("dark") ? "dark" : "light";
    },
    set: function (value) {
      var dark = value === "dark";
      document.documentElement.classList.toggle("dark", dark);
      try { localStorage.setItem("theme", dark ? "dark" : "light"); } catch (_) { }
    },
    configurable: true,
    enumerable: true
  });
  kitwork.toggleTheme = function () {
    kitwork.theme = kitwork.theme === "light" ? "dark" : "light";
  };
  kitwork.back = function () { history.back(); };
  kitwork.forward = function () { history.forward(); };
  kitwork.reload = function () { location.reload(); };

  kitwork.module("web", { supports: supports, captureCamera: captureCamera });
})(window, document);
