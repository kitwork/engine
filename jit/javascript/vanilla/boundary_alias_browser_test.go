package vanilla

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// This browser contract deliberately measures rendering through authored
// component getters. It therefore proves boundary ownership without exposing
// the scheduler, component records, or alias registry as public API.
func TestBrowserDirtyBoundariesAndExternalAliasCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS dirty-boundary browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	kitJS := readVanillaFile(t, "kit.js")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/contracts/dirty-boundary-alias.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(dirtyBoundaryAliasDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/contracts/dirty-boundary-alias.html")
}

const dirtyBoundaryAliasDocument = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>KitJS dirty boundary and alias contract</title></head>
<body>
  <section data-kit-component="isolation-a">
    <button id="isolation-a-increment" type="button" data-kit-click="increment()">A+</button>
    <button id="isolation-a-double-write" type="button" data-kit-click="count = count + 1; other = other + 1">A writes twice</button>
    <output id="isolation-a-output" data-kit-text="rendered">server-a</output>
  </section>

  <section data-kit-component="isolation-b">
    <output id="isolation-b-output" data-kit-text="rendered">server-b</output>
  </section>

  <section data-kit-component="nested-parent">
    <button id="nested-parent-increment" type="button" data-kit-click="increment()">parent+</button>
    <output id="nested-parent-output" data-kit-text="rendered">server-parent</output>
    <section data-kit-component="nested-child">
      <button id="nested-child-increment" type="button" data-kit-click="increment()">child+</button>
      <output id="nested-child-output" data-kit-text="rendered">server-child</output>
    </section>
  </section>

  <section data-kit-component="same-tick-parent">
    <button id="same-tick-remove" type="button" data-kit-click="$removalChild.dirty(); $interleavedSibling.dirty(); remove()">dirty child and sibling then remove child</button>
    <template data-kit-if="showChild">
      <section id="same-tick-child" data-kit-component="same-tick-child" data-kit-as="$removalChild">
        <output id="same-tick-child-output" data-kit-text="rendered">server-same-tick-child</output>
      </section>
    </template>
  </section>
  <section data-kit-component="same-tick-unrelated" data-kit-as="$interleavedSibling">
    <output id="same-tick-unrelated-output" data-kit-text="rendered">server-same-tick-unrelated</output>
  </section>

  <section data-kit-component="async-init-a">
    <output id="async-init-a-output" data-kit-text="rendered">server-async-a</output>
  </section>
  <section data-kit-component="async-init-b">
    <output id="async-init-b-output" data-kit-text="rendered">server-async-b</output>
  </section>

  <section data-kit-component="external-source">
    <button id="external-open" type="button" data-kit-click="$dialog.open(() => handleSuccess())">open dialog</button>
    <button id="external-open-load-source" type="button" data-kit-click="$dialog.open(); loadSource()">open dialog and load source</button>
    <button id="external-open-callback-source" type="button" data-kit-click="$dialog.open(() => loadCallbackSource())">open dialog with async source callback</button>
    <button id="external-transparent-wrapper" type="button" data-kit-click="identity($dialog.loadTransparent())">return dialog Promise through source identity</button>
    <button id="external-load" type="button" data-kit-click="$dialog.load()">load dialog</button>
    <button id="missing-alias" type="button" data-kit-click="$missing.open(() => handleSuccess())">missing alias</button>
    <button id="duplicate-alias" type="button" data-kit-click="$duplicate.touch()">duplicate alias</button>
    <output id="external-success-output" data-kit-text="rendered">server-success</output>
    <output id="external-source-load-output" data-kit-text="deep.value">server-source-load</output>
    <output id="external-callback-load-output" data-kit-text="callbackDeep.value">server-callback-load</output>
  </section>

  <button id="standalone-open" type="button" data-kit-click="$dialog.open(null)">open dialog outside a component</button>
  <button id="shared-alias-trigger" type="button" data-kit-click="$sharedA.shared(); $sharedB.shared()">settle one Promise for two aliases</button>

  <section data-kit-component="shared-owner-a" data-kit-as="$sharedA">
    <output id="shared-owner-a-output" data-kit-text="rendered">server-shared-a</output>
  </section>
  <section data-kit-component="shared-owner-b" data-kit-as="$sharedB">
    <output id="shared-owner-b-output" data-kit-text="rendered">server-shared-b</output>
  </section>

  <section id="external-dialog" data-kit-component="external-dialog" data-kit-as="$dialog" data-kit-show="visible" hidden>
    <output id="external-dialog-output" data-kit-text="rendered">server-dialog</output>
    <output id="external-dialog-load-output" data-kit-text="deep.value">server-load</output>
    <output id="external-dialog-transparent-output" data-kit-text="transparentDeep.value">server-transparent</output>
    <button id="external-confirm" type="button" data-kit-click="confirm()">confirm</button>
    <button id="external-close" type="button" data-kit-click="close()">close</button>
  </section>

  <section data-kit-component="alias-binding-probe">
    <output id="alias-binding-output" data-kit-text="$dialog.visible">server-alias-state</output>
  </section>

  <section data-kit-component="duplicate-one" data-kit-as="$duplicate">
    <output id="duplicate-one-output" data-kit-text="touches">server-duplicate-one</output>
  </section>
  <section data-kit-component="duplicate-two" data-kit-as="$duplicate">
    <output id="duplicate-two-output" data-kit-text="touches">server-duplicate-two</output>
  </section>

  <section data-kit-component="unrelated-probe">
    <output id="unrelated-output" data-kit-text="rendered">server-unrelated</output>
  </section>

  <section data-kit-component="thenable-probe">
    <button id="thenable-throw" type="button" data-kit-click="throwingThenable()">throwing then getter</button>
    <button id="thenable-multi" type="button" data-kit-click="multiThenable()">multi-settle thenable</button>
    <button id="thenable-call-throw" type="button" data-kit-click="throwingThenCall()">throwing then call</button>
    <output id="thenable-output" data-kit-text="rendered">server-thenable</output>
  </section>

  <script>
  (function () {
    globalThis.__boundaryRenders = {
      isolationA: 0,
      isolationB: 0,
      nestedParent: 0,
      nestedChild: 0,
      sameTickChild: 0,
      sameTickUnrelated: 0,
      asyncA: 0,
      asyncB: 0,
      source: 0,
      dialog: 0,
      sharedA: 0,
      sharedB: 0,
      unrelated: 0,
      thenable: 0
    };
    globalThis.__asyncInitCalls = 0;
    globalThis.__sameTickDirtyCalls = 0;
    globalThis.__dialogCalls = { open: 0, success: 0 };
    globalThis.__duplicateCalls = { one: 0, two: 0 };
    globalThis.__kitDiagnostics = [];
    globalThis.__sharedOwnerPromise = new Promise(function (resolve) {
      globalThis.__settleSharedOwnerPromise = function () {
        delete globalThis.__settleSharedOwnerPromise;
        resolve();
      };
    });
    var originalError = console.error;
    console.error = function () {
      globalThis.__kitDiagnostics.push(Array.prototype.map.call(arguments, String).join(" "));
      return originalError.apply(this, arguments);
    };
  })();
  </script>
  <script src="/kit.js"></script>
  <script>
    kit.component("isolation-a", {
      count: 0,
      other: 0,
      get rendered() {
        globalThis.__boundaryRenders.isolationA++;
        return this.count + this.other;
      },
      increment: function () { this.count++; }
    });
    kit.component("isolation-b", {
      count: 0,
      get rendered() {
        globalThis.__boundaryRenders.isolationB++;
        return this.count;
      }
    });
    kit.component("nested-parent", {
      count: 0,
      get rendered() {
        globalThis.__boundaryRenders.nestedParent++;
        return this.count;
      },
      increment: function () { this.count++; }
    });
    kit.component("nested-child", {
      count: 0,
      get rendered() {
        globalThis.__boundaryRenders.nestedChild++;
        return this.count;
      },
      increment: function () { this.count++; }
    });
    kit.component("same-tick-parent", {
      showChild: true,
      remove: function () { this.showChild = false; }
    });
    kit.component("same-tick-child", {
      count: 0,
      get rendered() {
        globalThis.__boundaryRenders.sameTickChild++;
        return this.count;
      },
      dirty: function () {
        globalThis.__sameTickDirtyCalls++;
        this.count++;
      }
    });
    kit.component("same-tick-unrelated", {
      count: 0,
      get rendered() {
        globalThis.__boundaryRenders.sameTickUnrelated++;
        return this.count;
      },
      dirty: function () { this.count++; }
    });
    kit.component("async-init-a", {
      ready: false,
      get rendered() {
        globalThis.__boundaryRenders.asyncA++;
        return this.ready ? "ready" : "waiting";
      },
      init: function () {
        var owner = this;
        globalThis.__asyncInitCalls++;
        return new Promise(function (resolve) {
          setTimeout(function () {
            owner.ready = true;
            resolve();
          }, 80);
        });
      }
    });
    kit.component("async-init-b", {
      ready: false,
      get rendered() {
        globalThis.__boundaryRenders.asyncB++;
        return this.ready ? "ready" : "waiting";
      }
    });
    kit.component("external-source", {
      successCount: 0,
      deep: { value: 0 },
      callbackDeep: { value: 0 },
      get rendered() {
        globalThis.__boundaryRenders.source++;
        return this.successCount;
      },
      handleSuccess: function () {
        globalThis.__dialogCalls.success++;
        this.successCount++;
      },
      identity: function (value) { return value; },
      loadSource: function () {
        var owner = this;
        return new Promise(function (resolve) {
          globalThis.__settleSourceLoad = function () {
            delete globalThis.__settleSourceLoad;
            owner.deep.value++;
            resolve();
          };
        });
      },
      loadCallbackSource: function () {
        var owner = this;
        return new Promise(function (resolve) {
          globalThis.__settleCallbackSource = function () {
            delete globalThis.__settleCallbackSource;
            owner.callbackDeep.value++;
            resolve();
          };
        });
      }
    });
    kit.component("shared-owner-a", {
      deep: { value: 0 },
      get rendered() {
        globalThis.__boundaryRenders.sharedA++;
        return this.deep.value;
      },
      shared: function () {
        this.deep.value++;
        return globalThis.__sharedOwnerPromise;
      }
    });
    kit.component("shared-owner-b", {
      deep: { value: 0 },
      get rendered() {
        globalThis.__boundaryRenders.sharedB++;
        return this.deep.value;
      },
      shared: function () {
        this.deep.value++;
        return globalThis.__sharedOwnerPromise;
      }
    });
    kit.component("external-dialog", {
      visible: false,
      callback: null,
      deep: { value: 0 },
      transparentDeep: { value: 0 },
      get rendered() {
        globalThis.__boundaryRenders.dialog++;
        return this.visible ? "open" : "closed";
      },
      open: function (callback) {
        globalThis.__dialogCalls.open++;
        this.callback = callback;
        this.visible = true;
      },
      load: function () {
        var owner = this;
        return new Promise(function (resolve) {
          setTimeout(function () {
            owner.deep.value++;
            resolve();
          }, 30);
        });
      },
      loadTransparent: function () {
        var owner = this;
        return new Promise(function (resolve) {
          globalThis.__settleTransparentDialog = function () {
            delete globalThis.__settleTransparentDialog;
            owner.transparentDeep.value++;
            resolve();
          };
        });
      },
      confirm: function () {
        var callback = this.callback;
        this.callback = null;
        this.visible = false;
        return callback ? callback() : undefined;
      },
      close: function () {
        this.callback = null;
        this.visible = false;
      }
    });
    kit.component("alias-binding-probe", { unused: 0 });
    kit.component("duplicate-one", {
      touches: 0,
      touch: function () {
        globalThis.__duplicateCalls.one++;
        this.touches++;
      }
    });
    kit.component("duplicate-two", {
      touches: 0,
      touch: function () {
        globalThis.__duplicateCalls.two++;
        this.touches++;
      }
    });
    kit.component("unrelated-probe", {
      value: "still",
      get rendered() {
        globalThis.__boundaryRenders.unrelated++;
        return this.value;
      }
    });
    kit.component("thenable-probe", {
      deep: { value: 0 },
      get rendered() {
        globalThis.__boundaryRenders.thenable++;
        return this.deep.value;
      },
      throwingThenable: function () {
        var value = {};
        Object.defineProperty(value, "then", {
          get: function () { throw new Error("throwing then getter"); }
        });
        return value;
      },
      multiThenable: function () {
        var owner = this;
        return {
          then: function (resolve, reject) {
            owner.deep.value++;
            resolve();
            resolve();
            reject(new Error("late thenable rejection"));
          }
        };
      },
      throwingThenCall: function () {
        var owner = this;
        return {
          then: function () {
            owner.deep.value++;
            throw new Error("throwing then call");
          }
        };
      }
    });
  </script>
  <script>
