package stylesheet

import (
	"strconv"
	"strings"
	"testing"
)

// cssRule is one style rule of a stylesheet: its selector, its declarations and the width its
// @media (min-width) starts at ("" for every screen).
type cssRule struct {
	selector, body, width string
}

// styleRules lists the style rules of compacted CSS (compact), walking into @layer, @media and
// @supports blocks; a min-width media query gives its rules that width.
func styleRules(css, width string) []cssRule {
	var out []cssRule
	for i := 0; i < len(css); {
		open := strings.IndexAny(css[i:], "{;")
		if open < 0 {
			break
		}
		open += i
		if css[open] == ';' { // a statement: @layer reboot,elements,page,show;
			i = open + 1
			continue
		}
		prelude := strings.TrimSpace(css[i:open])
		depth, end := 0, open
		for j := open; j < len(css); j++ {
			switch css[j] {
			case '{':
				depth++
			case '}':
				depth--
			case '"', '\'':
				quote := css[j]
				for j++; j < len(css) && css[j] != quote; j++ {
				}
			}
			if depth == 0 {
				end = j
				break
			}
		}
		block := css[open+1 : end]
		switch {
		case strings.HasPrefix(prelude, "@media"):
			w := width
			for _, word := range widthWords {
				if strings.Contains(prelude, "(min-width:"+widthPixels[word]+")") {
					w = word
				}
			}
			out = append(out, styleRules(block, w)...)
		case strings.HasPrefix(prelude, "@"):
			out = append(out, styleRules(block, width)...)
		default:
			out = append(out, cssRule{selector: prelude, body: block, width: width})
		}
		i = end + 1
	}
	return out
}

// colourCSS is a colour value of the table as reboot.css writes it: lighter -N as the table writes it,
// mixed toward the canvas in oklab with 100-N of the colour, stronger +N its custom property per mode,
// an engine colour (kitwork-error) its own custom property (colors.<hash>.css writes the one reboot.css
// reads), an alpha /N mixed with transparent in srgb.
func colourCSS(value string) string {
	switch value {
	case "inherit", "none", kitworkTint:
		return value
	case ErrorColour:
		return "var(--" + value + ")"
	}
	c, _ := SplitColour(value)
	css := "var(" + variable(c.Base) + ")"
	switch c.Op {
	case '-':
		css = "color-mix(in oklab," + css + " " + strconv.Itoa(100-c.Amount) + "%,var(--canvas))"
	case '+':
		css = "var(" + strongVariable(c.Base, c.Amount) + ")"
	}
	if c.Translucent {
		css = "color-mix(in srgb," + css + " " + strconv.Itoa(c.Alpha) + "%,transparent)"
	}
	return css
}

// checkLooks compares reboot.css, compacted, with the table of looks: every colour, background, font
// size and weight a rule sets is a look the table knows, with the same value, and every look the table
// knows is still set. It returns what differs.
func checkLooks(css string) []string {
	var problems []string
	matched := map[int]map[string]bool{}
	present := map[int]bool{}
	for _, rule := range styleRules(css, "") {
		for _, declaration := range strings.Split(rule.body, ";") {
			property, value, _ := strings.Cut(declaration, ":")
			value = strings.TrimSuffix(strings.TrimSpace(value), "!important")
			field := map[string]string{"color": "text", "background": "surface", "background-color": "surface",
				"font-size": "size", "font-weight": "weight", "font": "font"}[property]
			if field == "" {
				continue
			}
			index := -1
			for i, l := range elementLooks {
				if l.selector == rule.selector && l.width == rule.width {
					index = i
					break
				}
			}
			if index < 0 {
				problems = append(problems, rule.selector+" sets "+property+":"+value+", which the table of looks (looks.go) does not know")
				continue
			}
			present[index] = true
			l := elementLooks[index]
			if l.unread != "" {
				continue
			}
			if matched[index] == nil {
				matched[index] = map[string]bool{}
			}
			matched[index][field] = true
			want := ""
			switch field {
			case "text":
				want = colourCSS(l.text)
			case "surface":
				want = colourCSS(l.surface)
			case "size":
				if value == "1em" { // the size around it, as without the rule
					value = "0"
				} else {
					value = strconv.FormatFloat(pixels(value), 'f', -1, 64)
				}
				want = strconv.FormatFloat(l.size, 'f', -1, 64)
			case "weight":
				want = strconv.Itoa(l.weight)
			case "font":
				want = "inherit" // the controls speak the page's type: size and weight inherit
			}
			if l.text == "" && field == "text" || l.surface == "" && field == "surface" {
				want = "(nothing)"
			}
			if value != want {
				problems = append(problems, rule.selector+" sets "+property+":"+value+" where the table of looks has "+want)
			}
		}
	}
	for i, l := range elementLooks {
		if !present[i] {
			problems = append(problems, "the table of looks knows "+l.selector+" "+l.width+", which reboot.css no longer has")
			continue
		}
		if l.unread != "" {
			continue
		}
		for field, set := range map[string]bool{"text": l.text != "", "surface": l.surface != "", "size": l.size != 0, "weight": l.weight != 0} {
			if set && !matched[i][field] {
				problems = append(problems, "the table of looks gives "+l.selector+" a "+field+" reboot.css no longer sets")
			}
		}
	}
	return problems
}

