/* theme component — the page's view of kit.theme.
 *
 * Usage:
 *   <button data-kit-component="theme" data-kit-click="toggle()" aria-label="Toggle theme">
 *     <i data-kit-show="resolved === 'dark'" class="icon-moon"></i>
 *     <i data-kit-show="resolved !== 'dark'" class="icon-sun"></i>
 *   </button>
 *   elsewhere, through an alias:  data-kit-text="$theme.mode"   data-kit-click="$theme.set('dark')"
 *
 * Markup never calls kit: kit.theme owns the choice (stored, applied before paint, followed when the
 * system changes), and this component is how markup sees it. mode and resolved are getters that read
 * the service, so every host on the page agrees — a page may have several (a desktop and a mobile
 * toggle) without one copying the other.
 *
 *   mode       "light" | "dark" | "system" — what was chosen
 *   resolved   "light" | "dark" — what is shown (system resolved)
 *   toggle()   flip what is shown;  set(mode);  system() — follow the operating system
 *
 * (A theme@v1.0.0 with toggle() alone was removed on 23/09, when markup called kit.theme directly.
 * It returns with the same toggle() and only additions, now that markup does not.)
 */
var themeDef = {
  get mode() { return window.kit.theme.mode; },
  get resolved() { return window.kit.theme.resolved; },

  toggle: function () { window.kit.theme.toggle(); },
  set: function (mode) { window.kit.theme.set(mode); },
  system: function () { window.kit.theme.system(); }
};

window.kit.component("theme", themeDef);
