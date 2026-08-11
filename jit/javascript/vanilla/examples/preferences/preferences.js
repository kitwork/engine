;(function () {
"use strict";

kit.component("preferences", {
  mode: "system",
  message: "Loading saved preference…",

  init: async function () {
    this.mode = await kit.storage.get("theme", "system");
    this.message = "Preference ready";
  },

  choose: async function (mode) {
    var stored = await kit.storage.set("theme", mode);
    if (!stored) {
      this.message = "Could not save preference";
      return;
    }
    this.mode = await kit.storage.get("theme", "system");
    this.message = "Saved " + this.mode;
  },

  reset: async function () {
    await kit.storage.remove("theme");
    this.mode = await kit.storage.get("theme", "system");
    this.message = "Reset to system";
  }
});

})();
