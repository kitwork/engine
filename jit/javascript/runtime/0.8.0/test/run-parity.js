"use strict";

const fs = require("node:fs");
const path = require("node:path");
const modular = require("../src/index.js");
const bundled = require("../dist/kitwork-expression.js");

const fixture = JSON.parse(fs.readFileSync(path.join(__dirname, "conformance.json"), "utf8"));
const modularEngine = modular.createExpressionEngine();
const bundledEngine = bundled.createExpressionEngine();

function normalize(value) {
  if (value === undefined) return { $undefined: true };
  if (Array.isArray(value)) return value.map(normalize);
  if (value && typeof value === "object") {
    const output = {};
    for (const key of Object.keys(value)) output[key] = normalize(value[key]);
    return output;
  }
  return value;
}

function run(api, engine, entry) {
  const state = JSON.parse(JSON.stringify(entry.state || {}));
  try {
    const compiled = engine.compile(entry.mode, entry.source);
    const result = engine.execute(compiled, api.createObjectEnvironment(state));
    if (Object.prototype.hasOwnProperty.call(entry, "assignValue")) {
      engine.assign(compiled, api.createObjectEnvironment(state), entry.assignValue);
    }
    return { ok: true, value: normalize(result.value), state: normalize(state) };
  } catch (error) {
    return { ok: false, code: error && error.code, message: error && error.message };
  }
}

let passed = 0;
const failures = [];
for (const entry of fixture.cases) {
  const a = run(modular, modularEngine, entry);
  const b = run(bundled, bundledEngine, entry);
  if (JSON.stringify(a) === JSON.stringify(b)) {
    passed++;
    process.stdout.write("✓ " + entry.name + "\n");
  } else {
    failures.push({ name: entry.name, modular: a, bundled: b });
    process.stdout.write("✗ " + entry.name + "\n");
  }
}

const report = { passed, failed: failures.length, total: fixture.cases.length, failures };
process.stdout.write("\n" + JSON.stringify(report, null, 2) + "\n");
fs.writeFileSync(path.join(__dirname, "parity-report.json"), JSON.stringify(report, null, 2) + "\n");
if (failures.length) process.exitCode = 1;
