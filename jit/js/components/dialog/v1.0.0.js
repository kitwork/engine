/* dialog component @v1.0.0 — an open/close overlay driven by state (the component sibling of the
 * `dialog` VERB, which drives a native <dialog>). Use the verb when the native element is enough;
 * use this component when you want the state named, reachable and styled with utilities.
 * Supports:
 *   - <div data-kit-component="dialog">
 *       <button data-kit-click="show()">Open</button>
 *       <div data-kit-show="open" class="fixed inset-0 …">
 *         <div data-kit-click="hide()" class="absolute inset-0 bg-black/40"></div>
 *         <div class="relative …">…<button data-kit-click="hide()">Close</button></div>
 *       </div>
 *     </div>
 *   - <div data-kit-component="dialog@v1.0.0">      — pin the version
 *   - <div data-kit-component="dialog=$confirm">    — global handle: $confirm.show() from anywhere
 */
var dialogDef = {
  open: false,
  show: function () { this.open = true; },
  hide: function () { this.open = false; },
  toggle: function () { this.open = !this.open; }
};

window.kitwork.component("dialog", dialogDef);
window.kitwork.component("dialog@v1.0.0", dialogDef);
