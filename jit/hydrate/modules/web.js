// Browser-backed capabilities that share the same $app surface as native shells.
(function (window, document) {
  "use strict";

  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || !kit.module || kit.has("web")) return;

  var native = kit.module("native");

  function supports(capability) {
    var name = String(capability || "");
    if (name === "storage" || name === "dialog" || name === "theme") return true;
    if (name === "clipboard") return !!(navigator.clipboard && navigator.clipboard.writeText);
    if (name === "share") return !!navigator.share;
    if (name === "camera") return typeof FileReader !== "undefined";
    return false;
  }

  kit.runtimeInfo = function () {
    return native.available ?
      native.call("runtime.info") :
      Promise.resolve(kit.runtime.info());
  };
  kit.supports = function (capability) {
    return native.available ?
      native.call("runtime.supports", { capability: capability }) :
      Promise.resolve(supports(capability));
  };

  kit.dialog = {
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
  kit.share = {
    open: function (options) {
      if (navigator.share) return navigator.share(options);
      return native.call("share.open", options);
    }
  };

  kit.clipboard = function (text) {
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
  kit.clipboard.writeText = function (text) {
    return kit.clipboard(text);
  };
  kit.clipboard.readText = function () {
    return navigator.clipboard ?
      navigator.clipboard.readText() :
      Promise.reject(kit.KitworkError(
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

  kit.camera = function (key) {
    var request = native.available ? native.call("camera.capture") : captureCamera();
    request.then(function (uri) {
      if (uri) kit.set(key, uri);
    }).catch(function (error) {
      kit.set(key + "_error", String((error && error.message) || error));
    });
    return true;
  };
  kit.camera.capture = function (options) {
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
  kit.toggleTheme = function () {
    kit.theme = kit.theme === "light" ? "dark" : "light";
  };
  kit.back = function () { history.back(); };
  kit.forward = function () { history.forward(); };
  kit.reload = function () { location.reload(); };

  kit.module("web", { supports: supports, captureCamera: captureCamera });
})(window, document);
