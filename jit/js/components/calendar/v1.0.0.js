/* calendar component — a month grid you can walk with the keys.
 *
 * Usage — the component answers the dates; the markup draws them and binds every state:
 *
 *   <div data-kit-component="calendar" lang="en-GB" data-selected="2026-09-18" data-min="2026-09-03">
 *     <button data-kit-click="previous()" aria-label="Previous month">‹</button>
 *     <h3 aria-live="polite" data-kit-text="title"></h3>
 *     <button data-kit-click="next()" aria-label="Next month">›</button>
 *     <div role="grid" aria-label="Days">
 *       <div role="row"><span role="columnheader" data-kit-for="name of weekdays" data-kit-text="name"></span></div>
 *       <div role="row" data-kit-for="week of weeks" data-kit-key="week[0].iso">
 *         <button role="gridcell" data-kit-for="cell of week" data-kit-key="cell.iso"
 *                 data-kit-element="day" data-kit-bind:data-iso="cell.iso"
 *                 data-kit-bind:tabindex="cell.iso === focused ? 0 : -1"
 *                 data-kit-bind:aria-selected="cell.iso === selected ? 'true' : 'false'"
 *                 data-kit-bind:aria-label="cell.label" data-kit-bind:disabled="cell.disabled"
 *                 data-kit-class="cell.inMonth ? '' : 'text-ink-soft'"
 *                 data-kit-click="select(cell.iso)" data-kit-text="cell.day"></button>
 *       </div>
 *     </div>
 *     <span data-kit-text="selected ? formatted : 'No day chosen'"></span>
 *   </div>
 *
 * The host carries the configuration: data-selected, data-min and data-max as YYYY-MM-DD,
 * data-week-start (0 Sunday … 6 Saturday, Monday by default), and lang for the names — the
 * nearest lang, as a screen reader would read it.
 *
 * WHY THIS IS A COMPONENT. Two things the grammar cannot do. The DATES: a month grid needs real
 * Date arithmetic and Intl names, and the grammar has no `new`. So `weeks`, `weekdays`, `title` and
 * `formatted` are getters the markup reads. And the KEYS, the WAI-ARIA date grid: ← → a day, ↑ ↓ a
 * week, Home/End the ends of the week, PageUp/PageDown a month, Enter chooses. Walking past the
 * month's edge turns the month. The grid has one tab stop — `focused` — which the markup's tabindex
 * binding follows, so Tab enters it in one step and the arrows do the rest.
 *
 * The KitJS calendar had no key handling at all, and its role="grid" wrapped flat buttons with no
 * rows; here the rows are real, because a grid without them is announced wrong.
 *
 * The public surface: year, month, selected, focused, weeks, weekdays, title, formatted,
 * select(iso), clear(), next(), previous(), today(), focus().
 */
var CALENDAR = Symbol("kit:calendar");

function pad(n) { return (n < 10 ? "0" : "") + n; }
function isoOf(date) { return date.getFullYear() + "-" + pad(date.getMonth() + 1) + "-" + pad(date.getDate()); }
function dateOf(iso) {
  var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(iso || ""));
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null;
}
function addDays(iso, days) { var d = dateOf(iso); d.setDate(d.getDate() + days); return isoOf(d); }
function addMonths(iso, months) {
  var d = dateOf(iso), day = d.getDate();
  var target = new Date(d.getFullYear(), d.getMonth() + months, 1);
  var last = new Date(target.getFullYear(), target.getMonth() + 1, 0).getDate();
  target.setDate(Math.min(day, last)); // 31 January + a month is 28 or 29 February, not March
  return isoOf(target);
}

function calendarConfig(scope) {
  var own = scope[CALENDAR];
  var host = own ? own.context.host : null;
  var langHost = host && host.closest ? host.closest("[lang]") : null;
  // hasAttribute first: Number(null) is 0 — Sunday — so a missing attribute would silently pick it.
  var weekStart = host && host.hasAttribute("data-week-start") ? Number(host.getAttribute("data-week-start")) : NaN;
  return {
    min: host ? host.getAttribute("data-min") || "" : "",
    max: host ? host.getAttribute("data-max") || "" : "",
    weekStart: isFinite(weekStart) && weekStart >= 0 && weekStart <= 6 ? weekStart : 1,
    locale: langHost ? langHost.getAttribute("lang") : undefined
  };
}

function outOfRange(scope, iso) {
  var c = calendarConfig(scope);
  return (c.min && iso < c.min) || (c.max && iso > c.max); // YYYY-MM-DD compares as text
}

// Keep a date inside min…max, so the keys stop at the edge of what can be chosen.
function clampDate(scope, iso) {
  var c = calendarConfig(scope);
  if (c.min && iso < c.min) return c.min;
  if (c.max && iso > c.max) return c.max;
  return iso;
}

