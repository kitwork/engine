package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSafeRescuesAHardFailure(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(`
import { router } from "kitwork";
router.get((ctx) => {
	const check = safe(() => fail("boom"));
	return ctx.json({ rescued: check.error != null, error: check.error.message, code: check.error.code, at: check.error.at });
});
`), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("safe() response status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Rescued bool   `json:"rescued"`
		Error   string `json:"error"`
		Code    string `json:"code"`
		At      string `json:"at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode safe() response: %v", err)
	}
	// fail is always FAIL, and at is where fail was called, in the file's own name
	if !body.Rescued || body.Error != "boom" || body.Code != "FAIL" || body.At != "router.kitwork.js:4:27" {
		t.Errorf("safe() response = %#v", body)
	}
}
