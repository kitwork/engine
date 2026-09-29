/* otp component @v1.0.0 — a one-time code split across one box per digit.
 *
 * Usage — the markup draws the boxes and says what to show; the component moves the caret:
 *
 *   <div data-kit-component="otp" data-kit-alias="$code">
 *     <div role="group" aria-labelledby="code-label">
 *       <input data-kit-element="slot" inputmode="numeric" autocomplete="one-time-code" aria-label="Digit 1">
 *       <input data-kit-element="slot" inputmode="numeric" aria-label="Digit 2">
 *       …one per digit…
 *     </div>
 *     <output data-kit-text="complete ? 'code ' + value : 'six digits'"></output>
 *     <button data-kit-click="clear()">Clear</button>
 *   </div>
 *
 * WHY THIS IS A COMPONENT. Every visible part — how many boxes, their labels, what the output says
 * — is markup. What markup cannot do is walk the CARET across separate inputs: a digit typed in one
 * box moves focus to the next, Backspace in an empty box steps back and clears the one before, the
 * arrow keys walk, and a pasted "123456" fills every box from wherever it landed. Six inputs are
 * six places to type; the component is what makes them feel like one field.
 *
 * `value` is the digits joined, and `complete` is true once every box holds one — a getter, so it
 * always answers for the boxes as they stand. Only digits are kept: anything else typed or pasted
 * is dropped rather than half-accepted.
 *
 * autocomplete="one-time-code" on the FIRST box lets the phone offer a code from a message; the
 * paste path is what fills the rest.
 */
var OTP = Symbol("kit:otp");

function otpSlots(scope) {
  var own = scope[OTP];
  return own ? own.context.elements("slot") : [];
}

function onlyDigits(value) {
  return String(value == null ? "" : value).replace(/\D+/g, "");
}

function lastDigit(value) {
  var digits = onlyDigits(value);
  return digits ? digits.charAt(digits.length - 1) : "";
}

function focusSlot(slots, index) {
  var slot = slots[index];
  if (!slot || typeof slot.focus !== "function") return;
  slot.focus();
  if (typeof slot.select === "function") slot.select(); // typing over a filled box replaces it
}

// The scope's value always comes from the boxes, never the other way round, so it cannot disagree
// with what the person sees.
function collect(scope) {
  var value = otpSlots(scope).map(function (slot) { return lastDigit(slot.value); }).join("");
  if (scope.value !== value) scope.value = value;
}

var otpDef = {
  value: "",

  get complete() {
    var slots = otpSlots(this);
    return slots.length > 0 && this.value.length === slots.length;
  },

  clear: function () {
    var slots = otpSlots(this);
    slots.forEach(function (slot) { slot.value = ""; });
    this.value = "";
    focusSlot(slots, 0);
  },

  init: function (context) {
    var scope = this;
    scope[OTP] = { context: context };
    context.cleanup(function () { delete scope[OTP]; });

    function slotOf(target) {
      var slots = otpSlots(scope);
      var index = slots.indexOf(target);
      return index < 0 ? null : { slots: slots, index: index };
    }

    context.listen(context.host, "input", function (event) {
      var at = slotOf(event.target);
      if (!at) return;
      var digit = lastDigit(event.target.value);
      event.target.value = digit;
      if (digit) focusSlot(at.slots, at.index + 1);
      collect(scope);
    });

    context.listen(context.host, "keydown", function (event) {
      var at = slotOf(event.target);
      if (!at || event.altKey || event.ctrlKey || event.metaKey) return;
      if (event.key === "Backspace") {
        if (event.target.value) return; // the browser clears this box; nothing to add
        event.preventDefault();
        if (at.index > 0) {
          at.slots[at.index - 1].value = "";
          focusSlot(at.slots, at.index - 1);
          collect(scope);
        }
        return;
      }
      if (event.key === "ArrowLeft") { event.preventDefault(); focusSlot(at.slots, at.index - 1); return; }
      if (event.key === "ArrowRight") { event.preventDefault(); focusSlot(at.slots, at.index + 1); }
    });

    context.listen(context.host, "paste", function (event) {
      var at = slotOf(event.target);
      if (!at) return;
      event.preventDefault();
      var pasted = event.clipboardData && typeof event.clipboardData.getData === "function"
        ? onlyDigits(event.clipboardData.getData("text")) : "";
      if (!pasted) return;
      for (var i = 0; i < pasted.length && at.index + i < at.slots.length; i++) {
        at.slots[at.index + i].value = pasted.charAt(i);
      }
      focusSlot(at.slots, Math.min(at.index + pasted.length, at.slots.length) - 1);
      collect(scope);
    });
  }
};

window.kit.component("otp", otpDef);
window.kit.component("otp@v1.0.0", otpDef);
