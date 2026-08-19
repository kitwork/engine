;(function () {
"use strict";

kit.component("theme", {
  mode: "system",
  resolved: "light",

  set: function (mode) {
    return kit.appearance.set(mode);
  },

  toggle: function () {
    return kit.appearance.toggle();
  },

  system: function () {
    return kit.appearance.system();
  },

  init: function () {
    var scope = this;
    return kit.appearance.subscribe(function (appearance) {
      scope.mode = appearance.mode;
      scope.resolved = appearance.resolved;
    });
  }
});

})();
