package work

import (
	"strings"
	"testing"
)

// llms takes the same argument shapes as robots and sitemap. A real file on
// disk shadows a generated route, so only the FILE case writes one — otherwise
// every case would pass for the wrong reason.
func TestLlmsArgumentShapes(t *testing.T) {
	for _, c := range []struct {
		name, call, want string
		onDisk           bool
	}{
		{name: "true", call: `router.llms(true);`, want: "# "},
		{name: "file", call: `router.llms("./llms.txt");`, want: "SERVED FROM DISK", onDisk: true},
		{name: "map", call: `router.llms({ title: "Kitwork" });`, want: "# Kitwork"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{
				"filesystem.kitwork": "",
				"index.kitwork.html": shorthandShell,
				"page.kitwork.html":  `<main>home</main>`,
				"router.kitwork.js":  `import { router } from "kitwork";` + "\n" + c.call,
			}
			if c.onDisk {
				files["llms.txt"] = "SERVED FROM DISK"
			}
			rec := scaffold(t, files)("/llms.txt")
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("%s: code=%d body=%q, want 200 containing %q", c.call, rec.Code, rec.Body.String(), c.want)
			}
		})
	}
}

// llmsFull publishes the companion document at its own canonical path, and
// takes the same shapes.
func TestLlmsFullPublishesTheCompanionPath(t *testing.T) {
	get := scaffold(t, map[string]string{
		"filesystem.kitwork": "",
		"index.kitwork.html": shorthandShell,
		"page.kitwork.html":  `<main>home</main>`,
		"router.kitwork.js": `import { router } from "kitwork";` + "\n" +
			`router.llms({ title: "Short" });` + "\n" +
			`router.llmsFull({ title: "Full" });`,
	})

	if rec := get("/llms-full.txt"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "# Full") {
		t.Fatalf("/llms-full.txt: code=%d body=%q, want 200 containing '# Full'", rec.Code, rec.Body.String())
	}
	// The two documents must stay separate — one overwriting the other would be
	// the whole point of the second method lost.
	if rec := get("/llms.txt"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "# Short") {
		t.Fatalf("/llms.txt: code=%d body=%q, want 200 containing '# Short'", rec.Code, rec.Body.String())
	}
}

// The manifest API is camelCase elsewhere (hotReload, allowLocal, rateLimit),
// so the companion has to answer to that spelling, not only the flat one.
func TestLlmsFullAnswersToTheCamelCaseSpelling(t *testing.T) {
	for _, call := range []string{`router.llmsFull(true);`, `router.llmsfull(true);`} {
		get := scaffold(t, map[string]string{
			"filesystem.kitwork": "",
			"index.kitwork.html": shorthandShell,
			"page.kitwork.html":  `<main>home</main>`,
			"router.kitwork.js":  `import { router } from "kitwork";` + "\n" + call,
		})
		if rec := get("/llms-full.txt"); rec.Code != 200 {
			t.Fatalf("%s did not publish /llms-full.txt: code=%d", call, rec.Code)
		}
	}
}
