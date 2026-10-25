package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// A Raw-marked value is trusted, already-safe HTML: it is emitted verbatim even when the template
// would otherwise escape it — so an engine value like $.meta.jsonld needs no raw() wrapper.
func TestWriteResolvedValueRawSkipsEscape(t *testing.T) {
	raw := value.Trusted(`{"a":"<b>&"}`)

	var out strings.Builder
	writeResolvedValue(&out, raw, true) // escape requested, but Raw wins
	if got := out.String(); got != `{"a":"<b>&"}` {
		t.Fatalf("Raw value must be emitted verbatim, got: %s", got)
	}

	// Control: the same content WITHOUT the Raw flag is escaped as usual.
	var control strings.Builder
	writeResolvedValue(&control, value.New(`<b>&`), true)
	if !strings.Contains(control.String(), "&lt;") {
		t.Fatalf("non-Raw value should be escaped, got: %s", control.String())
	}
}

// Raw does not propagate through a method call (value.go, the Raw field): highlight() marks its
// result trusted, and any String method on that result gives an ordinary string the page escapes.
// Until 25/10/2026 replace() and replaceAll() kept the mark, so a replacement taken from the
// request was written into the page as markup, and int() kept the highlighted text in V, so the
// page printed that text in place of the number. CONTROL: highlight() alone still prints verbatim.
func TestRawDoesNotSurviveAMethodCall(t *testing.T) {
	base := t.TempDir()
	shell := "<main>{{ capture src }}<a>x</a>{{ end }}" +
		`<p id="replace">{{ src.highlight().replace("x", evil) }}</p>` +
		`<p id="all">{{ src.highlight().replaceAll("x", evil) }}</p>` +
		`<p id="int">{{ src.highlight().int() }}</p>` +
		`<p id="raw">{{ src.highlight() }}</p></main>`
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	evil := "<script>alert(1)</script>"
	out := newViews(base).Bind(value.New(map[string]any{"evil": evil})).String()

	paragraph := func(id string) string {
		t.Helper()
		open := `<p id="` + id + `">`
		start := strings.Index(out, open)
		if start < 0 {
			t.Fatalf("no <p id=%q> in:\n%s", id, out)
		}
		rest := out[start+len(open):]
		end := strings.Index(rest, "</p>")
		if end < 0 {
			t.Fatalf("<p id=%q> never closes in:\n%s", id, out)
		}
		return rest[:end]
	}

	for _, id := range []string{"replace", "all"} {
		got := paragraph(id)
		if strings.Contains(got, evil) {
			t.Errorf("%s: the replacement reached the page as markup:\n%s", id, got)
		}
		if !strings.Contains(got, "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Errorf("%s: the replacement is not escaped:\n%s", id, got)
		}
	}
	if got := paragraph("int"); !regexp.MustCompile(`^-?[0-9]+$`).MatchString(got) {
		t.Errorf("int: want a number only, got:\n%s", got)
	}
	// CONTROL: the highlighted source alone is Raw and prints its spans verbatim
	if got := paragraph("raw"); !strings.Contains(got, `<span class="`) || strings.Contains(got, "&lt;span") {
		t.Errorf("raw: highlight() alone must print verbatim:\n%s", got)
	}
}
