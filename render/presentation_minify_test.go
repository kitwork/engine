//go:build !stdminify

package render

import (
	"strings"
	"testing"

	hydrate "github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/value"
)

func TestPrepareMinifiesStaticPresentationOnce(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "index.kitwork.html", `<html>
		<head></head>
		<body>{{ @page }}</body>
	</html>`)
	mkfile(t, root, "page.kitwork.html", `<main class="p-4">
		{{ message }}
	</main>`)

	config := Config{
		Base:          root,
		Directory:     ".",
		JitCSS:        true,
		DefaultMinify: true,
	}
	prepared := New(config).Prepare()
	if err := prepared.PreparationError(); err != nil {
		t.Fatal(err)
	}
	if !prepared.presentationPrepared || !prepared.minifyPrepared {
		t.Fatal("static presentation was not minified with the generation")
	}

	data := value.New(map[string]any{"message": "Hello   Kitwork"})
	preparedOutput := prepared.Bind(data).String()
	if !strings.Contains(preparedOutput, "Hello   Kitwork") {
		t.Fatalf("request data was modified by presentation minification:\n%s", preparedOutput)
	}
	if strings.Contains(preparedOutput, "\n") {
		t.Fatalf("static template whitespace was not prepared:\n%s", preparedOutput)
	}
}

func TestPrepareMinifiesHydrateOperatorsWithDOMEquivalentEntities(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "index.kitwork.html", `<html><head></head><body>{{ @page }}</body></html>`)
	mkfile(t, root, "page.kitwork.html", `<main data-kit-hydrate="v1"><input type="number" data-kit-model="count" value="1"><b data-kit-text="count &gt; 0 ? 'visible' : 'hidden'">pending</b></main>`)

	config := Config{
		Base:          root,
		Directory:     ".",
		DefaultMinify: true,
	}
	prepared := New(config).Prepare()
	if err := prepared.PreparationError(); err != nil {
		t.Fatal(err)
	}
	if !prepared.minifyPrepared {
		t.Fatal("hydrate template did not use generation minification")
	}
	preparedOutput := prepared.Bind(value.New(map[string]any{})).String()
	// The page is sent as written: the server no longer fills in page-scope answers (router.javascript's
	// prerender went on 11/10/2026), so the browser is the one that evaluates the expression.
	if !strings.Contains(preparedOutput, ">pending</b>") {
		t.Fatalf("the server must send the author's content, not an answer: %s", preparedOutput)
	}
	// What the minifier left in the attribute must still read, once the DOM decodes its entities, as
	// the comparison the author wrote. The Go twin of the client's walker decodes and evaluates exactly
	// as the browser does, so it is the witness: count > 0 with count 1 is visible.
	if evaluated := hydrate.PreRender(preparedOutput); !strings.Contains(evaluated, ">visible</b>") {
		t.Fatalf("the minified comparison no longer evaluates as written: %s", preparedOutput)
	}
}
