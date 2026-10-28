// call.js — the browser side of router.view's bridge (28/10/2026, KITWORK_ROUTER_VIEW.md).
//
// A page drawn by router.view calls its jobs through this file until the page runs in the kernel. The engine
// writes each event that calls a job as the job's name and its arguments, read on the server as JSON, and
// marks each field that holds a var of the view's function (ui/view_bridge.go):
//
//   <button data-kitwork-job-click="remove" data-kitwork-args-click="[3]">
//   <input data-kitwork-var="title">
//
// One listener per kind of event, on the document: a call is a POST to the page's own URL naming the job
// (Kitwork-Call), its body { args, vars } where vars are what each marked field holds now. The answer is the
// page the job left, drawn by the server, and the vars the job changed (work/view_call.go): the page's main is
// replaced with the new one (morphed when Drive's morph is loaded) and the vars are written back. While a call
// is out, what started it is disabled; a call that fails is reported, on the console and as a
// kitwork:call-error event on the document ({ job, error: { code, message } }).
(function () {
  "use strict";
  if (window.__kitworkCall) return;
  window.__kitworkCall = true;

  var EVENTS = ["click", "change", "submit", "input"];

  // the vars the page holds now: each marked field's value, a checkbox's checked
  function vars() {
    var out = {};
    document.querySelectorAll("[data-kitwork-var]").forEach(function (field) {
      var name = field.getAttribute("data-kitwork-var");
      out[name] = field.type === "checkbox" || field.type === "radio" ? field.checked : field.value;
    });
    return out;
  }

  // write back the vars a job changed into their fields
  function writeVars(changed) {
    Object.keys(changed || {}).forEach(function (name) {
      var v = changed[name];
      document.querySelectorAll('[data-kitwork-var="' + name + '"]').forEach(function (field) {
        if (field.type === "checkbox" || field.type === "radio") field.checked = !!v;
        else field.value = v == null ? "" : String(v);
      });
    });
  }

  // the page the job left: its main (else its body) in place of this one's, and its title
  function swap(html) {
    var next = new DOMParser().parseFromString(html, "text/html");
    var to = next.querySelector("main") || next.body;
    var from = document.querySelector("main") || document.body;
    if (!to || !from) return;
    if (window.kit && typeof window.kit.morph === "function") window.kit.morph(from, to);
    else from.innerHTML = to.innerHTML;
    if (next.title) document.title = next.title;
  }

  // what started a call, kept from a second one while it is out: the element, or a form's submit buttons
  function hold(el, on) {
    var targets = el.tagName === "FORM" ? el.querySelectorAll('button[type="submit"], button:not([type]), input[type="submit"]') : [el];
    Array.prototype.forEach.call(targets, function (t) {
      if (on) {
        t.setAttribute("data-kitwork-pending", "");
        t.disabled = true;
      } else {
        t.removeAttribute("data-kitwork-pending");
        t.disabled = false;
      }
    });
    if (on) el.setAttribute("aria-busy", "true");
    else el.removeAttribute("aria-busy");
  }

  function report(job, error) {
    if (window.console) console.error("kitwork: việc " + job + " không thành", error);
    document.dispatchEvent(new CustomEvent("kitwork:call-error", { detail: { job: job, error: error } }));
  }

  function call(el, job, args) {
    if (el.hasAttribute("aria-busy")) return;
    hold(el, true);
    fetch(location.pathname + location.search, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "Accept": "application/json", "Kitwork-Call": job },
      body: JSON.stringify({ args: args, vars: vars() })
    }).then(function (response) {
      if (response.redirected) {
        location.href = response.url; // a guard sent the page away (context.redirect)
        return;
      }
      var type = response.headers.get("Content-Type") || "";
      if (type.indexOf("application/json") < 0) {
        location.reload(); // the folder answered a page, not the bridge: show it
        return;
      }
      return response.json().then(function (data) {
        if (!response.ok || data.error) {
          report(job, data.error || { code: response.status, message: "" });
          return;
        }
        swap(data.html);
        writeVars(data.vars);
      });
    }).catch(function (error) {
      report(job, { code: 0, message: String(error) });
    }).then(function () {
      if (el.isConnected) hold(el, false);
    });
  }

  EVENTS.forEach(function (type) {
    document.addEventListener(type, function (event) {
      var target = event.target;
      var el = target && target.closest ? target.closest("[data-kitwork-job-" + type + "]") : null;
      if (!el) return;
      if (type === "submit") event.preventDefault();
      var args = [];
      try {
        args = JSON.parse(el.getAttribute("data-kitwork-args-" + type) || "[]");
      } catch (error) {
        args = [];
      }
      call(el, el.getAttribute("data-kitwork-job-" + type), args);
    }, true);
  });
})();
