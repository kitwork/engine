/* progressbar component @v1.0.0 — the page's view of kit.progress.
 *
 * Usage:
 *   <div data-kit-component="progressbar" data-kit-show="active" hidden
 *        role="progressbar" aria-label="Loading" aria-valuemin="0" aria-valuemax="100"
 *        data-kit-bind:aria-valuenow="value">
 *     <div data-kit-style:width="value + '%'"></div>
 *   </div>
 *
 * Markup never calls kit: kit.progress is the one place work in flight reports to (Drive reports a
 * page change, a page reports an upload), and this component is how markup sees it. Its state is two
 * getters that read the service, so the bar can never disagree with it — nothing is copied, nothing
 * is timed here. A page that has a progressbar host gets no bar of Drive's own.
 *
 * start() / update(n) / finish() pass straight through, for a page that drives the bar itself:
 *   <button data-kit-click="$progress.start()">…
 */
var progressbarDef = {
  get value() { return window.kit.progress.value; },
  get active() { return window.kit.progress.active; },

  start: function () { window.kit.progress.start(); },
  update: function (value) { window.kit.progress.update(value); },
  finish: function () { window.kit.progress.finish(); }
};

window.kit.component("progressbar", progressbarDef);
window.kit.component("progressbar@v1.0.0", progressbarDef);
