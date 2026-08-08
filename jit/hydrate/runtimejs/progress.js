// Dịch vụ Điều khiển Thanh tiến trình Top Progress Bar
var progressBarEl = null;

function getProgressBar() {
  if (!progressBarEl || !progressBarEl.isConnected) {
    progressBarEl = document.createElement("div");
    progressBarEl.setAttribute("data-kit-keep", ""); // Giữ nguyên qua mọi đợt SPA Morphing
    progressBarEl.style.cssText = "position:fixed;top:0;left:0;height:2px;width:0;" +
      "background:var(--kitwork-progress,#1a73e8);" +
      "z-index:2147483647;opacity:0;pointer-events:none;transition:width .2s ease,opacity .3s";
    document.body.appendChild(progressBarEl);
  }
  return progressBarEl;
}

kit.progress = {
  start: function () {
    var bar = getProgressBar();
    bar.style.opacity = "1";
    bar.style.width = "30%";
  },

  set: function (percent) {
    var bar = getProgressBar();
    bar.style.opacity = "1";
    bar.style.width = percent + "%";
  },

  done: function () {
    var bar = getProgressBar();
    bar.style.width = "100%";
    setTimeout(function () {
      bar.style.opacity = "0";
      bar.style.width = "0";
    }, 250);
  }
};
