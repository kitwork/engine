/* sidebar component @v1.0.0 — the app-shell navigation panel.
 *
 * TWO independent axes, because a sidebar behaves differently by viewport and the two must not
 * fight each other:
 *   status  "expanded" | "collapsed" | "hidden"   the DESKTOP rail — full, icons-only, or gone
 *   drawer  true | false                          the MOBILE off-canvas panel
 *
 * Markup activates it and wires controls to bare names; styling is pure utilities via the
 * data-[state=…] / group-data-[…] variants, so there is no stylesheet to ship:
 *
 *   <body data-kit-component="sidebar" data-kit-alias="$sidebar"
 *         data-kit-bind:data-state="status" data-kit-bind:data-open="drawer">
 *     <button data-kit-click="cycle()">…</button>          expanded ⇄ collapsed
 *     <button data-kit-click="toggle()">…</button>          hidden  ⇄ expanded
 *     <button data-kit-click="openDrawer()" class="lg:hidden">…</button>
 *     <div data-kit-show="drawer" data-kit-click="closeDrawer()" class="fixed inset-0"></div>
 *   </body>
 *
 * Use data-kit-alias when controls live OUTSIDE the element that owns the component (a header
 * button toggling a sidebar that is a sibling); a bare `sidebar` resolves to the nearest one.
 *
 * PERSISTENCE: component state is in-memory and resets on reload, so `status` is written to
 * localStorage here and read back at registration — a rail the user collapsed stays collapsed.
 * `drawer` is deliberately NOT persisted: a page should never open with a mobile overlay already
 * covering it. One key for the page, since an app shell has one sidebar.
 *
 * NOTE — first paint. The rail is restored by JS, so a collapsed sidebar renders expanded for one
 * frame before the kernel runs. Fixing that needs a pre-paint inline script, the same treatment
 * jit/theme gives dark mode; it is not solved here.
 */
var sidebarKey = "kitwork:sidebar";

function sidebarSave(status) {
  try { localStorage.setItem(sidebarKey, status); } catch (e) { /* private mode / quota */ }
}

function sidebarLoad() {
  try {
    var v = localStorage.getItem(sidebarKey);
    return v === "expanded" || v === "collapsed" || v === "hidden" ? v : "expanded";
  } catch (e) { return "expanded"; }
}

var sidebarDef = {
  status: sidebarLoad(),
  drawer: false,

  // --- desktop rail ---
  cycle: function () { this.set(this.status === "collapsed" ? "expanded" : "collapsed"); },
  expand: function () { this.set("expanded"); },
  collapse: function () { this.set("collapsed"); },
  hide: function () { this.set("hidden"); },
  toggle: function () { this.set(this.status === "hidden" ? "expanded" : "hidden"); },
  set: function (status) { this.status = status; sidebarSave(status); },

  // --- mobile drawer ---
  openDrawer: function () { this.drawer = true; },
  closeDrawer: function () { this.drawer = false; },
  toggleDrawer: function () { this.drawer = !this.drawer; },

  // Convenience predicates, so markup can ask instead of comparing strings:
  //   data-kit-show="isHidden()"   rather than   data-kit-show="status === 'hidden'"
  isExpanded: function () { return this.status === "expanded"; },
  isCollapsed: function () { return this.status === "collapsed"; },
  isHidden: function () { return this.status === "hidden"; }
};

// ACCESSIBILITY, stated so a page can hold up its end: the component owns state only. The markup
// declares what that state MEANS — aria-expanded on the rail control, aria-controls pointing at the
// panel, and for the mobile drawer a focus trap is the platform's job (<dialog>) or the page's.

window.kit.component("sidebar", sidebarDef);
window.kit.component("sidebar@v1.0.0", sidebarDef);