// Move the tab stop, turn the month if it walked off the page, and put focus on the cell once the
// paint has drawn it.
function focusDay(scope, iso) {
  iso = clampDate(scope, iso);
  var d = dateOf(iso);
  scope.focused = iso;
  if (d.getFullYear() !== scope.year || d.getMonth() !== scope.month) {
    scope.year = d.getFullYear();
    scope.month = d.getMonth();
  }
  var own = scope[CALENDAR];
  if (!own) return;
  own.context.afterRender(function () {
    var cell = own.context.elements("day").filter(function (el) { return el.getAttribute("data-iso") === iso; })[0];
    if (cell && typeof cell.focus === "function") cell.focus();
  });
}

var calendarDef = {
  year: 2000,
  month: 0,
  selected: "",
  focused: "",

  get title() {
    return new Date(this.year, this.month, 1).toLocaleDateString(calendarConfig(this).locale, { month: "long", year: "numeric" });
  },

  get weekdays() {
    var c = calendarConfig(this), out = [];
    for (var i = 0; i < 7; i++) {
      out.push(new Date(2024, 0, 7 + ((c.weekStart + i) % 7)).toLocaleDateString(c.locale, { weekday: "short" })); // 7 Jan 2024 was a Sunday
    }
    return out;
  },

  // Whole weeks: from the week holding the 1st to the week holding the last day.
  get weeks() {
    var c = calendarConfig(this), scope = this;
    var offset = (new Date(this.year, this.month, 1).getDay() - c.weekStart + 7) % 7;
    var inMonth = new Date(this.year, this.month + 1, 0).getDate();
    var cells = Math.ceil((offset + inMonth) / 7) * 7;
    var todayIso = isoOf(new Date());
    var weeks = [];
    for (var i = 0; i < cells; i++) {
      var date = new Date(this.year, this.month, 1 - offset + i);
      var iso = isoOf(date);
      if (i % 7 === 0) weeks.push([]);
      weeks[weeks.length - 1].push({
        iso: iso,
        day: date.getDate(),
        inMonth: date.getMonth() === scope.month,
        today: iso === todayIso,
        disabled: !!outOfRange(scope, iso),
        label: date.toLocaleDateString(c.locale, { weekday: "long", day: "numeric", month: "long", year: "numeric" })
      });
    }
    return weeks;
  },

  get formatted() {
    var d = dateOf(this.selected);
    return d ? d.toLocaleDateString(calendarConfig(this).locale, { weekday: "long", day: "numeric", month: "long", year: "numeric" }) : "";
  },

  select: function (iso) {
    if (!dateOf(iso) || outOfRange(this, iso)) return;
    this.selected = iso;
    this.focused = iso;
  },

  clear: function () { this.selected = ""; },

  next: function () { var d = new Date(this.year, this.month + 1, 1); this.year = d.getFullYear(); this.month = d.getMonth(); },
  previous: function () { var d = new Date(this.year, this.month - 1, 1); this.year = d.getFullYear(); this.month = d.getMonth(); },

  today: function () {
    var iso = isoOf(new Date());
    this.select(iso);
    focusDay(this, iso);
  },

  // Puts focus on the grid's one tab stop, after the next paint — so it lands even when the grid was
  // hidden until this very click. A date picker calls it as it opens (WAI-ARIA: the dialog opens
  // onto the chosen day), which no markup can do: the cell is not focusable until it is shown.
  focus: function () { focusDay(this, this.focused || this.selected || isoOf(new Date())); },

  init: function (context) {
    var scope = this;
    scope[CALENDAR] = { context: context };
    context.require("day", "data-iso");
    context.cleanup(function () { delete scope[CALENDAR]; });

    var seeded = context.host.getAttribute("data-selected");
    var start = dateOf(seeded) ? seeded : clampDate(scope, isoOf(new Date()));
    if (dateOf(seeded) && !outOfRange(scope, seeded)) scope.selected = seeded;
    var d = dateOf(start);
    scope.year = d.getFullYear();
    scope.month = d.getMonth();
    scope.focused = start;

    context.listen(context.host, "keydown", function (event) {
      if (event.altKey || event.ctrlKey || event.metaKey) return;
      var target = event.target;
      if (!target || target.getAttribute("data-kit-element") !== "day") return;
      var iso = target.getAttribute("data-iso");
      var weekday = (dateOf(iso).getDay() - calendarConfig(scope).weekStart + 7) % 7;
      var to = null;
      switch (event.key) {
        case "ArrowLeft": to = addDays(iso, -1); break;
        case "ArrowRight": to = addDays(iso, 1); break;
        case "ArrowUp": to = addDays(iso, -7); break;
        case "ArrowDown": to = addDays(iso, 7); break;
        case "Home": to = addDays(iso, -weekday); break;
        case "End": to = addDays(iso, 6 - weekday); break;
        case "PageUp": to = addMonths(iso, -1); break;
        case "PageDown": to = addMonths(iso, 1); break;
        case "Enter":
        case " ":
          event.preventDefault();
          scope.select(iso);
          return;
      }
      if (!to) return;
      event.preventDefault();
      focusDay(scope, to);
    });
  }
};

window.kit.component("calendar", calendarDef);
