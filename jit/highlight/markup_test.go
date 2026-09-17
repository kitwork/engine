package highlight

import (
	"strings"
	"testing"
)

// HTML is what a Kitwork page is made of, and the component catalogue prints
// it. The roles are the page's own reading: tag = structure, attribute =
// property, quoted value = string, and a data-kit-* directive = the behaviour
// layer, which takes the command role so the site chooses what glows there.
func TestMarkupColorsTagsAttributesValuesAndDirectives(t *testing.T) {
	out := Code(`<button type="button" data-kit-click="toggle()" class='x'>Save</button>`, "html", tokenClasses)

	for _, want := range []string{
		`<span class="text-terminal-punctuation">&lt;</span><span class="text-terminal-keyword font-bold">button</span>`,
		`<span class="text-terminal-property">type</span><span class="text-terminal-punctuation">=</span><span class="text-terminal-string">&#34;button&#34;</span>`,
		`<span class="text-terminal-command font-bold">data-kit-click</span>`,
		`<span class="text-terminal-string">&#39;x&#39;</span>`,
		`<span class="text-terminal-text">Save</span>`,
		`<span class="text-terminal-punctuation">&lt;/</span><span class="text-terminal-keyword font-bold">button</span><span class="text-terminal-punctuation">&gt;</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s\ngot: %s", want, out)
		}
	}
	if strings.Contains(out, `<span class="text-terminal-property">data-kit-click</span>`) {
		t.Fatalf("a directive was coloured as an ordinary attribute.\ngot: %s", out)
	}
}

func TestMarkupKeepsWhitespaceCommentsAndSelfClosingTags(t *testing.T) {
	source := "<div>\n  <!-- note -->\n  <input disabled />\n</div>"
	out := Code(source, "html", tokenClasses)

	if !strings.Contains(out, "\n  ") {
		t.Fatalf("indentation did not survive.\ngot: %s", out)
	}
	if !strings.Contains(out, `<span class="text-terminal-comment">&lt;!-- note --&gt;</span>`) {
		t.Fatalf("the comment was not coloured as a comment.\ngot: %s", out)
	}
	if !strings.Contains(out, `<span class="text-terminal-property">disabled</span> <span class="text-terminal-punctuation">/&gt;</span>`) {
		t.Fatalf("a bare attribute before a self-closing tag came apart.\ngot: %s", out)
	}
}

// The catalogue prints a captured demo through a slot: the page escapes the
// markup into the slot, Render unescapes it, colours it, and escapes every run
// on the way out — so the demo's own tags can never become the page's.
func TestRenderFillsAnHTMLSlotWithoutLeakingTags(t *testing.T) {
	source := `<pre><code data-kit-highlight="html">&lt;div data-kit-show=&#34;open&#34;&gt;&lt;img onerror=x&gt;&lt;/div&gt;</code></pre>`
	out := Render(source, "", nil)

	if stray := strayAngleBracket(out); stray != "" {
		t.Fatalf("source text leaked an unescaped '<' at %q.\ngot: %s", stray, out)
	}
	if !strings.Contains(out, `<span class="text-terminal-command font-bold">data-kit-show</span>`) {
		t.Fatalf("the directive inside the slot was not coloured.\ngot: %s", out)
	}
	if !strings.Contains(out, `<span class="text-terminal-property">onerror</span>`) {
		t.Fatalf("the smuggled handler was not printed as an inert attribute name.\ngot: %s", out)
	}
	if Render(out, "", nil) != out {
		t.Fatal("a second pass over the filled slot changed it")
	}
}
