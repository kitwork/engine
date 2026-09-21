;(function () {
"use strict";

// The chart: numbers in, geometry out. The component is arithmetic over a
// series — it never touches the DOM — and the markup draws it: bars as HTML
// boxes from bars(), a line and its fill as SVG paths from line() and area(),
// grid lines from grid(), the axis from ticks(). Everything is in the
// viewBox's own units, width by height, so the SVG scales with its box.
//
//   <div data-kit-component="chart" data-kit-scope="series: [4, 8, 6]; labels: ['Mon', 'Tue', 'Wed']">
//     <svg data-kit-bind:viewbox="viewBox()"><path data-kit-bind:d="line()" /></svg>
//     <template data-kit-for="bar of bars()"><div data-kit-style="height: bar.percent + '%';"></div></template>

function list(value) {
  return Array.isArray(value) ? value : [];
}

function number(value, fallback) {
  value = Number(value);
  return Number.isFinite(value) ? value : fallback;
}

function values(scope) {
  return list(scope.series).map(function (entry) {
    if (entry && typeof entry === "object") return number(entry.value, 0);
    return number(entry, 0);
  });
}

function labelOf(scope, index) {
  var entry = list(scope.series)[index];
  if (entry && typeof entry === "object" && typeof entry.label === "string") return entry.label;
  var labels = list(scope.labels);
  return typeof labels[index] === "string" ? labels[index] : String(index + 1);
}

// The vertical range: min and max from the scope when set, else 0 up to the
// largest value (a bar chart that does not start at zero lies).
// null, undefined and "" mean "not set" — Number(null) would be a silent 0.
function optional(value) {
  if (value === null || value === undefined || value === "") return NaN;
  return number(value, NaN);
}

function range(scope) {
  var data = values(scope);
  var top = optional(scope.max);
  var bottom = optional(scope.min);
  if (!Number.isFinite(top)) top = data.length ? Math.max.apply(null, data.concat([0])) : 1;
  if (!Number.isFinite(bottom)) bottom = data.length ? Math.min.apply(null, data.concat([0])) : 0;
  if (top <= bottom) top = bottom + 1;
  return { min: bottom, max: top };
}

function box(scope) {
  var width = number(scope.width, 320);
  var height = number(scope.height, 120);
  var padding = number(scope.padding, 8);
  return { width: width > 0 ? width : 320, height: height > 0 ? height : 120, padding: padding >= 0 ? padding : 8 };
}

function round(value) {
  return Math.round(value * 100) / 100;
}

// x for the index-th point, y for a value, inside the padded box.
function plotX(scope, index, count) {
  var b = box(scope);
  if (count <= 1) return round(b.width / 2);
  return round(b.padding + (index / (count - 1)) * (b.width - b.padding * 2));
}

function plotY(scope, value) {
  var b = box(scope);
  var r = range(scope);
  var ratio = (value - r.min) / (r.max - r.min);
  return round(b.height - b.padding - ratio * (b.height - b.padding * 2));
}

function pointsOf(scope) {
  var data = values(scope);
  return data.map(function (value, index) {
    return { x: plotX(scope, index, data.length), y: plotY(scope, value), value: value, label: labelOf(scope, index), index: index };
  });
}

kit.component("chart", {
  series: [],
  labels: [],
  min: null,
  max: null,
  width: 320,
  height: 120,
  padding: 8,

  values: function () { return values(this); },
  count: function () { return values(this).length; },
  total: function () { return values(this).reduce(function (sum, value) { return sum + value; }, 0); },
  average: function () {
    var data = values(this);
    return data.length ? round(this.total() / data.length) : 0;
  },
  peak: function () {
    var data = values(this);
    return data.length ? Math.max.apply(null, data) : 0;
  },
  last: function () {
    var data = values(this);
    return data.length ? data[data.length - 1] : 0;
  },
  // The change from the first value to the last, as a percentage; 0 with fewer than two.
  change: function () {
    var data = values(this);
    if (data.length < 2 || data[0] === 0) return 0;
    return round(((data[data.length - 1] - data[0]) / Math.abs(data[0])) * 100);
  },
  // The range actually drawn — min/max when set, else 0 up to the peak.
  high: function () { return range(this).max; },
  low: function () { return range(this).min; },
  viewBox: function () {
    var b = box(this);
    return "0 0 " + b.width + " " + b.height;
  },

  // For HTML bars: percent is the height inside the range; x/y/width/height for SVG rects.
  bars: function () {
    var scope = this;
    var data = values(scope);
    var r = range(scope);
    var b = box(scope);
    var slot = data.length ? (b.width - b.padding * 2) / data.length : 0;
    return data.map(function (value, index) {
      var percent = round(((value - r.min) / (r.max - r.min)) * 100);
      var y = plotY(scope, value);
      return {
        index: index,
        label: labelOf(scope, index),
        value: value,
        percent: Math.max(0, Math.min(100, percent)),
        x: round(b.padding + slot * index + slot * 0.15),
        y: y,
        width: round(slot * 0.7),
        height: round(b.height - b.padding - y)
      };
    });
  },

  points: function () { return pointsOf(this); },

  // "x,y x,y …" for <polyline points>.
  polyline: function () {
    return pointsOf(this).map(function (p) { return p.x + "," + p.y; }).join(" ");
  },

  // "M x y L x y …" for <path d>; "" with no data.
  line: function () {
    var points = pointsOf(this);
    if (!points.length) return "";
    return points.map(function (p, i) { return (i ? "L" : "M") + p.x + " " + p.y; }).join(" ");
  },

  // The line closed down to the baseline, for a fill under it.
  area: function () {
    var points = pointsOf(this);
    if (!points.length) return "";
    var base = plotY(this, range(this).min);
    return this.line() + " L" + points[points.length - 1].x + " " + base + " L" + points[0].x + " " + base + " Z";
  },

  // n evenly spaced values from bottom to top, each with its y — for an axis.
  ticks: function (n) {
    n = number(n, 4);
    n = n >= 2 ? Math.floor(n) : 2;
    var r = range(this);
    var out = [];
    for (var i = 0; i < n; i++) {
      var value = round(r.min + ((r.max - r.min) * i) / (n - 1));
      out.push({ value: value, y: plotY(this, value), percent: round((i / (n - 1)) * 100) });
    }
    return out;
  },

  // One path of horizontal lines at the tick positions, for a grid behind the data.
  grid: function (n) {
    var b = box(this);
    return this.ticks(n).map(function (tick) {
      return "M" + b.padding + " " + tick.y + " H" + round(b.width - b.padding);
    }).join(" ");
  }
});

})();
