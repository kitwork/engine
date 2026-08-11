package vanilla

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBrowserStructuralDirectives is the public-surface contract for the
// template-only data-kit-if and data-kit-for directives. The fixture never
// reaches into a private registry: node identity, refreshed locals, component
// ownership, event delegation, and fail-closed behavior are all observed from
// authored HTML.
func TestBrowserStructuralDirectives(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS structural directive contract in short mode")
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
		case "/contracts/structure.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(structuralDirectiveDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/contracts/structure.html")
}

const structuralDirectiveDocument = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>KitJS structural directive contract</title></head>
<body>
  <main id="structure-root" data-kit-component="structure-host">
    <section id="conditional-region">
      <button type="button" id="conditional-toggle" data-kit-click="toggleConditional()">toggle conditional</button>
      <output id="conditional-direct-count" data-kit-text="directCount"></output>
      <output id="conditional-delay-count" data-kit-text="delayCount"></output>
      <template id="conditional-template" data-kit-if="visible">
        <article class="conditional-branch">
          <button type="button" class="conditional-direct" data-kit-click="recordDirect($event.type)">direct</button>
          <input class="conditional-debounce" data-kit-input:debounce(40)="recordDelayed($event.value)">
        </article>
      </template>
    </section>

    <section id="keyed-region">
      <button type="button" id="keyed-reorder" data-kit-click="reorderItems()">reorder</button>
      <button type="button" id="keyed-refresh-a" data-kit-click="refreshSameItem()">refresh a</button>
      <button type="button" id="keyed-remove-a" data-kit-click="removeA()">remove a</button>
      <button type="button" id="keyed-restore-a" data-kit-click="restoreA()">restore a</button>
      <button type="button" id="keyed-invoke-context" data-kit-click="invokeRowCallback()">invoke row callback</button>
      <output id="keyed-selection" data-kit-text="selection"></output>
      <output id="keyed-context-invocations" data-kit-text="contextInvocations"></output>
      <template id="keyed-template" data-kit-for="item, i of items" data-kit-key="item.id">
        <article class="keyed-row" data-kit-bind="data-row-id: item.id">
          <span class="keyed-label" data-kit-text="item.label">server label</span>
          <span class="keyed-index" data-kit-text="i">server index</span>
          <button type="button" class="keyed-select" data-kit-click="select(item.id, i, $event.type)">select</button>
          <button type="button" class="keyed-store-context" data-kit-click="storeRowCallback(() => item = null)">store row callback</button>
          <section class="keyed-child" data-kit-component="row-state">
            <button type="button" class="keyed-child-increment" data-kit-click="increment()">increment child</button>
            <output class="keyed-child-count" data-kit-text="count">0</output>
            <output class="keyed-child-label" data-kit-text="item.label">server child label</output>
          </section>
        </article>
      </template>
    </section>

    <section id="unkeyed-region">
      <button type="button" id="unkeyed-replace" data-kit-click="replacePlain()">replace</button>
      <button type="button" id="unkeyed-add" data-kit-click="addPlain()">add</button>
      <button type="button" id="unkeyed-shrink" data-kit-click="shrinkPlain()">shrink</button>
      <template id="unkeyed-template" data-kit-for="entry, index of plain">
        <p class="unkeyed-row">
          <span class="unkeyed-label" data-kit-text="entry.label">server label</span>
          <span class="unkeyed-index" data-kit-text="index">server index</span>
        </p>
      </template>
    </section>

    <section id="nested-region">
      <button type="button" id="nested-open-second" data-kit-click="openSecondGroup()">open second</button>
      <output id="nested-result" data-kit-text="nestedResult"></output>
      <template id="group-template" data-kit-for="group, gi of groups" data-kit-key="group.id">
        <article class="group-row" data-kit-bind="data-group-id: group.id">
          <span class="group-title" data-kit-text="group.title">server group</span>
          <template data-kit-if="group.open">
            <div class="group-open-branch">
              <template data-kit-for="entry, ei of group.entries" data-kit-key="entry.id">
                <button
                  type="button"
                  class="nested-action"
                  data-kit-bind="data-entry-id: entry.id"
                  data-kit-click="captureNested(group.id, gi, entry.id, ei, $event.type)">
                  <span data-kit-text="group.title + ':' + entry.label">server entry</span>
                </button>
              </template>
            </div>
          </template>
        </article>
      </template>
    </section>

    <section id="multi-root-region">
      <button type="button" id="multi-root-reorder" data-kit-click="reorderMulti()">reorder multi-root</button>
      <button type="button" id="multi-root-remove-x" data-kit-click="removeMultiX()">remove multi-root x</button>
      <template id="multi-root-template" data-kit-for="part, pi of multiItems" data-kit-key="part.id">
        <i class="multi-root-head" data-kit-bind="data-multi-id: part.id" data-kit-text="part.label + ':head'"></i>
        <section class="multi-root-child" data-kit-component="row-state">
          <output class="multi-root-count" data-kit-text="count">0</output>
        </section>
        <i class="multi-root-tail" data-kit-bind="data-multi-id: part.id" data-kit-text="part.label + ':tail'"></i>
      </template>
    </section>

    <section id="duplicate-region">
      <button type="button" id="make-duplicate" data-kit-click="makeDuplicate()">duplicate</button>
      <template id="duplicate-template" data-kit-for="candidate, ci of duplicateRows" data-kit-key="candidate.id">
        <p class="duplicate-row" data-kit-bind="data-duplicate-id: candidate.id">
          <span class="duplicate-label" data-kit-text="candidate.label">server duplicate label</span>
          <span class="duplicate-index" data-kit-text="ci">server duplicate index</span>
        </p>
      </template>
    </section>
  </main>

  <section id="invalid-root" data-kit-component="invalid-structure">
    <div id="invalid-nontemplate-if" data-kit-if="visible"><b id="invalid-if-server-child">server if child</b></div>
    <div id="invalid-nontemplate-for" data-kit-for="item, i of items">server for element</div>

    <template id="invalid-spec" data-kit-for="item, i items">
      <span class="invalid-spec-clone">must stay inert</span>
    </template>
    <template id="invalid-key-syntax" data-kit-for="item of items" data-kit-key="item.">
      <span class="invalid-key-syntax-clone">must stay inert</span>
    </template>
    <template id="invalid-key-value" data-kit-for="item of badKeys" data-kit-key="item.key">
      <span class="invalid-key-value-clone">must stay inert</span>
    </template>
    <template id="invalid-async-key" data-kit-for="item of items" data-kit-key="asyncKey()">
      <span class="invalid-async-key-clone">must stay inert</span>
    </template>
    <template id="invalid-nonarray" data-kit-for="item of nonArray">
      <span class="invalid-nonarray-clone">must stay inert</span>
    </template>
    <template id="invalid-orphan-key" data-kit-key="items[0].id">
      <span class="invalid-orphan-key-clone">must stay inert</span>
    </template>
    <template id="invalid-script-template" data-kit-if="visible">
      <script>globalThis.__structuralPayloadExecuted = (globalThis.__structuralPayloadExecuted || 0) + 1;</script>
      <span class="invalid-script-clone">must stay inert</span>
    </template>

    <button type="button" id="deep-invalidate" data-kit-click="touchDepth()">invalidate depth</button>
    <div id="deep-structure-root"></div>
    <script>
    (function () {
      var parent = document.getElementById("deep-structure-root");
      var previous = null;
      for (var depth = 0; depth <= 64; depth++) {
        var template = document.createElement("template");
        template.setAttribute("data-kit-if", "visible");
        template.setAttribute("data-depth", String(depth));
        if (previous) previous.content.appendChild(template);
        else parent.appendChild(template);
        previous = template;
      }
      var marker = document.createElement("span");
      marker.id = "too-deep-marker";
      previous.content.appendChild(marker);
    })();
    </script>
  </section>

  <script>
  (function () {
    "use strict";
    globalThis.__rowActionCalls = 0;
    globalThis.__structureErrors = [];
    globalThis.__structureUnhandled = 0;
    globalThis.addEventListener("unhandledrejection", function () {
      globalThis.__structureUnhandled++;
    });
    var originalError = console.error;
    console.error = function () {
      var message = [];
      for (var index = 0; index < arguments.length; index++) {
        var value = arguments[index];
        message.push(String(value && value.message || value));
      }
      globalThis.__structureErrors.push(message.join(" "));
      return originalError.apply(this, arguments);
    };
    globalThis.__restoreStructureError = function () {
      console.error = originalError;
      delete globalThis.__restoreStructureError;
    };
  })();
  </script>
  <script src="/kit.js"></script>
  <script>
    kit.component("row-state", {
      count: 0,
      increment: function () {
        this.count++;
        globalThis.__rowActionCalls++;
      }
    });

    kit.component("structure-host", {
      visible: true,
      directCount: 0,
      delayCount: 0,
      selection: "",
      contextInvocations: 0,
      rowCallback: null,
      nestedResult: "",
      items: [
        { id: "a", label: "Alpha" },
        { id: "b", label: "Beta" },
        { id: "c", label: "Gamma" }
      ],
      plain: [
        { label: "Plain A" },
        { label: "Plain B" }
      ],
      groups: [
        {
          id: "g1",
          title: "Group One",
          open: true,
          entries: [
            { id: "a1", label: "Entry A1" },
            { id: "a2", label: "Entry A2" }
          ]
        },
        {
          id: "g2",
          title: "Group Two",
          open: false,
          entries: [{ id: "b1", label: "Entry B1" }]
        }
      ],
      multiItems: [
        { id: "x", label: "Multi X" },
        { id: "y", label: "Multi Y" }
      ],
      duplicateRows: [
        { id: "one", label: "One" },
        { id: "two", label: "Two" }
      ],

      toggleConditional: function () { this.visible = !this.visible; },
      recordDirect: function (type) {
        if (type === "click") this.directCount++;
      },
      recordDelayed: function (value) {
        if (value === "pending" || value === "attached") this.delayCount++;
      },
      select: function (id, index, type) {
        this.selection = id + "|" + index + "|" + type;
      },
      storeRowCallback: function (callback) {
        this.rowCallback = callback;
      },
      invokeRowCallback: function () {
        if (this.rowCallback) this.rowCallback();
        this.contextInvocations++;
      },
      reorderItems: function () {
        this.items = [
          { id: "c", label: "Gamma refreshed" },
          { id: "a", label: "Alpha refreshed" },
          { id: "b", label: "Beta refreshed" }
        ];
      },
      refreshSameItem: function () {
        this.items[0].label = "Alpha deep";
        this.items = this.items.slice();
      },
      removeA: function () {
        this.items = [
          { id: "c", label: "Gamma after remove" },
          { id: "b", label: "Beta after remove" }
        ];
      },
      restoreA: function () {
        this.items = [
          { id: "c", label: "Gamma restored" },
          { id: "a", label: "Alpha restored" },
          { id: "b", label: "Beta restored" }
        ];
      },
      replacePlain: function () {
        this.plain = [{ label: "Plain B refreshed" }, { label: "Plain A refreshed" }];
      },
      addPlain: function () {
        this.plain = [
          { label: "Plain B added" },
          { label: "Plain A added" },
          { label: "Plain C added" }
        ];
      },
      shrinkPlain: function () { this.plain = [{ label: "Plain only" }]; },
      captureNested: function (groupID, groupIndex, entryID, entryIndex, type) {
        this.nestedResult = groupID + "|" + groupIndex + "|" + entryID + "|" + entryIndex + "|" + type;
      },
      openSecondGroup: function () {
        this.groups = [
          {
            id: "g1",
            title: "Group One refreshed",
            open: true,
            entries: [
              { id: "a1", label: "Entry A1 refreshed" },
              { id: "a2", label: "Entry A2 refreshed" }
            ]
          },
          {
            id: "g2",
            title: "Group Two refreshed",
            open: true,
            entries: [{ id: "b1", label: "Entry B1 refreshed" }]
          }
        ];
      },
      reorderMulti: function () {
        this.multiItems = [
          { id: "y", label: "Multi Y refreshed" },
          { id: "x", label: "Multi X refreshed" }
        ];
      },
      removeMultiX: function () {
        this.multiItems = [{ id: "y", label: "Multi Y retained" }];
      },
      makeDuplicate: function () {
        this.duplicateRows = [
          { id: "one", label: "One must not partially refresh" },
          { id: "one", label: "Duplicate must not render" }
        ];
      }
    });

    kit.component("invalid-structure", {
      visible: true,
      depthTick: 0,
      items: [{ id: "valid", label: "Valid" }],
      badKeys: [{ key: { nested: true }, label: "Invalid object key" }],
      nonArray: "not-an-array",
      asyncKey: function () { return Promise.reject(new Error("async key rejected")); },
      touchDepth: function () { this.depthTick++; }
    });
  </script>
  <script>
