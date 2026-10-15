package fonts

import (
	"strings"
	"testing"
)

// A Kitwork UI page ships the weights its tree uses (Faces), not every face vendored: Outfit 600 is
// used by every heading and was never shipped, 300 and 900 were shipped and never used.
func TestFacesShipExactlyTheWeightsGiven(t *testing.T) {
	css := Faces("outfit", []int{700, 400, 600, 400})
	for _, weight := range []string{"400", "600", "700"} {
		if n := strings.Count(css, "font-weight:"+weight+";"); n != 2 {
			t.Errorf("weight %s: %d faces, want one per subset (latin-ext, latin):\n%s", weight, n, css)
		}
	}
	for _, weight := range []string{"300", "500", "900"} {
		if strings.Contains(css, "font-weight:"+weight+";") {
			t.Errorf("weight %s is shipped but not asked for:\n%s", weight, css)
		}
	}
	// ascending, whatever order the weights came in
	if strings.Index(css, "font-weight:400;") > strings.Index(css, "font-weight:600;") ||
		strings.Index(css, "font-weight:600;") > strings.Index(css, "font-weight:700;") {
		t.Errorf("weights out of order:\n%s", css)
	}
	// every weight of a subset is one file, so the browser fetches it once
	latin := routePath("families/outfit/400-latin.woff2")
	if latin == "" || strings.Count(css, "url("+latin+")") != 3 {
		t.Errorf("the latin subset should be one URL for all three weights (%s):\n%s", latin, css)
	}
	if strings.Contains(css, "700-latin.") || strings.Contains(css, "600-latin.") {
		t.Errorf("a weight points at its own copy of the same bytes:\n%s", css)
	}
}

// A weight on the axis of a variable font is shipped even when Google's CSS did not list it (Fira
// Code 700, bold code); one off the axis is left to the browser.
func TestFacesFollowTheWeightAxis(t *testing.T) {
	if !Covers("fira-code", 700) || Covers("fira-code", 800) || Covers("fira-code", 200) {
		t.Errorf("fira-code covers 300–700")
	}
	css := Faces("fira-code", []int{400, 700, 800})
	if !strings.Contains(css, "font-weight:700;") || strings.Contains(css, "font-weight:800;") {
		t.Errorf("fira-code 700 shipped, 800 not:\n%s", css)
	}
	if Faces("not-a-font", []int{400}) != "" {
		t.Errorf("an unknown family ships nothing")
	}
	// control: the template pages' CSS still ships every vendored face, each at its own file
	all := CSS([]string{"outfit"}, nil)
	if !strings.Contains(all, "font-weight:300;") || !strings.Contains(all, "font-weight:900;") || !strings.Contains(all, "700-latin.") {
		t.Errorf("CSS changed for template pages:\n%s", all)
	}
}

func TestLookupByTheNameRouterFontsWrites(t *testing.T) {
	for name, want := range map[string]string{"Outfit": "outfit", "Fira Code": "fira-code", "'Fira Code'": "fira-code", "outfit": "outfit"} {
		if got, ok := Lookup(name); !ok || got != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	if _, ok := Lookup("Inter"); ok {
		t.Errorf("Inter is not vendored")
	}
	if !strings.Contains(Preload("outfit"), `rel="preload"`) || Preload("not-a-font") != "" {
		t.Errorf("Preload: %q", Preload("outfit"))
	}
}
