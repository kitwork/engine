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
				"router.work": `import * as router from "kitwork/router";` + "\n" + c.call,
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
		"router.work": `import * as router from "kitwork/router";` + "\n" +
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

// The companion is router.llmsFull, camelCase like the router's other names; in router.work the flat
// spelling is a compile error naming it. (A router.kitwork.js answers to both; this tested that, on a
// template site, until the template pages went, 27/10/2026.)
func TestLlmsFullAnswersToTheCamelCaseSpelling(t *testing.T) {
	get := scaffold(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";` + "\n" + `router.llmsFull(true);`,
	})
	if rec := get("/llms-full.txt"); rec.Code != 200 {
		t.Fatalf("router.llmsFull(true) did not publish /llms-full.txt: code=%d", rec.Code)
	}
	_, err := bootUISite(t, map[string]string{
		"localhost/router.work": `import * as router from "kitwork/router";` + "\n" + `router.llmsfull(true);`,
	}, true)
	if err == nil || !strings.Contains(err.Error(), "router.work:2:8: router.llmsfull không có; ý là router.llmsFull?") {
		t.Fatalf("router.llmsfull in router.work: %v, want the compile error naming router.llmsFull", err)
	}
}
