package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Drive is a site-wide choice now, not a side effect of a marker. The flag travels on the engine's
// own runtime script tag, so the client reads the site's answer without the page carrying anything
// an author had to write.
func TestDriveIsOffUntilTheSiteAsks(t *testing.T) {
	const page = `<main><a href="/next">next</a><b data-kit-text="'x'">x</b></main>`

	body := func(t *testing.T, router string) string {
		t.Helper()
		tenant, _ := writeTestSite(t, router, page)
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
		if strings.Contains(out, "data-kitwork-drive") {
			t.Fatalf("a site that never asked must not get Drive:\n%s", out)
		}
		if !strings.Contains(out, "/kit/kernel.") {
			t.Fatalf("the runtime is still served, Drive or not:\n%s", out)
		}
	})

	t.Run("asked for it", func(t *testing.T) {
		out := body(t, `router.drive();`)
		if !strings.Contains(out, "data-kitwork-drive") {
			t.Fatalf("router.drive() should flag the runtime tag:\n%s", out)
		}
		if strings.Count(out, "data-kitwork-drive") != 1 {
			t.Fatalf("the flag should appear once:\n%s", out)
		}
	})

	t.Run("asked and then said no", func(t *testing.T) {
		if out := body(t, `router.drive(false);`); strings.Contains(out, "data-kitwork-drive") {
			t.Fatalf("router.drive(false) must stay off:\n%s", out)
		}
	})

	// A root marker used to be what switched Drive on. It must no longer do anything.
	t.Run("a marker buys nothing", func(t *testing.T) {
		tenant, _ := writeTestSite(t, "", `<main data-kit-hydrate="v1"><b data-kit-text="'x'">x</b></main>`)
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		if strings.Contains(recorder.Body.String(), "data-kitwork-drive") {
			t.Fatalf("a marker must not turn Drive on:\n%s", recorder.Body.String())
		}
	})
}

// A misspelt argument is named at boot rather than shrugged off.
func TestDriveRejectsANonBoolean(t *testing.T) {
	tenant, _ := writeTestSite(t, `router.drive("yes");`, `<main></main>`)
	err := tenant.Run()
	if err == nil {
		t.Fatal(`router.drive("yes") should be refused`)
	}
	if !strings.Contains(err.Error(), "router.drive expects a boolean") {
		t.Fatalf("the error should name the problem: %v", err)
	}
	ok, _ := writeTestSite(t, `router.drive(true);`, `<main></main>`)
	if err := ok.Run(); err != nil {
		t.Fatalf("a boolean must be accepted: %v", err)
	}
}
