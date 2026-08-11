"use strict";

const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const {
  KitworkExpressionError,
  createExpressionEngine,
  createObjectEnvironment
} = require("../src/expression/index.js");

const fixturePath = path.join(__dirname, "expression-conformance.json");
const fixture = JSON.parse(fs.readFileSync(fixturePath, "utf8"));
const engine = createExpressionEngine();

function decodeSpecial(value) {
  if (value && typeof value === "object" && value.$undefined === true && Object.keys(value).length === 1) {
    return undefined;
  }
  if (Array.isArray(value)) return value.map(decodeSpecial);
  if (value && typeof value === "object") {
    const output = {};
    for (const key of Object.keys(value)) output[key] = decodeSpecial(value[key]);
    return output;
  }
  return value;
}

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

let passed = 0;
const failures = [];

for (const entry of fixture.cases) {
  const state = decodeSpecial(JSON.parse(JSON.stringify(entry.state || {})));
  const environment = createObjectEnvironment(state);

  try {
    const compiled = engine.compile(entry.mode, entry.source);
    const result = engine.execute(compiled, environment);

    if (entry.expectError) {
      throw new Error("Expected error " + entry.expectError + " but execution succeeded");
    }

    assert.deepEqual(normalize(result.value), normalize(decodeSpecial(entry.expectValue)));

    if (Object.prototype.hasOwnProperty.call(entry, "assignValue")) {
      engine.assign(compiled, environment, decodeSpecial(entry.assignValue));
    }

    if (entry.expectState) {
      assert.deepEqual(normalize(state), normalize(decodeSpecial(entry.expectState)));
    }

    passed++;
    process.stdout.write("✓ " + entry.name + "\n");
  } catch (error) {
    if (entry.expectError && error instanceof KitworkExpressionError && error.code === entry.expectError) {
      passed++;
      process.stdout.write("✓ " + entry.name + "\n");
      continue;
    }

    failures.push({
      name: entry.name,
      expectedError: entry.expectError || null,
      actualCode: error && error.code || null,
      message: error && error.message || String(error)
    });
    process.stdout.write("✗ " + entry.name + "\n");
  }
}

const report = {
  schema: fixture.schema,
  specification: fixture.specification,
  passed,
  failed: failures.length,
  total: fixture.cases.length,
  failures
};

process.stdout.write("\n" + JSON.stringify(report, null, 2) + "\n");
fs.writeFileSync(path.join(__dirname, "conformance-report.json"), JSON.stringify(report, null, 2) + "\n");
if (failures.length) process.exitCode = 1;
