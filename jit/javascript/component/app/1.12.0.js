;kit.component("app", {
  loader: Object.freeze({ visible: false, value: null }),
  init: function () {
    var scope = this;
    var timer = null;

    function clearTimer() {
      if (timer === null) return;
      globalThis.clearTimeout(timer);
      timer = null;
    }

    function replace(visible, value) {
      scope.loader = Object.freeze({ visible: visible, value: value });
    }

    function receive(state) {
      clearTimer();
      if (state.phase === "start") {
        replace(true, null);
        return;
      }
      if (state.phase === "progress") {
        replace(true, Math.min(99, Math.floor(state.loaded / state.total * 100)));
        return;
      }
      if (state.phase === "finish" && state.outcome === "loaded") {
        replace(true, 100);
        timer = globalThis.setTimeout(function () {
          timer = null;
          replace(false, null);
        }, 300);
        return;
      }
      replace(false, null);
    }

    replace(false, null);
    var unsubscribe = kit.progress.subscribe(receive);
    return function () {
      clearTimer();
      unsubscribe();
      scope = null;
    };
  }
});
