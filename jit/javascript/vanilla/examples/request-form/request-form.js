;(function () {
"use strict";

var requestKey = "profile-save";

function errorMessage(error) {
  var code = error && typeof error.code === "string" ? error.code : "NETWORK";
  if (code === "HTTP") return "The server rejected this profile.";
  if (code === "TIMEOUT") return "The request took too long and was stopped.";
  if (code === "CANCELLED") return "The request was cancelled.";
  if (code === "INVALID_RESPONSE") return "The server returned an invalid response.";
  if (code === "TOO_LARGE") return "The server response was too large.";
  return "The profile could not be saved. Check the connection and try again.";
}

kit.component("request-form", {
  name: "Ada Lovelace",
  email: "ada@example.test",
  phase: "idle",
  message: "Ready to save a profile.",
  responseStatus: "",
  attempt: 0,

  init: function () {
    return function () {
      kit.request.abort(requestKey);
    };
  },

  save: async function (url) {
    var endpoint = typeof url === "string" && url ? url : "/api/profile";
    var current = this.attempt + 1;
    this.attempt = current;
    this.phase = "pending";
    this.message = "Saving " + this.name + "...";
    this.responseStatus = "";

    try {
      var result = await kit.request.post(endpoint, {
        name: this.name,
        email: this.email
      }, {
        key: requestKey,
        timeout: 10000
      });

      if (current !== this.attempt) return;
      this.phase = "success";
      this.responseStatus = String(result.status);
      this.message = "Saved " + this.name + ".";
    } catch (error) {
      if (current !== this.attempt) return;
      this.phase = error && error.code === "CANCELLED" ? "cancelled" : "error";
      this.message = errorMessage(error);
      this.responseStatus = error && error.status ? String(error.status) : "";
    }
  },

  fail: function () {
    return this.save("/api/profile?demo=error");
  },

  latestWins: async function () {
    var older = this.save("/api/profile?demo=slow");
    var latest = this.save("/api/profile?demo=fast");
    await Promise.all([older, latest]);
  },

  cancel: function () {
    if (!kit.request.abort(requestKey)) {
      this.message = "There is no active request to cancel.";
      return;
    }
    this.phase = "cancelled";
    this.message = "Cancellation requested.";
  }
});

})();
