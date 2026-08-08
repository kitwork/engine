package hydrate

import (
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The conformance suite is the safety net the whole KitJS V2 direction rests on: the Go evaluator
// (eval.go, the server "twin") and the JS walker (kernel.js `walk`, the client) must produce the
// SAME value for the SAME source against the SAME scope. Both are hand-written implementations of one
// grammar; without this test they drift silently — eval.go already matches the client "by hand"
// (a missing key reads as 0, calling a non-lambda yields undefined), and a divergence there is how a
// server-rendered first paint disagrees with what the client then computes.
//
// One corpus, two runners. Each case carries its OWN expected value, and BOTH engines are checked
// against it — so passing means walk(src, scope) == want == eval(src, scope), i.e. the two agree.
//
// The load-bearing case is `lambda-method-mutates-state`: a component method written as an IR lambda
// (`add = () => count = count + 1`) that mutates its enclosing scope. If both engines run it to the
// same result, then a component's METHODS have a server twin — which is exactly the open decision
// ("are methods real JS, or portable IR lambdas?") settled by a run instead of an argument.

//go:embed conformance_corpus.json
var conformanceCorpus []byte

type confCase struct {
	Name  string         `json:"name"`
	Src   string         `json:"src"`
	Scope map[string]any `json:"scope"`
	Want  any            `json:"want"`
}

func loadConfCases(t *testing.T) []confCase {
	t.Helper()
	var cases []confCase
	if err := json.Unmarshal(conformanceCorpus, &cases); err != nil {
		t.Fatalf("conformance corpus is not valid JSON: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("conformance corpus is empty")
	}
	return cases
}

// canonicalJSON marshals a decoded value deterministically (Go sorts map keys), so two values that
// are equal-as-data compare equal-as-string regardless of key order or int/float spelling.
func canonicalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("cannot marshal %#v: %v", v, err)
	}
	return string(b)
}

// TestConformanceServer proves the Go side alone: Compile + Eval must produce each case's expected
// value. This is also the empirical answer to the method-language fork — if the lambda-method case
// passes here, eval.go can run a component method's IR lambda and mutate state, so the server twin
// covers component behaviour, not just leaf expressions.
func TestConformanceServer(t *testing.T) {
	for _, c := range loadConfCases(t) {
		scope := c.Scope
		if scope == nil {
			scope = map[string]any{}
		}
		ir, err := Compile(c.Src)
		if err != nil {
			t.Errorf("%s: compile %q: %v", c.Name, c.Src, err)
			continue
		}
		got, err := Eval(ir, scope)
		if err != nil {
			t.Errorf("%s: eval %q: %v", c.Name, c.Src, err)
			continue
		}
		if g, w := canonicalJSON(t, got), canonicalJSON(t, c.Want); g != w {
			t.Errorf("%s: eval(%q) = %s, want %s", c.Name, c.Src, g, w)
		}
	}
}

// TestConformanceClientMatchesServer runs the SAME corpus through the composed client runtime in
// node (window.kit.compile + window.kit.run) and asserts each result equals the same expected value.
// Because both TestConformanceServer and this test check against the corpus's `want`, agreement here
// means the client walker and the Go evaluator computed the same thing — the conformance guarantee.
func TestConformanceClientMatchesServer(t *testing.T) {
	node := requireNode(t)

	// A DOM shim just complete enough for kernel.js to boot in node (it wires one MutationObserver
	// and a few delegated listeners at load time). Mirrors bridge_test.go's shim.
	const setup = `
global.window = { addEventListener: function () {}, removeEventListener: function () {} };
var storage = Object.create(null);
global.document = {
  readyState: "complete",
  addEventListener: function () {}, removeEventListener: function () {},
  querySelector: function () { return null; }, querySelectorAll: function () { return []; },
  dispatchEvent: function () {},
  documentElement: { nodeType: 1, querySelectorAll: function () { return []; },
    classList: { contains: function () { return false; }, toggle: function () {} },
    style: { setProperty: function () {} } },
  body: {}
};
global.navigator = {};
global.localStorage = {
  getItem: function (k) { return Object.prototype.hasOwnProperty.call(storage, k) ? storage[k] : null; },
  setItem: function (k, v) { storage[k] = String(v); }, removeItem: function (k) { delete storage[k]; }
};
global.history = { back: function () {}, forward: function () {} };
global.location = { reload: function () {} };
global.MutationObserver = function () { this.observe = function () {}; this.disconnect = function () {}; };
global.CustomEvent = function () {};
window.document = document; window.navigator = navigator; window.localStorage = localStorage;
window.history = history; window.location = location;
`

	// The corpus is injected verbatim so the client reads exactly the bytes the Go side read.
	assertions := `
var CORPUS = ` + string(conformanceCorpus) + `;
var kit = window.kit;
if (!kit || typeof kit.compile !== "function" || typeof kit.run !== "function") {
  throw new Error("window.kit.compile/run missing — the runtime did not expose the canonical root");
}
function canon(v) {
  if (Array.isArray(v)) return "[" + v.map(canon).join(",") + "]";
  if (v && typeof v === "object") {
    var ks = Object.keys(v).sort();
    return "{" + ks.map(function (k) { return JSON.stringify(k) + ":" + canon(v[k]); }).join(",") + "}";
  }
  return JSON.stringify(v);
}
var failures = [];
CORPUS.forEach(function (c) {
  var got;
  // Mirror the server: Eval treats the passed scope AS the page root, so the dollar-root returns it.
  // The client walker reads the dollar-root as a scope key, so give the scope a self-reference. The
  // harness collapses page-root and a component local scope into ONE map — enough to test the
  // root read/write opcodes both sides hand-match; the client-only lexical CHAIN is a separate,
  // untestable-here structure because Eval only ever sees a flat snapshot.
  var scope = c.scope || {};
  scope["$"] = scope;
  try { got = kit.run(kit.compile(c.src), scope); }
  catch (e) { failures.push(c.name + ": threw " + e.message); return; }
  if (canon(got) !== canon(c.want)) {
    failures.push(c.name + ": walk(" + JSON.stringify(c.src) + ") = " + canon(got) + ", want " + canon(c.want));
  }
});
if (failures.length) { console.error(failures.join("\n")); process.exitCode = 1; }
else console.log("conformance: " + CORPUS.length + " cases — client agrees with server");
`

	script := setup + "\n" + Runtime() + "\n" + assertions
	path := filepath.Join(t.TempDir(), "conformance.test.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("client conformance failed:\n%s", out)
	}
	t.Logf("%s", out)
}
