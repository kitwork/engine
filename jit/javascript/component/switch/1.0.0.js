;(function () {
"use strict";

kit.component("switch", {
  checked: false,
  disabled: false,

  toggle: function () {
    if (this.disabled) return Boolean(this.checked);
    this.checked = !this.checked;
    return this.checked;
  },

  on: function () {
    return this.set(true);
  },

  off: function () {
    return this.set(false);
  },

  set: function (value) {
    if (this.disabled) return Boolean(this.checked);
    this.checked = Boolean(value);
    return this.checked;
  }
});

})();
