;(function () {
"use strict";

var instances = new WeakMap();

function slotsOf(data) {
  return data ? data.context.owned("[data-otp-slot]") : [];
}

function digitsOf(value) {
  return String(value === null || value === undefined ? "" : value).replace(/\D+/g, "");
}

function lastDigit(value) {
  var digits = digitsOf(value);
  return digits ? digits.charAt(digits.length - 1) : "";
}

function collect(slots) {
  return slots.map(function (slot) { return lastDigit(slot.value); }).join("");
}

function focusAt(slots, index) {
  if (index >= 0 && index < slots.length && typeof slots[index].focus === "function") {
    slots[index].focus();
  }
}

kit.component("otp", {
  value: "",

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false };
    instances.set(scope, data);

    function ownedSlot(target) {
      if (!target || typeof target.closest !== "function") return null;
      var slot = target.closest("[data-otp-slot]");
      return slot && slotsOf(data).indexOf(slot) >= 0 ? slot : null;
    }

    function push() {
      scope.value = collect(slotsOf(data));
    }

    context.listen(context.host, "input", function (event) {
      var slot = ownedSlot(event.target);
      if (!slot) return;
      var digit = lastDigit(slot.value);
      slot.value = digit;
      var slots = slotsOf(data);
      if (digit) focusAt(slots, slots.indexOf(slot) + 1);
      push();
    });

    context.listen(context.host, "keydown", function (event) {
      var slot = ownedSlot(event.target);
      if (!slot) return;
      var slots = slotsOf(data);
      var index = slots.indexOf(slot);
      if (event.key === "Backspace") {
        if (slot.value) return;
        event.preventDefault();
        if (index > 0) {
          slots[index - 1].value = "";
          focusAt(slots, index - 1);
          push();
        }
        return;
      }
      if (event.key === "Delete") {
        event.preventDefault();
        slot.value = "";
        push();
        return;
      }
      if (event.key === "ArrowLeft") {
        event.preventDefault();
        focusAt(slots, index - 1);
        return;
      }
      if (event.key === "ArrowRight") {
        event.preventDefault();
        focusAt(slots, index + 1);
      }
    });

    context.listen(context.host, "paste", function (event) {
      var slot = ownedSlot(event.target);
      if (!slot) return;
      event.preventDefault();
      var transfer = event.clipboardData && typeof event.clipboardData.getData === "function"
        ? event.clipboardData.getData("text") : "";
      var digits = digitsOf(transfer);
      if (!digits) return;
      var slots = slotsOf(data);
      var start = slots.indexOf(slot);
      if (start < 0) start = 0;
      for (var offset = 0; offset < digits.length && start + offset < slots.length; offset++) {
        slots[start + offset].value = digits.charAt(offset);
      }
      focusAt(slots, Math.min(start + digits.length, slots.length) - 1);
      push();
    });

    function afterRender() {
      if (data.disposed) return;
      var slots = slotsOf(data);
      var wanted = String(scope.value === null || scope.value === undefined ? "" : scope.value);
      slots.forEach(function (slot, index) {
        if (!slot.hasAttribute("maxlength")) slot.setAttribute("maxlength", "1");
        var digit = index < wanted.length ? wanted.charAt(index) : "";
        if (lastDigit(slot.value) !== digit) slot.value = digit;
      });
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);

    context.cleanup(function () {
      data.disposed = true;
      instances.delete(scope);
    });
  },

  clear: function () {
    var slots = slotsOf(instances.get(this));
    slots.forEach(function (slot) { slot.value = ""; });
    this.value = "";
    focusAt(slots, 0);
    return "";
  },

  isComplete: function () {
    var length = slotsOf(instances.get(this)).length;
    var value = String(this.value === null || this.value === undefined ? "" : this.value);
    return length > 0 && value.length === length;
  }
});

})();
