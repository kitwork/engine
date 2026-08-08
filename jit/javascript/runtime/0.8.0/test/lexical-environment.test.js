"use strict";

const assert = require("node:assert/strict");
const { MODES, createExpressionEngine } = require("../src/expression.js");
const { createLexicalEnvironment } = require("../src/lexical-environment.js");

const engine = createExpressionEngine();
let passed = 0;
let failed = 0;
const failures = [];

function test(name, fn) {
  try {
    fn();
    passed++;
    process.stdout.write("✓ " + name + "\n");
  } catch (error) {
    failed++;
    failures.push({ name, code: error && error.code, message: error && error.message });
    process.stdout.write("✗ " + name + "\n  " + (error.stack || error) + "\n");
  }
}

function make(overrides) {
  const dirty = [];
  const options = Object.assign({
    contexts: {
      $element: { tag: "button" },
      $component: null
    },
    loopFrames: [{ $item: { name: "Item" }, $index: 0 }],
    localScopes: [
      { scope: { open: false }, boundary: "local-near" },
      { scope: { count: 1 }, boundary: "local-parent" }
    ],
    component: {
      title: "Component",
      save() { return this.title; }
    },
    componentBoundary: "component",
    appScope: { locale: "vi", count: 100 },
    appBoundary: "app",
    aliases: new Map([["$modal", { instance: { open: false }, host: "modal" }]]),
    kit: { storage: { get() { return "ok"; } } },
    onDirty(boundary, mutation) { dirty.push({ boundary, mutation }); }
  }, overrides || {});
  return { environment: createLexicalEnvironment(options), dirty, options };
}

test("read order prefers loop, nearest local, component, then app", function () {
  const { environment } = make();
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "$item.name"), environment), "Item");
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "open"), environment), false);
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "title"), environment), "Component");
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "locale"), environment), "vi");
});

test("local owner receives assignment before app shadow", function () {
  const setup = make();
  engine.execute(engine.compile(MODES.ACTION, "count = count + 1"), setup.environment);
  assert.equal(setup.options.localScopes[1].scope.count, 2);
  assert.equal(setup.options.appScope.count, 100);
  assert.equal(setup.dirty[0].boundary, "local-parent");
});

test("new key is created in nearest local scope", function () {
  const setup = make();
  engine.execute(engine.compile(MODES.ACTION, "draft = 'x'"), setup.environment);
  assert.equal(setup.options.localScopes[0].scope.draft, "x");
  assert.equal(setup.dirty[0].boundary, "local-near");
});

test("component method call uses component as this", function () {
  const { environment } = make();
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "save()"), environment), "Component");
});

test("component method cannot be overwritten", function () {
  const { environment } = make();
  assert.throws(function () {
    engine.execute(engine.compile(MODES.ACTION, "save = 1"), environment);
  }, function (error) {
    return error && error.code === "KIT_COMPONENT_STATE_COLLISION";
  });
});

test("component state may be written", function () {
  const setup = make();
  engine.execute(engine.compile(MODES.ACTION, "title = 'Changed'"), setup.environment);
  assert.equal(setup.options.component.title, "Changed");
  assert.equal(setup.dirty[0].boundary, "component");
});

test("direct alias method/state is accessible", function () {
  const setup = make();
  engine.execute(engine.compile(MODES.ACTION, "$modal.open = true"), setup.environment);
  assert.equal(setup.options.aliases.get("$modal").instance.open, true);
  assert.equal(setup.dirty[0].boundary, "modal");
});

test("runtime context binding cannot be reassigned", function () {
  const { environment } = make();
  assert.throws(function () {
    engine.execute(engine.compile(MODES.ACTION, "$element = null"), environment);
  }, function (error) {
    return error && error.code === "KIT_READONLY_CONTEXT";
  });
});

test("runtime DOM handle cannot be mutated through member assignment", function () {
  const { environment } = make();
  assert.throws(function () {
    engine.execute(engine.compile(MODES.ACTION, "$element.tag = 'div'"), environment);
  }, function (error) {
    return error && error.code === "KIT_READONLY_PATH";
  });
});

test("parent component is readable but member assignment is blocked", function () {
  const parent = { open: false, close() { this.open = false; } };
  const setup = make({ contexts: { $parent: parent } });
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "$parent.open"), setup.environment), false);
  assert.throws(function () {
    engine.execute(engine.compile(MODES.ACTION, "$parent.open = true"), setup.environment);
  }, function (error) {
    return error && error.code === "KIT_READONLY_PATH";
  });
});

const report = { passed, failed, total: passed + failed, failures };
process.stdout.write("\n" + JSON.stringify(report, null, 2) + "\n");
if (failed) process.exitCode = 1;
