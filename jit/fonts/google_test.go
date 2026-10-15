package fonts

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The embedded catalog is Google's whole list, read without an error, every family open source today.
func TestGoogleCatalogReads(t *testing.T) {
	google()
	if googleErr != nil {
		t.Fatalf("google_catalog.json does not read: %v", googleErr)
	}
	if len(googleList) < 1900 {
		t.Fatalf("%d families: the catalog is not Google's list", len(googleList))
	}
	for _, f := range googleList {
		if !slugRe.MatchString(f.Slug) || f.Slug != Slug(f.Name) {
			t.Errorf("%q has the slug %q", f.Name, f.Slug)
		}
		for _, s := range f.Subsets {
			if s == "menu" {
				t.Errorf("%s keeps Google's menu subset", f.Name)
			}
		}
	}
}

// A family is found by its name whatever the case and the spacing, by its slug, by the way an address
// writes it, and in quotes.
func TestFindIgnoresCaseAndSpacing(t *testing.T) {
	for _, name := range []string{"Google Sans Flex", "google sans flex", "GOOGLE  SANS FLEX", "GoogleSansFlex", "google-sans-flex", "Google+Sans+Flex", `"Google Sans Flex"`, " 'google sans flex' "} {
		f, ok := Find(name)
		if !ok || f.Name != "Google Sans Flex" || f.Slug != "google-sans-flex" {
			t.Errorf("Find(%q) = %q %q %v", name, f.Name, f.Slug, ok)
		}
	}
	for _, name := range []string{"Google Sans Flexx", "Itim!", "", "../itim", "itim/../itim"} {
		if f, ok := Find(name); ok {
			t.Errorf("Find(%q) found %q", name, f.Name)
		}
	}
}

// What the compiler checks against: the category and its fallback, the weights (an axis or a static
// list), the language.
func TestFamilyWeightsCategoryAndFallback(t *testing.T) {
	flex, _ := Find(DefaultBody)
	if !flex.Variable() || flex.Category != "Sans Serif" {
		t.Fatalf("%+v", flex)
	}
	if low, high := flex.WeightRange(); low != 1 || high != 1000 {
		t.Errorf("Google Sans Flex weights %d–%d, want 1–1000", low, high)
	}
	if opsz, ok := flex.axis("opsz"); !ok || opsz.Min != 6 || opsz.Max != 144 {
		t.Errorf("Google Sans Flex opsz %+v", opsz)
	}
	if !flex.HasWeight(1) || !flex.HasWeight(1000) || flex.HasWeight(1001) {
		t.Errorf("Google Sans Flex draws 1–1000")
	}
	itim, _ := Find("Itim")
	if itim.Variable() || !reflect.DeepEqual(itim.Weights, []int{400}) || itim.HasWeight(700) || !itim.HasWeight(400) {
		t.Errorf("Itim is static, 400 only: %+v", itim)
	}
	if low, high := itim.WeightRange(); low != 400 || high != 400 {
		t.Errorf("Itim range %d–%d", low, high)
	}
	lato, _ := Find("Lato")
	if lato.Variable() || !reflect.DeepEqual(lato.Weights, []int{100, 300, 400, 700, 900}) || !reflect.DeepEqual(lato.Italics, lato.Weights) || lato.HasWeight(500) {
		t.Errorf("Lato: %+v", lato)
	}
	for name, want := range map[string]string{
		"Google Sans Flex": "ui-sans-serif, system-ui, sans-serif",
		"Lora":             "ui-serif, serif",
		"JetBrains Mono":   "ui-monospace, monospace",
		"Itim":             "cursive",
		"Lobster":          "system-ui, sans-serif",
	} {
		f, ok := Find(name)
		if !ok || f.Fallback() != want {
			t.Errorf("%s (%s): fallback %q, want %q", name, f.Category, f.Fallback(), want)
		}
	}
}

