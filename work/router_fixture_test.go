package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealAppDonateRouteFixture(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-fixture-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	donateDir := filepath.Join(tmp, "app", "localhost", "donate")
	if err := os.MkdirAll(donateDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router, qrcode } from "kitwork";

router.get((ctx) => {
    const payment = {
        bank: "970422",
        account: "123456789",
        amount: 50000,
        memo: "ung ho site"
    };
    const svg = qrcode.napas(payment)
        .template("circular")
        .logo("vietqr")
        .svg();
    return ctx.html(svg);
});
`
	if err := os.WriteFile(filepath.Join(donateDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(filepath.Join(tmp, "app"), "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/donate", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Fatalf("expected SVG response body, got: %s", rec.Body.String())
	}
}
