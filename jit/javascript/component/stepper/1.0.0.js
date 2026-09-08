;(function () {
"use strict";

function number(value, fallback) {
  value = Number(value);
  return Number.isFinite(value) ? value : fallback;
}

function stepSize(value) {
  value = Number(value);
  return Number.isFinite(value) && value > 0 ? value : 1;
}

function bounds(scope) {
  var min = number(scope.min, -Infinity);
  var max = number(scope.max, Infinity);
  return max < min ? { min: max, max: min } : { min: min, max: max };
}

function clamp(scope, value) {
  var range = bounds(scope);
  value = number(value, number(scope.value, 0));
  if (value < range.min) return range.min;
  if (value > range.max) return range.max;
  return value;
}

kit.component("stepper", {
  value: 0,
  min: 0,
  max: 100,
  step: 1,
  disabled: false,

  set: function (next) {
    if (this.disabled) return number(this.value, 0);
    this.value = clamp(this, next);
    return this.value;
  },

  increment: function () {
    return this.set(number(this.value, 0) + stepSize(this.step));
  },

  decrement: function () {
    return this.set(number(this.value, 0) - stepSize(this.step));
  },

  canIncrement: function () {
    return !this.disabled && number(this.value, 0) < bounds(this).max;
  },

  canDecrement: function () {
    return !this.disabled && number(this.value, 0) > bounds(this).min;
  }
});

})();
