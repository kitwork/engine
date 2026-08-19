;(function () {
"use strict";

function isModK(event) {
  if (!event || event.defaultPrevented || event.altKey || event.shiftKey ||
    event.repeat || event.isComposing || event.keyCode === 229) return false;
  if (!!event.ctrlKey === !!event.metaKey) return false;
  return String(event.key || "").toLowerCase() === "k";
}

kit.component("shortcut", {
  init: function (context) {
    var host = context.host;
    context.listen(host.ownerDocument, "keydown", function (event) {
      if (host.getAttribute("data-shortcut") !== "mod+k" || !isModK(event)) return;
      if (typeof host.click !== "function") return;
      event.preventDefault();
      host.click();
    });
  }
});

})();
