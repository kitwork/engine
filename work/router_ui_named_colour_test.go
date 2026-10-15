package work

import (
	"net/http"
	"strings"
	"testing"
)

// A named colour through a whole site: router.colors({ brand: "cerulean" }) is the brand #007ba7 in
// colors.<hash>.css with its on and its dark grown from it, and markup names colours by name —
// gold on navy, burnt-orange at 20% — as fixed colours in page.<hash>.css. A branch that seeds a role
// with a name that is not one (navy-blue) answers the error with the name to write, while developing.
// CONTROL: the site's own page, above the branch, keeps its cerulean brand.
func TestUISiteNamedColours(t *testing.T) {
	files := withRootRouter(t,
		`router.colors({ brand: "cerulean", canvas: { light: "ivory", dark: "#141110" } });`,
		map[string]string{
			"localhost/content.ui": `<content><box background="navy"><text color="gold">Cà phê</text></box><box background="burnt-orange/20"><text color="brand+15">Trà</text></box></content>`,
			"localhost/quan-ly/router.work": `import { router } from "kitwork";
router.colors({ brand: "navy-blue" });`,
			"localhost/quan-ly/content.ui": `<content><text>Quản lý</text></content>`,
		})
	tenant := buildUISite(t, files)

	home := pageWithStylesheet(t, tenant, "http://localhost/")
	for _, want := range []string{
		"--brand:#007ba7", "--canvas:#fffff0", "--canvas:#141110", "--brand-on:", darkBlock,
		`class="flex-column background-navy"`, `.background-navy{background-color:#000080}`, `class="color-gold"`, `.color-gold{color:#ffd700}`,
		`class="flex-column background-burnt-orange/20"`, `.background-burnt-orange\/20{background-color:color-mix(in srgb,#be5103 20%,transparent)}`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("/ is missing %q", want)
		}
	}
	if block := linesWith(home, darkBlock); !strings.Contains(block, "--brand:") {
		t.Errorf("the brand seeded by a name grows its dark like a hex; dark block:\n%s", block)
	}
	if strings.Contains(home, "--gold") || strings.Contains(home, "var(--navy") || strings.Contains(home, "var(--burnt-orange") {
		t.Error("a named colour in markup is no token of the palette")
	}

	branch := serveUI(t, tenant, "http://localhost/quan-ly")
	if body := branch.Body.String(); branch.Code != http.StatusInternalServerError ||
		!strings.Contains(body, `router.colors: "brand" là "navy-blue": navy-blue không phải tên màu: đó là tên Figma đặt cho navy (#000080) của CSS; viết navy`) {
		t.Errorf("/quan-ly = %d %q, want the error naming navy", branch.Code, body)
	}
}
