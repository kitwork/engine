;(function () {
"use strict";

function result(value) {
  if (value === null || value === undefined) return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return "";
}

kit.component("dialog", {
  open: false,
  returnValue: "",

  show: function () {
    this.returnValue = "";
    this.open = true;
    return true;
  },

  close: function (value) {
    this.returnValue = result(value);
    this.open = false;
    return false;
  },

  toggle: function () {
    if (this.open) return this.close("");
    return this.show();
  }
});

})();
