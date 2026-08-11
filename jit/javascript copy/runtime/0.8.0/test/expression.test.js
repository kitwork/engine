"use strict";

const assert = require("node:assert/strict");
const {
  MODES,
  KitworkExpressionError,
  createExpressionEngine,
  createObjectEnvironment,
  testing
} = require("../src/expression.js");

const engine = createExpressionEngine({
  evaluationBudget: 10000,
  callDepthLimit: 32,
  maxCacheEntries: 64
});

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
    failures.push({ name, error });
    process.stdout.write("✗ " + name + "\n");
    process.stdout.write("  " + (error && error.stack ? error.stack : String(error)) + "\n");
  }
}

function expectError(code, fn) {
  assert.throws(fn, function (error) {
    return error instanceof KitworkExpressionError && error.code === code;
  });
}

function env(state, options) {
  return createObjectEnvironment(state, options);
}

// ---------------------------------------------------------------------------
// Lexer and parser correctness
// ---------------------------------------------------------------------------

test("numbers support integers, decimals, leading dot, and exponent", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "1 + 2.5 + .5 + 1e2"), env({})), 104);
});

test("invalid exponent is rejected", function () {
  expectError("KIT_PARSE_INVALID_NUMBER", function () {
    engine.compile(MODES.BINDING, "1e");
  });
});

test("multiple decimal points are rejected as trailing input", function () {
  expectError("KIT_PARSE_TRAILING_INPUT", function () {
    engine.compile(MODES.BINDING, "1.2.3");
  });
});

test("strings decode common and unicode escapes", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "'a\\n\\u0042'"), env({})), "a\nB");
});

test("strict equality is supported", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "count === 2"), env({ count: 2 })), true);
});

test("loose equality is rejected", function () {
  expectError("KIT_PARSE_LOOSE_EQUALITY", function () {
    engine.compile(MODES.BINDING, "count == 2");
  });
});

test("missing closing token reports expected token", function () {
  expectError("KIT_PARSE_EXPECTED_TOKEN", function () {
    engine.compile(MODES.BINDING, "items[0");
  });
});

test("duplicate object keys are rejected", function () {
  expectError("KIT_PARSE_OBJECT_DUPLICATE_KEY", function () {
    engine.compile(MODES.BINDING, "{ a: 1, a: 2 }");
  });
});

test("blocked object key is rejected during parsing", function () {
  expectError("KIT_BLOCKED_MEMBER", function () {
    engine.compile(MODES.BINDING, "{ constructor: 1 }");
  });
});

// ---------------------------------------------------------------------------
// Binding language
// ---------------------------------------------------------------------------

test("arithmetic and precedence", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "1 + 2 * 3"), env({})), 7);
});

test("logical operators short circuit", function () {
  let calls = 0;
  const state = {
    run() { calls++; return true; }
  };
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "false && run()"), env(state)), false);
  assert.equal(calls, 0);
});

test("nullish preserves zero", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "value ?? 10"), env({ value: 0 })), 0);
});

test("optional member access returns undefined", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "user?.profile?.name"), env({ user: null })), undefined);
});

test("non-optional null member access raises a diagnostic", function () {
  expectError("KIT_NULL_MEMBER_ACCESS", function () {
    engine.evaluate(engine.compile(MODES.BINDING, "user.profile.name"), env({ user: null }));
  });
});

test("optional computed access", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "user?.['name']"), env({ user: { name: "Quoc" } })), "Quoc");
});

test("computed property access", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "items[index].name"), env({
    items: [{ name: "A" }, { name: "B" }],
    index: 1
  })), "B");
});

test("template literals interpolate values", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "`Hello ${user.name}`"), env({
    user: { name: "Quoc" }
  })), "Hello Quoc");
});

test("template null and undefined interpolate as empty string", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "`A${missing}B${value}C`"), env({ value: null })), "ABC");
});

test("template scanner handles closing brace inside string", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "`Value ${pick('}')}`"), env({
    pick(value) { return value; }
  })), "Value }");
});

test("template scanner handles nested template", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "`Outer ${`Inner ${name}`}`"), env({ name: "Q" })), "Outer Inner Q");
});

