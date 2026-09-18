;(function () {
"use strict";

// The calendar: one month as a grid of 42 cells, and the arithmetic around it.
// The component never touches the DOM — the markup draws the grid with
// data-kit-for over days() and the labels from weekdays() and title(). A day
// is a plain ISO string, "2026-09-18", so it binds, compares and posts as text.
//
//   <div data-kit-component="calendar" data-kit-scope="selected: '2026-09-18'">
//     <button data-kit-click="previous()">‹</button>
//     <span data-kit-text="title()"></span>
//     <template data-kit-for="cell of days()" data-kit-key="cell.iso">
//       <button data-kit-click="select(cell.iso)" data-kit-text="cell.day"></button>
//
// year and month left at 0 mean "the month that holds selected, else today".

var ISO = /^(\d{4})-(\d{2})-(\d{2})$/;

function pad(value) {
  return value < 10 ? "0" + value : String(value);
}

function iso(date) {
  return date.getFullYear() + "-" + pad(date.getMonth() + 1) + "-" + pad(date.getDate());
}

function parse(value) {
  var m = typeof value === "string" ? ISO.exec(value) : null;
  if (!m) return null;
  var date = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return iso(date) === value ? date : null;
}

function integer(value, fallback) {
  value = Number(value);
  return Number.isInteger(value) ? value : fallback;
}

function locale(scope) {
  return typeof scope.locale === "string" && scope.locale ? scope.locale : undefined;
}

function weekStart(scope) {
  var day = integer(scope.weekStart, 1);
  return day >= 0 && day <= 6 ? day : 1;
}

// The month on show: the seeded year/month, else the month of the selected day, else this month.
function shown(scope) {
  var year = integer(scope.year, 0);
  var month = integer(scope.month, 0);
  if (year > 0 && month >= 1 && month <= 12) return { year: year, month: month };
  var anchor = parse(scope.selected) || new Date();
  return { year: anchor.getFullYear(), month: anchor.getMonth() + 1 };
}

function within(scope, value) {
  var min = typeof scope.min === "string" && parse(scope.min) ? scope.min : "";
  var max = typeof scope.max === "string" && parse(scope.max) ? scope.max : "";
  if (min && value < min) return false;
  if (max && value > max) return false;
  return true;
}

function format(scope, date, options) {
  try {
    return new Intl.DateTimeFormat(locale(scope), options).format(date);
  } catch (_) {
    return iso(date);
  }
}

function move(scope, months) {
  var current = shown(scope);
  var date = new Date(current.year, current.month - 1 + months, 1);
  scope.year = date.getFullYear();
  scope.month = date.getMonth() + 1;
  return scope.year + "-" + pad(scope.month);
}

kit.component("calendar", {
  year: 0,
  month: 0,
  selected: "",
  min: "",
  max: "",
  weekStart: 1,
  locale: "",

  // "September 2026", in the reader's language.
  title: function () {
    var current = shown(this);
    return format(this, new Date(current.year, current.month - 1, 1), { month: "long", year: "numeric" });
  },

  // Seven short names starting on weekStart: Mon … Sun by default.
  weekdays: function () {
    var start = weekStart(this);
    var names = [];
    for (var i = 0; i < 7; i++) {
      // 2024-09-01 was a Sunday, so day + offset lands on the wanted weekday.
      names.push(format(this, new Date(2024, 8, 1 + ((start + i) % 7)), { weekday: "short" }));
    }
    return names;
  },

  // Six weeks of cells, so the grid never changes height between months.
  days: function () {
    var current = shown(this);
    var first = new Date(current.year, current.month - 1, 1);
    var offset = (first.getDay() - weekStart(this) + 7) % 7;
    var today = iso(new Date());
    var selected = typeof this.selected === "string" ? this.selected : "";
    var cells = [];
    for (var i = 0; i < 42; i++) {
      var date = new Date(current.year, current.month - 1, 1 - offset + i);
      var value = iso(date);
      var weekday = date.getDay();
      cells.push({
        iso: value,
        day: date.getDate(),
        outside: date.getMonth() + 1 !== current.month,
        today: value === today,
        selected: value === selected,
        disabled: !within(this, value),
        weekend: weekday === 0 || weekday === 6,
        label: format(this, date, { weekday: "long", day: "numeric", month: "long", year: "numeric" })
      });
    }
    return cells;
  },

  next: function () { return move(this, 1); },
  previous: function () { return move(this, -1); },
  today: function () {
    var now = new Date();
    this.year = now.getFullYear();
    this.month = now.getMonth() + 1;
    return iso(now);
  },
  go: function (year, month) {
    year = integer(year, 0);
    month = integer(month, 0);
    if (year <= 0 || month < 1 || month > 12) return false;
    this.year = year;
    this.month = month;
    return year + "-" + pad(month);
  },

  // Choose a day; a day outside min…max is refused, and choosing a day of a
  // neighbouring month turns the page to it.
  select: function (value) {
    var date = parse(value);
    if (!date || !within(this, value)) return this.selected;
    this.selected = value;
    this.year = date.getFullYear();
    this.month = date.getMonth() + 1;
    return this.selected;
  },
  clear: function () {
    this.selected = "";
    return "";
  },
  isSelected: function (value) {
    return typeof value === "string" && value !== "" && this.selected === value;
  },
  hasSelection: function () {
    return !!parse(this.selected);
  },

  // The selected day as a sentence — "Friday, 18 September 2026" — or "" with none.
  format: function (value) {
    var date = parse(arguments.length ? value : this.selected);
    if (!date) return "";
    return format(this, date, { weekday: "long", day: "numeric", month: "long", year: "numeric" });
  }
});

})();
