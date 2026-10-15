package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
)

// A site written in Kitwork UI is marked by router.work (work.RouterMarker) and the host serves it
// like any other. Until 15/10/2026 check and profile looked for router.kitwork.js only, so
// `go run . check` said "ok" over a tree of .ui sites it had never opened, and `profile` listed
// none of their routers. Every layout is tried, because each had its own discovery.
func TestCheckAndProfileFindAKitworkUISite(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(root, "acme")
	site := filepath.Join(identity, "shop.example")
	write := func(relative, body string) {
		t.Helper()
		file := filepath.Join(site, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("router.work", `import { router } from "kitwork";
router.colors({ brand: "#c8742c" });
router.view((ctx) => {
  const title = "Thực đơn";
  ctx.title(title);
});`)
	write("page.ui", `<page><content /></page>`)
	write("content.ui", `<content><text heading="1">{{ title }}</text></content>`)
	write("mon/{slug}/router.work", `import { router } from "kitwork";
router.view((ctx) => {
  const name = ctx.params("slug");
});`)
	write("mon/{slug}/content.ui", `<content><text>{{ name }}</text></content>`)

	assertOneValidSite := func(label string, report CheckReport) {
		t.Helper()
		if !report.OK() {
			for _, issue := range report.Issues {
				t.Log(issue.Error())
			}
			t.Fatalf("%s: check issues = %d", label, len(report.Issues))
		}
		if report.Sites != 1 || report.Valid != 1 {
			t.Fatalf("%s: report = %+v, want one site, prepared", label, report)
		}
		// The two router.work files are the site's programs; the compatibility pass compiles them.
		if report.Programs != 2 || report.Compatible != 2 {
			t.Fatalf("%s: compatibility = %d/%d, want 2/2", label, report.Compatible, report.Programs)
		}
	}
	fromRoot := []string{"acme/shop.example/mon/{slug}/router.work", "acme/shop.example/router.work"}
	for _, layout := range []work.RootLayout{work.RootLayoutAuto, work.RootLayoutMultiTenant} {
		assertOneValidSite(layout.String(), CheckWithLayout(root, 100_000, layout))
		assertProfileFiles(t, ProfileWithLayout(root, layout), fromRoot)
	}
	assertOneValidSite("multi-domain", CheckWithLayout(identity, 100_000, work.RootLayoutMultiDomain))
	assertProfileFiles(t, ProfileWithLayout(identity, work.RootLayoutMultiDomain),
		[]string{"shop.example/mon/{slug}/router.work", "shop.example/router.work"})
	assertOneValidSite("single", CheckWithLayout(site, 100_000, work.RootLayoutSingle))
	assertProfileFiles(t, ProfileWithLayout(site, work.RootLayoutSingle),
		[]string{"mon/{slug}/router.work", "router.work"})

	// CONTROL: a router.work that does not compile is an issue naming the file, not a site that was
	// never opened — which is exactly what "ok" over an unopened tree looked like.
	write("router.work", `const broken = ;`)
	broken := Check(root, 100_000)
	if broken.OK() {
		t.Fatal("CONTROL: a broken router.work passed the preflight")
	}
	joined := ""
	for _, issue := range broken.Issues {
		joined += issue.Error() + "\n"
	}
	if !strings.Contains(joined, "router.work") {
		t.Fatalf("CONTROL: the issue does not name router.work:\n%s", joined)
	}

	// When both routers exist router.kitwork.js wins (work.RouterMarker), so the profile lists the
	// router the host would run and not the one beside it.
	write("router.kitwork.js", `import { router } from "kitwork";
router.get((ctx) => ctx.text("ok"));`)
	assertProfileFiles(t, Profile(root),
		[]string{"acme/shop.example/mon/{slug}/router.work", "acme/shop.example/router.kitwork.js"})
}
