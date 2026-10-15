package stylesheet

import (
	"strings"
	"testing"
)

// On a heading, size is a step of the heading scale (KITWORK_UI §7), growing with the screen; the
// class's own rule stays the body step, so one class serves a paragraph and a heading on one page.
func TestHeadingScale(t *testing.T) {
	table := NewTable(nil)
	var r Rules
	class, err := table.Class(&r, Intent{Tag: "heading", Name: "size", Value: "huge"})
	if err != nil || class != "size-huge" {
		t.Fatalf("class %q, %v", class, err)
	}
	css := string(r.CSS())
	for _, want := range []string{
		"\n.size-huge{font-size:1.5rem}\n.size-huge:where(h1,h2,h3,h4,h5,h6){font-size:3rem;letter-spacing:-.05em}\n",
		"@media (min-width:640px){\n.size-huge:where(h1,h2,h3,h4,h5,h6){font-size:3.75rem}\n}",
		"@media (min-width:1024px){\n.size-huge:where(h1,h2,h3,h4,h5,h6){font-size:4.5rem}\n}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("heading huge lacks %q:\n%s", want, css)
		}
	}
	// a width-prefixed size on a heading is the step's largest size from that width on
	var prefixed Rules
	if _, err := table.Class(&prefixed, Intent{Width: "large", Tag: "heading", Name: "size", Value: "huge"}); err != nil {
		t.Fatal(err)
	}
	css = string(prefixed.CSS())
	want := "@media (min-width:1024px){\n.large\\:size-huge{font-size:1.5rem}\n.large\\:size-huge:where(h1,h2,h3,h4,h5,h6){font-size:4.5rem;letter-spacing:-.05em}\n}"
	if !strings.Contains(css, want) || strings.Contains(css, "font-size:3rem") || strings.Contains(css, "3.75rem") {
		t.Errorf("large:size=huge on a heading:\n%s\nwant %q", css, want)
	}
	// every step, its default tracking on the first size
	for word, first := range map[string]string{
		"mini": "font-size:1.125rem;letter-spacing:-.02em", "small": "font-size:1.5rem;letter-spacing:-.03em",
		"medium": "font-size:1.875rem;letter-spacing:-.04em", "large": "font-size:2.25rem;letter-spacing:-.045em",
	} {
		var step Rules
		if _, err := table.Class(&step, Intent{Tag: "heading", Name: "size", Value: word}); err != nil {
			t.Fatal(err)
		}
		if css := string(step.CSS()); !strings.Contains(css, ":where(h1,h2,h3,h4,h5,h6){"+first+"}") {
			t.Errorf("heading %s lacks %q:\n%s", word, first, css)
		}
	}
	// a step the unprefixed size carries to a wider screen comes before a size written for that width:
	// size="medium" small:size="large" is large from small on (medium's own step there would win if
	// the value decided, "large" < "medium")
	var both Rules
	for _, in := range []Intent{{Tag: "heading", Name: "size", Value: "medium"}, {Width: "small", Tag: "heading", Name: "size", Value: "large"}} {
		if _, err := table.Class(&both, in); err != nil {
			t.Fatal(err)
		}
	}
	css = string(both.CSS())
	carried := strings.Index(css, ".size-medium:where(h1,h2,h3,h4,h5,h6){font-size:2.25rem}")
	written := strings.Index(css, ".small\\:size-large:where(h1,h2,h3,h4,h5,h6){font-size:3rem;letter-spacing:-.045em}")
	if carried < 0 || written < 0 || carried > written {
		t.Errorf("from small the written size must come after the step medium carries there (carried %d, written %d):\n%s", carried, written, css)
	}
	// control: on body text huge is the body step alone
	var body Rules
	if _, err := table.Class(&body, Intent{Tag: "text", Name: "size", Value: "huge"}); err != nil {
		t.Fatal(err)
	}
	if css := string(body.CSS()); strings.Contains(css, "h1") || !strings.Contains(css, ".size-huge{font-size:1.5rem}") {
		t.Errorf("size=huge on body text:\n%s", css)
	}
}

// wrap on a text is how its words break (KITWORK_UI §7, "wrap trên chữ"); on a frame the children
// wrap. One class carries both: the frame's rule is the class's own, the text's is scoped to an
// element that lays out no children.
func TestWrapOnText(t *testing.T) {
	table := NewTable(nil)
	cases := map[string]string{
		"true":    ".wrap-true:where(:not(.flex-row,.flex-column,.grid)){text-wrap:wrap}",
		"false":   ".wrap-false:where(:not(.flex-row,.flex-column,.grid)){display:inline-block}",
		"balance": ".wrap-balance{text-wrap:balance}",
		"pretty":  ".wrap-pretty{text-wrap:pretty}",
	}
	for value, want := range cases {
		for _, tag := range []string{"text", "heading"} {
			var r Rules
			if _, err := table.Class(&r, Intent{Tag: tag, Name: "wrap", Value: value}); err != nil {
				t.Fatalf("wrap=%q on %s: %v", value, tag, err)
			}
			if css := string(r.CSS()); !strings.Contains(css, want) {
				t.Errorf("wrap=%q on %s lacks %q:\n%s", value, tag, want, css)
			}
		}
	}
	// control: on a frame wrap is flex-wrap and nothing of text
	var frame Rules
	if _, err := table.Class(&frame, Intent{Tag: "box", Name: "wrap", Value: "true"}); err != nil {
		t.Fatal(err)
	}
	if css := string(frame.CSS()); !strings.Contains(css, ".wrap-true{flex-wrap:wrap}") || strings.Contains(css, "text-wrap") {
		t.Errorf("wrap=true on a frame:\n%s", css)
	}
}

// width="fit" is as wide as what it holds, width="full" the whole width of its parent; the other sizes
// do not take them.
func TestWidthFitAndFull(t *testing.T) {
	table := NewTable(nil)
	for value, want := range map[string]string{"fit": ".width-fit{inline-size:fit-content}", "full": ".width-full{inline-size:100%}"} {
		var r Rules
		if _, err := table.Class(&r, Intent{Name: "width", Value: value}); err != nil {
			t.Fatalf("width=%q: %v", value, err)
		}
		if css := string(r.CSS()); !strings.Contains(css, want) {
			t.Errorf("width=%q lacks %q:\n%s", value, want, css)
		}
	}
	if _, err := table.Class(nil, Intent{Name: "max-width", Value: "fit"}); err == nil {
		t.Errorf("max-width=fit is not a size .ui takes")
	}
}
