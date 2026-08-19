;(function () {
"use strict";

function values(value) {
  return Array.isArray(value) ? value : [];
}

function validIndex(value, length) {
  value = Number(value);
  return Number.isInteger(value) && value >= 0 && value < length ? value : -1;
}

function selection(value) {
  if (value === null || value === undefined) return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return value;
  }
  return "";
}

kit.component("dropdown", {
  open: false,
  items: [],
  activeIndex: -1,
  selected: "",

  show: function () {
    var items = values(this.items);
    this.open = true;
    if (validIndex(this.activeIndex, items.length) < 0 && items.length) this.activeIndex = 0;
    return true;
  },

  hide: function () {
    this.open = false;
    this.activeIndex = -1;
    return false;
  },

  toggle: function () {
    return this.open ? this.hide() : this.show();
  },

  next: function () {
    var items = values(this.items);
    if (!items.length) return -1;
    var index = validIndex(this.activeIndex, items.length);
    this.open = true;
    this.activeIndex = index < 0 || index + 1 >= items.length ? 0 : index + 1;
    return this.activeIndex;
  },

  previous: function () {
    var items = values(this.items);
    if (!items.length) return -1;
    var index = validIndex(this.activeIndex, items.length);
    this.open = true;
    this.activeIndex = index <= 0 ? items.length - 1 : index - 1;
    return this.activeIndex;
  },

  choose: function (value) {
    this.selected = selection(value);
    this.hide();
    return this.selected;
  },

  isActive: function (index) {
    var items = values(this.items);
    return validIndex(index, items.length) === validIndex(this.activeIndex, items.length) &&
      validIndex(index, items.length) >= 0;
  }
});

})();
