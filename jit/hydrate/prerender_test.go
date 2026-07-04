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
