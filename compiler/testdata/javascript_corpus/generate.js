// JavaScript's own answer to every case of jit/hydrate/parity_corpus.json, as .work must give it
// (docs/KITWORK_LANG.md section 3, 27/10/2026: ".work theo JavaScript đúng từng chữ"). The corpus holds
// .ui's answers, markup-safety rules included; this writes what JavaScript itself says, for
// TestJavaScriptCorpus (javascript_corpus_test.go) to hold the VM to.
//
// Each case runs as the test runs it on Kitwork, `const result = (src);`, here inside a strict function
// (a module is strict), in a fresh realm whose globals are the corpus's scope. Both sides run at UTC+7
// (the test sets time.Local to it): not UTC, so a case that reads local time is held to JavaScript
// there too, and the same on every machine. Run it whenever the corpus changes, from the engine
// directory:
//
//	node compiler/testdata/javascript_corpus/generate.js
//
// then rerun the test with KITWORK_JAVASCRIPT_CORPUS=update to record the new classes.
"use strict";
process.env.TZ = "Etc/GMT-7"; // POSIX names turn the sign around: this is UTC+7

const fs = require("fs");
const path = require("path");
const vm = require("vm");

const here = __dirname;
const corpus = JSON.parse(fs.readFileSync(path.join(here, "..", "..", "..", "jit", "hydrate", "parity_corpus.json"), "utf8"));

// describe is describeParity (jit/hydrate/parity_corpus_test.go) in JavaScript, as the node runner of
// that test writes it.
const DESCRIBE = `
var GLOBALS = new Map([[Math, "Math"], [Date, "Date"], [JSON, "JSON"], [Number, "Number"], [String, "String"],
  [Boolean, "Boolean"], [Array, "Array"], [Object, "Object"], [parseInt, "parseInt"], [parseFloat, "parseFloat"],
  [encodeURIComponent, "encodeURIComponent"], [decodeURIComponent, "decodeURIComponent"], [isNaN, "isNaN"],
  [isFinite, "isFinite"]]);
function __describe(v, depth) {
  depth = depth || 0;
  if (depth > 12) return "<deep>";
  if (v === undefined) return "undefined";
  if (v === null) return "null";
  switch (typeof v) {
    case "boolean": return String(v);
    case "number": return v !== v ? "NaN" : v === 0 && 1 / v < 0 ? "-0" : String(v);
    case "string": return JSON.stringify(v);
  }
  if (GLOBALS.has(v)) return "global:" + GLOBALS.get(v);
  if (typeof v === "function") {
    if (/\\[native code\\]\\s*\\}$/.test(String(v))) return "native:" + v.name;
    return "fn:" + String(v);
  }
  if (Array.isArray(v)) {
    var items = [];
    for (var i = 0; i < v.length; i++) items.push(i in v ? __describe(v[i], depth + 1) : "<hole>");
    return "[" + items.join(",") + "]";
  }
  var proto = Object.getPrototypeOf(v);
  if (proto === Object.prototype || proto === null) {
    return "{" + Object.keys(v).map(function (k) { return JSON.stringify(k) + ":" + __describe(v[k], depth + 1); }).join(",") + "}";
  }
  return "<" + Object.prototype.toString.call(v).slice(8, -1) + ">";
}
`;

process.on("unhandledRejection", () => {}); // Array.fromAsync() settles after the expression

// key names a case as the test does: group and source, and #2, #3… for a later case with both alike.
const seen = new Map();
function key(c) {
  const base = c.group + " | " + c.src;
  const n = (seen.get(base) || 0) + 1;
  seen.set(base, n);
  return n === 1 ? base : base + " #" + n;
}

const answers = {};
for (const c of corpus.cases) {
  const scope = Object.assign({}, JSON.parse(JSON.stringify(corpus.scope)), c.scope ? JSON.parse(JSON.stringify(c.scope)) : {});
  const context = vm.createContext({ __scope: JSON.stringify(scope) });
  let answer;
  try {
    vm.runInContext(DESCRIBE + "Object.assign(globalThis, JSON.parse(__scope));", context);
    const run = vm.runInContext(
      '(function () { "use strict"; const result = (' + c.src.trim() + "); return result; })",
      context,
      { timeout: 2000 },
    );
    context.__run = run;
    answer = vm.runInContext("__describe(__run())", context, { timeout: 2000 });
  } catch (e) {
    answer = "throws";
  }
  answers[key(c)] = answer;
}

const sorted = {};
for (const k of Object.keys(answers).sort()) sorted[k] = answers[k];
fs.writeFileSync(path.join(here, "answers.json"), JSON.stringify(sorted, null, 1) + "\n");
console.log("answers", Object.keys(sorted).length);
