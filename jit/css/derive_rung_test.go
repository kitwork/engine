package css

import (
	"strings"
	"testing"
)

func rungCfg() Config {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["sage"] = Hex("#0e9f6e")       // flat token, no rungs spelled out
	cfg.Colors["brand"] = Hex("#635bff")      // base…
	cfg.Colors["brand-soft"] = Hex("#112233") // …with ONE rung hand-tuned
	return cfg
}

// A token given as a single flat colour used to answer only its own name: bg-sage-deep emitted
// nothing. The rungs are now grown on demand from the same OKLCH maths palette() uses.
func TestFlatTokenGrowsItsRungs(t *testing.T) {
	cfg := rungCfg()
	for _, cls := range []string{"bg-sage-deep", "bg-sage-soft", "bg-sage-wash", "text-sage-on"} {
		css, _, _ := ResolveCore(cls, &cfg)
		if css == "" {
			t.Errorf("%s should derive from the flat token, got nothing", cls)
		}
	}
	// The base name is untouched and stays themeable.
	if css, _, _ := ResolveCore("bg-sage", &cfg); !strings.Contains(css, "var(--color-sage") {
		t.Errorf("bg-sage must stay themeable, got %q", css)
	}
}

// CONTROL 1: Tailwind's own families must NOT grow our rungs. `blue` already ships eleven
// hand-tuned shades; a derived `blue-soft` would be a second way to say nearly the same thing.
func TestTailwindFamiliesDoNotGrowRungs(t *testing.T) {
	cfg := rungCfg()
	for _, cls := range []string{"bg-blue-soft", "bg-slate-deep", "text-zinc-wash"} {
		if css, _, _ := ResolveCore(cls, &cfg); css != "" {
			t.Errorf("%s must not be derived, got %q", cls, css)
		}
	}
}

// CONTROL 2: a rung the site spells out beats the derived one, or hand-tuning would be impossible.
func TestExplicitRungBeatsDerived(t *testing.T) {
	cfg := rungCfg()
	css, _, _ := ResolveCore("bg-brand-soft", &cfg)
	if !strings.Contains(css, "17, 34, 51") { // #112233, the declared value
		t.Errorf("declared brand-soft must win over derivation, got %q", css)
	}
}

// CONTROL 3: derivation must not resurrect the numeric form we deliberately made silent.
func TestDerivationDoesNotReviveNumericShades(t *testing.T) {
	cfg := rungCfg()
	for _, cls := range []string{"bg-sage-100", "bg-sage-500"} {
		if css, _, _ := ResolveCore(cls, &cfg); css != "" {
			t.Errorf("%s should still emit nothing, got %q", cls, css)
		}
	}
}
