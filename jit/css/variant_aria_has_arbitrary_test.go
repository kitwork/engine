package css

import (
	"strings"
	"testing"
)

// Three Tailwind variants the material recipes lean on — a toggle styled by its
// aria-pressed, a chosen radio card by :has(), a range thumb by an arbitrary
// pseudo-element selector. Before these existed the class produced no CSS and
// the demo looked unstyled without an error anywhere.
func TestAriaVariantStylesByAttribute(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<button class="aria-pressed:bg-brand aria-[sort=ascending]:underline group-aria-expanded:rotate-180 peer-aria-checked:opacity-50"></button>`, &cfg)
	for _, want := range []string{
		`.aria-pressed\:bg-brand[aria-pressed="true"]`,
		`.aria-\[sort\=ascending\]\:underline[aria-sort="ascending"]`,
		`.group[aria-expanded="true"] .group-aria-expanded\:rotate-180`,
		`.peer[aria-checked="true"] ~ .peer-aria-checked\:opacity-50`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin:\n%s", want, out)
		}
	}
}

func TestHasVariantStylesByDescendant(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<label class="has-[:checked]:border-brand group-has-[>_img]:pt-0 peer-has-[:focus]:ring-2"></label>`, &cfg)
	for _, want := range []string{
		`.has-\[\:checked\]\:border-brand:has(:checked)`,
		`.group:has(> img) .group-has-\[\>_img\]\:pt-0`,
		`.peer:has(:focus) ~ .peer-has-\[\:focus\]\:ring-2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin:\n%s", want, out)
		}
	}
}

func TestArbitraryVariantSubstitutesTheElement(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<input class="[&::-webkit-slider-thumb]:h-5 [&>*]:mt-2 [&_p]:leading-7 hover:[&::before]:opacity-100">`, &cfg)
	for _, want := range []string{
		`.\[\&\:\:-webkit-slider-thumb\]\:h-5::-webkit-slider-thumb { height: 1.25rem; }`,
		`.\[\&\>\*\]\:mt-2>* {`,
		`.\[\&_p\]\:leading-7 p {`,
		`.hover\:\[\&\:\:before\]\:opacity-100:hover::before {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin:\n%s", want, out)
		}
	}
}
