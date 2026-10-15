package stylesheet

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// The layer statement opens every layered file, so the order of the layers holds whatever order Drive
// leaves the <link>s in: reboot, a page's file, and what Elements wraps (icons). colors is custom
// properties only and stays unlayered.
func TestLayerStatementOpensEveryLayeredFile(t *testing.T) {
	if LayerStatement != "@layer reboot,elements,page,show;" {
		t.Fatalf("the four layers, in their order: got %q", LayerStatement)
	}
	var page Rules
	if _, err := NewTable(nil).Class(&page, Intent{Name: "gap", Value: "4"}); err != nil {
		t.Fatal(err)
	}
	layered := map[string][]byte{
		"reboot":   Reboot().CSS,
		"page":     page.CSS(),
		"elements": Elements([]byte(".icon-coffee{mask:url(x)}")),
	}
	for name, css := range layered {
		if !strings.HasPrefix(string(css), LayerStatement) {
			t.Errorf("%s does not start with the layer statement: %.80q", name, css)
		}
	}
	if strings.Contains(string(Colors(nil).CSS), "@layer") {
		t.Errorf("colors.css declares custom properties only and must stay unlayered:\n%s", Colors(nil).CSS)
	}
	if got := string(Elements([]byte(".icon-coffee{mask:url(x)}"))); got != LayerStatement+"\n@layer elements{\n.icon-coffee{mask:url(x)}\n}\n" {
		t.Errorf("Elements: %q", got)
	}
	if Elements([]byte(" \n")) != nil {
		t.Error("Elements of nothing is nothing: no file, no link")
	}
}

// The reboot is hashed once: its name is its bytes, the same on every call and every run, and the same
// whether the checkout ends its lines with LF or CRLF (git's autocrlf), because the bytes served are
// the source compacted.
func TestRebootNameIsStable(t *testing.T) {
	first, second := Reboot(), Reboot()
	if first.Role != "reboot" || first.Name != second.Name || string(first.CSS) != string(second.CSS) {
		t.Fatalf("two calls differ: %s %s", first.Name, second.Name)
	}
	sum := sha256.Sum256(first.CSS)
	if want := "reboot." + hex.EncodeToString(sum[:])[:16]; first.Name != want {
		t.Fatalf("name %s is not the hash of its bytes (%s)", first.Name, want)
	}
	lf := strings.ReplaceAll(rebootSource, "\r\n", "\n")
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	if string(compact(lf)) != string(first.CSS) || string(compact(crlf)) != string(first.CSS) {
		t.Fatal("the served bytes depend on the line endings of the checkout")
	}
	first.CSS[0] = 'X' // a caller that writes into its copy does not change the engine's
	if Reboot().CSS[0] != '@' {
		t.Fatal("Reboot hands out the engine's own bytes")
	}
	for i := 0; i < 10; i++ {
		if Reboot().Name != second.Name {
			t.Fatal("the name changes between calls")
		}
	}
}

// Two flaws the synthesis caught in the study's reboot stay fixed: a pseudo-element inside :where()
// matches nothing, so ::placeholder is plain; media queries are min-width, never the range syntax that
// raises the browser floor to Safari 16.4.
func TestRebootKeepsTheTwoFixes(t *testing.T) {
	css := string(Reboot().CSS)
	if strings.Contains(css, ":where(::") {
		t.Error("a pseudo-element inside :where() matches nothing")
	}
	if !strings.Contains(css, "::placeholder{opacity:1;") {
		t.Error("the placeholder rule is gone")
	}
	for _, block := range strings.Split(css, "@media ")[1:] {
		query := block[:strings.IndexByte(block, '{')]
		if strings.ContainsAny(query, "<>=") {
			t.Errorf("range media syntax %q: write (min-width:…)", query)
		}
	}
	if !strings.Contains(css, "@media (min-width:640px){:where(h1)") {
		t.Error("the heading's width step is not a min-width query")
	}
}

// Print rules sit last in the elements layer: in the reboot layer they lost to the looks of the
// elements layer (.toast's display:flex printed the toast host on every sheet, body kept a screen's
// height).
func TestRebootPrintRulesOverrideTheElementLooks(t *testing.T) {
	css := string(Reboot().CSS)
	elements := strings.Index(css, "@layer elements{")
	print := strings.Index(css, "@media print{")
	toast := strings.Index(css, ":where(.toast){")
	body := strings.Index(css, ":where(body){min-block-size:100vh")
	if elements < 0 || print < 0 || toast < 0 || body < 0 {
		t.Fatalf("rules missing: elements %d print %d toast %d body %d", elements, print, toast, body)
	}
	if print < elements || print < toast || print < body {
		t.Fatalf("@media print at %d must come after the elements layer (%d), .toast (%d) and body (%d)", print, elements, toast, body)
	}
	if !strings.Contains(css[print:], ":where(.progressbar,.toast){display:none}") {
		t.Error("print does not hide the floating hosts")
	}
}

