package vanilla

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// closedExpressionCase is deliberately exercised through authored HTML. The
// standalone runtime does not expose its parser or evaluator as a public API,
// so this corpus proves the same boundary an application actually uses.
type closedExpressionCase struct {
	ID     string
	Source string
	Want   string
}

type closedExpressionExpectation struct {
	ID   string `json:"id"`
	Want string `json:"want"`
}

var closedExpressionReadCases = []closedExpressionCase{
	// Literals and unary operators.
	{ID: "literal-number", Source: "1.5e2 + .5", Want: "150.5"},
	{ID: "literal-number-trailing-dot", Source: "1.", Want: "1"},
	{ID: "literal-number-negative-exponent", Source: "2E-2", Want: "0.02"},
	{ID: "literal-string", Source: "'Kit' + 'JS'", Want: "KitJS"},
	{ID: "literal-string-escape", Source: "'line\\nnext'", Want: "line\nnext"},
	{ID: "literal-string-unicode", Source: "'Chào 👋'", Want: "Chào 👋"},
	{ID: "literal-true", Source: "true", Want: "true"},
	{ID: "literal-false", Source: "false", Want: "false"},
	{ID: "literal-null", Source: "null ?? 'nil'", Want: "nil"},
	{ID: "unary-not", Source: "!false", Want: "true"},
	{ID: "unary-plus", Source: "+'5'", Want: "5"},
	{ID: "unary-minus", Source: "-5", Want: "-5"},
	{ID: "unary-nested", Source: "!-one", Want: "false"},

	// Arithmetic, comparison, and both equality families.
	{ID: "operator-add", Source: "seven + five", Want: "12"},
	{ID: "operator-subtract", Source: "seven - five", Want: "2"},
	{ID: "operator-multiply", Source: "seven * five", Want: "35"},
	{ID: "operator-divide", Source: "twenty / five", Want: "4"},
	{ID: "operator-modulo", Source: "seven % five", Want: "2"},
	{ID: "operator-less", Source: "five < seven", Want: "true"},
	{ID: "operator-less-equal", Source: "five <= five", Want: "true"},
	{ID: "operator-greater", Source: "seven > five", Want: "true"},
	{ID: "operator-greater-equal", Source: "seven >= seven", Want: "true"},
	{ID: "operator-loose-equal", Source: "one == '1'", Want: "true"},
	{ID: "operator-loose-not-equal", Source: "one != '2'", Want: "true"},
	{ID: "operator-strict-equal", Source: "one === 1", Want: "true"},
	{ID: "operator-strict-not-equal", Source: "one !== '1'", Want: "true"},
	{ID: "operator-loose-boolean", Source: "false == zero", Want: "true"},
	{ID: "operator-strict-null", Source: "null === null", Want: "true"},

	// Precedence and associativity. These cases intentionally overlap operators:
	// they catch a parser that recognizes a token but builds the wrong tree.
	{ID: "precedence-multiply", Source: "1 + 2 * 3", Want: "7"},
	{ID: "precedence-group", Source: "(1 + 2) * 3", Want: "9"},
	{ID: "precedence-left-subtract", Source: "10 - 3 - 2", Want: "5"},
	{ID: "precedence-left-product", Source: "20 / 5 * 2", Want: "8"},
	{ID: "precedence-unary", Source: "-2 * 3 + 7", Want: "1"},
	{ID: "precedence-relation-equality", Source: "1 + 2 < 4 === true", Want: "true"},
	{ID: "precedence-and-or", Source: "false || true && true", Want: "true"},
	{ID: "precedence-conditional", Source: "false || true ? 'yes' : 'no'", Want: "yes"},
	{ID: "precedence-nullish", Source: "missing ?? (false ? 'bad' : 'fallback')", Want: "fallback"},
	{ID: "associativity-conditional", Source: "true ? false : true ? 'yes' : 'no'", Want: "false"},

	// Short-circuit branches must not execute their authored call.
	{ID: "short-and", Source: "false && touch('and')", Want: "false"},
	{ID: "short-or", Source: "true || touch('or')", Want: "true"},
	{ID: "short-nullish", Source: "'ready' ?? touch('nullish')", Want: "ready"},
	{ID: "short-conditional-no", Source: "true ? 'yes' : touch('conditional-no')", Want: "yes"},
	{ID: "short-conditional-yes", Source: "false ? touch('conditional-yes') : 'no'", Want: "no"},
	{ID: "nullish-keeps-zero", Source: "zero ?? 9", Want: "0"},
	{ID: "nullish-keeps-false", Source: "falsy ?? true", Want: "false"},

	// Member reads, computed reads, optional reads, and calls.
	{ID: "member-read", Source: "user.profile.name", Want: "Kit"},
	{ID: "computed-read", Source: "user[profileKey][nameKey]", Want: "Kit"},
	{ID: "optional-present", Source: "user?.profile?.name", Want: "Kit"},
	{ID: "optional-missing", Source: "missing?.name ?? 'Guest'", Want: "Guest"},
	{ID: "array-computed", Source: "numbers[1]", Want: "4"},
	{ID: "array-length", Source: "numbers.length", Want: "3"},
	{ID: "array-method", Source: "numbers.join('-')", Want: "2-4-8"},
	{ID: "array-includes", Source: "numbers.includes(4)", Want: "true"},
	{ID: "array-index-of", Source: "numbers.indexOf(8)", Want: "2"},
	{ID: "array-slice", Source: "numbers.slice(1).join(',')", Want: "4,8"},
	{ID: "array-map-lambda", Source: "numbers.map((x) => x * factor).join(',')", Want: "4,8,16"},
	{ID: "array-filter-lambda", Source: "numbers.filter((x) => x > 2).join(',')", Want: "4,8"},
	{ID: "array-find-lambda", Source: "numbers.find((x) => x > 4)", Want: "8"},
	{ID: "array-some-lambda", Source: "numbers.some((x) => x === 4)", Want: "true"},
	{ID: "array-every-lambda", Source: "numbers.every((x) => x > 1)", Want: "true"},
	{ID: "method-call", Source: "user.label('!')", Want: "Kit!"},
	{ID: "computed-method-call", Source: "user['label']('?')", Want: "Kit?"},
	{ID: "primitive-method-call", Source: "word.toUpperCase()", Want: "VANILLA"},
	{ID: "string-includes", Source: "word.includes('nill')", Want: "true"},
	{ID: "string-starts", Source: "word.startsWith('van')", Want: "true"},
	{ID: "string-ends", Source: "word.endsWith('illa')", Want: "true"},
	{ID: "string-trim", Source: "'  KitJS  '.trim()", Want: "KitJS"},
	{ID: "string-lower", Source: "'KITJS'.toLowerCase()", Want: "kitjs"},
	{ID: "group-method-call", Source: "(seven + five).toFixed(1)", Want: "12.0"},
	{ID: "bare-call", Source: "sum(4, 5)", Want: "9"},
	{ID: "non-callable", Source: "one() ?? 'not-callable'", Want: "not-callable"},
	{ID: "non-callable-skips-args", Source: "one(touch('non-callable')) ?? 'safe'", Want: "safe"},
	{ID: "unknown-own-field", Source: "owns('unknownField')", Want: "false"},

	// Array/object literals and closed lambdas.
	{ID: "array-literal", Source: "[2, 4, 8][2]", Want: "8"},
	{ID: "object-literal", Source: "({ answer: 42, }).answer", Want: "42"},
	{ID: "object-null-prototype", Source: "({ answer: 42 }).toString ?? 'none'", Want: "none"},
	{ID: "object-string-key", Source: "({ 'display-name': 'KitJS' })['display-name']", Want: "KitJS"},
	{ID: "nested-literal", Source: "[{ value: 3 }][0].value", Want: "3"},
	{ID: "lambda-zero", Source: "(() => 7)()", Want: "7"},
	{ID: "lambda-one", Source: "((x) => x + 1)(4)", Want: "5"},
	{ID: "lambda-many", Source: "((x, y) => x * y)(3, 4)", Want: "12"},
	{ID: "lambda-capture", Source: "((x) => x * factor)(3)", Want: "6"},
}

