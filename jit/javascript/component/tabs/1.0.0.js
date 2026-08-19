;(function () {
"use strict";

function tabID(value) {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return "";
}

function tabIDs(value) {
  if (!Array.isArray(value)) return [];
  return value.map(tabID).filter(function (id) { return id !== ""; });
}

kit.component("tabs", {
  tabs: [],
  active: "",

  select: function (value) {
    var id = tabID(value);
    var items = tabIDs(this.tabs);
    if (!id || items.indexOf(id) < 0) return this.active;
    this.active = id;
    return id;
  },

  next: function () {
    var items = tabIDs(this.tabs);
    if (!items.length) return this.active;
    var index = items.indexOf(tabID(this.active));
    this.active = items[index < 0 || index + 1 >= items.length ? 0 : index + 1];
    return this.active;
  },

  previous: function () {
    var items = tabIDs(this.tabs);
    if (!items.length) return this.active;
    var index = items.indexOf(tabID(this.active));
    this.active = items[index <= 0 ? items.length - 1 : index - 1];
    return this.active;
  },

  first: function () {
    var items = tabIDs(this.tabs);
    if (items.length) this.active = items[0];
    return this.active;
  },

  last: function () {
    var items = tabIDs(this.tabs);
    if (items.length) this.active = items[items.length - 1];
    return this.active;
  },

  isActive: function (value) {
    var id = tabID(value);
    return id !== "" && tabID(this.active) === id;
  }
});

})();
