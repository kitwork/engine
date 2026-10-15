package fonts

import (
	"reflect"
	"strings"
	"testing"
)

// The address Google gives for several families at once, css2 form: the families come back in order,
// spelled as Google spells them, with what each asks for.
func TestParseURLReadsEveryFamily(t *testing.T) {
	const address = `https://fonts.googleapis.com/css2?family=Google+Sans+Flex:opsz,wght@6..144,1..1000&family=Isometra&family=Itim&family=Playwrite+CA+Guides&family=Sree+Krushnadevaraya&display=swap`
	got, err := ParseURL(address)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
		if !p.Known {
			t.Errorf("%s is in the catalog", p.Name)
		}
	}
	if want := []string{"Google Sans Flex", "Isometra", "Itim", "Playwrite CA Guides", "Sree Krushnadevaraya"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	if flex := got[0]; !reflect.DeepEqual(flex.Axes, []string{"opsz", "wght"}) || flex.Weights != nil || flex.Italic {
		t.Errorf("Google Sans Flex asks for the opsz and wght ranges: %+v", flex)
	}
	if line := RouterFontsLine(names); line != `router.fonts({ body: "Google Sans Flex", isometra: "Isometra", handwriting: "Itim", playwriteCaGuides: "Playwrite CA Guides", sreeKrushnadevaraya: "Sree Krushnadevaraya" })` {
		t.Errorf("router.fonts line: %s", line)
	}
	// the same address inside the <link> Google hands out, &amp; and all
	link := `<link href="` + strings.ReplaceAll(address, "&", "&amp;") + `" rel="stylesheet">`
	again, err := ParseURL(link)
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Errorf("the <link> form reads the same: %v %+v", err, again)
	}
}

// The other forms: static weights and italics in css2, the old css form with | and :400,700i, an
// @import, a specimen page, a share link; a family named twice is one. The role line puts code and
// handwriting where they belong.
func TestParseURLForms(t *testing.T) {
	for text, want := range map[string][]Pasted{
		`https://fonts.googleapis.com/css2?family=Inter:ital,wght@0,400;0,700;1,400&display=swap`: {
			{Name: "Inter", Known: true, Axes: []string{"ital", "wght"}, Weights: []int{400, 700}, Italic: true}},
		`https://fonts.googleapis.com/css?family=Roboto:300,400,700i|Open+Sans&subset=vietnamese`: {
			{Name: "Roboto", Known: true, Weights: []int{300, 400, 700}, Italic: true},
			{Name: "Open Sans", Known: true}},
		`@import url('https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@100..800&family=jetbrains+mono&display=swap');`: {
			{Name: "JetBrains Mono", Known: true, Axes: []string{"wght"}}},
		`https://fonts.google.com/specimen/Be+Vietnam+Pro?query=be`: {{Name: "Be Vietnam Pro", Known: true}},
		`https://fonts.google.com/share?selection.family=Itim|Lato:ital,wght@0,700;1,700`: {
			{Name: "Itim", Known: true},
			{Name: "Lato", Known: true, Axes: []string{"ital", "wght"}, Weights: []int{700}, Italic: true}},
		`//fonts.googleapis.com/css2?family=Not+A+Real+Family`: {{Name: "Not A Real Family"}},
	} {
		got, err := ParseURL(text)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s\n got %+v %v\nwant %+v", text, got, err, want)
		}
	}
	if line := RouterFontsLine([]string{"jetbrains mono", "google sans flex", "itim", "Fira Code"}); line != `router.fonts({ code: "JetBrains Mono", body: "Google Sans Flex", handwriting: "Itim", firaCode: "Fira Code" })` {
		t.Errorf("roles by category: %s", line)
	}
}

// What is not a Google Fonts address says so, in Vietnamese, with what to paste instead.
func TestParseURLRefusesWhatIsNotGoogleFonts(t *testing.T) {
	for text, want := range map[string]string{
		`https://example.com/fonts.css`:                           "không thấy địa chỉ Google Fonts",
		`font-family: Itim`:                                       "không thấy địa chỉ Google Fonts",
		`https://fonts.googleapis.com/icon?family=Material+Icons`: "không phải địa chỉ CSS của Google Fonts",
		`https://fonts.googleapis.com/css2?display=swap`:          "không có họ chữ nào",
	} {
		if _, err := ParseURL(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", text, err, want)
		}
	}
}

// Text pasted before the address does not move it: letters that change length when lowercased ('Ⱥ' is
// two bytes, 'ⱥ' three) and bytes that are not UTF-8 (a Latin-1 'é') are passed over, and the address is
// read as written.
func TestParseURLAfterAnyText(t *testing.T) {
	for _, prefix := range []string{strings.Repeat("Ⱥ", 40), strings.Repeat("Ⱥ", 7), strings.Repeat("\xe9", 30), "Đây là phông chữ: "} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%d bytes of %q before the address: panic %v", len(prefix), prefix[:2], r)
				}
			}()
			got, err := ParseURL(prefix + " https://fonts.googleapis.com/css2?family=Itim&display=swap")
			if err != nil || len(got) != 1 || got[0].Name != "Itim" {
				t.Errorf("%d bytes of %q before the address: %+v %v", len(prefix), prefix[:2], got, err)
			}
		}()
	}
}

// & in a <link> written as a number — &#038; as WordPress writes it, &#38;, &#x26; — or as &AMP; is read
// as &: the families after it are not lost.
func TestParseURLNumericAmpersands(t *testing.T) {
	for text, want := range map[string][]string{
		`<link rel='stylesheet' href='https://fonts.googleapis.com/css?subset=latin&#038;family=Open+Sans%3A400%2C700' type='text/css' media='all' />`: {"Open Sans"},
		`<link href="https://fonts.googleapis.com/css2?family=Roboto&#038;family=Open+Sans&#38;display=swap">`:                                         {"Roboto", "Open Sans"},
		`<link href="https://fonts.googleapis.com/css2?family=Roboto&#x26;family=Open+Sans&AMP;display=swap">`:                                         {"Roboto", "Open Sans"},
	} {
		got, err := ParseURL(text)
		var names []string
		for _, p := range got {
			names = append(names, p.Name)
		}
		if err != nil || !reflect.DeepEqual(names, want) {
			t.Errorf("%s:\n got %v %v, want %v", text, names, err, want)
		}
	}
}

// RouterFontsLine writes a family named twice once, and never the same key twice: a second family that
// would take a key already written gets a number. CONTROL: a key that does not start with a letter is
// quoted.
func TestRouterFontsLineWritesEachKeyOnce(t *testing.T) {
	if line := RouterFontsLine([]string{"Inter", "inter", "INTER"}); line != `router.fonts({ body: "Inter" })` {
		t.Errorf("a family named three times: %s", line)
	}
	if line := RouterFontsLine([]string{"Inter", "Roboto", "Body", "42 Things"}); line != `router.fonts({ body: "Inter", roboto: "Roboto", body2: "Body", "42Things": "42 Things" })` {
		t.Errorf("keys taken: %s", line)
	}
}
