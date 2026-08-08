// Dịch vụ Thông báo bằng Giọng đọc Screen Reader (Accessibility / A11y)
var announcerEl = null;

function getAnnouncer() {
  if (!announcerEl || !announcerEl.isConnected) {
    announcerEl = document.createElement("div");
    announcerEl.setAttribute("data-kit-keep", ""); // Giữ nguyên qua mọi đợt SPA Morphing
    announcerEl.setAttribute("aria-live", "polite");
    announcerEl.setAttribute("aria-atomic", "true");
    announcerEl.style.cssText = "position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;clip:rect(0 0 0 0);overflow:hidden;white-space:nowrap;";
    document.body.appendChild(announcerEl);
  }
  return announcerEl;
}

kit.announce = {
  say: function (message, politeness) {
    var el = getAnnouncer();
    el.setAttribute("aria-live", politeness === "assertive" ? "assertive" : "polite");
    el.textContent = "";
    setTimeout(function () {
      el.textContent = String(message || "");
    }, 50);
  }
};
