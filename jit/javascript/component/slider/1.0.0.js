;(function () {
"use strict";

function number(value, fallback) {
  value = Number(value);
  return Number.isFinite(value) ? value : fallback;
}

function positive(value, fallback) {
  value = Number(value);
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

function bounds(scope) {
  var min = number(scope.min, 0);
  var max = number(scope.max, 100);
  return max < min ? { min: max, max: min } : { min: min, max: max };
}

function clamp(scope, value) {
  var range = bounds(scope);
  value = number(value, number(scope.value, range.min));
  if (value < range.min) return range.min;
  if (value > range.max) return range.max;
  return value;
}

kit.component("slider", {
  value: 0,
  min: 0,
  max: 100,
  step: 1,
  page: 10,
  disabled: false,

  set: function (next) {
    if (this.disabled) return number(this.value, 0);
    this.value = clamp(this, next);
    return this.value;
  },

  increment: function () {
    return this.set(number(this.value, 0) + positive(this.step, 1));
  },

  decrement: function () {
    return this.set(number(this.value, 0) - positive(this.step, 1));
  },

  pageUp: function () {
    return this.set(number(this.value, 0) + positive(this.page, positive(this.step, 1)));
  },

  pageDown: function () {
    return this.set(number(this.value, 0) - positive(this.page, positive(this.step, 1)));
  },

  toStart: function () {
    return this.set(bounds(this).min);
  },

  toEnd: function () {
    return this.set(bounds(this).max);
  },

  percent: function () {
    var range = bounds(this);
    var span = range.max - range.min;
    if (span <= 0) return 0;
    var ratio = (clamp(this, this.value) - range.min) / span * 100;
    if (ratio < 0) return 0;
    if (ratio > 100) return 100;
    return ratio;
  }
});

})();
