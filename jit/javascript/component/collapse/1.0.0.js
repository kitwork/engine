;(function () {
"use strict";

kit.component("collapse", {
  open: false,
  disabled: false,

  show: function () {
    if (this.disabled) return Boolean(this.open);
    this.open = true;
    return true;
  },

  hide: function () {
    if (this.disabled) return Boolean(this.open);
    this.open = false;
    return false;
  },

  toggle: function () {
    return this.open ? this.hide() : this.show();
  }
});

})();
