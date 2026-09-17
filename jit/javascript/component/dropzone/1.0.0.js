;(function () {
"use strict";

// The dropzone: a file field you can drop onto. The native <input type=file> is
// the control — click it, or drop on the zone around it — and the component
// keeps what a method cannot reach: the dragging state, the chosen files as a
// plain list the markup can print, and the input's own FileList when a form
// submits. Limits are refusals, not silent truncation: a file over max or of a
// wrong type lands in rejected with a reason.
//
//   <div data-kit-component="dropzone">
//     <input type="file" data-dropzone-input multiple>
//     <button data-dropzone-browse>Choose files</button>
//     <template data-kit-for="file of files">…</template>
//   </div>

// The instance data hangs off the scope under a symbol. A method a directive invokes
// receives an action proxy as `this`, and the proxy hands symbol keys through to the
// scope — which a WeakMap keyed by the raw scope object would not survive.
var INSTANCE = Symbol("kit:dropzone");

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function number(value, fallback) {
  value = Number(value);
  return Number.isFinite(value) && value >= 0 ? value : fallback;
}

function list(value) {
  return Array.isArray(value) ? value : [];
}

function parts(data, selector) {
  return data ? data.context.owned(selector) : [];
}

function ownedPart(data, target, selector) {
  if (!target || typeof target.closest !== "function") return null;
  var part = target.closest(selector);
  return part && data.context.owned(selector).indexOf(part) >= 0 ? part : null;
}

function input(data) {
  var inputs = parts(data, "[data-dropzone-input]");
  return inputs.length ? inputs[0] : null;
}

function describe(file) {
  return { name: String(file.name || ""), size: number(file.size, 0), type: String(file.type || "") };
}

function accepts(scope, file) {
  var accept = String(scope.accept || "").trim();
  if (!accept) return true;
  var name = String(file.name || "").toLowerCase();
  var type = String(file.type || "").toLowerCase();
  return accept.split(",").some(function (rule) {
    rule = rule.trim().toLowerCase();
    if (!rule) return false;
    if (rule.charAt(0) === ".") return name.slice(-rule.length) === rule;
    if (rule.slice(-2) === "/*") return type.indexOf(rule.slice(0, -1)) === 0;
    return type === rule;
  });
}

// The kept File objects live beside the scope, in order — the scope holds only
// their plain descriptions. The input's FileList is rebuilt from them after every
// change, so a form submits exactly the list the page shows.
function sync(data) {
  var control = input(data);
  if (!control || typeof DataTransfer !== "function") return;
  var transfer = new DataTransfer();
  data.blobs.forEach(function (file) { transfer.items.add(file); });
  try { control.files = transfer.files; }
  catch (_) { /* A browser without an assignable FileList keeps its own selection. */ }
}

function take(scope, data, incoming) {
  var blobs = scope.multiple ? data.blobs.slice() : [];
  var kept = blobs.map(describe);
  var rejected = [];
  var max = number(scope.max, 0);
  var limit = number(scope.limit, 0);
  Array.prototype.forEach.call(incoming || [], function (file) {
    var entry = describe(file);
    if (!accepts(scope, file)) {
      rejected.push({ name: entry.name, reason: "type" });
      return;
    }
    if (max && entry.size > max) {
      rejected.push({ name: entry.name, reason: "size" });
      return;
    }
    if (limit && kept.length >= limit) {
      rejected.push({ name: entry.name, reason: "count" });
      return;
    }
    if (!scope.multiple) {
      kept.length = 0;
      blobs.length = 0;
    }
    kept.push(entry);
    blobs.push(file);
  });
  data.blobs = blobs;
  scope.files = kept;
  scope.rejected = rejected;
  sync(data);
  return kept.length;
}

kit.component("dropzone", {
  files: [],
  rejected: [],
  dragging: false,
  multiple: true,
  accept: "",
  max: 0,
  limit: 0,

  init: function (context) {
    var scope = this;
    var data = { context: context, depth: 0, blobs: [] };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    var control = input(data);
    if (control) {
      if (scope.multiple) control.multiple = true;
      if (scope.accept && !control.getAttribute("accept")) control.setAttribute("accept", String(scope.accept));
    }

    context.listen(context.host, "click", function (event) {
      if (!ownedPart(data, event.target, "[data-dropzone-browse]")) return;
      var field = input(data);
      if (field && typeof field.click === "function") field.click();
    });

    context.listen(context.host, "change", function (event) {
      if (event.target !== input(data)) return;
      take(scope, data, event.target.files);
    });

    context.listen(context.host, "dragenter", function (event) {
      event.preventDefault();
      data.depth++;
      scope.dragging = true;
    });
    context.listen(context.host, "dragover", function (event) {
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
      scope.dragging = true;
    });
    context.listen(context.host, "dragleave", function () {
      data.depth = Math.max(0, data.depth - 1);
      if (data.depth === 0) scope.dragging = false;
    });
    context.listen(context.host, "drop", function (event) {
      event.preventDefault();
      data.depth = 0;
      scope.dragging = false;
      take(scope, data, event.dataTransfer ? event.dataTransfer.files : []);
    });
  },

  add: function (files) {
    return take(this, instance(this), files);
  },

  remove: function (name) {
    var data = instance(this);
    name = String(name);
    var kept = list(this.files).filter(function (file) { return file.name !== name; });
    if (kept.length === list(this.files).length) return false;
    data.blobs = data.blobs.filter(function (file) { return String(file.name || "") !== name; });
    this.files = kept;
    sync(data);
    return true;
  },

  clear: function () {
    var data = instance(this);
    data.blobs = [];
    var control = input(data);
    if (control) control.value = "";
    this.files = [];
    this.rejected = [];
    return true;
  },

  hasFiles: function () {
    return list(this.files).length > 0;
  }
});

})();
