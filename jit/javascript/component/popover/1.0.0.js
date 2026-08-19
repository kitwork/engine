;(function () {
"use strict";

function placementValue(value) {
  return value === "top" || value === "right" || value === "bottom" || value === "left" ? value : "";
}

function currentPlacement(value) {
  return placementValue(value) || "bottom";
}

kit.component("popover", {
  open: false,
  placement: "bottom",

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
    this.placement = placementValue(value) || currentPlacement(this.placement);
    return this.placement;
  }
});

})();
