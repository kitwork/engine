;(function () {
"use strict";

function values(value) {
  return Array.isArray(value) ? value : [];
}

function indexValue(value, length) {
  value = Number(value);
  return Number.isInteger(value) && value >= 0 && value < length ? value : -1;
}

kit.component("carousel", {
  slides: [],
  active: 0,

  select: function (index) {
    var slides = values(this.slides);
    var selected = indexValue(index, slides.length);
    if (selected < 0) {
      var current = indexValue(this.active, slides.length);
      return current < 0 ? false : current;
    }
    this.active = selected;
    return this.active;
  },

  next: function () {
    var slides = values(this.slides);
    if (!slides.length) return false;
    var current = indexValue(this.active, slides.length);
    this.active = current < 0 || current + 1 >= slides.length ? 0 : current + 1;
    return this.active;
  },

  previous: function () {
    var slides = values(this.slides);
    if (!slides.length) return false;
    var current = indexValue(this.active, slides.length);
    this.active = current <= 0 ? slides.length - 1 : current - 1;
    return this.active;
  },

  first: function () {
    return this.select(0);
  },

  last: function () {
    var slides = values(this.slides);
    return slides.length ? this.select(slides.length - 1) : false;
  },

  isActive: function (index) {
    var slides = values(this.slides);
    var selected = indexValue(index, slides.length);
    return selected >= 0 && selected === indexValue(this.active, slides.length);
  }
});

})();
