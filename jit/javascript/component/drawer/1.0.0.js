;(function () {
"use strict";

function sideValue(value) {
  return value === "left" || value === "right" ? value : "";
}

function currentSide(value) {
  return sideValue(value) || "right";
}

kit.component("drawer", {
  open: false,
  side: "right",

  show: function () {
    this.open = true;
    return true;
  },

  hide: function () {
    this.open = false;
    return false;
  },

  toggle: function () {
    return this.open ? this.hide() : this.show();
  },

  place: function (value) {
    this.side = sideValue(value) || currentSide(this.side);
    return this.side;
  }
});

})();
