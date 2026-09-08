package css

import (
	"strings"
	"testing"
)

// An arbitrary background IMAGE has to reach the stylesheet. Tailwind resolves
// `bg-[…]` by inspecting the value: a gradient or url() is an image, a colour is
// a colour. Only the colour half was implemented, so a gradient produced no rule
// at all — silently, with the class still sitting in the markup.
func TestArbitraryBackgroundImage(t *testing.T) {
	cfg := DefaultConfig
	for _, c := range []struct{ name, class, want string }{
		{
			"linear",
			`bg-[linear-gradient(145deg,rgba(20,23,32,0.96),rgba(8,10,15,0.97))]`,
			"background-image: linear-gradient(145deg,rgba(20,23,32,0.96),rgba(8,10,15,0.97));",
		},
		{
			// Underscores are Tailwind's stand-in for spaces inside an arbitrary value.
			"radial voi dau gach duoi",
			`bg-[radial-gradient(circle_at_54%_48%,rgba(248,34,68,0.1),transparent_58%)]`,
			"background-image: radial-gradient(circle at 54% 48%,rgba(248,34,68,0.1),transparent 58%);",
		},
		{
			// The real class from kitwork.io's hero: two layers, commas at the top level.
			"nhieu lop",
			`bg-[radial-gradient(520px_420px_at_54%_48%,rgba(248,34,68,0.105),transparent_58%),linear-gradient(145deg,rgba(20,23,32,0.96),rgba(8,10,15,0.97))]`,
			"linear-gradient(145deg,rgba(20,23,32,0.96),rgba(8,10,15,0.97));",
		},
		{"url", `bg-[url(/assets/grid.svg)]`, "background-image: url(/assets/grid.svg);"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := GenerateJIT(`<div class="`+c.class+`"></div>`, &cfg)
			if !strings.Contains(out, c.want) {
				t.Fatalf("khong sinh %q\nCSS:\n%s", c.want, out)
			}
		})
	}
}

// The colour half must keep working, and must stay a COLOUR. Without this the
// fix could pass by claiming every bg-[…] and painting hexes as images.
func TestArbitraryBackgroundColourIsUntouched(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<div class="bg-[#080a0f] bg-[#080a0f]/85"></div>`, &cfg)
	if !strings.Contains(out, "background-color: #080a0f;") {
		t.Fatalf("mat background-color cho hex:\n%s", out)
	}
	if strings.Contains(out, "background-image: #080a0f") {
		t.Fatal("hex bi doc thanh anh nen")
	}
}

// A bare length is ambiguous (Tailwind needs a `length:`/`size:` hint), so it
// must NOT be guessed into an image. This is the control that stops the new
// rule from swallowing every remaining bg-[…] shape.
//
// Compared against the CSS generated for NO class, because the preflight reset
// already carries a `background-image: none` — matching the bare property name
// passes for the wrong reason.
func TestAmbiguousArbitraryBackgroundIsNotGuessed(t *testing.T) {
	cfg := DefaultConfig
	// The mock carries one harmless class so the preflight — which itself has a
	// `background-image: none` in the button reset — is present in BOTH sides.
	// An empty document generates no CSS at all, which made the subtraction
	// measure the preflight instead of the class under test.
	baseline := GenerateJIT(`<div class="relative"></div>`, &cfg)
	for _, class := range []string{`bg-[10px]`, `bg-[center]`, `bg-[--my-var]`} {
		out := GenerateJIT(`<div class="relative `+class+`"></div>`, &cfg)
		added := strings.Count(out, "background-image") - strings.Count(baseline, "background-image")
		if added != 0 {
			t.Fatalf("%s them %d luat anh nen, muon 0", class, added)
		}
	}
}
