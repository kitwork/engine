package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The JavaScript corpus (27/10/2026, step 0 of the plan in docs/KITWORK_LANG.md section 3: ".work theo
// JavaScript đúng từng chữ"). jit/hydrate/parity_corpus.json holds ~4000 expressions over every kind of
// value, every operator and every member the kernel reaches; testdata/javascript_corpus/answers.json
// holds what JavaScript itself answers for each, run by generate.js in node as a strict module runs it.
// This runs every case on the VM as `const result = (src);` and puts it in one of three classes:
//
//   - match: the VM answers as JavaScript does (both reject it, at compile or run time, included);
//   - compile: the VM rejects it at compile time where JavaScript gives a value, which the rule allows
//     ("nghĩa của JS, hoặc lỗi biên dịch"), whether the subset keeps it out for good or not yet;
//   - differs: anything else, a value JavaScript does not give, a run-time failure where JavaScript
//     gives a value, or a value where JavaScript throws;
//   - unstable: the VM's answer changes from run to run. A .work object keeps no key order today, so
//     Object.keys and the like answer in the Go map's order, which is the order of insertion seven runs
//     in eight for a small map and another order the eighth: such a case may match one run and not the
//     next. Its class is found by running it corpusRuns times.
//
// testdata/javascript_corpus/ratchet.json records each case's class, and the classes only move up:
// differs or unstable to compile or match, compile to match. A case that moves down fails the test; a
// case that moves up fails it too, asking for the file to be rewritten, so the file is always what the
// VM does. A plain run runs each case once, and a case recorded unstable corpusRuns times (it moves up
// when it stops changing); the rewrite runs every case whose answer has more than one part corpusRuns
// times, which is how an unstable case is found:
//
//	KITWORK_JAVASCRIPT_CORPUS=update go test ./compiler -run TestJavaScriptCorpus
//
// Changing semantics on purpose is then a diff of that file. A case with no answer yet (the corpus grew
// since generate.js ran) is left out and counted.
//
// The test sets time.Local to UTC+7 while it runs, as generate.js does for node: not UTC, so a case that
// reads local time is held to JavaScript there too, the same on every machine.

type javascriptCorpusCase struct {
	Group string         `json:"group"`
	Src   string         `json:"src"`
	Scope map[string]any `json:"scope"`
}

const (
	corpusDiffers  = "differs"
	corpusUnstable = "unstable"
	corpusCompile  = "compile"
	corpusMatch    = "match"
)

var corpusRank = map[string]int{corpusDiffers: 0, corpusUnstable: 0, corpusCompile: 1, corpusMatch: 2}

// corpusRuns is how many times a case that may be unstable runs. A two-key object answers in the
// order of insertion seven runs in eight, so 200 runs all alike happen once in about 400 billion.
const corpusRuns = 200

func TestJavaScriptCorpus(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("UTC+7", 7*60*60)
	t.Cleanup(func() { time.Local = local })

	var corpus struct {
		Scope map[string]any         `json:"scope"`
		Cases []javascriptCorpusCase `json:"cases"`
	}
	readCorpusJSON(t, filepath.Join("..", "jit", "hydrate", "parity_corpus.json"), &corpus)
	dir := filepath.Join("testdata", "javascript_corpus")
	answers := map[string]string{}
	readCorpusJSON(t, filepath.Join(dir, "answers.json"), &answers)
	recorded := map[string]string{}
	ratchetPath := filepath.Join(dir, "ratchet.json")
	readCorpusJSON(t, ratchetPath, &recorded)

	update := os.Getenv("KITWORK_JAVASCRIPT_CORPUS") == "update"
	classes := map[string]string{}
	count := map[string]int{}
	unanswered := 0
	var down, up []string
	seen := map[string]int{}
	for _, c := range corpus.Cases {
		key := javascriptCorpusKey(seen, c)
		js, ok := answers[key]
		if !ok {
			unanswered++
			continue
		}
		runs := 1
		if update || recorded[key] == corpusUnstable {
			runs = corpusRuns
		}
		class, got := classifyJavaScriptCorpusCase(corpus.Scope, c, js, runs)
		classes[key] = class
		count[class]++

		was, ok := recorded[key]
		line := fmt.Sprintf("%s: %s (was %s), Kitwork %s, JavaScript %s", key, class, was, clipCorpus(got), clipCorpus(js))
		switch {
		case !ok:
			up = append(up, fmt.Sprintf("%s: %s (new), Kitwork %s, JavaScript %s", key, class, clipCorpus(got), clipCorpus(js)))
		case corpusRank[class] < corpusRank[was]:
			down = append(down, line)
		case corpusRank[class] > corpusRank[was]:
			up = append(up, line)
		}
	}
	gone := 0
	for key := range recorded {
		if _, ok := classes[key]; !ok {
			gone++
		}
	}

	t.Logf("JavaScript corpus: %d match, %d compile, %d differ, %d unstable, of %d answered; %d without an answer yet",
		count[corpusMatch], count[corpusCompile], count[corpusDiffers], count[corpusUnstable], len(classes), unanswered)

	if update {
		for _, line := range down {
			t.Logf("moved down: %s", line)
		}
		writeCorpusJSON(t, ratchetPath, classes)
		t.Logf("%s rewritten: %d moved down, %d moved up or new, %d no longer in the corpus", ratchetPath, len(down), len(up), gone)
		return
	}
	for i, line := range down {
		if i == 25 {
			t.Errorf("… and %d more", len(down)-25)
			break
		}
		t.Errorf("moved down: %s", line)
	}
	if len(up) > 0 || gone > 0 {
		for i, line := range up {
			if i == 10 {
				t.Logf("… and %d more", len(up)-10)
				break
			}
			t.Logf("moved up: %s", line)
		}
		t.Errorf("%s is behind the VM: %d cases moved up or are new, %d are no longer in the corpus; record them with KITWORK_JAVASCRIPT_CORPUS=update", ratchetPath, len(up), gone)
	}
}

// javascriptCorpusKey names a case as generate.js does: group and source, and #2, #3… for a later case
// with both alike (the corpus runs one source over scopes of different sizes).
func javascriptCorpusKey(seen map[string]int, c javascriptCorpusCase) string {
	base := c.Group + " | " + c.Src
	seen[base]++
	if n := seen[base]; n > 1 {
		return fmt.Sprintf("%s #%d", base, n)
	}
	return base
}

// classifyJavaScriptCorpusCase compiles one case as `const result = (src);` and puts it in its class
// against js, JavaScript's answer; got is the VM's answer ("throws" for a rejection). With runs above
// one, a case whose answer has more than one part (a comma in it, where an order can change) runs that
// many times on fresh VMs, and two different answers make it unstable.
func classifyJavaScriptCorpusCase(scope map[string]any, c javascriptCorpusCase, js string, runs int) (class, got string) {
	bytecode, err := CompileSource("const result = (" + strings.TrimSpace(c.Src) + ");")
	if err != nil {
		if js == "throws" {
			return corpusMatch, "throws"
		}
		return corpusCompile, "throws"
	}
	got = runJavaScriptCorpusProgram(bytecode, scope, c)
	if strings.Contains(got, ",") {
		for run := 1; run < runs; run++ {
			if again := runJavaScriptCorpusProgram(bytecode, scope, c); again != got {
				return corpusUnstable, got + " / " + again
			}
		}
	}
	if got == js {
		return corpusMatch, got
	}
	return corpusDiffers, got
}

// runJavaScriptCorpusProgram runs a compiled case on a fresh VM whose globals are the language's own and
// the corpus's scope, the case's over it, and describes result as generate.js does; a run-time failure
// is "throws".
func runJavaScriptCorpusProgram(bytecode *Bytecode, scope map[string]any, c javascriptCorpusCase) string {
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	builtins.InjectJSCompat(vm.Globals)
	for name, v := range scope {
		vm.Globals[name] = value.New(copyCorpusJSON(v))
	}
	for name, v := range c.Scope {
		vm.Globals[name] = value.New(copyCorpusJSON(v))
	}
	var returned value.Value
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		returned = vm.Run()
	}()
	if panicked {
		return "panic" // an engine fault is never JavaScript's answer
	}
	if returned.IsInvalid() {
		return "throws"
	}
	result := vm.Vars["result"]
	if result.Failed() {
		return "throws"
	}
	return describeJavaScriptCorpus(result, 0)
}