// Vietnamese: Outfit lacks it, Itim has it, Google Sans Flex has it — and an embedded family covers
// what is embedded (Fira Code's cyrillic is Google's, not Kitwork's copy).
func TestSubsetCoverage(t *testing.T) {
	for name, want := range map[string]bool{"Outfit": false, "Itim": true, "Google Sans Flex": true, "Google Sans Code": true, "JetBrains Mono": true, "Fira Code": false} {
		f, ok := Find(name)
		if !ok || f.Covers("vietnamese") != want {
			t.Errorf("%s covers vietnamese: %v, want %v (%v)", name, f.Covers("vietnamese"), want, f.Subsets)
		}
	}
	fira, _ := Find("Fira Code")
	if !fira.Embedded || fira.Covers("cyrillic") || !fira.Covers("latin") {
		t.Errorf("embedded Fira Code ships latin and latin-ext only: %v", fira.Subsets)
	}
	inter, _ := Find("Inter")
	if inter.Embedded || !inter.Covers("cyrillic") || !inter.Covers("vietnamese") {
		t.Errorf("Inter is Google's whole family: %v", inter.Subsets)
	}
}

// The defaults are embedded families: no network, no cache — Google Sans Flex for text, Google Sans
// Code, a Monospace family, for code. JetBrains Mono, the code face before, stays embedded.
func TestDefaultsAreEmbedded(t *testing.T) {
	if DefaultBody != "Google Sans Flex" || DefaultCode != "Google Sans Code" {
		t.Fatalf("the defaults are Google Sans Flex and Google Sans Code, got %q and %q", DefaultBody, DefaultCode)
	}
	if code, _ := Find(DefaultCode); code.Category != "Monospace" {
		t.Errorf("the code face is a Monospace family: %+v", code)
	}
	for name, axis := range map[string][2]int{DefaultBody: {1, 1000}, DefaultCode: {300, 800}, "JetBrains Mono": {100, 800}} {
		f, ok := Find(name)
		if !ok || !f.Embedded || !f.Variable() || !f.OpenSource {
			t.Fatalf("%s: %+v", name, f)
		}
		if low, high := f.WeightRange(); low != axis[0] || high != axis[1] {
			t.Errorf("%s weights %d–%d, want %v", name, low, high, axis)
		}
		if !reflect.DeepEqual(f.Subsets, []string{"latin", "latin-ext", "vietnamese"}) {
			t.Errorf("%s embeds %v", name, f.Subsets)
		}
		if len(f.Italics) != 0 {
			t.Errorf("%s embeds no italic, but says %v", name, f.Italics)
		}
	}
	for _, name := range []string{"Itim", "Inter", "Roboto"} {
		if f, _ := Find(name); f.Embedded {
			t.Errorf("%s is not embedded", name)
		}
	}
}

// Embedding the defaults changed no template page: the template path (Render) and Lookup know Outfit
// and Fira Code by name, as before, and none of the families embedded since — a template page that
// names Google Sans Code, Google Sans Flex or JetBrains Mono keeps what it had. A Kitwork UI page
// reaches them through Find, FaceCSS and PreloadFace (work/router_ui.go). CONTROL: Outfit still is.
func TestEmbeddingTheDefaultsChangesNoPage(t *testing.T) {
	for _, page := range []string{
		`<html><head><style>code{font-family: "JetBrains Mono", monospace}</style></head><body><code>x</code></body></html>`,
		`<html><head><style>code{font-family: "Google Sans Code", monospace}</style></head><body><code>x</code></body></html>`,
		`<html><head></head><body><p class="font-google-sans-flex">x</p></body></html>`,
		`<html><head></head><body><code class="font-google-sans-code">x</code></body></html>`,
	} {
		if got := Render(page); got != page {
			t.Errorf("the page changed:\n%s\n%s", page, got)
		}
	}
	for _, name := range []string{DefaultBody, DefaultCode, "JetBrains Mono"} {
		if slug, ok := Lookup(name); ok {
			t.Errorf("Lookup(%q) = %s: the template path would know it", name, slug)
		}
	}
	if slug, ok := Lookup("Outfit"); !ok || slug != "outfit" || !strings.Contains(Render(`<head></head><p class="font-outfit">x</p>`), "@font-face{font-family:'Outfit'") {
		t.Errorf("Outfit is found by name as before")
	}
}

// A vendored face that is a whole weight range (Google's answer to wght@1..1000) is written with its
// range, and the face preloaded is the latin one that draws 400 — not the first subset listed.
func TestVendoredRangeFaces(t *testing.T) {
	css := CSS([]string{"google-sans-flex"}, nil)
	if strings.Count(css, "font-weight:1 1000;") != 3 {
		t.Errorf("one face per subset, written with its range:\n%s", css)
	}
	if preload := Preload("google-sans-flex"); !strings.Contains(preload, "/_kitwork/fonts/google-sans-flex-1-1000-latin.") {
		t.Errorf("the latin face is the one preloaded: %s", preload)
	}
	faces := Faces("jetbrains-mono", []int{400, 700})
	if strings.Count(faces, "font-weight:400;") != 3 || strings.Count(faces, "font-weight:700;") != 3 || strings.Count(faces, "jetbrains-mono-100-800-latin.") != 2 {
		t.Errorf("Faces points every weight at the subset's one file:\n%s", faces)
	}
}

