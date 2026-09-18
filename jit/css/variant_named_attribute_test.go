package css

import (
	"strings"
	"testing"
)

// Tailwind's name suffix on the attribute variants: `group-data-[state=selected]/item:` styles a
// child by ONE particular marked ancestor's data-state, the way `group-hover/item:` already did
// by its hover. The tree view's demo leaned on it and got nothing — the named form matched no
// rule, so the class silently produced no CSS.
func TestNamedGroupAttributeVariants(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<li class="group/item"><span class="group-data-[state=selected]/item:bg-brand-wash group-data-[open]/item:font-semibold group-aria-expanded/item:rotate-90 group-aria-[level=2]/item:pl-4 group-has-[:focus]/item:ring-2 peer-data-[state=checked]/row:opacity-50 peer-aria-checked/row:underline peer-has-[:checked]/row:hidden"></span></li>`, &cfg)
	for _, want := range []string{
		`.group\/item[data-state="selected"] .group-data-\[state\=selected\]\/item\:bg-brand-wash {`,
		`.group\/item[data-open] .group-data-\[open\]\/item\:font-semibold {`,
		`.group\/item[aria-expanded="true"] .group-aria-expanded\/item\:rotate-90 {`,
		`.group\/item[aria-level="2"] .group-aria-\[level\=2\]\/item\:pl-4 {`,
		`.group\/item:has(:focus) .group-has-\[\:focus\]\/item\:ring-2 {`,
		`.peer\/row[data-state="checked"] ~ .peer-data-\[state\=checked\]\/row\:opacity-50 {`,
		`.peer\/row[aria-checked="true"] ~ .peer-aria-checked\/row\:underline {`,
		`.peer\/row:has(:checked) ~ .peer-has-\[\:checked\]\/row\:hidden {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin:\n%s", want, out)
		}
	}
}

// CONTROLS: the unnamed forms keep their selectors, peer-data- (new) sits beside the group
// form, and a slash INSIDE the bracket is part of the value, not a group name.
func TestNamedGroupAttributeVariantControls(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<span class="group-data-[state=open]:rotate-180 data-[state=open]:bg-brand peer-data-[state=checked]:opacity-50 group-aria-expanded:rotate-180 data-[href=/docs]:underline"></span>`, &cfg)
	for _, want := range []string{
		`.group[data-state="open"] .group-data-\[state\=open\]\:rotate-180 {`,
		`.data-\[state\=open\]\:bg-brand[data-state="open"] {`,
		`.peer[data-state="checked"] ~ .peer-data-\[state\=checked\]\:opacity-50 {`,
		`.group[aria-expanded="true"] .group-aria-expanded\:rotate-180 {`,
		`[data-href="/docs"] {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin:\n%s", want, out)
		}
	}
	if strings.Contains(out, `.group\/docs`) || strings.Contains(out, `.peer\/docs`) {
		t.Errorf("a slash inside the bracket was taken for a group name:\n%s", out)
	}
}
