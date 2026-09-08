;(function () {
"use strict";

function options(value) {
  return Array.isArray(value) ? value : [];
}

function text(value) {
  if (value === null || value === undefined) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  if (typeof value === "boolean") return String(value);
  return "";
}

function selection(value) {
  if (value === null || value === undefined) return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return value;
  }
  return "";
}

function validIndex(value, length) {
  value = Number(value);
  return Number.isInteger(value) && value >= 0 && value < length ? value : -1;
}

kit.component("combobox", {
  open: false,
  query: "",
  options: [],
  activeIndex: -1,
  selected: "",

  matches: function (option) {
    var needle = text(this.query).trim().toLocaleLowerCase();
    if (!needle) return true;
    return text(option).toLocaleLowerCase().indexOf(needle) >= 0;
  },

  filtered: function () {
    var scope = this;
    return options(this.options).filter(function (option) { return scope.matches(option); });
  },

  search: function () {
    this.open = true;
    this.activeIndex = -1;
    return this.query;
  },

  show: function () {
    var results = this.filtered();
    this.open = true;
    if (validIndex(this.activeIndex, results.length) < 0 && results.length) this.activeIndex = 0;
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
    var results = this.filtered();
    if (!results.length) return -1;
    var index = validIndex(this.activeIndex, results.length);
    this.open = true;
    this.activeIndex = index < 0 || index + 1 >= results.length ? 0 : index + 1;
    return this.activeIndex;
  },

  previous: function () {
    var results = this.filtered();
    if (!results.length) return -1;
    var index = validIndex(this.activeIndex, results.length);
    this.open = true;
    this.activeIndex = index <= 0 ? results.length - 1 : index - 1;
    return this.activeIndex;
  },

  choose: function (value) {
    this.selected = selection(value);
    this.query = text(this.selected);
    this.hide();
    return this.selected;
  },

  chooseActive: function () {
    var results = this.filtered();
    var index = validIndex(this.activeIndex, results.length);
    if (index < 0) return this.selected;
    return this.choose(results[index]);
  },

  isActive: function (index) {
    var results = this.filtered();
    return validIndex(index, results.length) === validIndex(this.activeIndex, results.length) &&
      validIndex(index, results.length) >= 0;
  }
});

})();
