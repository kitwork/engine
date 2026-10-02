;(function (window, document) {
  "use strict";
  // data-kit-drag="true" marks a region the OS may drag the window by — a title bar the page draws
  // itself, because app.desktop({ chrome: "html" }) opens a window with no frame of its own.
  // Primary-button press hands the drag to the OS through the private kit.window transport;
  // double-click maximises, which is what a title bar does everywhere.
  //
  // A control inside it opts out with data-kit-drag="false", so pressing a button is a press and
  // not the start of a drag. ONE attribute, two values: the old pair (data-kit-drag beside a
  // separate data-kit-no-drag) made the opt-out look like a different feature.
  //
  // It lived in the kernel until 02/10. Only a desktop window's title bar uses it, so every web page
  // carried it for nothing; the engine now delivers this file only to a page that writes it. On the
  // web, where there is no bridge and no window to move, it does nothing.
  var kit = window.kit;
  if (!kit || kit.has("drag")) return;
  kit.module("drag", {});
  var listen = kit.internal.listen;
  var DRAG = '[data-kit-drag="true"]';
  var NO_DRAG = '[data-kit-drag="false"]';

  function runWindowCommand(command) {
    try {
      var result = kit.window[command]();
      if (result && typeof result.catch === "function") result.catch(function () { });
    } catch (_) { }
  }
  function draggableTarget(e) {
    if (!e.target || !e.target.closest) return false;
    if (e.target.closest(NO_DRAG)) return false;
    return !!e.target.closest(DRAG);
  }
  listen(document, "mousedown", function (e) {
    if (e.button !== 0) return;
    if (draggableTarget(e)) runWindowCommand("drag");
  });
  listen(document, "dblclick", function (e) {
    if (!draggableTarget(e)) return;
    // A double-click TOGGLES: the second one has to put the window back, which is what every other
    // title bar does. Asking the shell is the only way to know which way to go; if it cannot answer,
    // maximising is the safer guess than doing nothing.
    var answer;
    try { answer = kit.window.isMaximized(); } catch (_) { answer = null; }
    Promise.resolve(answer).then(
      function (maximized) { runWindowCommand(maximized === true ? "restore" : "maximize"); },
      function () { runWindowCommand("maximize"); }
    );
  });
})(window, document);
