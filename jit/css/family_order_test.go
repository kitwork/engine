package css

import (
	"strings"
	"testing"
)

// A side utility must sit after its axis, and the axis after the whole, so the most specific
// one written wins — `px-4 pl-11` pads the left by pl-11. The sheet used to be alphabetical,
// which put pl-11 before px-4 and let px-4 win: the data-table's filter icon sat on its text.
func TestShorthandFamiliesOrderWholeAxisSide(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<div class="pl-11 px-4 p-2 ml-0 mx-auto -mt-1 -m-2 border-b border-x-0 border top-0 inset-x-0 inset-0 rounded-tl-none rounded-t-xl rounded-2xl gap-x-2 gap-4 overflow-x-auto overflow-hidden hover:pl-2 hover:px-1 md:pt-0 md:py-2 duration-500 ease-out delay-100 transition-transform"></div>`, &cfg)
	before := func(earlier, later string) {
		t.Helper()
		i, j := strings.Index(out, earlier), strings.Index(out, later)
		if i < 0 || j < 0 {
			t.Fatalf("missing %q (%d) or %q (%d) in:\n%s", earlier, i, later, j, out)
		}
		if i > j {
			t.Errorf("%s should come before %s:\n%s", earlier, later, out)
		}
	}
	before(".p-2 {", ".px-4 {")
	before(".px-4 {", ".pl-11 {")
	before(`.\-m-2 {`, ".mx-auto {")
	before(".mx-auto {", ".ml-0 {")
	before(".mx-auto {", `.\-mt-1 {`)
	before(".border {", ".border-x-0 {")
	before(".border-x-0 {", ".border-b {")
	before(".inset-0 {", ".inset-x-0 {")
	before(".inset-x-0 {", ".top-0 {")
	before(".rounded-2xl {", ".rounded-t-xl {")
	before(".rounded-t-xl {", ".rounded-tl-none {")
	before(".gap-4 {", ".gap-x-2 {")
	before(".overflow-hidden {", ".overflow-x-auto {")
	// transition-transform carries its own 150ms; duration-500, ease-out and delay-100 must beat it.
	before(".transition-transform {", ".duration-500 {")
	before(".transition-transform {", ".ease-out {")
	before(".transition-transform {", ".delay-100 {")
	// Variants ride along: the rank is read off the core.
	before(`.hover\:px-1:hover {`, `.hover\:pl-2:hover {`)
	before(`.md\:py-2 {`, `.md\:pt-0 {`)
}

// CONTROL: a class outside every family keeps its alphabetical place, and a name that merely
// starts with a family letter (pointer-events, max-w, text-top) is not pulled onto the ladder.
func TestShorthandFamiliesLeaveOthersAlone(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<div class="pointer-events-none px-4 max-w-md mx-auto text-top top-0 block"></div>`, &cfg)
	for _, pair := range [][2]string{{".block {", ".px-4 {"}, {".max-w-md {", ".mx-auto {"}, {".pointer-events-none {", ".px-4 {"}} {
		if i, j := strings.Index(out, pair[0]), strings.Index(out, pair[1]); i < 0 || j < 0 || i > j {
			t.Errorf("%s (%d) should stay before %s (%d):\n%s", pair[0], i, pair[1], j, out)
		}
	}
	if familyRank("pointer-events-none", &cfg) != 0 || familyRank("max-w-md", &cfg) != 0 || familyRank("text-top", &cfg) != 0 {
		t.Error("a look-alike class was ranked into a family")
	}
	if familyRank("hover:-ml-2", &cfg) != 3 || familyRank("md:px-4", &cfg) != 2 || familyRank("p-2", &cfg) != 1 {
		t.Error("the ladder is misread")
	}
}
