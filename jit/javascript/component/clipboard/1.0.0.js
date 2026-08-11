// KitJS component: clipboard@1.0.0
;(function (kit) {
  "use strict";

  var revisions = new WeakMap();

  function nextRevision(instance) {
    var revision = (revisions.get(instance) || 0) + 1;
    revisions.set(instance, revision);
    return revision;
  }

  kit.component("clipboard", {
    copied: false,
    error: "",

    init() {
      var instance = this;
      revisions.set(instance, 0);
      return function () { revisions.delete(instance); };
    },

    async copy(value) {
      var text = value === null || value === undefined ? "" : String(value);
      var revision = nextRevision(this);
      this.copied = false;
      this.error = "";
      if (!text) return false;
      try {
        await kit.clipboard.writeText(text);
        if (revisions.get(this) !== revision) return false;
        this.copied = true;
        return true;
      } catch (error) {
        if (revisions.get(this) !== revision) return false;
        this.error = String(error && error.message || error);
        throw error;
      }
    },

    reset() {
      nextRevision(this);
      this.copied = false;
      this.error = "";
    }
  });
})(globalThis.kit);