// describeJavaScriptCorpus writes a .work value in the corpus's canonical form (describeParity of
// jit/hydrate): a number as JavaScript writes it with -0 and NaN by name, a string as JSON.stringify
// quotes it, a list its items, an object its keys, a lambda fn: (it keeps no source, JavaScript's has
// it), anything else <kind>. A .work object keeps no key order today, so its keys are sorted here; once
// objects keep their order (step 6 of the plan) this writes them in it.
func describeJavaScriptCorpus(v value.Value, depth int) string {
	if depth > 12 {
		return "<deep>"
	}
	switch v.K {
	case value.Nil:
		return "null"
	case value.Bool:
		if v.N > 0 {
			return "true"
		}
		return "false"
	case value.Number:
		switch {
		case v.N != v.N:
			return "NaN"
		case v.N == 0 && math.Signbit(v.N):
			return "-0"
		}
		return value.NumberText(v.N)
	case value.String:
		s, _ := v.AsString()
		return quoteCorpus(s)
	case value.Array:
		items := v.Array()
		parts := make([]string, len(items))
		for i, item := range items {
			parts[i] = describeJavaScriptCorpus(item, depth+1)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case value.Map:
		m := v.Map()
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = quoteCorpus(k) + ":" + describeJavaScriptCorpus(m[k], depth+1)
		}
		return "{" + strings.Join(parts, ",") + "}"
	case value.Func:
		return "fn:"
	}
	return "<" + v.K.String() + ">"
}

// quoteCorpus is JSON.stringify of a string: the quote, the backslash and the control characters
// escaped, nothing else.
func quoteCorpus(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
	return b.String()
}

func copyCorpusJSON(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}

func clipCorpus(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

func readCorpusJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// writeCorpusJSON writes the classes one case a line, sorted, so a change of semantics reads as a diff.
func writeCorpusJSON(t *testing.T, path string, classes map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", " ")
	if err := encoder.Encode(classes); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
