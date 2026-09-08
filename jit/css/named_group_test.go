package css

import (
	"strings"
	"testing"
)

// Tailwind lets groups nest by naming them (`group/item`), so a child can react to one particular
// marked ancestor. The engine had no rule for the named prefix, so `group-hover/item:text-brand`
// matched nothing and vanished — the child simply never reacted, with no error to notice.
func TestNamedGroupAndPeerVariants(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["brand"] = Hex("#635bff")

	cases := map[string]string{
		"group-hover/item:text-brand":       `.group\/item:hover `,
		"group-hover/btn:translate-x-0.5":   `.group\/btn:hover `,
		"group-focus-within/row:text-brand": `.group\/row:focus-within `,
		"peer-checked/row:bg-brand":         `.peer\/row:checked ~ `,
	}
	for cls, want := range cases {
		css, sel, _ := ResolveCore(cls, &cfg)
		if css == "" {
			t.Errorf("%s produced no CSS", cls)
			continue
		}
		if !strings.Contains(sel, want) {
			t.Errorf("%s selector = %q, want it to contain %q", cls, sel, want)
		}
	}

	// CONTROL 1: the UNNAMED forms must keep working exactly as before.
	if _, sel, _ := ResolveCore("group-hover:text-brand", &cfg); !strings.Contains(sel, ".group:hover ") {
		t.Errorf("unnamed group-hover regressed: %q", sel)
	}
	// CONTROL 2: group-focus-within (unnamed) was simply missing from the state table.
	if _, sel, _ := ResolveCore("group-focus-within:text-brand", &cfg); !strings.Contains(sel, ".group:focus-within ") {
		t.Errorf("group-focus-within got %q", sel)
	}
	// CONTROL 3: a slash that is NOT a group name — an alpha modifier — must not be mistaken for one.
	if css, _, _ := ResolveCore("text-brand/50", &cfg); !strings.Contains(css, "rgba(") {
		t.Errorf("alpha modifier broken by the named-variant rule: %q", css)
	}
}