var closedExpressionRejectCases = []closedExpressionCase{
	// Read bindings are pure. The identical assignment is valid on an action
	// below, which also verifies that the compile cache is keyed by parser mode.
	{ID: "binding-assignment", Source: "count = 99"},
	{ID: "binding-sequence", Source: "one; two"},

	// Assignment remains shallow and predictable: identifier targets only.
	{ID: "member-assignment", Source: "user.profile.name = 'Changed'"},
	{ID: "computed-assignment", Source: "numbers[0] = 99"},
	{ID: "compound-assignment", Source: "count += 1"},
	{ID: "postfix-update", Source: "count++"},
	{ID: "prefix-update", Source: "--count"},

	// This is a closed language, not a second ECMAScript implementation.
	{ID: "comma-operator", Source: "(one, two)"},
	{ID: "exponent", Source: "2 ** 3"},
	{ID: "bitwise-and", Source: "one & two"},
	{ID: "bitwise-or", Source: "one | two"},
	{ID: "bitwise-xor", Source: "one ^ two"},
	{ID: "bitwise-not", Source: "~one"},
	{ID: "shift-left", Source: "one << two"},
	{ID: "template-literal", Source: "`Kit${word}`"},
	{ID: "unknown-string-escape", Source: "'bad\\q'"},
	{ID: "undefined-literal", Source: "undefined"},
	{ID: "nan-literal", Source: "NaN"},
	{ID: "infinity-literal", Source: "Infinity"},
	{ID: "non-finite-number", Source: "1e999"},
	{ID: "new-expression", Source: "new Date()"},
	{ID: "typeof-expression", Source: "typeof one"},
	{ID: "delete-expression", Source: "delete user.name"},
	{ID: "function-expression", Source: "function () { return one; }"},
	{ID: "this-expression", Source: "this.user"},
	{ID: "regex-literal", Source: "/KitJS/"},
	{ID: "array-hole", Source: "[one,,two]"},
	{ID: "array-spread", Source: "[...numbers]"},
	{ID: "array-trailing-comma", Source: "[one, two,]"},
	{ID: "object-spread", Source: "({ ...user })"},
	{ID: "bare-lambda-param", Source: "x => x + one"},
	{ID: "invalid-lambda-param", Source: "(x, 1) => x"},
	{ID: "duplicate-lambda-param", Source: "(x, x) => x"},
	{ID: "blocked-lambda-param", Source: "(constructor) => constructor"},
	{ID: "reserved-alias-lambda-param", Source: "($dialog) => $dialog"},
	{ID: "optional-call", Source: "missing?.()"},
	{ID: "optional-computed", Source: "user?.[profileKey]"},
	{ID: "mixed-nullish-or", Source: "null ?? false || true"},
	{ID: "mixed-and-nullish", Source: "true && null ?? one"},

	// No ambient-global fallback and no prototype escape path.
	{ID: "global-window", Source: "window.location.href"},
	{ID: "global-global-this", Source: "globalThis.location.href"},
	{ID: "global-document", Source: "document.body"},
	{ID: "global-location", Source: "location.href"},
	{ID: "global-top", Source: "top.location.href"},
	{ID: "global-parent", Source: "parent.location.href"},
	{ID: "global-self", Source: "self.location.href"},
	{ID: "global-function", Source: "Function('return 1')()"},
	{ID: "global-eval", Source: "eval('1')"},
	{ID: "global-getter-probe", Source: "__kitGlobalExpressionProbe"},
	{ID: "blocked-constructor", Source: "user.constructor"},
	{ID: "blocked-computed-constructor", Source: "user['constructor']"},
	{ID: "blocked-dynamic-constructor", Source: "user[blockedKey]"},
	{ID: "blocked-object-computed-key", Source: "user[{}]"},
	{ID: "blocked-prototype", Source: "user.prototype"},
	{ID: "blocked-proto", Source: "user['__proto__']"},
	{ID: "blocked-define-getter", Source: "user.__defineGetter__"},
	{ID: "blocked-define-setter", Source: "user.__defineSetter__"},
	{ID: "blocked-lookup-getter", Source: "user.__lookupGetter__"},
	{ID: "blocked-lookup-setter", Source: "user.__lookupSetter__"},
	{ID: "blocked-caller", Source: "user.label.caller"},
	{ID: "blocked-arguments", Source: "user.label['arguments']"},
	{ID: "blocked-owner-document", Source: "user.ownerDocument"},
	{ID: "blocked-default-view", Source: "user.defaultView"},
	{ID: "blocked-content-window", Source: "user.contentWindow"},
	{ID: "blocked-object-key", Source: "({ constructor: one }).constructor"},
}

func TestBrowserClosedExpressionConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS expression conformance in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	kitJS := readVanillaFile(t, "kit.js")
	fixture := buildClosedExpressionDocument(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/contracts/closed-expressions.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write(fixture)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/contracts/closed-expressions.html")
}

func buildClosedExpressionDocument(t *testing.T) []byte {
	t.Helper()
	positive := make([]closedExpressionExpectation, 0, len(closedExpressionReadCases))
	rejected := make([]string, 0, len(closedExpressionRejectCases))
	var document strings.Builder
	document.WriteString(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>KitJS closed expression conformance</title></head>
<body>
<main data-kit-component="expression-conformance" id="expression-root">
  <section id="expression-read-cases">
`)
	for _, testCase := range closedExpressionReadCases {
		id := "expression-" + testCase.ID
		positive = append(positive, closedExpressionExpectation{ID: id, Want: testCase.Want})
		document.WriteString("    <output id=\"")
		document.WriteString(html.EscapeString(id))
		document.WriteString("\" data-kit-text=\"")
		document.WriteString(html.EscapeString(testCase.Source))
		document.WriteString("\">server</output>\n")
	}
	document.WriteString("  </section>\n  <section id=\"expression-reject-cases\">\n")
	for _, testCase := range closedExpressionRejectCases {
		id := "expression-reject-" + testCase.ID
		rejected = append(rejected, id)
		document.WriteString("    <output id=\"")
		document.WriteString(html.EscapeString(id))
		document.WriteString("\" data-kit-text=\"")
		document.WriteString(html.EscapeString(testCase.Source))
		document.WriteString("\">blocked</output>\n")
	}

	// A wide array keeps evaluator stack depth shallow while crossing the node
	// budget. Whether the implementation rejects it at compile or walk time, it
	// must fail closed and leave the server text untouched.
	budgetSource := "[" + strings.Repeat("1,", 25000) + "1].length"
	document.WriteString("    <output id=\"expression-budget\" data-kit-text=\"")
	document.WriteString(html.EscapeString(budgetSource))
	document.WriteString("\">budget-blocked</output>\n")
	depthSource := strings.Repeat("(", 80) + "one" + strings.Repeat(")", 80)
	document.WriteString("    <output id=\"expression-parser-depth\" data-kit-text=\"")
	document.WriteString(html.EscapeString(depthSource))
	document.WriteString("\">depth-blocked</output>\n  </section>\n")

	document.WriteString(`  <section id="expression-action-cases">
    <output id="expression-count" data-kit-text="count">server</output>
    <output id="expression-chain-a" data-kit-text="chainA">server</output>
    <output id="expression-chain-b" data-kit-text="chainB">server</output>
    <output id="expression-sequence-total" data-kit-text="sequenceTotal">server</output>
    <output id="expression-user-name" data-kit-text="user.profile.name">server</output>
    <output id="expression-first-number" data-kit-text="numbers[0]">server</output>
    <output id="expression-loop-null" data-kit-text="loop === null">server</output>
    <div id="expression-inner-html-sink" data-kit-bind="innerHTML: payload"><span>safe-inner</span></div>
    <div id="expression-outer-html-sink" data-kit-bind="outerHTML: payload">safe-outer</div>

    <button id="expression-mode-action" data-kit-click="count = 99">mode action</button>
    <button id="expression-chain-action" data-kit-click="chainA = chainB = 7">chain action</button>
    <button id="expression-sequence-action" data-kit-click="sequenceA = 1; sequenceB = sequenceA + 1; sequenceTotal = sequenceB + 1;">sequence action</button>
    <button id="expression-lambda-action" data-kit-click="setter = (value) => count = value; setter(13);">lambda action</button>
    <button id="expression-recursion-budget" data-kit-click="loop = () => loop(); loop();">recursive lambda</button>
    <button id="expression-after-budget" data-kit-click="count = count + 1">after budget</button>
    <button id="expression-late-failure" data-kit-click="count = 123; unknownField = 1">late failure</button>
    <button id="expression-accessor-failure" data-kit-click="count = 456; guarded = 'invalid'">accessor failure</button>
    <button id="expression-store-lambda" data-kit-click="reusable = () => count = count + 1">store lambda</button>
    <button id="expression-call-stored-lambda" data-kit-click="reusable()">call stored lambda</button>

    <button class="expression-invalid-action" data-kit-click="touch('member-assignment'); user.profile.name = 'Changed'">invalid member assignment</button>
    <button class="expression-invalid-action" data-kit-click="touch('computed-assignment'); numbers[0] = 99">invalid computed assignment</button>
    <button class="expression-invalid-action" data-kit-click="touch('compound'); count += 1">invalid compound assignment</button>
    <button class="expression-invalid-action" data-kit-click="touch('update'); count++">invalid update</button>
    <button class="expression-invalid-action" data-kit-click="touch('comma'), count = 2">invalid comma sequence</button>
    <button class="expression-invalid-action" data-kit-click="touch('global'); window.location.href">invalid global</button>
    <button class="expression-invalid-action" data-kit-click="touch('prototype'); user.constructor">invalid prototype escape</button>
    <button class="expression-invalid-action" data-kit-click="unknownField = 1">invalid unknown assignment</button>
    <button class="expression-invalid-action" data-kit-click="$.name = 'forbidden'">invalid page-scope assignment</button>
    <button class="expression-invalid-action" data-kit-click="touch('alias-write'); $dialog = null">invalid alias assignment</button>
  </section>
</main>
<script>
(function () {
  "use strict";
  globalThis.__kitExpressionSideEffects = 0;
  globalThis.__kitGlobalExpressionReads = 0;
  globalThis.__kitExpressionErrors = [];
  Object.defineProperty(globalThis, "__kitGlobalExpressionProbe", {
    configurable: true,
    get: function () {
      globalThis.__kitGlobalExpressionReads++;
      return "ambient-global-leaked";
    }
  });
  var originalError = console.error;
  console.error = function () {
    var parts = [];
    for (var index = 0; index < arguments.length; index++) {
      var value = arguments[index];
      parts.push(String(value && value.message || value));
    }
    globalThis.__kitExpressionErrors.push(parts.join(" "));
    return originalError.apply(this, arguments);
  };
})();
</script>
<script src="/kit.js"></script>
<script>
kit.component("expression-conformance", {
  zero: 0,
  one: 1,
  two: 2,
  five: 5,
  seven: 7,
  twenty: 20,
  factor: 2,
  falsy: false,
  missing: undefined,
  word: "vanilla",
  profileKey: "profile",
  nameKey: "name",
  blockedKey: "constructor",
  numbers: [2, 4, 8],
  user: {
    profile: { name: "Kit" },
    label: function (suffix) { return this.profile.name + suffix; }
  },
  count: 1,
  chainA: 0,
  chainB: 0,
  sequenceA: 0,
  sequenceB: 0,
  sequenceTotal: 0,
  setter: null,
  loop: null,
  reusable: null,
  payload: "<b>unsafe</b>",
  get guarded() { return "safe"; },
  set guarded(value) { throw new Error("guarded setter rejected " + value); },
  sum: function (left, right) { return left + right; },
  owns: function (name) { return Object.prototype.hasOwnProperty.call(this, name); },
  touch: function (label) {
    globalThis.__kitExpressionSideEffects++;
    return label;
  }
});
</script>
<script>
`)

	positiveJSON, err := json.Marshal(positive)
	if err != nil {
		t.Fatal(err)
	}
	rejectedJSON, err := json.Marshal(rejected)
	if err != nil {
		t.Fatal(err)
	}
	document.WriteString(browserHarness)
	document.WriteString("\n")
	document.WriteString(closedExpressionAssertions(string(positiveJSON), string(rejectedJSON)))
	document.WriteString("\n</script>\n</body>\n</html>")
	return []byte(document.String())
}

func closedExpressionAssertions(positiveJSON, rejectedJSON string) string {
	return `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var positive = ` + positiveJSON + `;
  var rejected = ` + rejectedJSON + `;

  await waitFor(function () {
    return document.getElementById("expression-precedence-multiply").textContent === "7";
  }, "closed expression bindings did not initialize");

  positive.forEach(function (testCase) {
    var actual = document.getElementById(testCase.id).textContent;
    assert(actual === testCase.want, testCase.id + " rendered " + JSON.stringify(actual) + ", want " + JSON.stringify(testCase.want));
  });
  rejected.forEach(function (id) {
    assert(document.getElementById(id).textContent === "blocked", id + " did not fail closed");
  });
  assert(document.getElementById("expression-budget").textContent === "budget-blocked", "oversized expression did not fail closed");
  assert(document.getElementById("expression-parser-depth").textContent === "depth-blocked", "parser depth limit was not enforced");
  assert(globalThis.__kitExpressionErrors.some(function (message) {
    return /budget|limit|depth|large|deep/i.test(message);
  }), "oversized expression did not report a budget/limit error");
  assert(globalThis.__kitGlobalExpressionReads === 0, "authored expression read an ambient global");
  assert(globalThis.__kitExpressionSideEffects === 0, "a short-circuited or rejected expression executed a call");

  assert(document.getElementById("expression-count").textContent === "1", "binding assignment mutated state");
  document.querySelectorAll(".expression-invalid-action").forEach(function (button) { button.click(); });
  await nextTurn();
  assert(globalThis.__kitExpressionSideEffects === 0, "an invalid action partially executed before rejection");
  assert(document.getElementById("expression-count").textContent === "1", "an invalid action changed count");
  assert(document.getElementById("expression-user-name").textContent === "Kit", "member assignment was accepted");
  assert(document.getElementById("expression-first-number").textContent === "2", "computed assignment was accepted");
  assert(document.getElementById("expression-unknown-own-field").textContent === "false", "assignment created an unknown component field");
  assert(document.getElementById("expression-inner-html-sink").innerHTML === "<span>safe-inner</span>", "innerHTML binding was accepted");
  assert(document.getElementById("expression-outer-html-sink").textContent === "safe-outer", "outerHTML binding was accepted");

  document.getElementById("expression-late-failure").click();
  await nextTurn();
  assert(document.getElementById("expression-count").textContent === "1", "late action failure partially committed state");
  document.getElementById("expression-accessor-failure").click();
  await nextTurn();
  assert(document.getElementById("expression-count").textContent === "1", "accessor assignment partially committed state");

  // The exact source was rejected above in binding mode but must compile in
  // action mode. This catches a source-only cache that forgets parser mode.
  document.getElementById("expression-mode-action").click();
  await waitFor(function () { return document.getElementById("expression-count").textContent === "99"; }, "identifier assignment action failed");

  document.getElementById("expression-chain-action").click();
  await waitFor(function () {
    return document.getElementById("expression-chain-a").textContent === "7" &&
      document.getElementById("expression-chain-b").textContent === "7";
  }, "right-associative assignment failed");

  document.getElementById("expression-sequence-action").click();
  await waitFor(function () {
    return document.getElementById("expression-sequence-total").textContent === "3";
  }, "semicolon action sequence failed");

  document.getElementById("expression-lambda-action").click();
  await waitFor(function () { return document.getElementById("expression-count").textContent === "13"; }, "action lambda could not assign its lexical state");

  var errorsBeforeRecursion = globalThis.__kitExpressionErrors.length;
  document.getElementById("expression-recursion-budget").click();
  await waitFor(function () {
    return globalThis.__kitExpressionErrors.slice(errorsBeforeRecursion).some(function (message) {
      return /budget|limit|depth/i.test(message);
    });
  }, "recursive lambda was not stopped by the call budget");
  assert(document.getElementById("expression-loop-null").textContent === "true", "failed recursive action committed its lambda");
  document.getElementById("expression-after-budget").click();
  await waitFor(function () { return document.getElementById("expression-count").textContent === "14"; }, "runtime did not recover after a budget rejection");

  document.getElementById("expression-store-lambda").click();
  var stored = document.getElementById("expression-call-stored-lambda");
  for (var invocation = 0; invocation < 2600; invocation++) stored.click();
  await waitFor(function () { return document.getElementById("expression-count").textContent === "2614"; }, "stored lambda reused an exhausted evaluation budget");

  assert(globalThis.__kitGlobalExpressionReads === 0, "an action read an ambient global");
});`
}
