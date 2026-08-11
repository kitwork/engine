// KitJS component: tooltip@1.0.0
;(function (kit) {
  "use strict";

  var privateState = new WeakMap();

  function stateFor(instance) {
    var value = privateState.get(instance);
    if (!value) {
      value = { hovered: false, focused: false };
      privateState.set(instance, value);
    }
    return value;
  }

  kit.component("tooltip", {
    visible: false,

    init: function () {
      var instance = this;
      var host = instance.$host;
      var details = stateFor(instance);

      var onPointerEnter = function () {
        details.hovered = true;
        instance.show();
      };
      var onPointerLeave = function () {
        details.hovered = false;
        if (!details.focused) instance.hide();
      };
      var onFocusIn = function () {
        details.focused = true;
        instance.show();
      };
      var onFocusOut = function (event) {
        details.focused = !!(event.relatedTarget && host.contains(event.relatedTarget));
        if (!details.focused && !details.hovered) instance.hide();
      };
      var onKeydown = function (event) {
        if (event.key !== "Escape" || !instance.visible) return;
        event.preventDefault();
        instance.hide();
      };

      host.addEventListener("pointerenter", onPointerEnter);
      host.addEventListener("pointerleave", onPointerLeave);
      host.addEventListener("focusin", onFocusIn);
      host.addEventListener("focusout", onFocusOut);
      host.addEventListener("keydown", onKeydown);

      return function () {
        host.removeEventListener("pointerenter", onPointerEnter);
        host.removeEventListener("pointerleave", onPointerLeave);
        host.removeEventListener("focusin", onFocusIn);
        host.removeEventListener("focusout", onFocusOut);
        host.removeEventListener("keydown", onKeydown);
        privateState.delete(instance);
      };
    },

    show: function () {
      this.visible = true;
      return true;
    },

    hide: function () {
      this.visible = false;
      return false;
    },

    toggle: function () {
      return this.visible ? this.hide() : this.show();
    }
  });
})(globalThis.kit);
