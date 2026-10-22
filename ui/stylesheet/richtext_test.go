package stylesheet

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// The text scope of <richtext> is the page's stylesheet's, written only when recorded: the layer
// statement, the link's engine colour per mode (the custom property it reads), then @layer elements{…}
// with the scope, once however many richtexts record it, before the page layer; the weights of its
// headings and strong text go to the fonts file. The fills of code are the table's colour values ink/8
// (written color-mix(in srgb,var(--ink) 8%,transparent)) and panel (var(--panel)). A rule set that never
// records it has no elements layer, no kitwork-link and no weight of its own.
// CONTROL: with Rules.CSS writing none of the scope's rules (the loop inside @layer elements left out)
// the row of a richtext alone and the row beside a class failed; with Table.Richtext recording no
// weights the weights row failed (run 20/10/2026 on overlaid copies of rules.go and richtext.go). With
// Table.Richtext writing the two fills the other way round, the rows of a richtext alone and beside a
// class failed.
func TestRichtextScopeIsThePageStylesheets(t *testing.T) {
	palette := examplePalette(t)
	table := NewTable(palette)
	paint := func(m Mode) string {
		value, ok := palette.Engine(LinkColour, m)
		if !ok {
			t.Fatalf("no %s in %s", LinkColour, m.Word())
		}
		c, _ := palette.Paint(value, m)
		return c.Hex()
	}
	// the link in its engine colour, code on ink at 8% (the table's ink/8), a code block on the panel
	fills := strings.NewReplacer("LINK", "var(--kitwork-link)", "CODE", "color-mix(in srgb,var(--ink) 8%,transparent)", "BLOCK", "var(--panel)")
	scope := "@layer elements{\n" + fills.Replace(richtextRules) + "}\n"
	want := LayerStatement + "\n" +
		":root{--kitwork-link:" + paint(LightMode) + "}\n" +
		"@media screen{:root[data-theme=dark]{--kitwork-link:" + paint(DarkMode) + "}}\n" + scope

	var alone Rules
	for i := 0; i < 2; i++ { // two richtexts on one page: one scope
		if err := table.Richtext(&alone); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(alone.CSS()); got != want {
		t.Errorf("a page with a richtext and nothing else:\n got  %q\n want %q", got, want)
	}
	if got := alone.Weights(); !reflect.DeepEqual(got, []int{600, 700}) {
		t.Errorf("weights %v, want the heading's 600 and strong text's 700", got)
	}
	if alone.File().Name == "" {
		t.Error("a page with a richtext and no class has no page stylesheet to link")
	}
	if len(alone.Classes()) != 0 {
		t.Errorf("the scope is no class of an attribute: recorded %v", alone.Classes())
	}

	// beside the classes of attributes: the scope first, the page layer after it, its rules as they were
	var plain, both Rules
	for _, r := range []*Rules{&plain, &both} {
		if _, err := table.Class(r, Intent{Name: "padding", Value: "4"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := table.Richtext(&both); err != nil {
		t.Fatal(err)
	}
	css := string(both.CSS())
	if !strings.Contains(css, scope+"@layer page{\n.padding-4{padding:1rem}\n}\n") {
		t.Errorf("the scope stands before the page layer:\n%s", css)
	}
	if got := string(plain.CSS()); strings.Contains(got, "@layer elements") || strings.Contains(got, "richtext") ||
		strings.Contains(got, LinkColour) || len(plain.Weights()) != 0 {
		t.Errorf("a page without a richtext carries its scope:\n%s (weights %v)", got, plain.Weights())
	}
	if err := table.Richtext(nil); err != nil {
		t.Errorf("Richtext(nil) checks it can be written: %v", err)
	}
}

// Every rule of the scope stays inside a richtext and weighs one class, the richtext's: its selector
// starts with .richtext, holds it once, and what follows is combinators, * and :where(…) (which weigh
// nothing). So it styles nothing outside a richtext, beats reboot.css's element looks (zero weight)
// in either order of the files, and loses to every class of the page layer. The link's colour is the
// engine colour the contrast check measures it in (RichtextLinkLook), and the fills of code are the
// colour values the check measures code on (RichtextCodeLook, RichtextCodeBlockLook). The alignment
// classes write the text-align align="center" and align="right" write on a <text>.
// CONTROL: with the link's rule written .richtext a (an element outside :where, one class and one
// element) and with a rule ul{list-style:disc} added, this test failed on each (run 20/10/2026 on
// overlaid copies of richtext.go). With RichtextCodeLook measuring code on the panel, the row of code
// in a line failed.
func TestRichtextRulesStayInsideTheScope(t *testing.T) {
	rules := rulesWithBodies(richtextRules)
	if len(rules) != strings.Count(richtextRules, "\n") {
		t.Fatalf("the scope is one rule a line: %d rules, %d lines", len(rules), strings.Count(richtextRules, "\n"))
	}
	for _, rule := range rules {
		rest, ok := strings.CutPrefix(rule.selector, "."+RichtextClass)
		if !ok || strings.Contains(rest, RichtextClass) {
			t.Errorf("%s is not scoped once to .%s", rule.selector, RichtextClass)
			continue
		}
		for {
			at := strings.Index(rest, ":where(")
			if at < 0 {
				break
			}
			depth, end := 0, -1
			for i := at + len(":where"); i < len(rest); i++ {
				switch rest[i] {
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth == 0 {
					end = i
					break
				}
			}
			if end < 0 {
				t.Fatalf("%s: unbalanced :where(", rule.selector)
			}
			rest = rest[:at] + rest[end+1:]
		}
		if strings.Trim(rest, " >+~*") != "" {
			t.Errorf("%s weighs more than one class: %q outside :where()", rule.selector, rest)
		}
	}
	if look := RichtextLinkLook(); look.Text != LinkColour || !strings.Contains(richtextRules, ".richtext :where(a){color:LINK;") {
		t.Errorf("the link is measured as %+v; its rule must paint it in %s", look, LinkColour)
	}
	// the alignment classes are the words align="center" and align="right" write on a <text>, with the
	// same text-align (textAlign: right is end, mirrored right to left), so a page with both agrees
	for word, class := range map[string]string{"center": richtext.AlignCenter, "right": richtext.AlignRight} {
		if rule := ".richtext :where(." + class + "){text-align:" + textAlign[word] + "}"; class != "align-"+word || !strings.Contains(richtextRules, rule) {
			t.Errorf("the scope has no %s: align=%q on a <text> writes the class %s with text-align:%s", rule, word, "align-"+word, textAlign[word])
		}
	}
	// code is measured on the fills its rules paint, the same colour values of the table
	if look := RichtextCodeLook(); look != (Look{Surface: RichtextCodeFill}) || !strings.Contains(richtextRules, ":where(code:not(pre code,a code)){padding:0.125em 0.25em;border-radius:0.25em;background:CODE}") {
		t.Errorf("code in a line is measured as %+v; its rule must paint %s behind it", look, RichtextCodeFill)
	}
	if look := RichtextCodeBlockLook(); look != (Look{Surface: RichtextCodeBlockFill}) || !strings.Contains(richtextRules, ":where(pre){padding:1em;border-radius:0.5em;background:BLOCK;") {
		t.Errorf("a code block is measured as %+v; its rule must paint %s behind it", look, RichtextCodeBlockFill)
	}
}
