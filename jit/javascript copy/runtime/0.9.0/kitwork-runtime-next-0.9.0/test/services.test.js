"use strict";

var assert = require("assert");
var taskModule = require("../src/service/task.js");
var requestModule = require("../src/service/request.js");

async function testTaskService() {
  var task = taskModule.createTaskService({ globalObject: globalThis });
  var owner = {};
  var firstAborted = false;

  var first = task.latest(owner, "search", async function (context) {
    try {
      await task.delay(60, { signal: context.signal });
      return "old";
    } catch (error) {
      firstAborted = context.signal.aborted && error && error.name === "AbortError";
      throw error;
    }
  }).catch(function (error) { return error && error.name; });

  var second = task.latest(owner, "search", async function (context) {
    await task.delay(5, { signal: context.signal });
    return "new";
  });

  assert.strictEqual(await second, "new");
  assert.strictEqual(await first, "AbortError");
  assert.strictEqual(firstAborted, true);
  assert.strictEqual(task.pending(owner), 0);

  var allOwner = {};
  var signalSeen = null;
  var pending = task.run(allOwner, async function (context) {
    signalSeen = context.signal;
    await task.delay(100, { signal: context.signal });
  }).catch(function (error) { return error && error.name; });
  assert.strictEqual(task.pending(allOwner), 1);
  assert.strictEqual(task.abort(allOwner), true);
  assert.strictEqual(await pending, "AbortError");
  assert.strictEqual(signalSeen.aborted, true);
  assert.strictEqual(task.pending(allOwner), 0);
}

async function testRequestService() {
  var captured = null;
  var fakeGlobal = {
    AbortController: globalThis.AbortController,
    Headers: globalThis.Headers,
    FormData: globalThis.FormData,
    Blob: globalThis.Blob,
    URL: globalThis.URL,
    location: { href: "https://example.test/page" },
    document: {
      querySelector: function () { return { content: "csrf-token" }; }
    },
    fetch: async function (url, options) {
      captured = { url: url, options: options };
      return new Response(JSON.stringify({ ok: true }), {
        status: 201,
        headers: { "content-type": "application/json" }
      });
    }
  };

  var request = requestModule.createRequestService(fakeGlobal);
  var result = await request.post("https://example.test/api", { name: "Kitwork" }, { timeout: 100 });
  assert.strictEqual(result.ok, true);
  assert.strictEqual(result.status, 201);
  assert.deepStrictEqual(result.data, { ok: true });
  assert.strictEqual(captured.options.method, "POST");
  assert.strictEqual(captured.options.headers.get("X-CSRF-Token"), "csrf-token");
  assert.strictEqual(captured.options.headers.get("Content-Type"), "application/json");
  assert.strictEqual(captured.options.body, JSON.stringify({ name: "Kitwork" }));
}

(async function () {
  await testTaskService();
  await testRequestService();
  console.log("service tests: passed");
})().catch(function (error) {
  console.error(error);
  process.exitCode = 1;
});
