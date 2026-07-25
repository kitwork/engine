/* toast component @v1.0.0 — show auto-dismissing toast notification.
 * Usage: <button data-kit-component="toast" data-kit-message="Item saved successfully!">Notify</button>
 */
window.kitwork.components.register("toast", function (el) {
  var msg = el.getAttribute("data-kit-message") || el.getAttribute("data-kitwork-message") || "Notification";
  var toast = document.createElement("div");
  toast.className = "toast alert alert-info";
  toast.style.position = "fixed";
  toast.style.bottom = "20px";
  toast.style.right = "20px";
  toast.style.zIndex = "9999";
  toast.style.boxShadow = "0 10px 15px -3px rgba(0,0,0,0.3)";
  toast.style.transition = "opacity 0.3s ease, transform 0.3s ease";
  toast.textContent = msg;

  document.body.appendChild(toast);
  setTimeout(function () {
    toast.style.opacity = "0";
    toast.style.transform = "translateY(10px)";
    setTimeout(function () { toast.remove(); }, 300);
  }, 3000);
});
