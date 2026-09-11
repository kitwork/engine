package css

import (
	"strings"
	"testing"
)

// Every one of these classes used to resolve to the empty string: written in the markup, valid
// Tailwind, and producing no rule at all. The assertions check the DECLARATION, not merely that
// something came back — an `appearance-none` that emitted `resize: none;` would be just as broken.
func TestV3UtilitiesEmitTheRightDeclaration(t *testing.T) {
	cases := []struct{ cls, want string }{
		{"visible", "visibility: visible;"},
		{"invisible", "visibility: hidden;"},
		{"contents", "display: contents;"},
		{"align-middle", "vertical-align: middle;"},
		{"appearance-none", "appearance: none;"},
		{"bg-cover", "background-size: cover;"},
		{"bg-center", "background-position: center;"},
		{"bg-left-top", "background-position: left top;"},
		{"bg-no-repeat", "background-repeat: no-repeat;"},
		{"bg-fixed", "background-attachment: fixed;"},
		{"bg-none", "background-image: none;"},
		{"object-center", "object-position: center;"},
		{"resize", "resize: both;"},
		{"resize-y", "resize: vertical;"},
		{"resize-none", "resize: none;"},
		{"scroll-smooth", "scroll-behavior: smooth;"},
		{"snap-x", "scroll-snap-type: x var(--kitwork-scroll-snap-strictness);"},
		{"snap-mandatory", "--kitwork-scroll-snap-strictness: mandatory;"},
		{"snap-start", "scroll-snap-align: start;"},
		{"flex-col-reverse", "flex-direction: column-reverse;"},
		{"flex-wrap-reverse", "flex-wrap: wrap-reverse;"},
		{"justify-self-end", "justify-self: flex-end;"},
		{"justify-items-center", "justify-items: center;"},
		{"basis-1/2", "flex-basis: 50%;"},
		{"list-inside", "list-style-position: inside;"},
		{"decoration-2", "text-decoration-thickness: 2px;"},
		{"decoration-dotted", "text-decoration-style: dotted;"},
		{"text-ellipsis", "text-overflow: ellipsis;"},
		{"float-left", "float: left;"},
		{"clear-both", "clear: both;"},
		{"table-fixed", "table-layout: fixed;"},
		{"border-collapse", "border-collapse: collapse;"},
		{"caption-top", "caption-side: top;"},
		{"grid-flow-col", "grid-auto-flow: column;"},
		{"auto-rows-min", "grid-auto-rows: min-content;"},
		{"overscroll-contain", "overscroll-behavior: contain;"},
		{"touch-pan-x", "touch-action: pan-x;"},
		{"will-change-transform", "will-change: transform;"},
		{"indent-4", "text-indent: 1rem;"},
		{"columns-2", "columns: 2;"},
		{"mix-blend-multiply", "mix-blend-mode: multiply;"},
		{"break-inside-avoid", "break-inside: avoid;"},
		{"break-before-page", "break-before: page;"},
		{"hyphens-auto", "hyphens: auto;"},
		{"isolation-auto", "isolation: auto;"},
		{"divide-dashed", "border-style: dashed;"},
		{"space-x-reverse", "--kitwork-space-x-reverse: 1;"},
	}
	cfg := DefaultConfig
	for _, c := range cases {
		css, _, _ := ResolveCore(c.cls, &cfg)
		if !strings.Contains(css, c.want) {
			t.Errorf("%s → %q, want it to contain %q", c.cls, css, c.want)
		}
	}
}

// Filters must COMPOSE. Tailwind gives every filter utility its own slot and restates the whole
// chain, so `blur-sm grayscale` keeps both; a handler that emitted `filter: grayscale(100%)` flat
// would silently throw away the blur declared next to it.
func TestFiltersComposeInsteadOfOverwriting(t *testing.T) {
	cfg := DefaultConfig
	for _, c := range []struct{ cls, slot string }{
		{"grayscale", "--kitwork-grayscale: grayscale(100%)"},
		{"grayscale-0", "--kitwork-grayscale: grayscale(0)"},
		{"invert", "--kitwork-invert: invert(100%)"},
		{"saturate-150", "--kitwork-saturate: saturate(1.5)"},
		{"brightness-110", "--kitwork-brightness: brightness(1.1)"},
		{"hue-rotate-15", "--kitwork-hue-rotate: hue-rotate(15deg)"},
		{"drop-shadow-sm", "--kitwork-drop-shadow: drop-shadow(0 1px 1px rgb(0 0 0 / 0.05))"},
	} {
		css, _, _ := ResolveCore(c.cls, &cfg)
		if !strings.Contains(css, c.slot) {
			t.Errorf("%s → %q, want slot %q", c.cls, css, c.slot)
		}
		if !strings.Contains(css, "filter: "+filterChain+";") {
			t.Errorf("%s → %q, want it to restate the whole filter chain", c.cls, css)
		}
	}
	// The chain is only usable if every slot it names has a default; an undefined var makes the
	// whole `filter:` declaration invalid and the browser drops it.
	pre := buildJITCSS([]string{"filter"}, nil)
	for _, v := range strings.Split(filterChain, " ") {
		name := strings.TrimSuffix(strings.TrimPrefix(v, "var("), ")")
		if !strings.Contains(pre, name+": ;") {
			t.Errorf("Preflight does not default %s — the filter chain would be dropped", name)
		}
	}
}

// `group-open:rotate-45` is written fourteen times on buildinpublic.guide's <details> chevrons and
// has never rotated: jitcss had no `open` state at all, so the class resolved to nothing.
func TestOpenVariant(t *testing.T) {
	cfg := DefaultConfig
	css, sel, _ := ResolveCore("group-open:rotate-45", &cfg)
	if css == "" {
		t.Fatal("group-open:rotate-45 emitted nothing")
	}
	if !strings.HasPrefix(sel, ".group[open] ") {
		t.Errorf("selector = %q, want it scoped under .group[open]", sel)
	}
	_, sel, _ = ResolveCore("open:bg-brand", &cfg)
	if !strings.HasSuffix(sel, "[open]") {
		t.Errorf("open: selector = %q, want it to end in [open]", sel)
	}
}

// A `bg-<keyword>` must not be mistaken for a colour named after the keyword, and vice versa: the
// keyword patterns sit before the colour ones, so this is the test that keeps that order honest.
func TestKeywordsDoNotStealColours(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["brand"] = Hex("#635bff")
	if css, _, _ := ResolveCore("bg-brand", &cfg); !strings.Contains(css, "background-color") {
		t.Errorf("bg-brand → %q, want a background-color", css)
	}
	if css, _, _ := ResolveCore("bg-cover", &cfg); strings.Contains(css, "background-color") {
		t.Errorf("bg-cover → %q, want background-size, not a colour", css)
	}
	if css, _, _ := ResolveCore("text-ellipsis", &cfg); strings.Contains(css, "color:") {
		t.Errorf("text-ellipsis → %q, want text-overflow, not a colour", css)
	}
}
