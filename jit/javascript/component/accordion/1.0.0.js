;(function () {
"use strict";

function itemID(value) {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return "";
}

function includes(items, id) {
  return Array.isArray(items) && items.indexOf(id) >= 0;
}

kit.component("accordion", {
  multiple: false,
  openItems: [],

  toggle: function (value) {
    var id = itemID(value);
    if (!id) return false;
    if (this.isOpen(id)) {
      this.collapse(id);
      return false;
    }
    this.expand(id);
    return true;
  },

  expand: function (value) {
    var id = itemID(value);
    if (!id) return false;
    if (!this.multiple) {
      this.openItems = [id];
      return true;
    }
    var current = Array.isArray(this.openItems) ? this.openItems.slice() : [];
    if (!includes(current, id)) current.push(id);
    this.openItems = current;
    return true;
  },

  collapse: function (value) {
    var id = itemID(value);
    if (!id) return false;
    var current = Array.isArray(this.openItems) ? this.openItems : [];
    this.openItems = current.filter(function (item) { return itemID(item) !== id; });
    return false;
  },

  collapseAll: function () {
    this.openItems = [];
  },

  isOpen: function (value) {
    var id = itemID(value);
    if (!id) return false;
    var current = Array.isArray(this.openItems) ? this.openItems : [];
    return current.some(function (item) { return itemID(item) === id; });
  }
});

})();
