package js

import "testing"

// The KitJS `chart` component computed an SVG path from a series — line(), area(), grid(n), bars(),
// total(), change(), average() — and drew nothing itself: the markup bound each answer to an
// attribute. Every one of those is arithmetic over an array, which the grammar can now write where a
// reader sees it (array methods fbd6665e, globals 44257a2e). So chart is grammar, not a component.
//
// Two charts, the two the catalogue showed: a line with its area, and bars.
func TestBrowserChartIsGrammar(t *testing.T) {
	runBrowserProof(t, "chart", nil, `
<div data-kit-scope="{
  series: [3200, 4100, 3900, 5200, 6100, 5800, 7400], width: 320, height: 120, padding: 6,
  top: () => Math.max.apply(null, series),
  x: (i) => Math.round((padding + i * (width - 2 * padding) / (series.length - 1)) * 10) / 10,
  y: (v) => Math.round((height - padding - v / top() * (height - 2 * padding)) * 10) / 10,
  line: () => series.map((v, i) => (i ? 'L' : 'M') + x(i) + ',' + y(v)).join(' '),
  area: () => line() + ' L' + x(series.length - 1) + ',' + height + ' L' + x(0) + ',' + height + ' Z',
  total: () => series.reduce((a, b) => a + b, 0),
  change: () => Math.round((series[series.length - 1] - series[0]) / series[0] * 10000) / 100
}">
  <b id="total" data-kit-text="total()"></b>
  <b id="change" data-kit-text="(change() >= 0 ? '+' : '') + change() + '%'"></b>
  <svg viewBox="0 0 320 120"><path id="line" data-kit-bind:d="line()"></path><path id="area" data-kit-bind:d="area()"></path></svg>
  <button id="add" data-kit-click="series = series.concat([9000])">add a day</button>
</div>

<div data-kit-scope="{
  sites: [{ label: 'shop', value: 42 }, { label: 'api', value: 18 }, { label: 'staging', value: 120 }],
  top: () => Math.max.apply(null, sites.map((s) => s.value)),
  average: () => Math.round(sites.reduce((sum, s) => sum + s.value, 0) / sites.length * 10) / 10
}">
  <b id="average" data-kit-text="average() + ' ms'"></b>
  <div style="display:flex;height:100px;align-items:flex-end">
    <div data-kit-for="bar of sites" data-kit-key="bar.label" data-kit-element="bar" style="flex:1"
         data-kit-style:height="(bar.value / top() * 100) + '%'"
         data-kit-class="bar.value > 100 ? 'warn' : 'ok'"
         data-kit-bind:aria-label="bar.label + ' ' + bar.value + ' ms'"></div>
  </div>
</div>
<script>
(async function () {
  await wait(80);
  var d = function (id) { return document.getElementById(id).getAttribute("d") || ""; };
  var text = function (id) { return document.getElementById(id).textContent; };

  // The line: one M and six L, from the left edge at the first value to the top-right at the max.
  var line = d("line");
  assert(line.indexOf("M6,") === 0 && (line.match(/L/g) || []).length === 6, "the line starts at the left padding with one M and six L, got " + line);
  assert(/L314,6$/.test(line), "the largest value sits at the top-right corner, got " + line);
  assert(d("area") === line + " L314,120 L6,120 Z", "the area is the line closed down to the baseline, got " + d("area"));
  assert(text("total") === "35700" && text("change") === "+131.25%", "total and change are arithmetic over the series, got " + text("total") + " / " + text("change"));

  // State changes redraw it — the path is a binding, not a drawing made once.
  document.getElementById("add").click(); await wait(40);
  assert((d("line").match(/L/g) || []).length === 7 && /L314,6$/.test(d("line")), "a new day redraws the line with the new maximum at the top-right, got " + d("line"));

  // The bars: heights as a share of the tallest, and the class decided by the value.
  var bars = [].slice.call(document.querySelectorAll('[data-kit-element="bar"]'));
  var heights = bars.map(function (b) { return b.style.height; }).join(",");
  assert(heights === "35%,15%,100%", "each bar is its share of the tallest, got " + heights);
  assert(bars[2].className.indexOf("warn") >= 0 && bars[0].className.indexOf("ok") >= 0, "a value over 100 is drawn as a warning");
  assert(bars[1].getAttribute("aria-label") === "api 18 ms", "each bar says what it is, got " + bars[1].getAttribute("aria-label"));
  assert(text("average") === "60 ms", "the average is arithmetic over the bars, got " + text("average"));

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
