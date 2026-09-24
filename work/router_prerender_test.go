package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Server pre-rendering is OFF unless the site says otherwise. It is worth a test of its own because
// the default is the whole point: it costs a scan of every response and reaches only page scope, so
// a site should get it by asking, never by accident.
func TestPrerenderIsOffUntilTheSiteAsks(t *testing.T) {
	const page = `<main><input data-kit-model="qty" value="3"><b data-kit-text="qty * 2">pending</b></main>`

	body := func(t *testing.T, router string) string {
		t.Helper()
		tenant, _ := writeKitJSTestSite(t, router, page)
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET / = %d", recorder.Code)
		}
		return recorder.Body.String()
	}

	t.Run("silent by default", func(t *testing.T) {
		out := body(t, "")
		if strings.Contains(out, ">6<") {
			t.Fatalf("a site that never asked must not be pre-rendered:\n%s", out)
		}
		if !strings.Contains(out, ">pending<") {
			t.Fatalf("the author's own content should survive untouched:\n%s", out)
		}
	})

	t.Run("asked for it", func(t *testing.T) {
		out := body(t, `router.javascript({ prerender: true });`)
		if !strings.Contains(out, ">6<") {
			t.Fatalf("qty * 2 should be baked to 6:\n%s", out)
		}
	})

	t.Run("asked and then said no", func(t *testing.T) {
		if out := body(t, `router.javascript({ prerender: false });`); strings.Contains(out, ">6<") {
			t.Fatalf("prerender: false must stay off:\n%s", out)
		}
	})
}

// The trap this key had to avoid: router.javascript({...}) means "staged KitJS" for every other
// shape, and a site asking for a server-side setting must not be moved onto a different client
// runtime behind its back.
func TestPrerenderAloneDoesNotEnableStagedDelivery(t *testing.T) {
	for _, test := range []struct {
		name          string
		call          string
		staged        bool
		wantPrerender bool
	}{
		{name: "prerender alone", call: `router.javascript({ prerender: true });`, staged: false, wantPrerender: true},
		{name: "empty object keeps the old contract", call: `router.javascript({});`, staged: true},
		{name: "components keep the old contract", call: `router.javascript({ components: {} });`, staged: true},
		{name: "both together", call: `router.javascript({ components: {}, prerender: true });`, staged: true, wantPrerender: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenant, _ := writeKitJSTestSite(t, test.call, `<main></main>`)
			if err := tenant.Run(); err != nil {
				t.Fatal(err)
			}
			snapshot := tenant.SiteGeneration().Presentation().Snapshot()
			if snapshot.KitJS != test.staged {
				t.Fatalf("staged KitJS = %t, want %t", snapshot.KitJS, test.staged)
			}
			if snapshot.Prerender != test.wantPrerender {
				t.Fatalf("prerender = %t, want %t", snapshot.Prerender, test.wantPrerender)
			}
		})
	}
}

// A misspelt option should be named at boot, not shrugged off — the site would otherwise run for
// months believing it had asked for something.
func TestPrerenderRejectsANonBoolean(t *testing.T) {
	tenant, _ := writeKitJSTestSite(t, `router.javascript({ prerender: "yes" });`, `<main></main>`)
	err := tenant.Run()
	if err == nil {
		t.Fatal("prerender: \"yes\" should be refused")
	}
	if !strings.Contains(err.Error(), "prerender must be a boolean") {
		t.Fatalf("the error should name the field: %v", err)
	}
	// Control: the same call with a boolean is accepted.
	ok, _ := writeKitJSTestSite(t, `router.javascript({ prerender: true });`, `<main></main>`)
	if err := ok.Run(); err != nil {
		t.Fatalf("a boolean must be accepted: %v", err)
	}
}
