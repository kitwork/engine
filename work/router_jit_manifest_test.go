package work

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJitjsRootManifestSnapshotsAndSelectsTenantComponent(t *testing.T) {
	for _, test := range []struct {
		name      string
		page      string
		wantRoles string
	}{
		{
			name:      "used exact component",
			page:      `<main data-kit-component="counter@1.0.0"></main>`,
			wantRoles: "runtime,hydrate,graph,component",
		},
		{
			name:      "unused catalog component",
			page:      `<main data-kit-scope="ready: true"></main>`,
			wantRoles: "runtime,hydrate,graph",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, directory := writeKitJSTestSite(t, `
router.jitjs({
  components: {
    counter: { version: "1.0.0", source: "./components/counter.js" }
  }
});`, test.page)
			filename := filepath.Join(directory, "components", "counter.js")
			writeKitJSFile(t, directory, "components/counter.js", `kit.component("counter", { count: 0 });`)

			if err := tenant.Run(); err != nil {
				t.Fatal(err)
			}
			presentation := tenant.SiteGeneration().Presentation().Snapshot()
			if !presentation.KitJS || len(presentation.JITComponents) != 1 {
				t.Fatalf("presentation = %#v", presentation)
			}
			component := presentation.JITComponents[0]
			if component.Name != "counter" || component.Version != "1.0.0" ||
				filepath.Clean(component.Filename) != filepath.Clean(filename) {
				t.Fatalf("manifest component = %#v", component)
			}

			tags, body := serveKitJSPage(t, tenant, "/")
			if got := stagedJITRoles(tags); got != test.wantRoles {
				t.Fatalf("delivery roles=%s want=%s body=%s", got, test.wantRoles, body)
			}
		})
	}
}

func TestJitjsManifestIsRootOnly(t *testing.T) {
	tenant, directory := writeKitJSTestSite(t, `router.jitjs(true);`, `<main>Home</main>`)
	writeKitJSFile(t, directory, "docs/router.kitwork.js", `import { router } from "kitwork";
router.jitjs({ components: { counter: { version: "1.0.0", source: "./counter.js" } } });`)
	writeKitJSFile(t, directory, "docs/page.kitwork.html", `<main>Docs</main>`)

	err := tenant.Run()
	if err == nil || !strings.Contains(err.Error(), "only in the site-root router.kitwork.js") {
		t.Fatalf("Run error=%v, want root-only manifest rejection", err)
	}
}

func TestJitjsManifestRejectsInvalidShapeAndIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		call string
		want string
	}{
		{
			name: "components is not an object",
			call: `router.jitjs({ components: [] });`,
			want: "components must be an object",
		},
		{
			name: "descriptor is not an object",
			call: `router.jitjs({ components: { counter: true } });`,
			want: `component "counter" must be an object`,
		},
		{
			name: "missing version",
			call: `router.jitjs({ components: { counter: { source: "counter.js" } } });`,
			want: `component "counter" requires field "version"`,
		},
		{
			name: "missing source",
			call: `router.jitjs({ components: { counter: { version: "1.0.0" } } });`,
			want: `component "counter" requires field "source"`,
		},
		{
			name: "extra descriptor field",
			call: `router.jitjs({ components: { counter: { version: "1.0.0", source: "counter.js", preload: true } } });`,
			want: `component "counter" contains unsupported field "preload"`,
		},
		{
			name: "version must be a string",
			call: `router.jitjs({ components: { counter: { version: 1, source: "counter.js" } } });`,
			want: `field "version" must be a string`,
		},
		{
			name: "source must be a string",
			call: `router.jitjs({ components: { counter: { version: "1.0.0", source: true } } });`,
			want: `field "source" must be a string`,
		},
		{
			name: "version must be exact",
			call: `router.jitjs({ components: { counter: { version: "latest", source: "counter.js" } } });`,
			want: `version must be an exact SemVer`,
		},
		{
			name: "source must be relative",
			call: `router.jitjs({ components: { counter: { version: "1.0.0", source: "/components/counter.js" } } });`,
			want: `must be relative to its router folder`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, _ := writeKitJSTestSite(t, test.call, `<main></main>`)
			err := tenant.Run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestJitjsManifestValidationOrderIsDeterministic(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, `router.jitjs({ components: {
	  zed: { version: "1.0.0", source: "zed.js" },
	  alpha: { version: "latest", source: "missing.js" }
	} });`, `<main></main>`)

	err := tenant.Run()
	if err == nil || !strings.Contains(err.Error(), `component "alpha" version must be an exact SemVer`) {
		t.Fatalf("Run error=%v, want sorted alpha validation first", err)
	}
}

func TestJitjsManifestRejectsEmbeddedShadowAndDuplicate(t *testing.T) {
	t.Run("embedded shadow", func(t *testing.T) {
		tenant, directory := writeKitJSTestSite(t, `router.jitjs({ components: {
  dialog: { version: "9.0.0", source: "components/dialog.js" }
} });`, `<main></main>`)
		writeKitJSFile(t, directory, "components/dialog.js", `kit.component("dialog", {});`)

		err := tenant.Run()
		if err == nil || !strings.Contains(err.Error(), "shadows managed component") {
			t.Fatalf("Run error=%v, want embedded shadow rejection during preparation", err)
		}
	})

	t.Run("deprecated declaration duplicates manifest", func(t *testing.T) {
		tenant, directory := writeKitJSTestSite(t, `router.jitjs({ components: {
  counter: { version: "1.0.0", source: "components/counter.js" }
} });
router.jitComponent("counter", "1.0.0", "components/counter.js");`, `<main></main>`)
		writeKitJSFile(t, directory, "components/counter.js", `kit.component("counter", {});`)

		err := tenant.Run()
		if err == nil || !strings.Contains(err.Error(), "duplicate JIT component declaration") {
			t.Fatalf("Run error=%v, want duplicate declaration rejection", err)
		}
	})
}
