package hydrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The structural block directives (data-kit-for, data-kit-if) are verified against a small hand-built
// DOM in node — no jsdom, no browser. The shim implements exactly the tree operations the kernel's
// render pass uses (attribute-selector querySelectorAll/closest, deep cloneNode, insertBefore/
// removeChild ordering, nextSibling, comment anchors), so these are real integration tests of the
// runtime, not mocks. Both for_test.go and if_test.go share this one shim + runner.
const blockDOMShim = `
function matchesAny(el, sel) {
  var parts = sel.split(",");
  for (var i = 0; i < parts.length; i++) {
    if (parts[i].trim() === "*") return true; // universal — cleanupTree sweeps descendants with it
    var m = /^\s*\[([^\]=]+)(?:=["']?([^"'\]]*)["']?)?\]\s*$/.exec(parts[i]);
    if (m && el.attributes && Object.prototype.hasOwnProperty.call(el.attributes, m[1])) {
      if (m[2] === undefined || el.attributes[m[1]] === m[2]) return true;
    }
  }
  return false;
}
function walk(root, fn) {
  for (var i = 0; i < root.childNodes.length; i++) {
    var c = root.childNodes[i];
    if (c.nodeType === 1) { fn(c); walk(c, fn); }
  }
}
function makeNode(nodeType) {
  var node = {
    nodeType: nodeType, tagName: "", attributes: {}, childNodes: [], parentNode: null, _text: "",
    hidden: false, value: "", __kitClass: null,
    style: { setProperty: function () {} },
    classList: { add: function () {}, remove: function () {}, contains: function () { return false; }, toggle: function () {} },
    setAttribute: function (k, v) { this.attributes[k] = String(v); },
    getAttribute: function (k) { return Object.prototype.hasOwnProperty.call(this.attributes, k) ? this.attributes[k] : null; },
    removeAttribute: function (k) { delete this.attributes[k]; },
    hasAttribute: function (k) { return Object.prototype.hasOwnProperty.call(this.attributes, k); },
    appendChild: function (c) { if (c.parentNode) c.parentNode.removeChild(c); c.parentNode = this; this.childNodes.push(c); return c; },
    insertBefore: function (c, ref) {
      if (c.parentNode) c.parentNode.removeChild(c);
      c.parentNode = this;
      if (ref == null) { this.childNodes.push(c); return c; }
      var i = this.childNodes.indexOf(ref);
      if (i < 0) this.childNodes.push(c); else this.childNodes.splice(i, 0, c);
      return c;
    },
    removeChild: function (c) { var i = this.childNodes.indexOf(c); if (i >= 0) this.childNodes.splice(i, 1); c.parentNode = null; return c; },
    remove: function () { if (this.parentNode) this.parentNode.removeChild(this); },
    replaceWith: function (n) { if (this.parentNode) { this.parentNode.insertBefore(n, this); this.parentNode.removeChild(this); } },
    cloneNode: function (deep) {
      var n = makeNode(this.nodeType); n.tagName = this.tagName; n._text = this._text;
      for (var k in this.attributes) n.attributes[k] = this.attributes[k];
      if (deep) for (var i = 0; i < this.childNodes.length; i++) n.appendChild(this.childNodes[i].cloneNode(true));
      return n;
    },
    querySelectorAll: function (sel) { var out = []; walk(this, function (el) { if (matchesAny(el, sel)) out.push(el); }); return out; },
    querySelector: function (sel) { return this.querySelectorAll(sel)[0] || null; },
    closest: function (sel) { var p = this; while (p) { if (p.nodeType === 1 && matchesAny(p, sel)) return p; p = p.parentElement; } return null; },
    matches: function (sel) { return this.nodeType === 1 && matchesAny(this, sel); },
    contains: function (other) { for (var p = other; p; p = p.parentNode) if (p === this) return true; return false; },
    _events: {},
    addEventListener: function (type, fn) { (this._events[type] || (this._events[type] = [])).push(fn); },
    removeEventListener: function (type, fn) { var a = this._events[type]; if (!a) return; var i = a.indexOf(fn); if (i >= 0) a.splice(i, 1); },
    dispatchEvent: function (evt) { var a = (this._events[evt.type] || []).slice(); for (var i = 0; i < a.length; i++) a[i].call(this, evt); return !evt.defaultPrevented; }
  };
  Object.defineProperty(node, "nextSibling", { get: function () {
    if (!this.parentNode) return null;
    var i = this.parentNode.childNodes.indexOf(this);
    return this.parentNode.childNodes[i + 1] || null;
  } });
  Object.defineProperty(node, "parentElement", { get: function () {
    var p = this.parentNode; while (p && p.nodeType !== 1) p = p.parentNode; return p;
  } });
  Object.defineProperty(node, "firstElementChild", { get: function () {
    for (var i = 0; i < this.childNodes.length; i++) if (this.childNodes[i].nodeType === 1) return this.childNodes[i];
    return null;
  } });
  Object.defineProperty(node, "isConnected", { get: function () {
    var p = this; while (p) { if (p === root) return true; p = p.parentNode; } return false;
  } });
  Object.defineProperty(node, "type", { // input.type reflects the type attribute in real DOM
    get: function () { return this.attributes.type || ""; },
    set: function (v) { this.attributes.type = String(v); }
  });
  Object.defineProperty(node, "textContent", {
    get: function () { return this._text; },
    set: function (v) { this._text = v == null ? "" : String(v); this.childNodes = []; }
  });
  return node;
}
function el(tag, attrs) { var n = makeNode(1); n.tagName = tag.toUpperCase(); if (attrs) for (var k in attrs) n.setAttribute(k, attrs[k]); return n; }

var root = makeNode(1); root.tagName = "HTML";
var body = el("body", { "data-kit-app": "proof" });
root.appendChild(body);

global.window = { addEventListener: function () {}, removeEventListener: function () {} };
global.document = {
  readyState: "complete", documentElement: root, body: body,
  _events: {},
  addEventListener: function (type, fn) { (this._events[type] || (this._events[type] = [])).push(fn); },
  removeEventListener: function (type, fn) { var a = this._events[type]; if (!a) return; var i = a.indexOf(fn); if (i >= 0) a.splice(i, 1); },
  dispatchEvent: function (evt) { var a = (this._events[evt.type] || []).slice(); for (var i = 0; i < a.length; i++) a[i].call(this, evt); return !evt.defaultPrevented; },
  querySelector: function (s) { return root.querySelector(s); },
  querySelectorAll: function (s) { return root.querySelectorAll(s); },
  createElement: function (t) { return el(t); },
  createComment: function (txt) { var n = makeNode(8); n._text = txt; delete n.closest; delete n.matches; return n; } // real comment nodes have neither
};
global.navigator = {};
global.localStorage = { getItem: function () { return null; }, setItem: function () {}, removeItem: function () {} };
global.history = { back: function () {}, forward: function () {}, replaceState: function () {}, pushState: function () {} };
global.location = { href: "http://x/", pathname: "/", search: "", origin: "http://x", reload: function () {}, assign: function () {} };
global.MutationObserver = function () { this.observe = function () {}; this.disconnect = function () {}; };
global.CustomEvent = function () {};
window.document = document; window.navigator = navigator; window.localStorage = localStorage;
window.history = history; window.location = location;
`

// requireNode returns the node binary. Locally it SKIPS when node is absent (a convenience). In CI it
// FAILS instead — these node-shim + conformance-client tests ARE the client half of the twin, and a
// silent skip there is exactly the blind spot that let two shipped for/if bugs through (scope via a
// comment anchor; a modal self-closing on its opening click). CI must actually run them.
func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err == nil {
		return node
	}
	if os.Getenv("CI") != "" {
		t.Fatal("node is required for the client-side tests and CI must provide it — the twin's client half must not silently skip")
	}
	t.Skip("node not found — skipping the client-side test locally (CI requires it)")
	return ""
}

// runNodeDOMScript concatenates the DOM shim, the compiled kernel (Runtime()), and the test's own
// assertion script, runs the whole thing under node, and fails on any non-zero exit. The assertion
// script drives the real kernel (kit.component / kit.render / kit.scopeFor) and throws on mismatch.
func runNodeDOMScript(t *testing.T, name, assertions string) {
	t.Helper()
	node := requireNode(t)
	script := blockDOMShim + "\n" + Runtime() + "\n" + assertions
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed:\n%s", name, out)
	}
	t.Logf("%s", out)
}
