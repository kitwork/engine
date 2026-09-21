package hydrate

import (
	"strings"
	"testing"
)

// data-kit-seed on the server: the page scope PreRender bakes from includes what the seeds say —
// leaf text, a JSON island, a numeric input's value, a reflected boolean, a dotted path, list[] in
// document order — so text/show/bind read the same state the client will seed. A seed inside a local
// boundary is the client's; the DOM's word wins over a data-kit-model value for the same key.
func TestPreRenderReadsSeeds(t *testing.T) {
	in := marker +
		`<h1 data-kit-seed="title">  Rendered by the server </h1>` +
		`<input value="ada@example.com" data-kit-seed:value="user.email">` +
		`<input type="number" value="36" data-kit-seed:value="count">` +
		`<details open data-kit-seed:open="expanded"><summary>more</summary></details>` +
		`<button aria-busy="true" data-kit-seed:aria-busy="busy" data-kit-seed:data-missing="user.missing">go</button>` +
		`<ul><li data-kit-seed="tags[]">go</li><li data-kit-seed="tags[]">sqlite</li></ul>` +
		`<script type="application/json" data-kit-seed="items">[{"id": 1, "name": "Ada"}, {"id": 2, "name": "Bob"}]</script>` +
		`<output data-kit-text="title + '|' + user.email + '|' + count + '|' + expanded + '|' + busy + '|' + (user.missing == null ? 'none' : 'set') + '|' + tags.length + '|' + items.length">flash</output>` +
		`<p data-kit-show="!expanded">shown when closed</p>` +
		`</section>`
	out := PreRender(in)
	want := `>Rendered by the server|ada@example.com|36|true|true|none|2|2<`
	if !strings.Contains(out, want) {
		t.Fatalf("PreRender should bake from the seeded scope\nwant %s\n got: %s", want, out)
	}
	if !strings.Contains(out, `<p data-kit-show="!expanded" hidden>`) {
		t.Fatalf("a show that is false by the seeded state should be hidden at first paint\n got: %s", out)
	}
	if !strings.Contains(out, `<h1 data-kit-seed="title">  Rendered by the server </h1>`) {
		t.Fatal("a seed element without a binding must ride unchanged")
	}
}

func TestPreRenderSeedOverridesModelAndStaysOutOfBoundaries(t *testing.T) {
	in := marker +
		`<input data-kit-model="q" value="from model">` +
		`<span data-kit-seed="q">from seed</span>` +
		`<b data-kit-text="q">flash</b>` +
		`<div data-kit-scope="{ inner: 'literal' }"><i data-kit-seed="inner">client</i><em data-kit-text="inner">x</em></div>` +
		`</section>`
	out := PreRender(in)
	if !strings.Contains(out, `<b data-kit-text="q">from seed</b>`) {
		t.Fatalf("the DOM's seed should win over the model's value\n got: %s", out)
	}
	if !strings.Contains(out, `<em data-kit-text="inner">x</em>`) {
		t.Fatalf("a seed inside a local boundary is the client's; the server must not bake from it\n got: %s", out)
	}
}
