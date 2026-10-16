package hydrate

import (
	_ "embed"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// The parity corpus (16/10/2026, the owner's decision): a .ui expression gives one answer when the
// server paints the page first and when the browser's kernel evaluates it again, and that answer is
// JavaScript's wherever the grammar lets it be. parity_corpus.json holds ~4000 cases taken from the
// matrix that found where the two ends differed — every operator over values of every kind (-0, NaN,
// Infinity, numeric text, emoji, null, a member nobody set, lists, objects, lambdas, globals), every
// member of String, Array, Number and Object and every static of Math, JSON, String, Number, Array,
// Date and Boolean the kernel reaches, and the hand-written families (number literals, escapes, lambdas
// as functions, lists a method changes, holes, sort, number formats, JSON, Unicode text, Date, URI,
// regular expressions, normalize, the depth calls may nest to). Each case carries its answer in one
// canonical form (describeParity, twin of describe in the node runner) and the text the page shows when
// that is not the value's own; TestParityCorpusServer holds eval.go to it and
// TestParityCorpusClientMatchesServer the kernel, so the two ends are checked against each other
// forever. The answers are where JavaScript and Kitwork agree, and where Kitwork differs on purpose
// (a name nothing declares is 0, a member of null is undefined, a value that is not there is 0 in
// arithmetic) — the expression section of docs/KITWORK_UI.md lists those.

//go:embed parity_corpus.json
var parityCorpus []byte

type parityCase struct {
	Group string         `json:"group"`
	Src   string         `json:"src"`
	Scope map[string]any `json:"scope"`
	Want  string         `json:"want"`
	Text  *string        `json:"text"`
}

type parityFile struct {
	Scope map[string]any `json:"scope"`
	Cases []parityCase   `json:"cases"`
}

func loadParity(t *testing.T) parityFile {
	t.Helper()
	var f parityFile
	if err := json.Unmarshal(parityCorpus, &f); err != nil {
		t.Fatalf("parity corpus is not valid JSON: %v", err)
	}
	if len(f.Cases) < 1000 {
		t.Fatalf("parity corpus has %d cases", len(f.Cases))
	}
	return f
}

// parityScope is a fresh copy of the corpus's scope with the case's own over it: a case that writes
// (x = 1, l.push(3)) touches no other.
func parityScope(t *testing.T, base, own map[string]any) map[string]any {
	t.Helper()
	var scope map[string]any
	b, _ := json.Marshal(base)
	if err := json.Unmarshal(b, &scope); err != nil {
		t.Fatal(err)
	}
	for k, v := range own {
		var copied any
		b, _ := json.Marshal(v)
		json.Unmarshal(b, &copied)
		scope[k] = copied
	}
	return scope
}

// describeParity is the canonical form of a value, as describe in the node runner writes the kernel's:
// a number as JavaScript writes it with -0 and NaN by name, a string as JSON.stringify quotes it, a list
// its items (a hole <hole>), an object its keys in its own order, a lambda fn: and its source, a
// built-in function native: and its name, a global global: and its name.
func describeParity(v any, depth int) string {
	if depth > 12 {
		return "<deep>"
	}
	switch t := v.(type) {
	case undefinedType:
		return "undefined"
	case nil:
		return "null"
	case bool:
		return primitiveText(t)
	case float64:
		switch {
		case t != t:
			return "NaN"
		case t == 0 && math.Signbit(t):
			return "-0"
		}
		return value.NumberText(t)
	case string:
		return quoteJSON(t)
	case globalRef:
		return "global:" + string(t)
	case *Lambda:
		return "fn:" + t.Source
	case nativeFunc:
		return "native:" + t.member().name
	case *boundFunc:
		name := ""
		switch target := t.target.(type) {
		case nativeFunc:
			name = target.member().name
		case globalRef:
			name = string(target)
		}
		return "native:bound " + name
	case Func:
		return "native:"
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			if isHole(x) {
				parts[i] = "<hole>"
			} else {
				parts[i] = describeParity(norm(x), depth+1)
			}
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any, *Object:
		keys := ownKeys(t)
		parts := make([]string, len(keys))
		for i, k := range keys {
			x, _ := ownGet(t, k)
			parts[i] = quoteJSON(k) + ":" + describeParity(x, depth+1)
		}
		return "{" + strings.Join(parts, ",") + "}"
	case *rawJSON:
		return `{"rawJSON":` + quoteJSON(t.text) + "}"
	case *iterator:
		return "<" + t.tag + ">"
	case *promise:
		return "<Promise>"
	}
	return "<other>"
}

// TestParityCorpusServer holds the server's evaluator to every answer of the corpus, and its first
// paint to the text the page shows.
func TestParityCorpusServer(t *testing.T) {
	f := loadParity(t)
	bad := 0
	for _, c := range f.Cases {
		got := "throws"
		ir, err := Compile(strings.TrimSpace(c.Src))
		if err == nil {
			if v, err := Eval(ir, parityScope(t, f.Scope, c.Scope)); err == nil {
				got = describeParity(v, 0)
			}
		}
		if got != c.Want {
			bad++
			if bad <= 25 {
				t.Errorf("%s: %s = %s, want %s", c.Group, c.Src, got, c.Want)
			}
			continue
		}
		if c.Want == "throws" {
			continue
		}
		text, err := EvalText(ir, parityScope(t, f.Scope, c.Scope))
		if err != nil {
			text = "" // the page shows nothing, and the browser keeps it
		}
		want := ""
		if c.Text != nil {
			want = *c.Text
		} else if err := json.Unmarshal([]byte(c.Want), &want); err != nil {
			t.Fatalf("%s: no text and no string answer: %s", c.Src, c.Want)
		}
		if text != want {
			bad++
			if bad <= 25 {
				t.Errorf("%s: %s prints %q, want %q", c.Group, c.Src, text, want)
			}
		}
		// the value handed to the browser as a literal (a component's input) reads back as itself
		if h := parityHandoff(t, f, c); h.exact && h.Want != c.Want {
			bad++
			if bad <= 25 {
				t.Errorf("%s: %s is handed on as %s, which reads back as %s, want %s", c.Group, c.Src, h.Literal, h.Want, c.Want)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d parity cases differ on the server", bad, len(f.Cases))
	}
	t.Logf("parity corpus: %d cases, the server agrees", len(f.Cases))
}

// parityHandoff is a case's value as the server hands it to the browser (Literal, 16/10/2026) and what
// the server reads that back as (Want, in the canonical form; "throws" past the budget), which the
// kernel must read back as too. exact is whether Literal says it is the value itself.
type parityHandoffCase struct {
	Literal string `json:"literal"`
	Want    string `json:"want"`
	exact   bool
}

func parityHandoff(t *testing.T, f parityFile, c parityCase) parityHandoffCase {
	t.Helper()
	ir, err := Compile(strings.TrimSpace(c.Src))
	if err != nil {
		return parityHandoffCase{}
	}
	v, err := Eval(ir, parityScope(t, f.Scope, c.Scope))
	if err != nil {
		return parityHandoffCase{}
	}
	h := parityHandoffCase{}
	h.Literal, h.exact = Literal(v)
	back, err := Compile(h.Literal)
	if err != nil {
		t.Errorf("%s: %s is handed on as %s, which does not compile: %v", c.Group, c.Src, h.Literal, err)
		return parityHandoffCase{}
	}
	h.Want = "throws"
	if v, err := Eval(back, map[string]any{}); err == nil {
		h.Want = describeParity(v, 0)
	} else {
		h.exact = false // past the budget: neither end reads it
	}
	return h
}

// TestParityCorpusClientMatchesServer runs the same corpus through the composed client runtime in node
// (window.kit.compile + window.kit.run) and holds each answer and each text to the same corpus: with
// TestParityCorpusServer passing, the kernel and the server answer alike. Each value is also handed on
// as the server hands a component's input to the browser (Literal), and the kernel must read it back as
// the server does (16/10/2026: NaN, undefined, a lambda, a global read back as null in the browser).
func TestParityCorpusClientMatchesServer(t *testing.T) {
	node := requireNode(t)
	f := loadParity(t)
	handoffs := make([]*parityHandoffCase, len(f.Cases))
	for i, c := range f.Cases {
		if h := parityHandoff(t, f, c); h.Literal != "" {
			handoffs[i] = &h
		}
	}
	handoffJSON, err := json.Marshal(handoffs)
	if err != nil {
		t.Fatal(err)
	}
	assertions := `
process.on("unhandledRejection", function () {}); // Array.fromAsync() settles after the expression
var CORPUS = ` + string(parityCorpus) + `;
var HANDOFF = ` + string(handoffJSON) + `;
var kit = window.kit;
var GLOBALS = new Map([[Math, "Math"], [Date, "Date"], [JSON, "JSON"], [Number, "Number"], [String, "String"], [Boolean, "Boolean"],
  [Array, "Array"], [parseInt, "parseInt"], [parseFloat, "parseFloat"], [encodeURIComponent, "encodeURIComponent"],
  [decodeURIComponent, "decodeURIComponent"], [isNaN, "isNaN"], [isFinite, "isFinite"]]);
function describe(v, depth) {
  depth = depth || 0;
  if (depth > 12) return "<deep>";
  if (v === undefined) return "undefined";
  if (v === null) return "null";
  switch (typeof v) {
    case "boolean": return String(v);
    case "number": return v !== v ? "NaN" : v === 0 && 1 / v < 0 ? "-0" : String(v);
    case "string": return JSON.stringify(v);
  }
  if (GLOBALS.has(v)) return "global:" + GLOBALS.get(v);
  if (typeof v === "object" && !Array.isArray(v) && Object.isFrozen(v) && Object.keys(v).join() === "keys" && v.keys === Object.keys) return "global:Object";
  if (typeof v === "function") {
    // a lambda is a bound function whose own toString gives its source: String(v), as a page prints it
    if (/\[native code\]\s*\}$/.test(String(v))) return "native:" + v.name;
    return "fn:" + String(v);
  }
  if (Array.isArray(v)) {
    var items = [];
    for (var i = 0; i < v.length; i++) items.push(i in v ? describe(v[i], depth + 1) : "<hole>");
    return "[" + items.join(",") + "]";
  }
  var proto = Object.getPrototypeOf(v);
  if (proto === Object.prototype || proto === null) {
    return "{" + Object.keys(v).map(function (k) { return JSON.stringify(k) + ":" + describe(v[k], depth + 1); }).join(",") + "}";
  }
  return "<" + Object.prototype.toString.call(v).slice(8, -1) + ">";
}
function shown(s) { return s.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, "�"); }
function copy(v) { return JSON.parse(JSON.stringify(v)); }
function sorted(v) {
  if (Array.isArray(v)) return v.map(sorted);
  if (v && typeof v === "object") return Object.keys(v).sort().reduce(function (o, k) { o[k] = sorted(v[k]); return o; }, {});
  return v;
}
var failures = [];
var quiet = console.error;
console.error = function () {}; // the kernel reports a failing expression; the corpus says "throws"
var handed = 0;
CORPUS.cases.forEach(function (c, i) {
  var h = HANDOFF[i];
  if (h) {
    var back;
    try { back = describe(kit.run(kit.compile(h.literal), {})); } catch (e) { back = "throws"; }
    if (back !== h.want) failures.push(c.group + ": " + c.src + " is handed on as " + h.literal + ", which reads back as " + back + ", want " + h.want);
    handed++;
  }
  // host data reaches the browser as literal() writes it, an object's keys sorted
  var scope = sorted(Object.assign(copy(CORPUS.scope), c.scope ? copy(c.scope) : {}));
  var got, v, failed = false;
  try { v = kit.run(kit.compile(c.src.trim()), scope); got = describe(v); } catch (e) { failed = true; got = "throws"; }
  if (got !== c.want) { failures.push(c.group + ": " + c.src + " = " + got + ", want " + c.want); return; }
  if (failed) return;
  var text;
  try { text = shown(v == null ? "" : String(v)); } catch (e) { text = ""; }
  var want = c.text !== undefined ? c.text : JSON.parse(c.want);
  if (text !== want) failures.push(c.group + ": " + c.src + " prints " + JSON.stringify(text) + ", want " + JSON.stringify(want));
});
console.error = quiet;
if (failures.length) { console.error(failures.slice(0, 25).join("\n") + "\n" + failures.length + " of " + CORPUS.cases.length + " differ in the kernel"); process.exitCode = 1; }
else console.log("parity corpus: " + CORPUS.cases.length + " cases, the kernel agrees; " + handed + " values handed on read back alike");
`
	script := conformanceSetup + "\n" + composedRuntime() + "\n" + assertions
	path := filepath.Join(t.TempDir(), "parity.test.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("client parity failed:\n%s", out)
	}
	t.Logf("%s", out)
}
