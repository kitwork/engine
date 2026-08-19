;(function () {
"use strict";

function messageValue(value) {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  if (typeof value === "boolean") return String(value);
  return "";
}

function toneValue(value) {
  return value === "info" || value === "success" || value === "warning" || value === "danger" ? value : "";
}

function currentTone(value) {
  return toneValue(value) || "info";
}

kit.component("toast", {
  visible: false,
  message: "",
  tone: "info",

  show: function (message, tone) {
    this.message = messageValue(message);
    this.tone = toneValue(tone) || currentTone(this.tone);
    this.visible = true;
    return true;
  },

  dismiss: function () {
    this.visible = false;
    return false;
  },

  isTone: function (tone) {
    var expected = toneValue(tone);
    return Boolean(expected) && currentTone(this.tone) === expected;
  }
});

})();
