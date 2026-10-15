package stylesheet

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	jitcss "github.com/kitwork/engine/jit/css"
)

// Color is a token's value as router.colors() writes it: one colour ("#hex", Dark empty) whose dark
// value the engine derives when the page has a dark look, or a { light, dark } pair.
type Color struct{ Light, Dark string }

// kitDesign is the palette a site starts from: Kit Design's default skin (cool light ground, deep
// navy ink, blurple brand) and its status scale. router.colors() merges over it token by token, so a
// site that declares nothing still has canvas, ink and line. Only the hues are written here: every
// accent grows its on (the label on its fill) from its one hue, and a site's own brand regrows it.
// Lighter and stronger are operators, not tokens (operators.go): secondary text is ink-30, a tinted
// fill brand-85, a stronger brand brand+15; a role name is the site's to declare.
var kitDesign = map[string]Color{
	"canvas":  {Light: "#f6f9fc"},
	"paper":   {Light: "#ffffff"},
	"panel":   {Light: "#eff3f8"},
	"ink":     {Light: "#0a2540"},
	"line":    {Light: "#e6ebf1"},
	"brand":   {Light: "#635bff"},
	"success": {Light: "#2f9e63"},
	"warning": {Light: "#c98a1e"},
	"danger":  {Light: "#e5484d"},
	"info":    {Light: "#2f7dc9"},
}

// surfaces are the neutral grounds; with ink (the text) they are the tokens that grow no family.
var surfaces = map[string]bool{"canvas": true, "panel": true, "paper": true, "line": true}

// rungs are what an accent grows from its one hue: on, a label on the solid fill (jitcss.Palette). The
// rungs deep, soft and wash are gone (13/10/2026): a stronger, a tinted and a faint colour are the
// operators X+N, X-85 and X-95 (operators.go), and a site may not declare one (checkToken).
var rungs = []string{"on"}

// droppedRungs are the rungs accents no longer grow, each with the step that replaces it.
var droppedRungs = map[string]string{"soft": "-" + strconv.Itoa(TintStep), "wash": "-" + strconv.Itoa(FaintStep), "deep": "+" + strconv.Itoa(StrongStep)}

// fixedColors never change with the mode: a scrim behind a dialog, text over a photo.
var fixedColors = map[string]string{"white": "#fff", "black": "#000"}

