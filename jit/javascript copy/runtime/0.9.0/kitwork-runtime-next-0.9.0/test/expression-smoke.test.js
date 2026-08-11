"use strict";

const assert = require("assert");
const { MODES, createExpressionEngine, createObjectEnvironment } = require("../src/expression/index.js");
const expression = createExpressionEngine();
const env = createObjectEnvironment({ user: { name: "Quoc" }, count: 1, open: true });

assert.strictEqual(expression.evaluate(expression.compile(MODES.BINDING, "`Hello ${user?.name ?? 'Guest'}`"), env), "Hello Quoc");
assert.strictEqual(expression.evaluate(expression.compile(MODES.BINDING, "missing?.name ?? 'Guest'"), env), "Guest");
expression.execute(expression.compile(MODES.ACTION, "count = count + 1; open = false;"), env);
assert.strictEqual(env.state.count, 2);
assert.strictEqual(env.state.open, false);
const map = expression.evaluate(expression.compile(MODES.NAMED_MAP, "aria-expanded: open; data-state: 'ready';"), env);
assert.deepStrictEqual(map.map((entry) => entry.key), ["aria-expanded", "data-state"]);
console.log("expression smoke: passed");
