/* dialog — open/close a native <dialog> (modal). data-kitwork-target = the <dialog>;
 * data-kitwork-command = "open" (default), "close" or "toggle". A close button INSIDE the dialog can
 * omit the target: data-kitwork-action="dialog" data-kitwork-command="close".
 *   <button data-kitwork-action="dialog" data-kitwork-target="#confirm">Delete…</button>
 *   <dialog id="confirm">… <button data-kitwork-action="dialog" data-kitwork-command="close">Cancel</button></dialog>
 */
window.kitwork.components.action("dialog", function (el) {
  var command = el.getAttribute("data-kitwork-command") || "open";
  var dialog = el.getAttribute("data-kitwork-target")
    ? window.kitwork.components.target(el)
    : el.closest("dialog");
  if (!dialog || typeof dialog.showModal !== "function") return;
  if (command === "toggle") command = dialog.open ? "close" : "open";
  if (command === "close") dialog.close();
  else if (!dialog.open) dialog.showModal();
});
