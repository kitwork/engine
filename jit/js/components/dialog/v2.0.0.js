/* dialog component @v2.0.0 — drive a native <dialog>. The HOST IS the dialog element.
 *
 * Usage:
 *   <button data-kit-click="$confirm.show()">Delete project</button>
 *   <dialog data-kit-component="dialog" data-kit-alias="$confirm" class="…">
 *     <p data-kit-text="result ? 'You chose ' + result : ''"></p>
 *     <button data-kit-click="close('cancel')">Cancel</button>
 *     <button data-kit-click="close('deleted')">Delete</button>
 *   </dialog>
 *
 * State: `open` mirrors the element (true between showModal and close, however it closed) and
 * `result` carries what the last close passed — the same value as the element's returnValue.
 *
 * Accessibility belongs to the platform and is left there: <dialog>.showModal() puts the dialog in
 * the top layer, renders ::backdrop, traps focus, makes the rest of the page inert, closes on
 * Escape and returns focus to the opener. This component adds only the two things the platform
 * does not do — a click on the backdrop closes, and the page behind cannot scroll while it is open
 * — and keeps state in step when the platform closes the dialog on its own.
 *
 * v1.0.0 took a SELECTOR (show("#id") → document.querySelector) and drove a <div> pretending to be
 * a dialog. That was the verb era's data-kit-target thinking; the grammar replaced it with
 * data-kit-element and $element.x.showModal(). This version is the case that expression does not
 * cover: state a binding can read, plus the two platform gaps.
 */
var dialogDef = {
  open: false,
  result: "",

  show: function () {
    var host = this.dialogHost;
    if (!host || typeof host.showModal !== "function" || host.open) return;
    host.showModal();
    this.open = true;
    this.result = "";
    this.lockScroll();
  },

  // The component sets its own state here rather than waiting for the element's "close" event: a
  // component must not need an event to know what it just did. The listener in init() stays for the
  // closes the component did NOT make — Escape, or a form with method="dialog".
  close: function (result) {
    var host = this.dialogHost;
    var value = result == null ? "" : String(result);
    this.open = false;
    this.result = value;
    this.releaseScroll();
    if (host && typeof host.close === "function" && host.open) host.close(value);
  },

  lockScroll: function () {
    var root = document.documentElement;
    if (this.scrollLocked) return;
    this.previousOverflow = root.style.overflow;
    root.style.overflow = "hidden";
    this.scrollLocked = true;
  },

  releaseScroll: function () {
    if (!this.scrollLocked) return;
    document.documentElement.style.overflow = this.previousOverflow || "";
    this.scrollLocked = false;
  },

  init: function (context) {
    var host = context.host;
    var self = this;
    this.dialogHost = host;
    this.scrollLocked = false;
    this.previousOverflow = "";

    if (!host || host.tagName !== "DIALOG") {
      if (typeof console !== "undefined" && console.warn) {
        console.warn('kitwork: data-kit-component="dialog" belongs on a <dialog> element — the platform owns focus, Escape and the backdrop. Got <' +
          (host && host.tagName ? host.tagName.toLowerCase() : "?") + ">.");
      }
      return;
    }

    // The platform closes on Escape and on form method="dialog": follow it rather than fight it.
    // Idempotent, because close() above may already have recorded the same close.
    context.listen(host, "close", function () {
      if (!self.open) return;
      self.open = false;
      self.result = host.returnValue == null ? "" : String(host.returnValue);
      self.releaseScroll();
    });

    // A click that lands on the dialog element itself is a click on the backdrop — the content sits
    // in child elements. The platform renders ::backdrop but never closes on it.
    context.listen(host, "click", function (event) {
      if (event.target === host) self.close("");
    });

    this.open = !!host.open;
    if (this.open) this.lockScroll();
    context.cleanup(function () { self.releaseScroll(); });
  }
};

window.kit.component("dialog", dialogDef);
window.kit.component("dialog@v2.0.0", dialogDef);