// An unknown name gets up to three close names; asking for a subset ranks the families that cover it
// first. CONTROL: a name close to nothing gets none.
func TestSuggestTheClosestNames(t *testing.T) {
	for typed, want := range map[string]string{"Itum": "Itim", "Interr": "Inter", "Robto": "Roboto", "Monserat": "Montserrat", "nunito sanz": "Nunito Sans", "Be Vietnam": "Be Vietnam Pro"} {
		got := Suggest(typed, "")
		if len(got) == 0 || got[0] != want || len(got) > 3 {
			t.Errorf("Suggest(%q) = %v, want %s first", typed, got, want)
		}
	}
	if got := Suggest("google sans", ""); !contains(got, "Google Sans Flex") {
		t.Errorf("a name that starts what was typed: %v", got)
	}
	plain, vietnamese := Suggest("Mont", ""), Suggest("Mont", "vietnamese")
	if len(plain) == 0 || plain[0] != "Montaga" {
		t.Fatalf("Suggest(Mont) = %v", plain)
	}
	if contains(vietnamese, "Montaga") || !contains(vietnamese, "Montserrat") {
		t.Errorf("for a Vietnamese site the families covering it come first: %v", vietnamese)
	}
	for _, name := range vietnamese {
		if f, _ := Find(name); !f.Covers("vietnamese") {
			t.Errorf("%s does not cover vietnamese: %v", name, vietnamese)
		}
	}
	if got := Suggest("Zqxjvkwy Plorbn", ""); got != nil {
		t.Errorf("nothing is close: %v", got)
	}
	err := unknownFamily("Itum")
	if err == nil || !strings.Contains(err.Error(), `"Itum" không có trong Google Fonts`) || !strings.Contains(err.Error(), `"Itim"`) {
		t.Errorf("the error names the family and the close one: %v", err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// A Family lists the axes of the files Kitwork serves — wght, and opsz with it — not Google's whole
// variable font: Google Sans Flex's wdth, slnt, GRAD and ROND stay at their defaults in the file
// Kitwork embeds, Google Sans Code's MONO at its 1 (monospaced), and Roboto Flex's XTRA and YT* in the
// file Ensure downloads. A family with opsz and no weight axis is asked for as wght@400, so it is
// served at its default optical size.
func TestAxesAreTheServedOnes(t *testing.T) {
	for name, want := range map[string][]Axis{
		DefaultBody:      {{"opsz", 6, 144}, {"wght", 1, 1000}},
		"Roboto Flex":    {{"opsz", 8, 144}, {"wght", 100, 1000}},
		DefaultCode:      {{"wght", 300, 800}},
		"JetBrains Mono": {{"wght", 100, 800}},
		"Jaro":           nil,
		"Kablammo":       nil,
	} {
		f, ok := Find(name)
		if !ok || !reflect.DeepEqual(f.Axes, want) {
			t.Errorf("%s: axes %v, want %v", name, f.Axes, want)
		}
	}
}

// FONTS_LICENSE carries what OFL 1.1 asks every copy of the fonts to carry: each embedded family's
// copyright line and the license itself, in full.
func TestFontsLicenseCarriesTheOFL(t *testing.T) {
	data, err := os.ReadFile("FONTS_LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"SIL OPEN FONT LICENSE Version 1.1 - 26 February 2007", "PERMISSION & CONDITIONS",
		"5) The Font Software, modified or unmodified, in part or in whole,", "TERMINATION", "DISCLAIMER"} {
		if !strings.Contains(text, want) {
			t.Errorf("FONTS_LICENSE lacks %q", want)
		}
	}
	for _, fam := range catalog {
		if !regexp.MustCompile(`(?m)^- ` + regexp.QuoteMeta(fam.name) + ` +— SIL Open Font License 1\.1 — Copyright \d{4}`).MatchString(text) {
			t.Errorf("FONTS_LICENSE has no copyright line for %s", fam.name)
		}
	}
}
