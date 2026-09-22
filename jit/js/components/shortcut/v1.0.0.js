/* shortcut component @v1.0.0 — a keyboard shortcut that clicks its host.
 * Usage: <a href="/search" data-kit-component="shortcut" data-shortcut="mod+k">Search</a>
 *
 * The host stays an ordinary link or button: the keyboard is a second way to reach it, never the
 * only one. "mod" is Ctrl on Windows/Linux and Command on macOS — exactly one of the two, so
 * Ctrl+Cmd+K is not the shortcut. A repeat, an IME composition, or an event something else already
 * handled is left alone.
 */
var shortcutDef = {
  init: function (context) {
    var host = context.host;
    context.listen(host.ownerDocument, "keydown", function (event) {
      if (host.getAttribute("data-shortcut") !== "mod+k") return;
      if (!event || event.defaultPrevented || event.altKey || event.shiftKey ||
        event.repeat || event.isComposing || event.keyCode === 229) return;
      if (!!event.ctrlKey === !!event.metaKey) return;
      if (String(event.key || "").toLowerCase() !== "k") return;
      if (typeof host.click !== "function") return;
      event.preventDefault();
      host.click();
    });
  }
};

window.kit.component("shortcut", shortcutDef);
window.kit.component("shortcut@v1.0.0", shortcutDef);