test("assignment is rejected in binding mode", function () {
  expectError("KIT_BINDING_ASSIGNMENT", function () {
    engine.compile(MODES.BINDING, "count = 2");
  });
});

test("binding Promise is rejected", function () {
  expectError("KIT_ASYNC_BINDING", function () {
    engine.execute(engine.compile(MODES.BINDING, "load()"), env({
      load() { return Promise.resolve(1); }
    }));
  });
});

test("method call preserves owning object as this", function () {
  const state = {
    counter: {
      value: 2,
      get() { return this.value; }
    }
  };
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "counter.get()"), env(state)), 2);
});

// ---------------------------------------------------------------------------
// Action program and mutation
// ---------------------------------------------------------------------------

test("action program supports assignment and semicolon sequence", function () {
  const state = { count: 0, open: false };
  const result = engine.execute(engine.compile(MODES.ACTION, "count = count + 1; open = true; count"), env(state));
  assert.equal(result.value, 1);
  assert.deepEqual(state, { count: 1, open: true });
  assert.equal(result.mutations.length, 2);
});

test("action assignment to nested member", function () {
  const state = { user: { name: "A" } };
  engine.execute(engine.compile(MODES.ACTION, "user.name = 'B'"), env(state));
  assert.equal(state.user.name, "B");
});

test("action cannot assign to runtime context root", function () {
  expectError("KIT_READONLY_PATH", function () {
    engine.execute(engine.compile(MODES.ACTION, "$element.title = 'x'"), env({}, {
      contexts: { $element: { title: "" } }
    }));
  });
});

test("action cannot replace direct alias binding", function () {
  expectError("KIT_READONLY_CONTEXT", function () {
    engine.execute(engine.compile(MODES.ACTION, "$modal = null"), env({}, {
      aliases: { $modal: { open: false } }
    }));
  });
});

test("action may mutate state on direct component alias", function () {
  const modal = { open: false };
  engine.execute(engine.compile(MODES.ACTION, "$modal.open = true"), env({}, {
    aliases: { $modal: modal }
  }));
  assert.equal(modal.open, true);
});

test("blocked member access is rejected", function () {
  expectError("KIT_BLOCKED_MEMBER", function () {
    engine.evaluate(engine.compile(MODES.BINDING, "value.constructor"), env({ value: {} }));
  });
});

test("action tracks every top-level Promise once", function () {
  const p1 = Promise.resolve(1);
  const p2 = Promise.resolve(2);
  const result = engine.execute(engine.compile(MODES.ACTION, "first(); second()"), env({
    first() { return p1; },
    second() { return p2; }
  }));
  assert.deepEqual(result.effects, [p1, p2]);
});

// ---------------------------------------------------------------------------
// Named maps, class values, writable paths, identity, iterator
// ---------------------------------------------------------------------------

test("named map supports bare hyphenated attributes without quotes", function () {
  const result = engine.evaluate(engine.compile(MODES.NAMED_MAP,
    "aria-expanded: open; data-state: status; disabled: saving"), env({
      open: false,
      status: "ready",
      saving: true
    }));
  assert.deepEqual(result, [
    { key: "aria-expanded", value: false },
    { key: "data-state", value: "ready" },
    { key: "disabled", value: true }
  ]);
});

test("named map supports CSS custom property keys", function () {
  const result = engine.evaluate(engine.compile(MODES.NAMED_MAP, "--progress: progress;"), env({ progress: "50%" }));
  assert.deepEqual(result, [{ key: "--progress", value: "50%" }]);
});

test("named map supports quoted colon key", function () {
  const result = engine.evaluate(engine.compile(MODES.NAMED_MAP, "'xlink:href': href;"), env({ href: "#icon" }));
  assert.deepEqual(result, [{ key: "xlink:href", value: "#icon" }]);
});

test("named map duplicate key is rejected", function () {
  expectError("KIT_MAP_DUPLICATE_KEY", function () {
    engine.compile(MODES.NAMED_MAP, "title: a; title: b;");
  });
});

