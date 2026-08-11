// KitJS component: announce@1.0.0
// Small state facade over the exact announce service dependency.
kit.component("announce", {
    message: "",
    mode: "polite",

    speak: function (message, mode) {
      this.message = String(message === null || message === undefined ? "" : message).trim();
      this.mode = mode === "assertive" || mode === "urgent" ? "assertive" : "polite";
      return kit.announce.say(this.message, this.mode);
    },

    clear: function () {
      this.message = "";
      this.mode = "polite";
      return kit.announce.clear();
    }
});