// The table of looks is reboot.css: every rule that sets a colour, a background, a font size or a
// weight is in it with the same value, and nothing else is: the hint's words are ink-30 (the bytes
// ink.70 had), the error's the engine colour kitwork-error (danger+25 in light, per mode). CONTROLS: a
// reboot.css whose error goes back to var(--danger), to the rung var(--danger-deep) or to the author's
// one-step danger+25, whose card loses its paper, and with a new rule painting text, each fails.
func TestLooksAreRebootCSS(t *testing.T) {
	css := string(compact(rebootSource))
	if problems := checkLooks(css); len(problems) > 0 {
		t.Fatalf("reboot.css and the table of looks differ:\n%s", strings.Join(problems, "\n"))
	}
	if n := len(styleRules(css, "")); n < 40 {
		t.Fatalf("read %d rules of reboot.css: the walk no longer reads it", n)
	}
	for name, changed := range map[string]string{
		"error back to danger":       strings.Replace(css, ":where(.error){font-size:0.875rem;color:var(--kitwork-error)}", ":where(.error){font-size:0.875rem;color:var(--danger)}", 1),
		"error back on the rung":     strings.Replace(css, ":where(.error){font-size:0.875rem;color:var(--kitwork-error)}", ":where(.error){font-size:0.875rem;color:var(--danger-deep)}", 1),
		"error on one step":          strings.Replace(css, ":where(.error){font-size:0.875rem;color:var(--kitwork-error)}", ":where(.error){font-size:0.875rem;color:var(--kitwork-strong-25-danger)}", 1),
		"hint written the wrong way": strings.Replace(css, ":where(.hint){font-size:0.875rem;color:color-mix(in oklab,var(--ink) 70%,var(--canvas))}", ":where(.hint){font-size:0.875rem;color:color-mix(in oklab,var(--ink) 30%,var(--canvas))}", 1),
		"card without paper":         strings.Replace(css, ":where(.card){background:var(--paper);", ":where(.card){", 1),
		"a new text colour":          strings.Replace(css, ":where(blockquote){", ":where(blockquote){color:var(--brand);", 1),
		"a larger hint":              strings.Replace(css, ":where(.hint){font-size:0.875rem;", ":where(.hint){font-size:1rem;", 1),
		// the translucent fills (decision F1, 13/10/2026): a field back on a solid panel, a quote's alpha
		// changed, each is a look the table does not have
		"a field back on the panel": strings.Replace(css, "border-radius:0.375rem;background:color-mix(in srgb,var(--ink) 5%,transparent)}", "border-radius:0.375rem;background:var(--panel)}", 1),
		"a quote's alpha changed":   strings.Replace(css, "background:color-mix(in srgb,var(--ink) 4%,transparent)}", "background:color-mix(in srgb,var(--ink) 40%,transparent)}", 1),
	} {
		if changed == css {
			t.Fatalf("CONTROL %s: the replacement did not apply", name)
		}
		if len(checkLooks(changed)) == 0 {
			t.Errorf("CONTROL %s: the check finds nothing", name)
		}
	}
}

// LookOf merges the rules that match, later ones winning: a heading's size by level and width, the
// surfaces, the text of a hint and an error, an input that is a checkbox painting nothing.
func TestLookOf(t *testing.T) {
	for _, c := range []struct {
		element, class, input string
		width                 int
		want                  Look
	}{
		{element: "h1", want: Look{Size: 30, Weight: 600}},
		{element: "h1", width: 1, want: Look{Size: 36, Weight: 600}},
		{element: "h2", width: 5, want: Look{Size: 24, Weight: 600}},
		{element: "h4", want: Look{Size: 16, Weight: 600}},
		{element: "div", class: "card", want: Look{Surface: "paper"}},
		{element: "div", class: "popover", want: Look{Surface: "paper"}},
		{element: "span", class: "badge", want: Look{Tinted: true, Size: 12, Weight: 500}},
		{element: "p", class: "hint", want: Look{Text: "ink-30", Size: 14}},
		{element: "p", class: "error", want: Look{Text: "kitwork-error", Size: 14}},
		{element: "input", want: Look{Surface: "ink/5"}},
		{element: "input", input: "checkbox", want: Look{}},
		{element: "select", want: Look{Surface: "ink/5"}},
		{element: "textarea", want: Look{Surface: "ink/5"}},
		{element: "blockquote", want: Look{Surface: "ink/4"}},
		{element: "button", want: Look{}},
		{element: "p", want: Look{}},
	} {
		var classes []string
		if c.class != "" {
			classes = []string{c.class}
		}
		if got := LookOf(c.element, classes, c.input, c.width); got != c.want {
			t.Errorf("LookOf(%s .%s %s, %d) = %+v, want %+v", c.element, c.class, c.input, c.width, got, c.want)
		}
	}
	// a field's fill deepens with focus; a checkbox has no such fill, and a card none in any state
	for _, c := range []struct {
		element, class, input string
		want                  Look
	}{
		{element: "input", want: Look{Surface: "ink/8"}},
		{element: "textarea", want: Look{Surface: "ink/8"}},
		{element: "select", want: Look{Surface: "ink/8"}},
		{element: "input", input: "radio", want: Look{}},
		{element: "div", class: "card", want: Look{Surface: "paper"}},
		{element: "blockquote", want: Look{Surface: "ink/4"}},
	} {
		var classes []string
		if c.class != "" {
			classes = []string{c.class}
		}
		if got := LookIn("focus", c.element, classes, c.input, 0); got != c.want {
			t.Errorf("LookIn(focus, %s .%s %s) = %+v, want %+v", c.element, c.class, c.input, got, c.want)
		}
	}
	if got := LookIn("hover", "input", nil, "", 0); got != (Look{Surface: "ink/5"}) {
		t.Errorf("a rule of focus holds in no other state: LookIn(hover, input) = %+v", got)
	}
	if got := RootLook(); got != (Look{Text: "ink", Surface: "canvas", Size: 16, Weight: 400}) {
		t.Errorf("RootLook = %+v", got)
	}
}