// Every selector of the reboot weighs nothing (*, a bare pseudo-element, or :where()), so inside each
// layer only the order of the file decides and one class of a page outranks any of it.
func TestRebootWeighsNothing(t *testing.T) {
	for _, selector := range selectorsOf(string(Reboot().CSS)) {
		for _, part := range splitTopLevel(selector) {
			if rest := weighed(part); rest != "" {
				t.Errorf("%q weighs more than nothing: %q is outside :where() (in %q)", part, rest, selector)
			}
		}
	}
	if weighed(":where(a) .x") != ".x" || weighed(":where(a)::before") != "" ||
		weighed("::highlight(kitwork-terminal-caret)") != "" || weighed("::highlight(x).y") != ".y" {
		t.Fatal("control: the reader of weight is wrong")
	}
	if n := len(selectorsOf(string(Reboot().CSS))); n < 40 {
		t.Fatalf("the reader found %d selectors: it no longer reads the reboot", n)
	}
}

// The reboot styles every word the engine writes without an attribute, and the page writes no rule for
// them.
func TestRebootStylesEveryElementWord(t *testing.T) {
	css := string(Reboot().CSS)
	for _, word := range ElementWords() {
		if !strings.Contains(css, "."+word+",") && !strings.Contains(css, "."+word+")") && !strings.Contains(css, "."+word+">") {
			t.Errorf("reboot.css has no rule for .%s", word)
		}
	}
}

// The reboot reads the variables the page's align and space set, resets them on every container, and
// covers an image written with aspect-ratio (fit="contain" in the page layer wins).
func TestRebootDirectionMachinery(t *testing.T) {
	css := string(Reboot().CSS)
	for _, want := range []string{
		":where(.flex-row,.flex-column){display:flex;--kitwork-horizontal:initial;--kitwork-vertical:initial;--kitwork-space:initial}",
		":where(.flex-row){flex-direction:row;justify-content:var(--kitwork-space,var(--kitwork-horizontal,normal));align-items:var(--kitwork-vertical,normal)}",
		":where(.flex-column){flex-direction:column;justify-content:var(--kitwork-space,var(--kitwork-vertical,normal));align-items:var(--kitwork-horizontal,normal)}",
		":where(img[class*=\"aspect-ratio-\"]){object-fit:cover}",
		"background-color:var(--kitwork-tint)",
		":where(:focus-visible){outline:2px solid var(--kitwork-focus,var(--brand))",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("reboot.css lacks %s", want)
		}
	}
	reset := strings.Index(css, ":where(.flex-row,.flex-column){display:flex;")
	button := strings.Index(css, ":where(a,button){--kitwork-vertical:center}")
	if reset < 0 || button < 0 || button < reset {
		t.Error("an element's own alignment must come after the reset, or the reset wipes it")
	}
	// Every element's own alignment comes after the reset (same weight, so the later wins); the reset
	// and .grid's are the only rules that set a variable to initial.
	elements := strings.Index(css, "@layer elements{")
	setters := 0
	for _, rule := range rulesWithBodies(css[elements:]) {
		if !strings.Contains(rule.body, "--kitwork-") || strings.Contains(rule.body, ":initial") {
			continue
		}
		setters++
		if at := strings.Index(css, rule.selector+"{"+rule.body); at < reset {
			t.Errorf("%s sets an alignment before the reset that would wipe it", rule.selector)
		}
	}
	if setters < 4 {
		t.Fatalf("found %d element alignments (a, button, badge, breadcrumb…): the reader no longer reads the reboot", setters)
	}
}

