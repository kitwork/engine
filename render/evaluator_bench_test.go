package render

import (
	"html"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

const evaluatorBenchmarkTemplate = `<section>
<h1>{{ user.name }}</h1>
{{ if user.active }}
<p>{{ user.tagline ? user.tagline : "Building quietly" }}</p>
{{ else }}
<p>Offline</p>
{{ end }}
<ol>
{{ for (index, item) in items }}
<li data-position="{{ index + 1 }}">
<h2>{{ item.title }}</h2>
{{ if item.score >= 10 }}
<strong>{{ item.score * 2 }}</strong>
{{ else }}
<span>{{ item.summary ?? "No summary" }}</span>
{{ end }}
</li>
{{ end }}
</ol>
<script type="application/ld+json">{{ raw(meta.jsonld) }}</script>
</section>`

var evaluatorBenchmarkResult string

func evaluatorFixture() (*node, value.Value, map[string]value.Value) {
	items := make([]any, 16)
	for index := range items {
		items[index] = map[string]any{
			"title":   "Item <" + value.New(index).String() + ">",
			"summary": "A measured evaluator fixture",
			"score":   index,
		}
	}
	data := value.New(map[string]any{
		"user": map[string]any{
			"name":    "Kitwork <VM>",
			"active":  true,
			"tagline": "Small runtime, explicit boundaries.",
		},
		"items": items,
		"meta": map[string]any{
			"jsonld": `{"name":"Kitwork"}`,
		},
	})
	scope := map[string]value.Value{"$": data}
	for key, item := range data.Map() {
		scope[key] = item
	}
	return parse(specializeTokens(evaluatorBenchmarkTemplate)), data, scope
}

func TestEvaluatorFixture(t *testing.T) {
	program, data, scope := evaluatorFixture()
	output := eval(program, data, scope)

	for _, expected := range []string{
		"Kitwork &lt;VM&gt;",
		"Small runtime, explicit boundaries.",
		`data-position="16"`,
		"Item &lt;15&gt;",
		`{"name":"Kitwork"}`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("evaluator output missing %q:\n%s", expected, output)
		}
	}
	if count := strings.Count(output, "<li "); count != 16 {
		t.Fatalf("rendered list items = %d, want 16", count)
	}
}

func TestWriteResolvedValueMatchesHTMLEscapeString(t *testing.T) {
	fixtures := []value.Value{
		value.New(`Kitwork & <VM> "runtime" 'engine'`),
		value.New(42),
		value.New(3.5),
		value.New(true),
		value.New(nil),
		value.New([]byte("<bytecode>")),
	}
	for _, fixture := range fixtures {
		t.Run(fixture.String(), func(t *testing.T) {
			var escaped strings.Builder
			writeResolvedValue(&escaped, fixture, true)
			if expected := html.EscapeString(fixture.String()); escaped.String() != expected {
				t.Fatalf("escaped value = %q, want %q", escaped.String(), expected)
			}

			var raw strings.Builder
			writeResolvedValue(&raw, fixture, false)
			if expected := fixture.String(); raw.String() != expected {
				t.Fatalf("raw value = %q, want %q", raw.String(), expected)
			}
		})
	}
}

func TestLoopScopeDoesNotLeakBetweenIterations(t *testing.T) {
	template := `{{ let label = user.name }}{{ for item in items }}` +
		`{{ if item.keep }}{{ let label = item.name }}{{ end }}` +
		`[{{ label }}]{{ end }}|{{ label }}`
	data := map[string]any{
		"user": map[string]any{"name": "Root"},
		"items": []any{
			map[string]any{"name": "First", "keep": true},
			map[string]any{"name": "Second", "keep": false},
		},
	}
	if output := engineRender(template, data, "", ""); output != "[First][Root]|Root" {
		t.Fatalf("loop scope output = %q, want %q", output, "[First][Root]|Root")
	}
}

func TestPreparedEvaluatorIsDeterministic(t *testing.T) {
	program, data, scope := evaluatorFixture()
	expected := eval(program, data, scope)
	for iteration := 0; iteration < 100; iteration++ {
		if output := eval(program, data, scope); output != expected {
			t.Fatalf("iteration %d changed evaluator output", iteration)
		}
	}
}

func BenchmarkTemplateEvaluator(b *testing.B) {
	program, data, scope := evaluatorFixture()
	evaluatorBenchmarkResult = eval(program, data, scope)

	b.ReportAllocs()
	b.SetBytes(int64(len(evaluatorBenchmarkResult)))
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		evaluatorBenchmarkResult = eval(program, data, scope)
	}
}

func TestTemplateEvaluatorAllocationBudget(t *testing.T) {
	program, data, scope := evaluatorFixture()
	allocations := testing.AllocsPerRun(100, func() {
		evaluatorBenchmarkResult = eval(program, data, scope)
	})
	if allocations > 12 {
		t.Fatalf("allocations/run = %.2f, budget is 12", allocations)
	}
}
