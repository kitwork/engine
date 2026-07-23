/* tab component @v1.0.0 — one named active tab (the component sibling of the `tab` VERB, which
 * drives ARIA tablist markup). Use the verb for standard ARIA tabs; use this component when you want
 * the active tab as NAMED state you can read, bind and reach.
 * Supports:
 *   - <div data-kit-component="tab">
 *       <button data-kit-click="select('profile')">Profile</button>
 *       <button data-kit-click="select('billing')">Billing</button>
 *       <section data-kit-show="active == '' || active == 'profile'">…</section>   (first = default)
 *       <section data-kit-show="active == 'billing'">…</section>
 *     </div>
 *   - style the active button with data-kit-bind="{ 'data-state': active == 'profile' }" + data-[state]:…
 *   - <div data-kit-component="tab@v1.0.0"> / "tab=$settings" → $settings.select('billing')
 */
var tabDef = {
  active: "",
  select: function (name) { this.active = name; }
};

window.kitwork.component("tab", tabDef);
window.kitwork.component("tab@v1.0.0", tabDef);
