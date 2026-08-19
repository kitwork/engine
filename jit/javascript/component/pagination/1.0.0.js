;(function () {
"use strict";

function pageCount(value) {
  value = Number(value);
  return Number.isFinite(value) && value >= 1 ? Math.floor(value) : 1;
}

function pageValue(value, pages) {
  value = Number(value);
  if (!Number.isFinite(value)) return null;
  value = Math.floor(value);
  return Math.max(1, Math.min(pageCount(pages), value));
}

function currentPage(value, pages) {
  var page = pageValue(value, pages);
  return page === null ? 1 : page;
}

kit.component("pagination", {
  page: 1,
  pages: 1,

  select: function (value) {
    var selected = pageValue(value, this.pages);
    if (selected === null) return currentPage(this.page, this.pages);
    this.page = selected;
    return this.page;
  },

  next: function () {
    return this.select(currentPage(this.page, this.pages) + 1);
  },

  previous: function () {
    return this.select(currentPage(this.page, this.pages) - 1);
  },

  first: function () {
    return this.select(1);
  },

  last: function () {
    return this.select(pageCount(this.pages));
  },

  canPrevious: function () {
    return currentPage(this.page, this.pages) > 1;
  },

  canNext: function () {
    return currentPage(this.page, this.pages) < pageCount(this.pages);
  }
});

})();
