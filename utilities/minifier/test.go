package minifier

import (
	"strings"
	"testing"
)

// These assertions hold for BOTH builds (default tdewolff and `-tags stdminify`): HTML
// whitespace/comment collapse and CSS comment/whitespace stripping are common to both. JS/JSON/
// SVG/XML differ (tdewolff minifies, the stdlib build passes them through) and are asserted in the
// build-tagged tdewolff_test.go instead.

func TestHTML(t *testing.T) {
	in := "<div>   <p>hi</p>   <!-- note -->\n\n   <span>x</span>   </div>"
	out := HTML(in)

	if strings.Contains(out, "<!--") {
		t.Errorf("HTML comment not removed: %q", out)
	}
	if strings.Contains(out, "  ") {
		t.Errorf("insignificant whitespace not collapsed: %q", out)
	}
	if len(out) >= len(in) {
		t.Errorf("output not smaller: in=%d out=%d", len(in), len(out))
	}
	if !strings.Contains(out, "hi") || !strings.Contains(out, "<span>x</span>") {
		t.Errorf("content lost: %q", out)
	}
}

func TestCSS(t *testing.T) {
	in := "body {  /* c */  color : red ;  margin : 0 1px 2px ;  }"
	out := CSS(in)

	if strings.Contains(out, "/*") {
		t.Errorf("CSS comment not removed: %q", out)
	}
	if !strings.Contains(out, "color:red") {
		t.Errorf("expected collapsed `color:red`, got %q", out)
	}
	if !strings.Contains(out, "0 1px 2px") {
		t.Errorf("value spacing was destroyed: %q", out)
	}
}

func TestTypeDispatch(t *testing.T) {
	css := "a {  color : red ;  }"

	if Type("css", css) != CSS(css) {
		t.Errorf("Type(\"css\") must equal CSS()")
	}
	if got := Type("totally-unknown", css); got != css {
		t.Errorf("unknown type should return input unchanged, got %q", got)
	}
}

func TestInlineStyleInHTML(t *testing.T) {
	in := "<html><head><style>  body {  /* x */  color : red ;  }  </style></head><body>hi</body></html>"
	out := HTML(in)

	if strings.Contains(out, "/*") {
		t.Errorf("inline <style> CSS comment not removed: %q", out)
	}
	if !strings.Contains(out, "color:red") {
		t.Errorf("inline <style> CSS not minified: %q", out)
	}
}
