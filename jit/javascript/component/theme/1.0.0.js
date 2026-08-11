// KitJS component: theme@1.0.0
// Small component state; storage remains a reusable primitive service.
;(function (global, kit) {
  "use strict";

  function modeOf(value) {
    value = String(value || "system").toLowerCase();
    return value === "light" || value === "dark" ? value : "system";
  }

  function preference() {
    if (typeof global.matchMedia !== "function") return null;
    return global.matchMedia("(prefers-color-scheme: dark)");
  }

  kit.component("theme", {
    mode: "system",

    get resolved() {
      if (this.mode === "light" || this.mode === "dark") return this.mode;
      var media = preference();
      return media && media.matches ? "dark" : "light";
    },

    async init() {
      this.mode = modeOf(await kit.storage.get("theme", "system"));
      var component = this;
      var media = preference();
      if (!media) return;
      var onChange = function () {
        if (component.mode === "system") component.$invalidate();
      };
      if (typeof media.addEventListener === "function") media.addEventListener("change", onChange);
      else if (typeof media.addListener === "function") media.addListener(onChange);
      return function () {
        if (typeof media.removeEventListener === "function") media.removeEventListener("change", onChange);
        else if (typeof media.removeListener === "function") media.removeListener(onChange);
      };
    },

    set(mode) {
      this.mode = modeOf(mode);
      return kit.storage.set("theme", this.mode);
    },

    toggle() {
      return this.set(this.resolved === "dark" ? "light" : "dark");
    }
  });
})(globalThis, globalThis.kit);