` + browserHarness + `

__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;

  function elements(selector, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(selector));
  }
  function labels(nodes, selector) {
    return nodes.map(function (node) { return node.querySelector(selector).textContent.trim(); });
  }
  function keyedRow(id) {
    return document.querySelector('#keyed-region .keyed-row[data-row-id="' + id + '"]');
  }
  function groupRow(id) {
    return document.querySelector('#nested-region .group-row[data-group-id="' + id + '"]');
  }
  function fireInput(input, value) {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true, cancelable: true }));
  }

  __kitTestPublicContract();

  await waitFor(function () {
    return elements("#keyed-region .keyed-row").length === 3 &&
      elements("#unkeyed-region .unkeyed-row").length === 2 &&
      elements("#nested-region .group-row").length === 2 &&
      elements("#duplicate-region .duplicate-row").length === 2 &&
      document.querySelector("#conditional-region .conditional-branch");
  }, "structural directives did not produce their initial branches");

  // data-kit-if mounts a fresh branch, disposes it, and suppresses pending
  // delegated work once that branch is no longer connected.
  var firstConditional = document.querySelector("#conditional-region .conditional-branch");
  var detachedDirect = firstConditional.querySelector(".conditional-direct");
  var pendingInput = firstConditional.querySelector(".conditional-debounce");
  detachedDirect.click();
  await waitFor(function () {
    return document.getElementById("conditional-direct-count").textContent.trim() === "1";
  }, "event action inside data-kit-if did not execute");
  fireInput(pendingInput, "pending");
  document.getElementById("conditional-toggle").click();
  await waitFor(function () { return !document.querySelector("#conditional-region .conditional-branch"); },
    "data-kit-if did not detach its false branch");
  assert(!firstConditional.isConnected, "removed if branch remained connected");
  detachedDirect.click();
  await new Promise(function (resolve) { setTimeout(resolve, 90); });
  assert(document.getElementById("conditional-direct-count").textContent.trim() === "1",
    "detached if branch still dispatched an event action");
  assert(document.getElementById("conditional-delay-count").textContent.trim() === "0",
    "pending debounce executed after its if branch was detached");
  document.getElementById("conditional-toggle").click();
  await waitFor(function () { return document.querySelector("#conditional-region .conditional-branch"); },
    "data-kit-if did not remount its true branch");
  var secondConditional = document.querySelector("#conditional-region .conditional-branch");
  assert(secondConditional !== firstConditional, "data-kit-if reused a disposed branch");
  fireInput(secondConditional.querySelector(".conditional-debounce"), "attached");
  await waitFor(function () {
    return document.getElementById("conditional-delay-count").textContent.trim() === "1";
  }, "attached debounce inside data-kit-if did not execute");

  // Keyed rows retain exact nodes and nested component instances through a
  // reorder, while their item/index locals refresh to the new data objects.
  var initialKeyed = elements("#keyed-region .keyed-row");
  var rowA = keyedRow("a");
  var rowB = keyedRow("b");
  var rowC = keyedRow("c");
  assert(initialKeyed[0] === rowA && initialKeyed[1] === rowB && initialKeyed[2] === rowC,
    "initial keyed order was not a,b,c");
  assert(labels(initialKeyed, ".keyed-label").join("|") === "Alpha|Beta|Gamma",
    "initial keyed locals did not render");

  var childA = rowA.querySelector(".keyed-child");
  var oldChildButton = childA.querySelector(".keyed-child-increment");
  oldChildButton.click();
  await waitFor(function () { return childA.querySelector(".keyed-child-count").textContent.trim() === "1"; },
    "nested row component did not update");
  assert(childA.querySelector(".keyed-child-label").textContent.trim() === "Alpha",
    "nested component did not receive its initial row local");
  document.getElementById("keyed-refresh-a").click();
  await waitFor(function () {
    return childA.querySelector(".keyed-child-label").textContent.trim() === "Alpha deep";
  }, "replacing the list shallowly did not refresh a nested component using the retained item object");
  assert(childA.querySelector(".keyed-child-count").textContent.trim() === "1",
    "refreshing retained row locals reset nested component state");
  rowA.querySelector(".keyed-select").click();
  await waitFor(function () {
    return document.getElementById("keyed-selection").textContent.trim() === "a|0|click";
  }, "row event did not receive initial item/index locals and $event");
  rowA.querySelector(".keyed-store-context").click();
  await nextTurn();
  var contextErrorsBefore = globalThis.__structureErrors.length;
  document.getElementById("keyed-invoke-context").click();
  await nextTurn();
  assert(globalThis.__structureErrors.length > contextErrorsBefore,
    "retained callback changed a read-only structural local without a diagnostic");
  assert(document.getElementById("keyed-context-invocations").textContent.trim() === "0",
    "failed retained callback partially committed its owner action");

  document.getElementById("keyed-reorder").click();
  await waitFor(function () {
    var rows = elements("#keyed-region .keyed-row");
    return rows.length === 3 && labels(rows, ".keyed-label").join("|") ===
      "Gamma refreshed|Alpha refreshed|Beta refreshed";
  }, "keyed reorder did not refresh same-key data");
  var reordered = elements("#keyed-region .keyed-row");
  assert(reordered[0] === rowC && reordered[1] === rowA && reordered[2] === rowB,
    "keyed reorder replaced nodes instead of moving them");
  assert(rowA.querySelector(".keyed-child") === childA,
    "keyed reorder replaced a nested component boundary");
  assert(childA.querySelector(".keyed-child-count").textContent.trim() === "1",
    "keyed reorder reset nested component state");
  rowA.querySelector(".keyed-select").click();
  await waitFor(function () {
    return document.getElementById("keyed-selection").textContent.trim() === "a|1|click";
  }, "reused keyed row retained stale item/index locals");

  document.getElementById("keyed-remove-a").click();
  await waitFor(function () { return !keyedRow("a") && elements("#keyed-region .keyed-row").length === 2; },
    "keyed removal did not detach row a");
  assert(!rowA.isConnected && !childA.isConnected, "removed keyed row or child component stayed connected");
  assert(keyedRow("c") === rowC && keyedRow("b") === rowB,
    "removing one keyed row replaced surviving rows");
  oldChildButton.click();
  await nextTurn();
  assert(globalThis.__rowActionCalls === 1, "detached nested component still executed an event action");

  document.getElementById("keyed-restore-a").click();
  await waitFor(function () { return keyedRow("a") && elements("#keyed-region .keyed-row").length === 3; },
    "keyed re-add did not create row a");
  var restoredA = keyedRow("a");
  var restoredChildA = restoredA.querySelector(".keyed-child");
  assert(restoredA !== rowA && restoredChildA !== childA,
    "removed keyed row/component was resurrected instead of recreated");
  await waitFor(function () { return restoredChildA.querySelector(".keyed-child-count").textContent.trim() === "0"; },
    "re-added nested component did not start with fresh state");
  restoredChildA.querySelector(".keyed-child-increment").click();
  await waitFor(function () { return globalThis.__rowActionCalls === 2; },
    "re-added nested component was not active");

  // Without data-kit-key, row identity is positional: existing indices are
  // reused, locals refresh, growth appends, and shrinkage disposes the tail.
  var initialPlain = elements("#unkeyed-region .unkeyed-row");
  var plainZero = initialPlain[0];
  var plainOne = initialPlain[1];
  document.getElementById("unkeyed-replace").click();
  await waitFor(function () {
    return labels(elements("#unkeyed-region .unkeyed-row"), ".unkeyed-label").join("|") ===
      "Plain B refreshed|Plain A refreshed";
  }, "unkeyed replacement did not refresh locals");
  var replacedPlain = elements("#unkeyed-region .unkeyed-row");
  assert(replacedPlain[0] === plainZero && replacedPlain[1] === plainOne,
    "unkeyed renderer did not reuse rows by index");
  assert(replacedPlain.map(function (row) { return row.querySelector(".unkeyed-index").textContent.trim(); }).join("|") === "0|1",
    "unkeyed indices were not refreshed");
  document.getElementById("unkeyed-add").click();
  await waitFor(function () { return elements("#unkeyed-region .unkeyed-row").length === 3; },
    "unkeyed growth did not append a row");
  var grownPlain = elements("#unkeyed-region .unkeyed-row");
  var plainTwo = grownPlain[2];
  assert(grownPlain[0] === plainZero && grownPlain[1] === plainOne,
    "unkeyed growth replaced existing indices");
  document.getElementById("unkeyed-shrink").click();
  await waitFor(function () { return elements("#unkeyed-region .unkeyed-row").length === 1; },
    "unkeyed shrink did not remove tail rows");
  assert(elements("#unkeyed-region .unkeyed-row")[0] === plainZero,
    "unkeyed shrink replaced the surviving index");
  assert(!plainOne.isConnected && !plainTwo.isConnected, "unkeyed shrink retained removed tail rows");
  assert(plainZero.querySelector(".unkeyed-label").textContent.trim() === "Plain only",
    "unkeyed shrink retained stale local data");

  // A nested for -> if -> for chain receives every lexical local, and the
  // delegated action receives those locals plus the safe $event snapshot.
  var groupOne = groupRow("g1");
  var groupTwo = groupRow("g2");
  assert(elements(".nested-action", groupOne).length === 2, "open nested group did not render its entries");
  assert(elements(".nested-action", groupTwo).length === 0, "false nested if rendered its entries");
  groupOne.querySelector('.nested-action[data-entry-id="a2"]').click();
  await waitFor(function () {
    return document.getElementById("nested-result").textContent.trim() === "g1|0|a2|1|click";
  }, "nested action did not receive outer/inner locals and $event");
  document.getElementById("nested-open-second").click();
  await waitFor(function () {
    var second = groupRow("g2");
    return second && second.querySelector('.nested-action[data-entry-id="b1"]');
  }, "nested if/for did not react to refreshed outer locals");
  assert(groupRow("g1") === groupOne && groupRow("g2") === groupTwo,
    "refreshing nested data replaced stable keyed group nodes");
  groupRow("g2").querySelector('.nested-action[data-entry-id="b1"]').click();
  await waitFor(function () {
    return document.getElementById("nested-result").textContent.trim() === "g2|1|b1|0|click";
  }, "new nested branch did not receive correct lexical locals");

  // One keyed item may own several top-level nodes. Reconciliation treats
  // that sequence as one range so heads, component boundaries, and tails move
  // together and are disposed together.
  var xHead = document.querySelector('#multi-root-region .multi-root-head[data-multi-id="x"]');
  var yHead = document.querySelector('#multi-root-region .multi-root-head[data-multi-id="y"]');
  var xChild = xHead && xHead.nextElementSibling;
  var yChild = yHead && yHead.nextElementSibling;
  var xTail = xChild && xChild.nextElementSibling;
  var yTail = yChild && yChild.nextElementSibling;
  assert(xChild && xChild.classList.contains("multi-root-child") &&
    xTail && xTail.matches('.multi-root-tail[data-multi-id="x"]') &&
    yChild && yChild.classList.contains("multi-root-child") &&
    yTail && yTail.matches('.multi-root-tail[data-multi-id="y"]'),
    "initial multi-root keyed ranges were not contiguous");
  document.getElementById("multi-root-reorder").click();
  await waitFor(function () {
    var heads = elements("#multi-root-region .multi-root-head");
    return heads.length === 2 && heads[0].getAttribute("data-multi-id") === "y" &&
      heads[0].textContent.trim() === "Multi Y refreshed:head";
  }, "multi-root keyed ranges did not reorder");
  var multiAfter = elements("#multi-root-region .multi-root-head, #multi-root-region .multi-root-child, #multi-root-region .multi-root-tail");
  assert(multiAfter.length === 6 && multiAfter[0] === yHead && multiAfter[1] === yChild && multiAfter[2] === yTail &&
    multiAfter[3] === xHead && multiAfter[4] === xChild && multiAfter[5] === xTail,
    "multi-root keyed reorder split a range or replaced one of its nodes");
  assert(xTail.textContent.trim() === "Multi X refreshed:tail" && yTail.textContent.trim() === "Multi Y refreshed:tail",
    "multi-root keyed reorder retained stale row locals");
  document.getElementById("multi-root-remove-x").click();
  await waitFor(function () {
    return elements("#multi-root-region .multi-root-head").length === 1 &&
      !document.querySelector('#multi-root-region [data-multi-id="x"]');
  }, "multi-root keyed removal did not remove its complete range");
  assert(!xHead.isConnected && !xChild.isConnected && !xTail.isConnected,
    "multi-root keyed removal left part of its range connected");
  var retainedMulti = elements("#multi-root-region .multi-root-head, #multi-root-region .multi-root-child, #multi-root-region .multi-root-tail");
  assert(retainedMulti.length === 3 && retainedMulti[0] === yHead && retainedMulti[1] === yChild && retainedMulti[2] === yTail,
    "removing a neighboring multi-root range replaced or split the retained range");

  // Invalid structural authorship is inert. Non-template hosts retain their
  // server DOM; malformed specs/keys and non-array values clone nothing.
  assert(document.getElementById("invalid-nontemplate-if").textContent.trim() === "server if child" &&
    document.getElementById("invalid-if-server-child").isConnected,
    "invalid non-template if partially mutated server DOM");
  assert(document.getElementById("invalid-nontemplate-for").textContent.trim() === "server for element",
    "invalid non-template for partially mutated server DOM");
  ["invalid-spec-clone", "invalid-key-syntax-clone", "invalid-key-value-clone", "invalid-async-key-clone",
    "invalid-nonarray-clone", "invalid-orphan-key-clone", "invalid-script-clone"].forEach(function (name) {
    assert(!document.querySelector("." + name), name + " escaped an invalid template");
  });
  assert(!globalThis.__structuralPayloadExecuted,
    "script nested in a rejected structural template executed");
  assert(document.getElementById("invalid-script-template").content.querySelector("script"),
    "script rejection mutated the still-inert template content");
  assert(document.getElementById("invalid-spec").content.querySelector(".invalid-spec-clone").textContent.trim() === "must stay inert",
    "invalid structural preparation mutated template content");
  assert(globalThis.__structureErrors.length >= 6,
    "invalid structural directives did not report diagnostics");
  assert(globalThis.__structureUnhandled === 0,
    "Promise-valued data-kit-key escaped as an unhandled rejection");

  // The nesting limit is absolute, not a per-render work budget. Repeated
  // invalidations must never continue materializing the rejected level.
  assert(elements('#deep-structure-root template[data-depth]').length === 65,
    "structural depth fixture did not reach the rejected boundary");
  assert(!document.getElementById("too-deep-marker"),
    "structural nesting materialized content beyond 64 levels");
  var errorsAtDepthLimit = globalThis.__structureErrors.length;
  document.getElementById("deep-invalidate").click();
  document.getElementById("deep-invalidate").click();
  document.getElementById("deep-invalidate").click();
  await nextTurn();
  await nextTurn();
  assert(!document.getElementById("too-deep-marker"),
    "repeated renders bypassed the absolute structural nesting limit");
  assert(globalThis.__structureErrors.length === errorsAtDepthLimit,
    "the rejected structural depth reported repeatedly after rerender");

  // Duplicate-key validation must happen before updating any row/local. The
  // previous valid list therefore remains byte-for-byte visible and keeps the
  // exact same nodes after the rejected render.
  var duplicateRows = elements("#duplicate-region .duplicate-row");
  var duplicateHTML = document.getElementById("duplicate-region").innerHTML;
  var errorsBeforeDuplicate = globalThis.__structureErrors.length;
  document.getElementById("make-duplicate").click();
  await waitFor(function () { return globalThis.__structureErrors.length > errorsBeforeDuplicate; },
    "duplicate keys did not report a diagnostic");
  await nextTurn();
  var duplicateAfter = elements("#duplicate-region .duplicate-row");
  assert(duplicateAfter.length === 2 && duplicateAfter[0] === duplicateRows[0] && duplicateAfter[1] === duplicateRows[1],
    "duplicate-key failure replaced or partially removed existing rows");
  assert(labels(duplicateAfter, ".duplicate-label").join("|") === "One|Two",
    "duplicate-key failure partially refreshed row locals");
  assert(document.getElementById("duplicate-region").innerHTML === duplicateHTML,
    "duplicate-key failure mutated structural DOM");

  globalThis.__restoreStructureError();
});
  </script>
</body>
</html>`
