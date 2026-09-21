;(function () {
"use strict";

// The data table: rows in, a page of sorted and filtered rows out. The
// component is arithmetic over plain data — it never touches the DOM — and the
// markup draws the table with data-kit-for over visible(). Sorting compares
// numbers as numbers and strings by locale; filtering matches every typed word
// against every field; paging is one-based.
//
//   <table data-kit-component="data-table" data-kit-scope="rows: […], pageSize: 5">
//     <th aria-sort="…" data-kit-bind:aria-sort="direction('name')">
//       <button data-kit-click="sortBy('name')">Name</button>
//     <template data-kit-for="row of visible()">…</template>

function list(value) {
  return Array.isArray(value) ? value : [];
}

function text(value) {
  return typeof value === "string" ? value : "";
}

function positive(value, fallback) {
  value = Number(value);
  return Number.isInteger(value) && value > 0 ? value : fallback;
}

function comparable(value) {
  if (typeof value === "number") return value;
  if (typeof value === "boolean") return value ? 1 : 0;
  if (value === null || value === undefined) return "";
  var number = Number(value);
  if (typeof value === "string" && value.trim() !== "" && Number.isFinite(number)) return number;
  return String(value);
}

function compare(a, b) {
  var left = comparable(a);
  var right = comparable(b);
  if (typeof left === "number" && typeof right === "number") return left - right;
  return String(left).localeCompare(String(right), undefined, { numeric: true, sensitivity: "base" });
}

function matches(row, words) {
  if (!words.length) return true;
  var body = Object.keys(row || {}).map(function (key) { return String(row[key] === null || row[key] === undefined ? "" : row[key]); })
    .join(" ").toLocaleLowerCase();
  return words.every(function (word) { return body.indexOf(word) >= 0; });
}

function filteredRows(scope) {
  var query = text(scope.query).trim().toLocaleLowerCase();
  var words = query ? query.split(/\s+/) : [];
  return list(scope.rows).filter(function (row) { return matches(row, words); });
}

function sortedRows(scope) {
  var rows = filteredRows(scope);
  var key = text(scope.sortKey);
  if (!key) return rows;
  var direction = scope.sortDir === "desc" ? -1 : 1;
  return rows.slice().sort(function (a, b) {
    return compare(a ? a[key] : undefined, b ? b[key] : undefined) * direction;
  });
}

function pageCount(scope) {
  var size = positive(scope.pageSize, 10);
  return Math.max(1, Math.ceil(filteredRows(scope).length / size));
}

function currentPage(scope) {
  return Math.min(positive(scope.page, 1), pageCount(scope));
}

kit.component("data-table", {
  rows: [],
  sortKey: "",
  sortDir: "asc",
  query: "",
  page: 1,
  pageSize: 10,

  sortBy: function (key) {
    key = text(key);
    if (!key) return this.sortKey;
    if (this.sortKey === key) this.sortDir = this.sortDir === "asc" ? "desc" : "asc";
    else {
      this.sortKey = key;
      this.sortDir = "asc";
    }
    this.page = 1; // a new order starts from the top
    return this.sortKey;
  },

  isSorted: function (key) {
    return text(key) !== "" && this.sortKey === text(key);
  },

  // aria-sort wants "ascending", "descending" or "none".
  direction: function (key) {
    if (!this.isSorted(key)) return "none";
    return this.sortDir === "desc" ? "descending" : "ascending";
  },

  search: function (value) {
    this.query = text(value);
    this.page = 1;
    return filteredRows(this).length;
  },

  filtered: function () { return filteredRows(this); },
  sorted: function () { return sortedRows(this); },

  visible: function () {
    var size = positive(this.pageSize, 10);
    var start = (currentPage(this) - 1) * size;
    return sortedRows(this).slice(start, start + size);
  },

  total: function () { return filteredRows(this).length; },
  pages: function () { return pageCount(this); },
  current: function () { return currentPage(this); },

  // "1–10 of 42", as parts the markup can print.
  from: function () {
    if (!filteredRows(this).length) return 0;
    return (currentPage(this) - 1) * positive(this.pageSize, 10) + 1;
  },
  to: function () {
    return Math.min(currentPage(this) * positive(this.pageSize, 10), filteredRows(this).length);
  },

  goTo: function (page) {
    page = positive(page, 1);
    this.page = Math.min(page, pageCount(this));
    return this.page;
  },
  next: function () { return this.goTo(currentPage(this) + 1); },
  previous: function () { return this.goTo(currentPage(this) - 1); },
  hasNext: function () { return currentPage(this) < pageCount(this); },
  hasPrevious: function () { return currentPage(this) > 1; }
});

})();
