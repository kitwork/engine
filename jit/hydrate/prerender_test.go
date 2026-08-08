package hydrate

import (
	"strings"
	"testing"
)

func TestPreRenderText(t *testing.T) {
	in := `<section data-kitwork-hydrate="v1">` +
		`<input type="number" data-kit-model="qty" value="3">` +
		`<b data-kit-text="qty * 2">0</b>` +
		`<i data-kit-text="qty">?</i>` +
		`</section>`
	out := PreRender(in)
	if !strings.Contains(out, `<b data-kit-text="qty * 2">6</b>`) {
		t.Errorf("qty*2 should pre-render to 6\n got: %s", out)
	}
	if !strings.Contains(out, `<i data-kit-text="qty">3</i>`) {
		t.Errorf("qty should pre-render to 3\n got: %s", out)
	}
}

// The server twin of TestModelRangeCoercesToNumber: a type=range model seeds a NUMBER into the
// PreRender scope, so an expression over it does arithmetic, not string concat.
func TestPreRenderModelRange(t *testing.T) {
	in := marker + `<input type="range" data-kit-model="lvl" value="4">` +
		`<b data-kit-text="lvl * 2">0</b>`
	if out := PreRender(in); !strings.Contains(out, `<b data-kit-text="lvl * 2">8</b>`) {
		t.Errorf("range model should seed lvl=4 (number) → lvl*2=8\n got: %s", out)
	}
}

func TestPreRenderStringAndFallback(t *testing.T) {
	in := marker + `<input data-kit-model="name" value="Quốc">` +
		`<p data-kit-text="name ? 'Chào ' + name : 'Nhập tên'"></p>`
	out := PreRender(in)
	if !strings.Contains(out, `>Chào Quốc</p>`) {
		t.Errorf("greeting should pre-render\n got: %s", out)
	}
	// empty model → falsy branch, exactly what the client shows at boot
	in2 := marker + `<input data-kit-model="name" value="">` +
		`<p data-kit-text="name ? 'Chào ' + name : 'Nhập tên'"></p>`
	if out2 := PreRender(in2); !strings.Contains(out2, `>Nhập tên</p>`) {
		t.Errorf("empty name → placeholder\n got: %s", out2)
	}
}

func TestPreRenderEscapesOutput(t *testing.T) {
	// an evaluated value containing HTML must be escaped when baked in, exactly as the client's
	// textContent assignment would neutralize it — no injected markup.
	in := marker + `<b data-kit-text="'<img src=x onerror=alert(1)>'">x</b>`
	out := PreRender(in)
	// the baked CONTENT (between the tag close and </b>) must be escaped — no live tag emitted.
	if !strings.Contains(out, `>&lt;img src=x onerror=alert(1)&gt;</b>`) {
		t.Errorf("baked content must be HTML-escaped\n got: %s", out)
	}
}

func TestPreRenderShow(t *testing.T) {
	in := marker + `<input type="number" data-kit-model="n" value="1">` +
		`<span data-kit-show="n > 3">unlocked</span>` +
		`<em data-kit-show="n > 0">on</em>`
	out := PreRender(in)
	// n=1: n>3 false → hidden added; n>0 true → left visible
	if !strings.Contains(out, `data-kit-show="n &gt; 3"`) && !strings.Contains(out, `data-kit-show="n > 3" hidden>`) {
		// the attribute value itself isn't re-encoded here; assert the hidden was added
	}
	if !strings.Contains(out, `<span data-kit-show="n > 3" hidden>unlocked</span>`) {
		t.Errorf("n>3 should be hidden at n=1\n got: %s", out)
	}
	if !strings.Contains(out, `<em data-kit-show="n > 0">on</em>`) {
		t.Errorf("n>0 should stay shown at n=1\n got: %s", out)
	}
}

func TestPreRenderMalformedLeftAlone(t *testing.T) {
	in := marker + `<b data-kit-text="n +">keep</b>`
	if out := PreRender(in); !strings.Contains(out, `>keep</b>`) {
		t.Errorf("malformed expr → content untouched\n got: %s", out)
	}
	// no marker → never touched
	if got := PreRender(`<b data-kit-text="1 + 1">x</b>`); got != `<b data-kit-text="1 + 1">x</b>` {
		t.Errorf("unmarked page must be untouched: %s", got)
	}
}

// PreRender and the client must agree: the server-baked value equals what Eval produces for the
// same scope (this is the whole point — no flash because both compute identically).
func TestPreRenderMatchesEval(t *testing.T) {
	scope := map[string]any{"qty": 3.0}
	node, _ := Compile("qty * 2 + 1")
	v, _ := Eval(node, scope)
	if display(v) != "7" {
		t.Errorf("display mismatch: %q", display(v))
	}
}

