package hydrate

import (
	"encoding/json"
	"testing"
)

// The corpus pins the cases the contract names. The twins can still part ways on what it does not
// pin, and a value that splits one way on the server and another in the browser is a directive that
// verifies and then does nothing. These are such edges: a modifier's parentheses nested, and the white
// space the two languages disagree on by default (Go's strings.TrimSpace takes U+0085 and leaves
// U+FEFF, JavaScript's trim the other way round; Go's \s is ASCII without \v, JavaScript's is not).
//
// The server answers every case first (runGroupedCase, the corpus runner's own shape), and the kernel
// must give the SAME answer, case for case — the same pairs, or a refusal of its own (hydrate: …), not
// a TypeError. A disagreement names the case and both readings.
func TestGroupedTwinsAgreeBeyondTheCorpus(t *testing.T) {
	cases := []groupedCase{
		{Kind: "pairs", Name: "nested-modifier-parentheses", Src: "click.debounce((300)): x"},
		{Kind: "pairs", Name: "colon-inside-a-modifier", Src: "click.debounce(3:00): x"},
		{Kind: "pairs", Name: "dot-inside-a-modifier", Src: "intersect-enter.margin(-1.5rem): seen = true"},
		{Kind: "pairs", Name: "next-line-before-a-name", Src: "\u0085text: x"},
		{Kind: "pairs", Name: "byte-order-mark-before-a-name", Src: "\ufefftext: x"},
		{Kind: "pairs", Name: "no-break-spaces-round-a-value", Src: "text:\u00a0x\u00a0"},
		{Kind: "pairs", Name: "line-separator-before-a-semicolon", Src: "text: x\u2028; show: y"},
		{Kind: "pairs", Name: "vertical-tab-after-a-value", Src: "text: x\v"},
		{Kind: "pairs", Name: "ideographic-space-after-a-name", Src: "a\u3000: x"},
		{Kind: "pairs", Name: "escaped-multibyte-in-a-string", Src: "text: \"é\\é;\"; show: y"},
		{Kind: "component", Name: "byte-order-mark-before-the-name", Src: "\ufefftheme: $t"},
		{Kind: "component", Name: "next-line-after-the-name", Src: "theme\u0085"},
		{Kind: "component", Name: "no-break-space-before-the-alias", Src: "theme@1.0.0:\u00a0$t"},
		{Kind: "for", Name: "vertical-tab-around-of", Src: "x\vof xs"},
		{Kind: "for", Name: "no-break-spaces-around-of", Src: "x\u00a0of\u00a0xs; key: x.id"},
		{Kind: "for", Name: "em-space-after-the-comma", Src: "x,\u2003i of xs"},
		{Kind: "for", Name: "next-line-before-of", Src: "x\u0085of xs"},
		{Kind: "for", Name: "next-line-after-the-list", Src: "x of xs\u0085"},
	}
	refused := 0
	for i, c := range cases {
		got, err := runGroupedCase(c)
		if err != nil {
			cases[i].Error = true
			refused++
			continue
		}
		want, merr := json.Marshal(got)
		if merr != nil {
			t.Fatal(merr)
		}
		cases[i].Want = want
	}
	// both kinds of answer are in the list, so agreement is not a list of refusals agreeing
	if refused == 0 || refused == len(cases) {
		t.Fatalf("the list must hold cases the server reads and cases it refuses, got %d refused of %d", refused, len(cases))
	}
	list, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	assertions := `
var CASES = ` + string(list) + `;
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
var failures = [];
CASES.forEach(function (c) {
  var got, thrown = null;
  try { got = read[c.kind](c.src); } catch (e) { thrown = e; }
  var server = c.error ? "a refusal" : canon(c.want);
  if (thrown && !(thrown instanceof Error && String(thrown.message).indexOf("hydrate: ") === 0)) {
    failures.push(c.kind + "/" + c.name + ": the kernel fell over instead of reading or refusing: " + thrown);
  } else if (c.error !== !!thrown) {
    failures.push(c.kind + "/" + c.name + ": server read " + server + ", kernel read " + (thrown ? "a refusal (" + thrown.message + ")" : canon(got)));
  } else if (!c.error && canon(got) !== canon(c.want)) {
    failures.push(c.kind + "/" + c.name + ": server read " + server + ", kernel read " + canon(got));
  }
});
if (failures.length) throw new Error("the twins disagree:\n" + failures.join("\n"));
console.log("grouped twins agree on " + CASES.length + " cases beyond the corpus");
`
	runNodeDOMScript(t, "grouped-parity.test.js", assertions)
}
