/* progressbar component — the page's view of kit.progress.
 *
 * Usage (the grouped attributes the engine writes, KITWORK_UI.md §11):
 *   <div data-kitwork-component="progressbar" hidden
 *        role="progressbar" aria-label="Loading" aria-valuemin="0" aria-valuemax="100"
 *        data-kitwork-bind="show: active; aria-valuenow: value" data-kitwork-style="top: top">
 *     <div data-kitwork-style="width: value + '%'"></div>
 *   </div>
 * By hand on a template page the same host reads data-kit-component="progressbar" data-kit-show="active"
 * data-kit-bind:aria-valuenow="value" data-kit-style:top="top", with data-kit-style:width on the fill.
 *
 * Markup never calls kit: kit.progress is the one place work in flight reports to (Drive reports a
 * page change, a page reports an upload), and this component is how markup sees it. Its state is two
 * getters that read the service, so the bar can never disagree with it — nothing is copied, nothing
 * is timed here. It is THE bar between pages: Drive draws none of its own (10/10/2026); on a page that
 * moves without a reload and has no progressbar, the engine puts one at the top of <body>.
 *
 * start() / update(n) / finish() pass straight through, for a page that drives the bar itself:
 *   <button data-kit-click="$progress.start()">…
 */
var progressbarDef = {
  get value() { return window.kit.progress.value; },
  get active() { return window.kit.progress.active; },
  // Under a title bar the page draws itself (a desktop window opened with chrome: "html" marks it
  // data-kit-drag="true"), the bar sits below it, where a browser's own progress sits under its
  // toolbar, not across the window's controls. A drag region that does not touch the top edge is not
  // a title bar. Measured only while the bar shows, since the title bar may come and go with a page
  // swap; null otherwise, which leaves the host where its own CSS puts it.
  get top() {
    if (!window.kit.progress.active) return null;
    var titleBar = document.querySelector('[data-kit-drag="true"]');
    if (!titleBar) return null;
    var box = titleBar.getBoundingClientRect();
    return box.top <= 0 && box.bottom > 0 ? box.bottom + "px" : null;
  },

  start: function () { window.kit.progress.start(); },
  update: function (value) { window.kit.progress.update(value); },
  finish: function () { window.kit.progress.finish(); }
};

window.kit.component("progressbar", progressbarDef);
