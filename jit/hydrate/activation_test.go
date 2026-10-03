package hydrate

import (
	"io"
	"os"
	"strings"
	"testing"
)

// What makes a page a Kitwork page? The markup itself. A page that USES the grammar is recognised
// with no marker attribute; a page that merely PRINTS the grammar — a documentation page showing an
// example inside <pre> or <code> — is left byte-for-byte alone.
//
// This replaces the root marker (data-kit-app / data-kit-hydrate), which asked every author to say
// twice what the markup already said once. Measured across this repo: 157 pages said it once and
// the engine never looked at them, while exactly 3 files depend on the example/usage distinction.
func TestRenderRecognisesAPageByItsMarkup(t *testing.T) {
	cases := []struct {
		name      string
		html      string
		recognise bool
	}{
		{
			name:      "a real directive, no marker anywhere",
			html:      `<head></head><body><b data-kit-text="title">t</b></body>`,
			recognise: true,
		},
		{
			name: "only an example inside <pre>",
			html: `<head></head><body><pre>data-kit-click="n = n + 1"</pre></body>`,
		},
		{
			name: "only an example inside <code>",
			html: `<head></head><body><p>Write <code>data-kit-show="open"</code> on it.</p></body>`,
		},
		{
			name:      "an example AND a real one",
			html:      `<head></head><body><pre>data-kit-click="x"</pre><b data-kit-text="title">t</b></body>`,
			recognise: true,
		},
		{
			name: "no grammar at all",
			html: `<head></head><body><div class="card">hello</div></body>`,
		},
		{
			name: "a data- attribute that only looks like one",
			html: `<head></head><body><div data-kitten="fluffy"></div></body>`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := UsesGrammar(c.html); got != c.recognise {
				t.Fatalf("recognised=%t, want %t — page was %s", got, c.recognise, c.html)
			}
		})
	}
}

// The marker is not merely optional now — it is not read at all. A page carrying one behaves exactly
// like the same page without it, so nothing hinges on remembering to write it.
func TestRootMarkerNoLongerDecidesAnything(t *testing.T) {
	const body = `<b data-kit-text="title">t</b>`
	if !UsesGrammar(`<head></head><body>`+body+`</body>`) || !UsesGrammar(`<head></head><body data-kit-hydrate="v1">`+body+`</body>`) {
		t.Fatal("a page with a directive is recognised, marker or not")
	}
	if UsesGrammar(`<head></head><body data-kit-hydrate="v1"><div>hello</div></body>`) {
		t.Fatal("a marker alone must not make a page a Kitwork page")
	}
}

// Diagnostics are for a person at a keyboard: on while developing, off in production, where the
// scan is not free and nobody reads the log. The proof WATCHES stdout, because "printed nothing" is
// the whole claim.
func TestDiagnosticsAreDevelopmentOnly(t *testing.T) {
	const page = `<head></head><body><b data-kit-text="nameNobodyDeclares">t</b></body>`
	previous := Diagnostics
	defer func() { Diagnostics = previous }()

	check := func(on bool) string {
		t.Helper()
		Diagnostics = on
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout := os.Stdout
		os.Stdout = write
		Check(page)
		os.Stdout = stdout
		_ = write.Close()
		printed, _ := io.ReadAll(read)
		return string(printed)
	}

	quietLog := check(false)
	loudLog := check(true)

	if quietLog != "" {
		t.Fatalf("production must print nothing, got: %s", quietLog)
	}
	if !strings.Contains(loudLog, "[hydrate]") || !strings.Contains(loudLog, "nameNobodyDeclares") {
		t.Fatalf("development must name the mistake, got: %s", loudLog)
	}
}
