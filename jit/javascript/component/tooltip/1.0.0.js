;(function () {
"use strict";

kit.component("tooltip", {
  open: false,
  content: "",

  show: function (content) {
    if (typeof content === "string") this.content = content;
    this.open = true;
    return true;
  },

  hide: function () {
    this.open = false;
    return false;
  },

  toggle: function () {
    return this.open ? this.hide() : this.show();
  }
});

})();
