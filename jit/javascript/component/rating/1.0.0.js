;(function () {
"use strict";

function count(value, fallback) {
  value = Number(value);
  if (!Number.isFinite(value)) return fallback;
  value = Math.floor(value);
  return value > 0 ? value : fallback;
}

function level(value, max) {
  value = Number(value);
  if (!Number.isFinite(value)) return 0;
  value = Math.floor(value);
  if (value < 0) return 0;
  if (value > max) return max;
  return value;
}

kit.component("rating", {
  value: 0,
  max: 5,
  readonly: false,
  hovered: 0,

  rate: function (star) {
    if (this.readonly) return level(this.value, count(this.max, 5));
    this.value = level(star, count(this.max, 5));
    return this.value;
  },

  hover: function (star) {
    if (this.readonly) return this.hovered;
    this.hovered = level(star, count(this.max, 5));
    return this.hovered;
  },

  clear: function () {
    if (this.readonly) return this.value;
    this.value = 0;
    this.hovered = 0;
    return this.value;
  },

  current: function () {
    var max = count(this.max, 5);
    return this.hovered > 0 ? level(this.hovered, max) : level(this.value, max);
  },

  isFilled: function (star) {
    star = Number(star);
    if (!Number.isInteger(star) || star < 1) return false;
    return star <= this.current();
  }
});

})();
