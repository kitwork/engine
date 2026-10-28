/* appearance component — the page's view of the light/dark choice (KITWORK_UI §7, Kitwork UI's
 * <appearance>). The engine writes it from:
 *
 *   <appearance click="toggle()">
 *     <icon value="{{ dark ? 'sun' : 'moon' }}" alt="Đổi giao diện sáng/tối"></icon>
 *   </appearance>
 *
 * as data-kitwork-component="appearance" and data-kitwork-event="click: toggle()" on the button
 * the tag builds, and a data-kitwork-class on the icon that turns icon-sun or icon-moon on while its
 * condition holds.
 *
 * Markup never calls kit: kitwork.appearance owns the choice (stored, applied before paint by the
 * pre-paint script); this component is how markup sees it. mode and dark are getters that read the
 * service, so every host on a page agrees.
 *
 *   mode       "light" | "dark" | "system" — what was chosen; until the visitor chooses, the mode
 *              the site shows first (router.appearance, <html data-kitwork-appearance>), else system
 *   dark       true while the page shows dark (system resolved)
 *   toggle()   flip what is shown;  set(mode);  system() — follow the operating system (stored, so
 *              it holds even on a site whose default is dark)
 *
 * The server does not know the visitor's choice, so it paints the first frame with dark unknown
 * (the light icon) and the kernel corrects it when it starts. The theme component keeps the older
 * contract (mode, resolved) for the pages written before this one.
 */
var appearanceDef = {
  get mode() { return window.kitwork.appearance.mode; },
  get dark() { return window.kitwork.appearance.dark; },

  toggle: function () { window.kitwork.appearance.toggle(); },
  set: function (mode) { window.kitwork.appearance.set(mode); },
  system: function () { window.kitwork.appearance.system(); }
};

window.kitwork.component("appearance", appearanceDef);