var tokenName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*$`)

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// accent reports whether a token is the hue of a family: a word without a dot that is neither a
// surface nor the text.
func accent(token string) bool {
	return !strings.Contains(token, ".") && !surfaces[token] && token != "ink"
}

// variable is a token's CSS custom property: brand.on → --brand-on.
func variable(token string) string { return "--" + strings.ReplaceAll(token, ".", "-") }

// Palette is one colour chain resolved: Kit Design's default palette with the router.colors() of the
// site root and of every branch down to a route merged over it, plus the families it grows. It knows
// every token the interface may name and its light and dark value. Immutable; safe across goroutines.
type Palette struct {
	light, dark map[string]string // token → #rrggbb; dark holds the tokens that have a dark value
	declared    map[string]bool   // written by the default palette or router.colors(), not grown
	names       []string          // every token, sorted
	hasDark     bool
	focus       [2]string // --kitwork-focus in light and in dark: "brand" or "ink"
	// gradients are the site's router.gradients down the route (WithGradients); the standard set is
	// every palette's (gradients.go)
	gradients map[string]Gradient
	// engine holds every engine colour (engine_colour.go) as the colour value it is in light and in dark,
	// by name: kitwork-error is danger+25 in light and danger in dark on Kit Design. Read only once built.
	engine map[string][2]string
	// highlight is the site's router.highlight down the route, each kind's colour with the call that set
	// it (WithHighlight, highlight.go); shortfalls its colours short of contrast on the page's grounds, and
	// highlightLines the line each call is told, both worked out once there
	highlight      map[string]HighlightColour
	shortfalls     []HighlightShortfall
	highlightLines map[Source]string
}

// NewPalette resolves a colour chain: chain holds the router.colors() of each folder from the site
// root down to the route, nearest last, and each merges over Kit Design's default palette token by
// token. A token a folder writes is that folder's whole: its dark value is the one it writes, or,
// when it writes one colour, derived. An accent a folder writes regrows its family unless the folder
// writes the rung itself. dark asks for the dark look even when no token writes a dark value (a route
// shown dark first, router.appearance("dark")); a dark value anywhere in the chain asks for it too.
func NewPalette(dark bool, chain ...map[string]Color) (*Palette, error) {
	type value struct {
		light, dark string
		declared    bool
	}
	tokens := map[string]*value{}
	layers := append([]map[string]Color{kitDesign}, chain...)
	byVariable := map[string]string{}
	for _, layer := range layers {
		names := sortedKeys(layer)
		for _, name := range names {
			if err := checkToken(name); err != nil {
				return nil, err
			}
			if other, taken := byVariable[variable(name)]; taken && other != name {
				return nil, fmt.Errorf("router.colors: %q and %q are both the CSS variable %s: keep one name", other, name, variable(name))
			}
			byVariable[variable(name)] = name
		}
		for _, name := range names {
			if !accent(name) {
				continue
			}
			for _, rung := range rungs {
				if _, own := layer[name+"."+rung]; !own {
					delete(tokens, name+"."+rung) // the family grows again from the new hue
				}
			}
		}
		for _, name := range names {
			c := layer[name]
			light, err := checkHex(name, "", c.Light)
			if err != nil {
				return nil, err
			}
			night := ""
			if c.Dark != "" {
				if night, err = checkHex(name, "dark", c.Dark); err != nil {
					return nil, err
				}
				dark = true
			}
			tokens[name] = &value{light: light, dark: night, declared: true}
		}
	}
	for _, name := range sortedKeys(tokens) {
		if !accent(name) {
			continue
		}
		grown := jitcss.Palette(tokens[name].light)
		for _, rung := range rungs {
			if _, written := tokens[name+"."+rung]; !written && grown[rung] != "" {
				tokens[name+"."+rung] = &value{light: grown[rung]}
			}
		}
	}

	p := &Palette{light: map[string]string{}, dark: map[string]string{}, declared: map[string]bool{}, hasDark: dark}
	for name, v := range tokens {
		p.light[name] = v.light
		if v.declared {
			p.declared[name] = true
		}
		if dark && v.dark != "" {
			p.dark[name] = v.dark
		}
		p.names = append(p.names, name)
	}
	sort.Strings(p.names)
	if dark {
		p.deriveDark()
	}
	p.focus[0] = focusOn(p.light, p.light)
	if dark {
		p.focus[1] = focusOn(p.dark, p.light)
	}
	p.engine = p.engineColours()
	return p, nil
}

func checkToken(name string) error {
	switch {
	case fixedColors[name] != "":
		return fmt.Errorf("router.colors: %q is a fixed colour (white, black), the same in light and dark: name the token by its role (paper, ink…)", name)
	case namedColours[name] != "":
		return namedTokenError(name) // tomato already means #ff6347 (named_colour.go)
	case standardByName[name].Type != "":
		// glow already means the standard gradient: one namespace for colours and gradients (gradients.go)
		return fmt.Errorf("router.colors: %q đã là tên một %s (background=%q): tên màu và tên gradient chung một chỗ, đặt tên token theo vai trò khác", name, builtinKind(name), name)
	case !tokenName.MatchString(name):
		return fmt.Errorf("router.colors: %q is not a token name: lowercase words joined by - or . (brand, chart.muted), each part after a dot starting with a letter (ink-30 is ink made lighter in the markup, not a token)", name)
	case strings.HasSuffix(variable(name), "-dark"):
		return fmt.Errorf("router.colors: %q: a token's dark value is written with it, { light: \"#…\", dark: \"#…\" }, not as a token of its own", name)
	case strings.HasPrefix(variable(name), "--kitwork-"):
		return fmt.Errorf("router.colors: %q: tên bắt đầu bằng kitwork- là của engine (--kitwork-focus, --kitwork-strong-…): đặt tên khác", name)
	}
	if base, word, dotted := cutLast(name, "."); dotted && droppedRungs[word] != "" {
		// the markup refuses x.soft, x.wash and x.deep as the old rungs (oldColour), so a token named so
		// could never be written: chart.soft is chart-85 now, ink.soft ink-30
		role := map[string]string{"soft": "tint", "wash": "faint", "deep": "strong"}[word]
		write := map[string]string{"soft": "nền nhạt", "wash": "nền rất nhạt", "deep": "đậm hơn"}[word]
		step := base + droppedRungs[word]
		if base == "ink" && word == "soft" {
			write, step = "chữ phụ", "ink-30"
		}
		return fmt.Errorf("router.colors: %q: bậc .%s đã bỏ (13/10/2026), màu nhấn chỉ còn mọc .on; %s viết %s trong giao diện; "+
			"một vai trò riêng thì đặt tên khác (%s-%s, %s.muted)", name, word, write, step, base, role, base)
	}
	return stepNameError("router.colors", name) // chart-2 would read as chart made lighter
}

// cutLast cuts a string around the last separator: brand.on is brand and on.
func cutLast(s, separator string) (before, after string, found bool) {
	if i := strings.LastIndex(s, separator); i >= 0 {
		return s[:i], s[i+len(separator):], true
	}
	return s, "", false
}

// checkHex reads "#rgb" or "#rrggbb" as lowercase #rrggbb, and a named colour as its value: a role it
// seeds grows its rungs and its dark from that hex (brand: "cerulean" is brand: "#007ba7").
func checkHex(token, mode, hex string) (string, error) {
	h := strings.TrimSpace(hex)
	if seed, named, err := namedSeed(token, mode, h); named {
		return seed, err
	}
	if !hexColor.MatchString(h) {
		which := ""
		if mode != "" {
			which = " (" + mode + ")"
		}
		return "", fmt.Errorf("router.colors: %q%s is %q: a colour is \"#rgb\", \"#rrggbb\" or a named colour (tomato, cerulean…)", token, which, hex)
	}
	h = strings.ToLower(h)
	if len(h) == 4 {
		h = string([]byte{'#', h[1], h[1], h[2], h[2], h[3], h[3]})
	}
	return h, nil
}

// deriveDark fills the dark value of every token that has none, with router.themes()'s formula
// (jitcss.DeriveThemes): surfaces take the hue of ink, text lifts into the light band, accents keep
// their hue and regrow their label (on) on the dark fill. The formula knows tokens by their role
// names, so each accent family goes in under a neutral alias: an accent named like a Tailwind or a
// legacy jit/css colour (blue, primary) still gets a dark value.
func (p *Palette) deriveDark() {
	alias := map[string]string{}
	family := 0
	other := 0
	for _, name := range p.names {
		switch {
		case surfaces[name] || name == "ink":
			alias[name] = name
		case accent(name):
			key := "accent" + strconv.Itoa(family)
			family++
			alias[name] = key
			for _, rung := range rungs {
				if _, ok := p.light[name+"."+rung]; ok {
					alias[name+"."+rung] = key + "-" + rung
				}
			}
		}
	}
	for _, name := range p.names {
		if _, ok := alias[name]; ok {
			continue
		}
		alias[name] = "other-" + strconv.Itoa(other) // no formula reaches it: it keeps its light value
		other++
	}
	config := &jitcss.Config{Colors: map[string]jitcss.Color{}}
	for _, name := range p.names {
		config.Colors[alias[name]] = jitcss.Hex(p.light[name])
		if night, ok := p.dark[name]; ok {
			config.Colors[alias[name]+"-dark"] = jitcss.Hex(night)
		}
	}
	jitcss.DeriveThemes(config, "dark")
	derived := config.Themes["dark"]
	for _, name := range p.names {
		if _, hand := p.dark[name]; hand {
			continue
		}
		if c, ok := derived[alias[name]]; ok {
			p.dark[name] = c.HexString()
		}
	}
}

// focusOn picks the keyboard focus ring of one mode: the brand where it reaches 3:1 (WCAG's
// non-text contrast) on both canvas and paper, else ink. fallback holds the light value of a token the
// mode does not re-value.
func focusOn(mode, fallback map[string]string) string {
	get := func(token string) string {
		if v, ok := mode[token]; ok {
			return v
		}
		return fallback[token]
	}
	brand := get("brand")
	if jitcss.Contrast(brand, get("canvas")) >= 3 && jitcss.Contrast(brand, get("paper")) >= 3 {
		return "brand"
	}
	return "ink"
}

// Tokens lists every token the interface may name: declared and grown, sorted.
func (p *Palette) Tokens() []string { return append([]string(nil), p.names...) }

// Declared lists the tokens the default palette and router.colors() write, without the grown rungs:
// the values a {{ }} colour may take (Table.Domain).
func (p *Palette) Declared() []string {
	var out []string
	for _, name := range p.names {
		if p.declared[name] {
			out = append(out, name)
		}
	}
	return out
}

// Has reports whether the interface may name the token.
func (p *Palette) Has(token string) bool { _, ok := p.light[token]; return ok }

// Value is a token's light colour and its dark one ("" when the page has no dark look or nothing
// re-values it), as #rrggbb: the same palette a native renderer reads.
func (p *Palette) Value(token string) (light, dark string, ok bool) {
	light, ok = p.light[token]
	return light, p.dark[token], ok
}

// Dark reports whether the chain has a dark look.
func (p *Palette) Dark() bool { return p.hasDark }

// Colors is the colour chain's stylesheet, colors.<hash>.css: every token as a custom property of
// :root in light, and the tokens the dark look re-values under :root[data-theme=dark], for screens
// only, so print stays light. --kitwork-focus, the keyboard focus ring reboot.css draws, is var(--brand)
// or var(--ink) per mode. --kitwork-error, the words of .error in reboot.css, is that engine colour of
// each mode (engine_colour.go: danger+25 in light, danger in dark on Kit Design), as a page sets an
// engine colour it uses (Rules.CSS). Custom properties only, so the file is unlayered. Its name changes
// only when the palette does.
func Colors(p *Palette) File {
	if p == nil {
		p, _ = NewPalette(false)
	}
	errorText, _ := p.enginePair(ErrorColour)
	var b strings.Builder
	b.WriteString(":root{color-scheme:light")
	for _, name := range p.names {
		b.WriteString(";" + variable(name) + ":" + p.light[name])
	}
	b.WriteString(";" + errorText.name + ":" + errorText.light)
	b.WriteString(";--kitwork-focus:var(--" + p.focus[0] + ")}\n")
	if p.hasDark {
		b.WriteString("@media screen{:root[data-theme=dark]{color-scheme:dark")
		for _, name := range p.names {
			if night, ok := p.dark[name]; ok {
				b.WriteString(";" + variable(name) + ":" + night)
			}
		}
		b.WriteString(";" + errorText.name + ":" + errorText.dark)
		b.WriteString(";--kitwork-focus:var(--" + p.focus[1] + ")}}\n")
	}
	return newFile("colors", []byte(b.String()))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