test("named map does not split semicolon inside string", function () {
  const result = engine.evaluate(engine.compile(MODES.NAMED_MAP, "title: 'A;B';"), env({}));
  assert.deepEqual(result, [{ key: "title", value: "A;B" }]);
});

test("class map is canonical and accepts quoted Tailwind variant", function () {
  const compiled = engine.compile(MODES.CLASS_VALUE, "active: open; 'md:grid-cols-6': desktop;");
  assert.equal(compiled.ast.type, "ClassMap");
  assert.deepEqual(engine.evaluate(compiled, env({ open: true, desktop: false })), [
    { key: "active", value: true },
    { key: "md:grid-cols-6", value: false }
  ]);
});

test("class object expression remains accepted", function () {
  const compiled = engine.compile(MODES.CLASS_VALUE, "{ active: open, loading: saving }");
  assert.equal(compiled.ast.type, "ClassExpression");
  const value = engine.evaluate(compiled, env({ open: true, saving: false }));
  assert.equal(value.active, true);
  assert.equal(value.loading, false);
});

test("class ternary remains accepted", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.CLASS_VALUE, "open ? 'active' : 'disabled'"), env({ open: false })), "disabled");
});

test("writable path can read and assign", function () {
  const state = { form: { name: "A" } };
  const compiled = engine.compile(MODES.WRITABLE_PATH, "form.name");
  assert.equal(engine.evaluate(compiled, env(state)), "A");
  engine.assign(compiled, env(state), "B");
  assert.equal(state.form.name, "B");
});

test("optional path is not writable", function () {
  expectError("KIT_MODEL_PATH", function () {
    engine.compile(MODES.WRITABLE_PATH, "form?.name");
  });
});

test("call expression is not writable path", function () {
  expectError("KIT_MODEL_PATH", function () {
    engine.compile(MODES.WRITABLE_PATH, "getForm().name");
  });
});

test("identity literal is returned unchanged after trim", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.IDENTITY, "  $paymentModal  "), env({})), "$paymentModal");
});

test("iterator parses item, index, and collection", function () {
  const result = engine.evaluate(engine.compile(MODES.ITERATOR, "$item, $index of items"), env({ items: [1, 2] }));
  assert.equal(result.itemName, "$item");
  assert.equal(result.indexName, "$index");
  assert.deepEqual(result.collection, [1, 2]);
});

test("iterator rejects in syntax", function () {
  expectError("KIT_ITERATOR_PARSE", function () {
    engine.compile(MODES.ITERATOR, "$item in items");
  });
});

// ---------------------------------------------------------------------------
// Cache, limits, and environment contract
// ---------------------------------------------------------------------------

test("compile cache returns the same compiled object", function () {
  const a = engine.compile(MODES.BINDING, "count + 1");
  const b = engine.compile(MODES.BINDING, "count + 1");
  assert.strictEqual(a, b);
});

test("evaluation budget is enforced", function () {
  expectError("KIT_EVALUATION_BUDGET", function () {
    engine.execute(engine.compile(MODES.BINDING, "1 + 2 + 3 + 4"), env({}), { evaluationBudget: 2 });
  });
});

test("missing identifier resolves to undefined", function () {
  assert.equal(engine.evaluate(engine.compile(MODES.BINDING, "missing"), env({})), undefined);
});

test("environment mutation callback receives member mutation", function () {
  const mutations = [];
  const state = { user: { name: "A" } };
  engine.execute(engine.compile(MODES.ACTION, "user.name = 'B'"), env(state, {
    onMutation(mutation) { mutations.push(mutation); }
  }));
  assert.equal(mutations.length, 1);
  assert.equal(mutations[0].key, "name");
});

test("testing lexer preserves optional operator token", function () {
  const tokens = testing.lex("user?.name");
  assert.equal(tokens[1].value, "?.");
});

const report = {
  passed,
  failed,
  total: passed + failed,
  failures: failures.map(({ name, error }) => ({
    name,
    code: error && error.code,
    message: error && error.message
  }))
};

process.stdout.write("\n" + JSON.stringify(report, null, 2) + "\n");
if (failed) process.exitCode = 1;
