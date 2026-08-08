//go:build !stdminify

package render

import (
	"strings"
	"testing"

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
	if !strings.Contains(preparedOutput, ">visible</b>") {
		t.Fatalf("encoded comparison was not pre-rendered: %s", preparedOutput)
	}
}
