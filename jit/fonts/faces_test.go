package fonts

import (
	"fmt"
	"strings"
	"testing"
)

// testFace is one @font-face of a css2 answer built for a test.
type testFace struct {
	comment string // the subset comment Google writes before it; "" for a slice Google leaves unnamed
	style   string
	weight  int
	unicode string
}

const (
	latinRange      = "U+0000-00FF, U+0131, U+0152-0153"
	latinExtRange   = "U+0100-02BA, U+02BD-02C5"
	vietnameseRange = "U+0102-0103, U+0110-0111, U+1EA0-1EF9"
)

// googleCSS is a css2 answer shaped as Google writes one: each face's file on fonts.gstatic.com.
func googleCSS(family string, faces ...testFace) string {
	var b strings.Builder
	for i, f := range faces {
		if f.comment != "" {
			b.WriteString("/* " + f.comment + " */\n")
		}
		fmt.Fprintf(&b, "@font-face {\n  font-family: '%s';\n  font-style: %s;\n  font-weight: %d;\n  font-display: swap;\n"+
			"  src: url(https://fonts.gstatic.com/s/%s/v1/%d.woff2) format('woff2');\n  unicode-range: %s;\n}\n",
			family, f.style, f.weight, Slug(family), i, f.unicode)
	}
	return b.String()
}

// latoCSS is Lato as Google answers ital,wght@0,100;…;1,900: every weight, upright and italic,
// latin-ext and latin.
func latoCSS() string {
	var faces []testFace
	for _, style := range []string{"normal", "italic"} {
		for _, w := range []int{100, 300, 400, 700, 900} {
			faces = append(faces, testFace{"latin-ext", style, w, latinExtRange}, testFace{"latin", style, w, latinRange})
		}
	}
	return googleCSS("Lato", faces...)
}

// hrefOf is the address a <link rel="preload"> names.
func hrefOf(link string) string {
	_, after, _ := strings.Cut(link, `href="`)
	href, _, _ := strings.Cut(after, `"`)
	return href
}

// A family with no upright style (Molle: italic 400 only) ships its italic faces for upright text —
// the browser draws upright text from them, as it would from Google's own CSS — and preloads one.
func TestItalicOnlyFamilyShipsItsItalic(t *testing.T) {
	g := newFakeGoogle(t)
	g.set(func(g *fakeGoogle) {
		g.css["Molle"] = googleCSS("Molle", testFace{"latin-ext", "italic", 400, latinExtRange}, testFace{"latin", "italic", 400, latinRange})
	})
	if err := Ensure("Molle"); err != nil {
		t.Fatal(err)
	}
	if q := g.query(0); q != "family=Molle:ital,wght@1,400&display=swap" {
		t.Errorf("css2 asked for %q", q)
	}
	for _, need := range []Need{{Weights: []int{400}}, {}, {Weights: []int{400}, Subsets: []string{"latin", "latin-ext", "vietnamese"}}} {
		css, err := FaceCSS("Molle", need)
		if err != nil || len(served(css)) != 2 || strings.Count(css, "font-style:italic;font-weight:400;") != 2 {
			t.Errorf("%+v: the italic faces ship for upright text: %v\n%s", need, err, css)
		}
	}
	if preload := PreloadFace("Molle", Need{Weights: []int{400}}); !strings.Contains(preload, `href="/_kitwork/fonts/molle-400-latin-italic.`) {
		t.Errorf("preload the latin italic face: %q", preload)
	}
}

// The face preloaded is one the stylesheet names for the same Need: the latin face of the page's first
// weight — a weight the family lacks drawn from the face CSS matching picks. CONTROL: no weight named
// preloads 400.
func TestPreloadIsAFaceTheStylesheetNames(t *testing.T) {
	g := newFakeGoogle(t)
	g.set(func(g *fakeGoogle) { g.css["Lato"] = latoCSS() })
	if err := Ensure("Lato"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		need  Need
		files []string // the faces the stylesheet names
		first string   // the face preloaded
	}{
		{Need{Weights: []int{300, 700}, Subsets: []string{"latin"}}, []string{"lato-300-latin.", "lato-700-latin."}, "lato-300-latin."},
		{Need{Weights: []int{700, 300}, Subsets: []string{"latin"}}, []string{"lato-300-latin.", "lato-700-latin."}, "lato-700-latin."},
		{Need{Weights: []int{600}, Subsets: []string{"latin", "latin-ext"}}, []string{"lato-700-latin-ext.", "lato-700-latin."}, "lato-700-latin."},
		{Need{}, []string{"lato-400-latin-ext.", "lato-400-latin."}, "lato-400-latin."},
	} {
		css, err := FaceCSS("Lato", c.need)
		if err != nil {
			t.Fatal(err)
		}
		srcs := served(css)
		if len(srcs) != len(c.files) {
			t.Errorf("%+v ships %v, want %v", c.need, srcs, c.files)
			continue
		}
		for i, file := range c.files {
			if !strings.Contains(srcs[i], "/"+file) {
				t.Errorf("%+v ships %v, want %v", c.need, srcs, c.files)
			}
		}
		href := hrefOf(PreloadFace("Lato", c.need))
		if !contains(srcs, href) || !strings.Contains(href, "/"+c.first) {
			t.Errorf("%+v preloads %q, want the %s face of %v", c.need, href, c.first, srcs)
		}
	}
}

