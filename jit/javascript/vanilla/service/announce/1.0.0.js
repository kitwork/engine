;(function (document, kit) {
"use strict";

// KitJS service: announce@1.0.0
var MAX_MESSAGE_LENGTH = 1024;
var REVEAL_DELAY = 20;
var CLEAR_DELAY = 10000;
var states = {
  polite: { revealTimer: 0, clearTimer: 0, generation: 0, settle: null },
  assertive: { revealTimer: 0, clearTimer: 0, generation: 0, settle: null }
};

function messageOf(value) {
  if (typeof value !== "string" || !value.trim()) {
    throw new TypeError("Announcement must be a non-empty string");
  }
  if (value.length > MAX_MESSAGE_LENGTH) {
    throw new TypeError("Announcement must not exceed 1024 characters");
  }
  return value;
}

function modeOf(value) {
  if (value === undefined) return "polite";
  if (value !== "polite" && value !== "assertive") {
    throw new TypeError("Announcement mode must be polite or assertive");
  }
  return value;
}

function connected(mode) {
  var candidate = document.getElementById("kit-announcer-" + mode);
  if (!candidate || candidate.ownerDocument !== document || !candidate.isConnected ||
    candidate.getAttribute("data-kit-announcer") !== mode) return null;
  return candidate;
}

function createRegion(mode) {
  var region = document.createElement("div");
  region.id = "kit-announcer-" + mode;
  region.setAttribute("data-kit-announcer", mode);
  region.setAttribute("role", mode === "assertive" ? "alert" : "status");
  region.setAttribute("aria-live", mode);
  region.setAttribute("aria-atomic", "true");
  region.style.cssText = "position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0";
  (document.body || document.documentElement).appendChild(region);
  return region;
}

function region(mode) {
  return connected(mode) || createRegion(mode);
}

function cancel(mode) {
  var state = states[mode];
  state.generation++;
  if (state.revealTimer) clearTimeout(state.revealTimer);
  if (state.clearTimer) clearTimeout(state.clearTimer);
  state.revealTimer = 0;
  state.clearTimer = 0;
  if (state.settle) {
    var settle = state.settle;
    state.settle = null;
    settle(false);
  }
  var current = connected(mode);
  if (current) current.textContent = "";
}

function say(message, mode) {
  message = messageOf(message);
  mode = modeOf(mode);
  cancel(mode);
  var state = states[mode];
  var ownGeneration = state.generation;
  return new Promise(function (resolve) {
    state.settle = resolve;
    state.revealTimer = setTimeout(function () {
      state.revealTimer = 0;
      if (state.generation !== ownGeneration) return;
      region(mode).textContent = message;
      state.settle = null;
      resolve(true);
      state.clearTimer = setTimeout(function () {
        state.clearTimer = 0;
        if (state.generation !== ownGeneration) return;
        var current = connected(mode);
        if (current) current.textContent = "";
      }, CLEAR_DELAY);
    }, REVEAL_DELAY);
  });
}

function polite(message) {
  return say(message, "polite");
}

function assertive(message) {
  return say(message, "assertive");
}

function clear(mode) {
  if (mode === undefined) {
    cancel("polite");
    cancel("assertive");
    return true;
  }
  cancel(modeOf(mode));
  return true;
}

kit.service("announce", {
  say: say,
  polite: polite,
  assertive: assertive,
  clear: clear
});
})(document, kit);
