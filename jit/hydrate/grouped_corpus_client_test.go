package hydrate

import (
	"os"
	"testing"
)

// The grouped splitter has two copies, pairs.go on the server and kernel.js in the browser, and
// grouped_corpus.json is the contract both are held to: every case's "want", or an error where the
// case says "error": true. The Go half runs in pairs_test.go; this is the browser half, run through
// the kernel's own reader (kit.internal.grouped) in node. A value that verifies on the server and
// reads differently in the browser would be a directive that checks out and does nothing, so the two
// must give the same answer, case for case.
//
// Each case is checked for its REASON: a "want" case must not throw and must equal want exactly, an
// "error" case must throw an Error whose message is the kernel's own (hydrate: …), not a TypeError
// from a reader that fell over. The corpus is read from disk, byte for byte what the Go half reads.
func TestGroupedCorpusClient(t *testing.T) {
	corpus, err := os.ReadFile("grouped_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	assertions := `
var CORPUS = ` + string(corpus) + `;
// Beyond the corpus: what pairs_test.go also refuses, so the twins agree on a modifier's parentheses.
CORPUS.push(
  { kind: "pairs", name: "colon-inside-modifier-argument", src: "click.debounce(3:00): x", error: true },
  { kind: "pairs", name: "modifier-argument-left-open", src: "click.debounce(300: x", error: true },
  { kind: "pairs", name: "modifier-argument-closed-first", src: "click.debounce)300(: x", error: true },
  { kind: "for", name: "key-given-twice", src: "x of xs; key: x.id; key: x.name", error: true }
);
var grouped = window.kit.internal && window.kit.internal.grouped;
if (!grouped) throw new Error("kit.internal.grouped is missing: the kernel exposes no grouped reader");
var read = { pairs: grouped.pairs, component: grouped.component, "for": grouped.loop };
function canon(v) {
  if (Array.isArray(v)) return "[" + v.map(canon).join(",") + "]";
  if (v && typeof v === "object") {
    return "{" + Object.keys(v).sort().map(function (k) { return JSON.stringify(k) + ":" + canon(v[k]); }).join(",") + "}";
  }
  return JSON.stringify(v);
}
var failures = [], wants = 0, errors = 0;
CORPUS.forEach(function (c) {
  if (!read[c.kind]) { failures.push(c.name + ": unknown kind " + c.kind); return; }
  var got, thrown = null;
  try { got = read[c.kind](c.src); } catch (e) { thrown = e; }
  if (c.error) {
    errors++;
    if (!thrown) failures.push(c.name + ": " + JSON.stringify(c.src) + " must be refused, read as " + canon(got));
    else if (!(thrown instanceof Error) || String(thrown.message).indexOf("hydrate: ") !== 0) failures.push(c.name + ": refused for the wrong reason: " + thrown);
    return;
  }
  wants++;
  if (thrown) { failures.push(c.name + ": " + JSON.stringify(c.src) + " threw " + thrown.message); return; }
  if (canon(got) !== canon(c.want)) failures.push(c.name + ": " + JSON.stringify(c.src) + " read as " + canon(got) + ", want " + canon(c.want));
});
if (!wants || !errors) failures.push("the corpus must hold both kinds of case, got " + wants + " wants and " + errors + " errors");
if (failures.length) throw new Error("grouped corpus (client):\n" + failures.join("\n"));
console.log("grouped corpus (client): " + CORPUS.length + " cases, " + wants + " read exactly, " + errors + " refused");
`
	runNodeDOMScript(t, "grouped-corpus.test.js", assertions)
}