// A static family ships, for a weight it lacks, the face CSS font matching draws it from (CSS Fonts 4
// §5.2) — not merely the nearest one.
func TestNearestWeightIsCSSFontMatching(t *testing.T) {
	for _, c := range []struct {
		have      []int
		want, got int
	}{
		{[]int{400, 900}, 600, 900}, // over 500: the heavier ones first
		{[]int{100, 400}, 300, 100}, // under 400: the lighter ones first
		{[]int{400, 500}, 450, 500}, // 400–500: up to 500 first
		{[]int{300, 700}, 500, 300}, // 400–500 with nothing up to 500: the lighter ones next
		{[]int{600, 900}, 450, 600}, // then those over 500, lightest first
		{[]int{100, 300, 400, 700, 900}, 600, 700},
		{[]int{100, 300, 400, 700, 900}, 200, 100},
		{[]int{400}, 400, 400},
	} {
		if got, ok := nearestWeight(c.have, c.want); !ok || got != c.got {
			t.Errorf("%v for %d: %d, CSS draws it from %d", c.have, c.want, got, c.got)
		}
	}
	if _, ok := nearestWeight(nil, 400); ok {
		t.Errorf("no weight, no face")
	}
}

// Google cuts emoji into numbered slices with no comment, as it cuts CJK scripts. The slices are named
// by the subset they are cut from and ship with it: emoji with emoji, never with Japanese.
func TestEmojiSlicesShipWithEmoji(t *testing.T) {
	g := newFakeGoogle(t)
	slices := []testFace{
		{"", "normal", 400, "U+1F300-1F3FF"},
		{"", "normal", 400, "U+1F400-1F4FF"},
		{"", "normal", 400, "U+1F500-1F5FF"},
	}
	g.set(func(g *fakeGoogle) {
		g.css["Kablammo"] = googleCSS("Kablammo", append(append([]testFace(nil), slices...),
			testFace{"vietnamese", "normal", 400, vietnameseRange}, testFace{"latin-ext", "normal", 400, latinExtRange},
			testFace{"latin", "normal", 400, latinRange})...)
		g.css["Noto Color Emoji"] = googleCSS("Noto Color Emoji", slices...)
	})
	for _, name := range []string{"Kablammo", "Noto Color Emoji"} {
		if err := Ensure(name); err != nil {
			t.Fatal(err)
		}
	}
	if !contains(g.entries(), "kablammo/400-emoji-slice-0.woff2") {
		t.Errorf("an emoji slice is named by its subset: %v", g.entries())
	}
	count := func(name string, subsets ...string) int {
		css, err := FaceCSS(name, Need{Subsets: subsets})
		if err != nil {
			t.Fatal(err)
		}
		return len(served(css))
	}
	for _, c := range []struct {
		name    string
		subsets []string
		want    int
	}{
		{"Kablammo", []string{"latin", "emoji"}, 4},
		{"Kablammo", []string{"latin", "japanese"}, 1},
		{"Noto Color Emoji", []string{"emoji"}, 3},
		{"Noto Color Emoji", []string{"latin", "japanese"}, 0},
	} {
		if got := count(c.name, c.subsets...); got != c.want {
			t.Errorf("%s for %v: %d faces, want %d", c.name, c.subsets, got, c.want)
		}
	}
	// CONTROL: a CJK slice ships with its own script, and not with another
	jp := rule{style: "normal", subset: "japanese-slice-0", unicode: "U+4e00-4e0f"}
	if !(Need{Subsets: []string{"japanese"}}).ships(jp) || (Need{Subsets: []string{"korean"}}).ships(jp) {
		t.Errorf("a Japanese slice ships with japanese only")
	}
}
