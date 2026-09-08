;(function () {
"use strict";

function label(value) {
  if (typeof value === "string") return value.trim();
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return "";
}

function list(value) {
  return Array.isArray(value) ? value : [];
}

function limit(value) {
  value = Number(value);
  return Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

kit.component("tags", {
  tags: [],
  draft: "",
  max: 0,

  add: function (value) {
    var next = label(value === undefined ? this.draft : value);
    if (!next || !this.canAdd() || this.has(next)) return false;
    this.tags = list(this.tags).concat([next]);
    this.draft = "";
    return true;
  },

  remove: function (value) {
    var target = label(value);
    this.tags = list(this.tags).filter(function (tag) { return label(tag) !== target; });
    return false;
  },

  removeLast: function () {
    var current = list(this.tags);
    if (!current.length) return false;
    this.tags = current.slice(0, current.length - 1);
    return true;
  },

  clear: function () {
    this.tags = [];
    return true;
  },

  has: function (value) {
    var target = label(value);
    if (!target) return false;
    return list(this.tags).some(function (tag) { return label(tag) === target; });
  },

  canAdd: function () {
    var cap = limit(this.max);
    return cap === 0 || list(this.tags).length < cap;
  }
});

})();
