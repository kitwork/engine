"use strict";

const fs = require("fs");
const path = require("path");

const root = path.resolve(__dirname, "..");
const entry = path.join(root, "src", "runtime.js");
const output = path.join(root, "dist", "kitwork-runtime.js");
const modules = new Map();

function normalize(file) {
  let resolved = file;
  if (!path.extname(resolved)) resolved += ".js";
  return path.normalize(resolved);
}

function collect(file) {
  file = normalize(file);
  if (modules.has(file)) return;
  const source = fs.readFileSync(file, "utf8");
  const deps = [];
  source.replace(/require\(["'](.+?)["']\)/g, (_, request) => {
    if (!request.startsWith(".")) throw new Error(`External require not supported: ${request}`);
    const dep = normalize(path.resolve(path.dirname(file), request));
    deps.push({ request, file: dep });
    collect(dep);
    return _;
  });
  modules.set(file, { source, deps });
}

collect(entry);
const ids = new Map();
Array.from(modules.keys()).forEach((file, index) => ids.set(file, index));

function transform(moduleFile, data) {
  let source = data.source;
  data.deps.forEach(({ request, file }) => {
    const pattern = new RegExp(`require\\(["']${request.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}["']\\)`, "g");
    source = source.replace(pattern, `__require(${ids.get(file)})`);
  });
  return source;
}

let bundle = `/* Kitwork Client Runtime Next — M2 Core\n * Generated from modular source. Do not edit dist directly.\n */\n(function(global){\n"use strict";\nvar __modules = {\n`;
Array.from(modules.entries()).forEach(([file, data], index, list) => {
  bundle += `${ids.get(file)}: function(module, exports, __require){\n${transform(file, data)}\n}`;
  if (index < list.length - 1) bundle += ",";
  bundle += "\n";
});
bundle += `};\nvar __cache = {};\nfunction __require(id){\n  if(__cache[id]) return __cache[id].exports;\n  var module = __cache[id] = { exports: {} };\n  __modules[id](module, module.exports, __require);\n  return module.exports;\n}\nvar api = __require(${ids.get(entry)});\nif (global) global.KitworkRuntimeNext = api;\n})(typeof window !== "undefined" ? window : globalThis);\n`;

fs.writeFileSync(output, bundle);
console.log(`Built ${output} (${bundle.length} bytes, ${modules.size} modules)`);
