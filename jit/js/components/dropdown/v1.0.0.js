/* dropdown component @v1.0.0 — a local open/close menu. LOCAL by design: without an alias every
 * dropdown on the page resolves to its NEAREST one, so many coexist with no collision.
 * Supports:
 *   - <div data-kit-component="dropdown">
 *       <button data-kit-click="toggle()">Menu</button>
 *       <div data-kit-show="open" data-kit-click="close()" class="fixed inset-0 z-40"></div>  (backdrop)
 *       <div data-kit-show="open" class="absolute …">…</div>
 *     </div>
 *   - <div data-kit-component="dropdown@v1.0.0">  — pin the version
 *   - <div data-kit-component="dropdown=$menu">   — also expose a global handle: $menu.toggle()
 */
var dropdownDef = {
  open: false,
  toggle: function () { this.open = !this.open; },
  show: function () { this.open = true; },
  close: function () { this.open = false; }
};

window.kitwork.component("dropdown", dropdownDef);
window.kitwork.component("dropdown@v1.0.0", dropdownDef);
