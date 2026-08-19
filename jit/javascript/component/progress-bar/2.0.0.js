;(function () {
"use strict";

kit.component("progress-bar", {
  visible: false,
  value: null,

  init: function () {
    var scope = this;
    var hideTimer = null;

    function clearHide() {
      if (hideTimer === null) return;
      clearTimeout(hideTimer);
      hideTimer = null;
    }

    function hide() {
      scope.visible = false;
      scope.value = null;
    }

    var unsubscribe = kit.progress.subscribe(function (progress) {
      clearHide();

      if (progress.phase === "start") {
        scope.visible = true;
        scope.value = null;
        return;
      }

      if (progress.phase === "progress") {
        scope.visible = true;
        scope.value = Math.min(99, Math.floor(progress.loaded / progress.total * 100));
        return;
      }

      if (progress.phase === "finish" && progress.outcome === "loaded") {
        scope.visible = true;
        scope.value = 100;
        hideTimer = setTimeout(function () {
          hideTimer = null;
          hide();
        }, 300);
        return;
      }

      hide();
    });

    return function () {
      clearHide();
      unsubscribe();
    };
  }
});

})();