// The weights reboot.css gives the elements are ElementWeights: a heading 600, a badge 500, and
// nothing else, so text keeps the browser's 400. fonts.<hash>.css ships what this says.
func TestElementWeightsAreTheReboots(t *testing.T) {
	css := string(Reboot().CSS)
	set := map[string]string{}
	for _, rule := range rulesWithBodies(css) {
		for _, declaration := range strings.Split(rule.body, ";") {
			if value, ok := strings.CutPrefix(declaration, "font-weight:"); ok {
				set[rule.selector] = value
			}
		}
	}
	weights := ElementWeights()
	want := map[string]string{
		":where(h1,h2,h3,h4,h5,h6)": strconv.Itoa(weights["heading"]),
		":where(.badge)":            strconv.Itoa(weights["badge"]),
	}
	if len(set) != len(want) {
		t.Fatalf("reboot.css sets font-weight on %v, want only %v", set, want)
	}
	for selector, weight := range want {
		if set[selector] != weight {
			t.Errorf("%s: font-weight %q, ElementWeights says %s", selector, set[selector], weight)
		}
	}
	if weights["text"] != 400 || len(weights) != 3 {
		t.Errorf("ElementWeights %v", weights)
	}
}

type ruleText struct{ selector, body string }

// rulesWithBodies lists the style rules of compact CSS with their declarations (at-rules entered).
func rulesWithBodies(css string) []ruleText {
	var out []ruleText
	start := 0
	for i := 0; i < len(css); i++ {
		switch css[i] {
		case '"', '\'':
			i += strings.IndexByte(css[i+1:], css[i]) + 1
		case '{':
			prelude := strings.TrimSpace(css[start:i])
			start = i + 1
			if strings.HasPrefix(prelude, "@") {
				continue
			}
			end := strings.IndexByte(css[i:], '}')
			out = append(out, ruleText{selector: prelude, body: css[i+1 : i+end]})
			i += end
			start = i + 1
		case '}', ';':
			start = i + 1
		}
	}
	return out
}

// compact drops comments and the whitespace CSS does not read, and keeps the spaces it does read.
func TestCompactKeepsWhatCSSReads(t *testing.T) {
	cases := map[string]string{
		"/* a */ :where(a) :where(b) { color: red ; }":                 ":where(a) :where(b){color:red}",
		"@media (min-width: 640px) and (hover: hover) { a { x: y; } }": "@media (min-width:640px) and (hover:hover){a{x:y}}",
		"a { content: \"a  ;  b\"; }":                                  "a{content:\"a  ;  b\"}",
		"*, ::before {\r\n  margin: 0;\r\n}":                           "*,::before{margin:0}",
		"a { background: x 0 50% / 1rem 1rem, y; z: 1px !important; }": "a{background:x 0 50% / 1rem 1rem,y;z:1px!important}",
		"a > b + c { x: calc(1px + 2px); }":                            "a>b + c{x:calc(1px + 2px)}",
	}
	for in, want := range cases {
		if got := string(compact(in)); got != want {
			t.Errorf("compact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// selectorsOf lists the selector of every style rule in compact CSS (at-rule preludes skipped).
func selectorsOf(css string) []string {
	var out []string
	start := 0
	for i := 0; i < len(css); i++ {
		switch css[i] {
		case '"', '\'':
			j := strings.IndexByte(css[i+1:], css[i])
			i += j + 1
		case '{':
			prelude := strings.TrimSpace(css[start:i])
			if !strings.HasPrefix(prelude, "@") {
				out = append(out, prelude)
			}
			start = i + 1
		case '}', ';':
			start = i + 1
		}
	}
	return out
}

// weighed is what of a selector counts for its weight: it removes every :where(…), pseudo-element, *,
// and combinator; what is left weighs.
func weighed(selector string) string {
	var b strings.Builder
	for i := 0; i < len(selector); i++ {
		switch {
		case strings.HasPrefix(selector[i:], ":where("):
			depth := 0
			for j := i + len(":where"); j < len(selector); j++ {
				if selector[j] == '(' {
					depth++
				} else if selector[j] == ')' {
					depth--
					if depth == 0 {
						i = j
						break
					}
				}
			}
		case strings.HasPrefix(selector[i:], "::"):
			j := i + 2
			for j < len(selector) && (selector[j] == '-' || selector[j] >= 'a' && selector[j] <= 'z') {
				j++
			}
			if j < len(selector) && selector[j] == '(' {
				// a pseudo-element's argument (::highlight(kitwork-terminal-caret)) is a name, not a selector:
				// it weighs nothing either
				if end := strings.IndexByte(selector[j:], ')'); end >= 0 {
					j += end + 1
				}
			}
			i = j - 1
		case strings.ContainsRune("* >+~", rune(selector[i])):
		default:
			b.WriteByte(selector[i])
		}
	}
	return b.String()
}

// splitTopLevel splits a selector list at the commas outside parentheses and brackets.
func splitTopLevel(selector string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(selector); i++ {
		switch selector[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, selector[start:i])
				start = i + 1
			}
		}
	}
	return append(out, selector[start:])
}