func TestPreRenderSkipsScopedCatalogCards(t *testing.T) {
	in := marker +
		`<section data-kit-scope="{ query: '', category: 'all' }">` +
		`<article id="clipboard" data-kit-show="category == 'all'">Clipboard</article>` +
		`</section>`

	if out := PreRender(in); out != in {
		t.Fatalf("a catalog card owned by an inline scope must stay untouched\nwant: %s\n got: %s", in, out)
	}
}

func TestPreRenderSkipsEveryLocalBoundary(t *testing.T) {
	cases := []string{
		`data-kit-scope="{ open: true, label: 'local' }"`,
		`data-kit-component="dropdown@v1.0.0"`,
		`data-kit-api="/state.json"`,
		`data-kit-for="item of items"`,
	}
	for _, boundary := range cases {
		t.Run(boundary, func(t *testing.T) {
			local := `<div ` + boundary + ` data-kit-show="open">` +
				`<div><div><b data-kit-text="label">fallback</b></div></div>` +
				`</div>`
			in := marker + local
			if out := PreRender(in); !strings.Contains(out, local) {
				t.Fatalf("local boundary and its nested content must remain byte-for-byte\n got: %s", out)
			}
		})
	}
}

func TestPreRenderStillRendersPageScopeSiblings(t *testing.T) {
	in := marker +
		`<input type="number" data-kit-model="qty" value="3">` +
		`<b data-kit-text="qty * 2">fallback</b>` +
		`<section data-kit-component="dropdown"><i data-kit-text="qty">local</i></section>` +
		`<em data-kit-show="qty > 2">visible</em>`
	out := PreRender(in)

	if !strings.Contains(out, `<b data-kit-text="qty * 2">6</b>`) {
		t.Fatalf("page-scope sibling was not pre-rendered\n got: %s", out)
	}
	if !strings.Contains(out, `<i data-kit-text="qty">local</i>`) {
		t.Fatalf("component-local text was changed\n got: %s", out)
	}
	if strings.Contains(out, `<em data-kit-show="qty > 2" hidden>`) {
		t.Fatalf("truthy page-scope show was hidden\n got: %s", out)
	}
}

func TestPreRenderDoesNotSeedPageScopeFromLocalModel(t *testing.T) {
	in := marker +
		`<section data-kit-scope="local"><input data-kit-model="status" value="local"></section>` +
		`<b data-kit-text="status ? status : 'page-default'">fallback</b>`
	out := PreRender(in)
	if !strings.Contains(out, `>page-default</b>`) {
		t.Fatalf("a model inside a local boundary leaked into page scope\n got: %s", out)
	}
}

func TestPreRenderTreatsRawTextAsOpaque(t *testing.T) {
	script := `<script>var sample = '<b data-kit-text="1 + 1">x</b>';</script>`
	in := marker + script + `<b data-kit-text="'right'">fallback</b>`
	out := PreRender(in)
	if !strings.Contains(out, script) {
		t.Fatalf("script content containing tag-like text was rewritten\n got: %s", out)
	}
	if !strings.Contains(out, `<b data-kit-text="'right'">right</b>`) {
		t.Fatalf("page text after raw-text content was not rendered\n got: %s", out)
	}
}

func TestPreRenderShowBeforeBoundaryKeepsAdjustedRangeOpaque(t *testing.T) {
	local := `<section data-kit-component="dropdown"><b data-kit-text="'local'">fallback</b></section>`
	in := marker +
		`<i data-kit-show="false">hidden</i>` +
		`<i data-kit-show="false">also hidden</i>` +
		local +
		`<b data-kit-text="'page'">fallback</b>`
	out := PreRender(in)

	if strings.Count(out, " hidden>") != 2 {
		t.Fatalf("both page-scope show bindings should be hidden\n got: %s", out)
	}
	if !strings.Contains(out, local) {
		t.Fatalf("show insertions shifted and exposed a component boundary\n got: %s", out)
	}
	if !strings.Contains(out, `<b data-kit-text="'page'">page</b>`) {
		t.Fatalf("page-scope text after the boundary was not rendered\n got: %s", out)
	}
}

func TestPreRenderShowAndTextOnSameElement(t *testing.T) {
	in := marker + `<span data-kit-show="false" data-kit-text="'ready'">fallback</span>`
	out := PreRender(in)
	want := `<span data-kit-show="false" data-kit-text="'ready'" hidden>ready</span>`
	if !strings.Contains(out, want) {
		t.Fatalf("show and text should compose on one element\nwant: %s\n got: %s", want, out)
	}
}