` + browserHarness + `
` + dirtyBoundaryAliasAssertions + `
  </script>
</body>
</html>`

const dirtyBoundaryAliasAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var renders = globalThis.__boundaryRenders;
  __kitTestPublicContract();

  await waitFor(function () {
    return document.getElementById("isolation-a-output").textContent === "0" &&
      document.getElementById("isolation-b-output").textContent === "0" &&
      document.getElementById("nested-parent-output").textContent === "0" &&
      document.getElementById("nested-child-output").textContent === "0" &&
      document.getElementById("same-tick-child-output") &&
      document.getElementById("same-tick-child-output").textContent === "0" &&
      document.getElementById("same-tick-unrelated-output").textContent === "0" &&
      document.getElementById("async-init-a-output").textContent === "waiting" &&
      document.getElementById("async-init-b-output").textContent === "waiting" &&
      document.getElementById("external-success-output").textContent === "0" &&
      document.getElementById("external-source-load-output").textContent === "0" &&
      document.getElementById("external-callback-load-output").textContent === "0" &&
      document.getElementById("shared-owner-a-output").textContent === "0" &&
      document.getElementById("shared-owner-b-output").textContent === "0" &&
      document.getElementById("external-dialog-output").textContent === "closed" &&
      document.getElementById("external-dialog-transparent-output").textContent === "0" &&
      document.getElementById("unrelated-output").textContent === "still" &&
      document.getElementById("thenable-output").textContent === "0";
  }, "dirty-boundary fixture did not initialize");

  assert(document.getElementById("alias-binding-output").textContent === "server-alias-state",
    "component alias leaked into binding resolution");
  assert(globalThis.__asyncInitCalls === 1, "async init did not run exactly once");

  var asyncBBefore = renders.asyncB;
  await waitFor(function () {
    return document.getElementById("async-init-a-output").textContent === "ready";
  }, "async init mutation did not render its owner boundary");
  assert(renders.asyncB === asyncBBefore,
    "settling async init evaluated an unrelated boundary");

  var isolationABefore = renders.isolationA;
  var isolationBBefore = renders.isolationB;
  document.getElementById("isolation-a-increment").click();
  await waitFor(function () {
    return document.getElementById("isolation-a-output").textContent === "1";
  }, "boundary A mutation did not render boundary A");
  assert(renders.isolationA > isolationABefore, "boundary A getter was not evaluated");
  assert(renders.isolationB === isolationBBefore,
    "boundary A mutation evaluated boundary B");

  isolationABefore = renders.isolationA;
  isolationBBefore = renders.isolationB;
  document.getElementById("isolation-a-double-write").click();
  await waitFor(function () {
    return document.getElementById("isolation-a-output").textContent === "3";
  }, "two synchronous writes did not render their owner");
  assert(renders.isolationA === isolationABefore + 1,
    "two synchronous writes rendered their owner more than once in one microtask");
  assert(renders.isolationB === isolationBBefore,
    "coalesced writes evaluated a sibling boundary");

  var parentBefore = renders.nestedParent;
  var childBefore = renders.nestedChild;
  document.getElementById("nested-parent-increment").click();
  await waitFor(function () {
    return document.getElementById("nested-parent-output").textContent === "1";
  }, "nested parent mutation did not render the parent");
  assert(renders.nestedParent > parentBefore, "nested parent getter was not evaluated");
  assert(renders.nestedChild === childBefore,
    "rendering a parent crossed into its nested component owner");

  parentBefore = renders.nestedParent;
  childBefore = renders.nestedChild;
  document.getElementById("nested-child-increment").click();
  await waitFor(function () {
    return document.getElementById("nested-child-output").textContent === "1";
  }, "nested child mutation did not render the child");
  assert(renders.nestedChild > childBefore, "nested child getter was not evaluated");
  assert(renders.nestedParent === parentBefore,
    "rendering a nested child evaluated its parent owner");

  var removedChild = document.getElementById("same-tick-child");
  var removedChildOutput = document.getElementById("same-tick-child-output");
  var removedChildRenders = renders.sameTickChild;
  var interleavedRenders = renders.sameTickUnrelated;
  document.getElementById("same-tick-remove").click();
  await waitFor(function () {
    return !removedChild.isConnected &&
      document.getElementById("same-tick-unrelated-output").textContent === "1";
  }, "parent did not remove its dirty child around an interleaved sibling record");
  await nextTurn();
  assert(globalThis.__sameTickDirtyCalls === 1,
    "same-tick fixture did not dirty the child before parent removal");
  assert(renders.sameTickChild === removedChildRenders,
    "a dirty child rendered after its parent detached and disposed it");
  assert(removedChildOutput.textContent === "0",
    "disposed child state updated detached DOM after parent removal");
  assert(renders.sameTickUnrelated === interleavedRenders + 1,
    "interleaved unrelated dirty boundary did not render exactly once");

  var sharedABefore = renders.sharedA;
  var sharedBBefore = renders.sharedB;
  var sharedSourceBefore = renders.source;
  var sharedUnrelatedBefore = renders.unrelated;
  document.getElementById("shared-alias-trigger").click();
  await nextTurn();
  assert(document.getElementById("shared-owner-a-output").textContent === "0" &&
    document.getElementById("shared-owner-b-output").textContent === "0",
    "shared Promise owners repainted before their Promise settled");
  assert(renders.sharedA === sharedABefore && renders.sharedB === sharedBBefore,
    "shared Promise calls synchronously dirtied an owner");
  globalThis.__settleSharedOwnerPromise();
  await waitFor(function () {
    return document.getElementById("shared-owner-a-output").textContent === "1" &&
      document.getElementById("shared-owner-b-output").textContent === "1";
  }, "one shared Promise did not repaint both top-level alias owners");
  assert(renders.sharedA === sharedABefore + 1 && renders.sharedB === sharedBBefore + 1,
    "shared Promise settlement did not repaint each alias owner exactly once");
  assert(renders.source === sharedSourceBefore && renders.unrelated === sharedUnrelatedBefore,
    "shared Promise settlement repainted an origin or unrelated boundary");

  var transparentSourceBefore = renders.source;
  var transparentDialogBefore = renders.dialog;
  var transparentUnrelatedBefore = renders.unrelated;
  document.getElementById("external-transparent-wrapper").click();
  await waitFor(function () { return typeof globalThis.__settleTransparentDialog === "function"; },
    "source identity wrapper did not return the dialog Promise");
  assert(renders.source === transparentSourceBefore && renders.dialog === transparentDialogBefore,
    "unsettled transparent Promise synchronously repainted an owner");
  globalThis.__settleTransparentDialog();
  await waitFor(function () {
    return document.getElementById("external-dialog-transparent-output").textContent === "1";
  }, "transparent wrapper lost the dialog Promise owner");
  assert(renders.dialog === transparentDialogBefore + 1,
    "transparent dialog Promise did not repaint dialog exactly once");
  assert(renders.source === transparentSourceBefore,
    "source identity wrapper stole ownership of the dialog Promise");
  assert(renders.unrelated === transparentUnrelatedBefore,
    "transparent dialog Promise repainted an unrelated sibling");

  var sourceBefore = renders.source;
  var dialogBefore = renders.dialog;
  var unrelatedBefore = renders.unrelated;
  document.getElementById("standalone-open").click();
  await waitFor(function () {
    return !document.getElementById("external-dialog").hidden &&
      document.getElementById("external-dialog-output").textContent === "open";
  }, "alias-only trigger outside every component did not open the dialog");
  assert(globalThis.__dialogCalls.open === 1,
    "alias-only trigger did not call the dialog exactly once");
  assert(globalThis.__dialogCalls.success === 0,
    "alias-only trigger with null callback invoked source success");
  assert(renders.dialog > dialogBefore,
    "alias-only trigger did not render the dialog owner");
  assert(renders.source === sourceBefore,
    "alias-only trigger dirtied a component source it does not own");
  assert(renders.unrelated === unrelatedBefore,
    "alias-only trigger evaluated an unrelated boundary");

  dialogBefore = renders.dialog;
  document.getElementById("external-close").click();
  await waitFor(function () {
    return document.getElementById("external-dialog").hidden &&
      document.getElementById("external-dialog-output").textContent === "closed";
  }, "dialog did not reset after the alias-only trigger");
  assert(renders.dialog > dialogBefore, "dialog reset did not render its owner");
  assert(renders.source === sourceBefore && renders.unrelated === unrelatedBefore,
    "dialog reset evaluated a source or unrelated boundary");

  sourceBefore = renders.source;
  dialogBefore = renders.dialog;
  unrelatedBefore = renders.unrelated;
  document.getElementById("external-open-load-source").click();
  await waitFor(function () {
    return !document.getElementById("external-dialog").hidden &&
      typeof globalThis.__settleSourceLoad === "function";
  }, "mixed alias/source action did not return its source Promise");
  assert(globalThis.__dialogCalls.open === 2,
    "mixed alias/source action did not open the dialog exactly once");
  assert(renders.source === sourceBefore,
    "creating the unresolved source Promise repainted its source");
  var dialogAfterMixedOpen = renders.dialog;
  var sourceBeforeMixedSettle = renders.source;
  globalThis.__settleSourceLoad();
  await waitFor(function () {
    return document.getElementById("external-source-load-output").textContent === "1";
  }, "mixed action Promise settlement did not repaint its source owner");
  assert(renders.source > sourceBeforeMixedSettle,
    "mixed action Promise settlement skipped its source owner");
  assert(renders.dialog === dialogAfterMixedOpen,
    "mixed action Promise settlement repainted the earlier alias owner");
  assert(renders.unrelated === unrelatedBefore,
    "mixed action Promise settlement repainted an unrelated sibling");

  dialogBefore = renders.dialog;
  document.getElementById("external-close").click();
  await waitFor(function () { return document.getElementById("external-dialog").hidden; },
    "dialog did not close after mixed alias/source action");
  assert(renders.dialog > dialogBefore, "mixed action cleanup did not repaint dialog");

  sourceBefore = renders.source;
  dialogBefore = renders.dialog;
  unrelatedBefore = renders.unrelated;
  document.getElementById("external-open-callback-source").click();
  await waitFor(function () { return !document.getElementById("external-dialog").hidden; },
    "dialog did not open with its deferred source callback");
  assert(globalThis.__dialogCalls.open === 3,
    "deferred callback setup did not open dialog exactly once");
  assert(renders.source === sourceBefore,
    "storing a deferred source callback repainted its source");
  document.getElementById("external-confirm").click();
  await waitFor(function () {
    return document.getElementById("external-dialog").hidden &&
      typeof globalThis.__settleCallbackSource === "function";
  }, "dialog confirm did not return the stored source callback Promise");
  var dialogAfterCallbackConfirm = renders.dialog;
  var sourceBeforeCallbackSettle = renders.source;
  globalThis.__settleCallbackSource();
  await waitFor(function () {
    return document.getElementById("external-callback-load-output").textContent === "1";
  }, "stored callback Promise settlement did not repaint its source owner");
  assert(renders.source > sourceBeforeCallbackSettle,
    "stored callback Promise settlement skipped its source owner");
  assert(renders.dialog === dialogAfterCallbackConfirm,
    "stored callback Promise settlement repainted the confirming dialog");
  assert(renders.unrelated === unrelatedBefore,
    "stored callback Promise settlement repainted an unrelated sibling");

  sourceBefore = renders.source;
  dialogBefore = renders.dialog;
  document.getElementById("external-open").click();
  await waitFor(function () {
    return !document.getElementById("external-dialog").hidden &&
      document.getElementById("external-dialog-output").textContent === "open";
  }, "external $dialog.open() did not open the aliased dialog");
  assert(globalThis.__dialogCalls.open === 4, "external alias did not call the dialog instance exactly once");
  assert(globalThis.__dialogCalls.success === 0, "open eagerly invoked the success callback");
  assert(renders.dialog > dialogBefore, "alias mutation did not render the dialog owner");
  assert(renders.source === sourceBefore, "alias command rendered its unchanged source boundary");
  assert(renders.unrelated === unrelatedBefore, "alias command evaluated an unrelated boundary");

  sourceBefore = renders.source;
  dialogBefore = renders.dialog;
  document.getElementById("external-load").click();
  await waitFor(function () {
    return document.getElementById("external-dialog-load-output").textContent === "1";
  }, "async alias result did not render the alias owner after a deep mutation");
  assert(renders.dialog > dialogBefore, "async alias result did not dirty the dialog boundary");
  assert(renders.source === sourceBefore, "async alias result dirtied its unchanged action source");
  assert(renders.unrelated === unrelatedBefore, "async alias result evaluated an unrelated boundary");

  sourceBefore = renders.source;
  dialogBefore = renders.dialog;
  document.getElementById("external-confirm").click();
  await waitFor(function () {
    return document.getElementById("external-dialog").hidden &&
      document.getElementById("external-dialog-output").textContent === "closed" &&
      document.getElementById("external-success-output").textContent === "1";
  }, "dialog confirm did not invoke the callback in its original source owner");
  assert(globalThis.__dialogCalls.success === 1, "dialog callback was not invoked exactly once");
  assert(renders.dialog > dialogBefore, "confirm did not render the dialog owner");
  assert(renders.source > sourceBefore, "callback mutation did not render its source owner");
  assert(renders.unrelated === unrelatedBefore, "confirm evaluated an unrelated boundary");

  var diagnosticsBefore = globalThis.__kitDiagnostics.length;
  var thenableBefore = renders.thenable;
  document.getElementById("thenable-throw").click();
  await nextTurn();
  assert(globalThis.__kitDiagnostics.length > diagnosticsBefore,
    "throwing then getter escaped without a caught diagnostic");
  assert(renders.thenable === thenableBefore &&
    document.getElementById("thenable-output").textContent === "0",
    "throwing then getter dirtied or partially rendered its owner");

  diagnosticsBefore = globalThis.__kitDiagnostics.length;
  thenableBefore = renders.thenable;
  unrelatedBefore = renders.unrelated;
  document.getElementById("thenable-multi").click();
  await waitFor(function () {
    return document.getElementById("thenable-output").textContent === "1";
  }, "multi-settle thenable did not repaint its owner");
  await nextTurn();
  assert(renders.thenable === thenableBefore + 1,
    "multi-settle thenable repainted its owner more than once");
  assert(globalThis.__kitDiagnostics.length === diagnosticsBefore,
    "late multi-settle rejection produced a second settlement diagnostic");
  assert(renders.unrelated === unrelatedBefore,
    "multi-settle thenable repainted an unrelated sibling");

  diagnosticsBefore = globalThis.__kitDiagnostics.length;
  thenableBefore = renders.thenable;
  unrelatedBefore = renders.unrelated;
  document.getElementById("thenable-call-throw").click();
  await waitFor(function () {
    return document.getElementById("thenable-output").textContent === "2";
  }, "throwing then call did not settle as a caught rejection");
  await nextTurn();
  assert(globalThis.__kitDiagnostics.length > diagnosticsBefore,
    "throwing then call escaped without a caught diagnostic");
  assert(renders.thenable === thenableBefore + 1,
    "throwing then call did not repaint its owner exactly once");
  assert(renders.unrelated === unrelatedBefore,
    "throwing then call repainted an unrelated sibling");

  diagnosticsBefore = globalThis.__kitDiagnostics.length;
  sourceBefore = renders.source;
  document.getElementById("missing-alias").click();
  await nextTurn();
  assert(globalThis.__kitDiagnostics.length > diagnosticsBefore,
    "missing alias did not produce a caught diagnostic");
  assert(globalThis.__dialogCalls.open === 4 && globalThis.__dialogCalls.success === 1,
    "missing alias executed component state or its callback");
  assert(renders.source === sourceBefore, "failed missing-alias action dirtied its source owner");

  diagnosticsBefore = globalThis.__kitDiagnostics.length;
  sourceBefore = renders.source;
  document.getElementById("duplicate-alias").click();
  await nextTurn();
  assert(globalThis.__kitDiagnostics.length > diagnosticsBefore,
    "duplicate alias did not produce a caught diagnostic");
  assert(globalThis.__duplicateCalls.one === 0 && globalThis.__duplicateCalls.two === 0,
    "duplicate alias selected an arbitrary component instead of failing closed");
  assert(document.getElementById("duplicate-one-output").textContent === "0" &&
    document.getElementById("duplicate-two-output").textContent === "0",
    "duplicate alias mutated one of its conflicting owners");
  assert(renders.source === sourceBefore, "failed duplicate-alias action dirtied its source owner");

  var dialogHost = document.getElementById("external-dialog");
  dialogHost.remove();
  await nextTurn();
  diagnosticsBefore = globalThis.__kitDiagnostics.length;
  sourceBefore = renders.source;
  document.getElementById("external-open").click();
  await nextTurn();
  assert(globalThis.__kitDiagnostics.length > diagnosticsBefore,
    "detached alias did not produce a caught diagnostic");
  assert(globalThis.__dialogCalls.open === 4 && globalThis.__dialogCalls.success === 1,
    "detached alias retained a callable component instance");
  assert(renders.source === sourceBefore, "failed detached-alias action dirtied its source owner");
});`
