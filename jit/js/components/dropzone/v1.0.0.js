/* dropzone component — files dropped on a region, or picked, checked and listed.
 *
 * Usage — the file input carries the rules; the markup lists what came in:
 *
 *   <div data-kit-component="dropzone" data-kit-class="dragging ? 'border-brand' : 'border-line'">
 *     <input data-kit-element="picker" type="file" multiple accept="image/*,.pdf"
 *            data-max-bytes="5000000" data-limit="4" class="sr-only">
 *     <button type="button" data-kit-click="$element.picker.click()">Browse</button>
 *     <ul><li data-kit-for="file of files" data-kit-key="file.name">
 *       <span data-kit-text="file.name"></span>
 *       <button data-kit-click="remove(file.name)">Remove</button>
 *     </li></ul>
 *     <p data-kit-for="item of rejected" data-kit-text="item.name + ' — ' + item.reason"></p>
 *   </div>
 *
 * THE INPUT IS THE CONFIGURATION. What may be chosen is already said on the file input for the
 * browser's own picker — accept="image/*,.pdf", multiple — so the component reads those same
 * attributes to judge a DROP. data-max-bytes and data-limit add the two rules the platform has no
 * attribute for. Nothing is said twice, and the picker and the drop can never disagree.
 *
 * WHY THIS IS A COMPONENT. Opening the picker is the grammar ($element.picker.click()), and so is
 * everything drawn. What the grammar cannot do is the DRAG: dragenter, dragover, dragleave and
 * drop are not events it listens to, a drop has to be accepted (dragover's default refused) for the
 * browser to allow it at all, and `dragging` has to survive the pointer crossing child elements —
 * each crossing fires a leave and an enter, so the component counts them instead of trusting one.
 *
 * files is [{ name, size, type, file }] — `file` is the real File, for a form or an upload.
 * rejected is [{ name, reason }], reset by every new batch. A name already in the list is kept
 * once; a second copy is rejected as a duplicate.
 *
 * The public surface: files, rejected, dragging, remove(name), clear().
 */
var DROPZONE = Symbol("kit:dropzone");

function dropzonePicker(scope) {
  var own = scope[DROPZONE];
  return own ? own.context.element("picker") : null;
}

// accept="image/*,.pdf,application/json": a MIME family, an extension, or an exact MIME.
function accepts(accept, file) {
  var rules = String(accept || "").split(",").map(function (rule) { return rule.trim().toLowerCase(); }).filter(Boolean);
  if (!rules.length) return true;
  var name = String(file.name || "").toLowerCase(), type = String(file.type || "").toLowerCase();
  return rules.some(function (rule) {
    if (rule.charAt(0) === ".") return name.slice(-rule.length) === rule;
    if (rule.slice(-2) === "/*") return type.indexOf(rule.slice(0, -1)) === 0;
    return type === rule;
  });
}

// The limit as a person reads it: 5 MB, 1 KB, 300 bytes — never "0 MB" for a limit under a megabyte.
function readableSize(bytes) {
  if (bytes >= 1e6) return Math.round(bytes / 1e5) / 10 + " MB";
  if (bytes >= 1e3) return Math.round(bytes / 1e2) / 10 + " KB";
  return bytes + " bytes";
}

function take(scope, list) {
  var picker = dropzonePicker(scope);
  var accept = picker ? picker.getAttribute("accept") : "";
  var multiple = picker ? picker.hasAttribute("multiple") : true;
  var maxBytes = picker ? Number(picker.getAttribute("data-max-bytes")) : 0;
  var limit = picker ? Number(picker.getAttribute("data-limit")) : 0;
  if (!multiple) limit = 1;

  var kept = multiple ? scope.files.slice() : [];
  var refused = [];
  Array.prototype.forEach.call(list || [], function (file) {
    if (!accepts(accept, file)) { refused.push({ name: file.name, reason: "not an accepted type" }); return; }
    if (maxBytes > 0 && file.size > maxBytes) { refused.push({ name: file.name, reason: "larger than " + readableSize(maxBytes) }); return; }
    if (kept.some(function (existing) { return existing.name === file.name; })) { refused.push({ name: file.name, reason: "already added" }); return; }
    if (limit > 0 && kept.length >= limit) { refused.push({ name: file.name, reason: "over the limit of " + limit }); return; }
    kept.push({ name: file.name, size: file.size, type: file.type, file: file });
  });
  scope.files = kept;
  scope.rejected = refused;
}

var dropzoneDef = {
  files: [],
  rejected: [],
  dragging: false,

  remove: function (name) {
    this.files = this.files.filter(function (file) { return file.name !== name; });
  },

  clear: function () {
    this.files = [];
    this.rejected = [];
  },

  init: function (context) {
    var scope = this;
    var depth = 0;
    scope[DROPZONE] = { context: context };
    context.cleanup(function () { delete scope[DROPZONE]; });

    var carriesFiles = function (event) {
      var types = event.dataTransfer && event.dataTransfer.types;
      return !!types && Array.prototype.indexOf.call(types, "Files") >= 0;
    };

    context.listen(context.host, "dragenter", function (event) {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      depth++;
      if (!scope.dragging) scope.dragging = true;
    });
    // A drop is only allowed where dragover's default was refused.
    context.listen(context.host, "dragover", function (event) {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
    });
    context.listen(context.host, "dragleave", function (event) {
      if (!carriesFiles(event)) return;
      depth = Math.max(0, depth - 1);
      if (depth === 0 && scope.dragging) scope.dragging = false;
    });
    context.listen(context.host, "drop", function (event) {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      depth = 0;
      scope.dragging = false;
      take(scope, event.dataTransfer.files);
    });
    // The picker: the same rules, the same list.
    context.listen(context.host, "change", function (event) {
      var picker = dropzonePicker(scope);
      if (!picker || event.target !== picker) return;
      take(scope, picker.files);
      picker.value = ""; // so choosing the same file again still fires change
    });
  }
};

window.kit.component("dropzone", dropzoneDef);
