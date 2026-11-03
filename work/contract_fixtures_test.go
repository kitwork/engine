package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealAppHTTPContractFixture(t *testing.T) {
	reachLoopback(t) // the upstream is an httptest server on loopback

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"hello kitwork"}`))
	}))
	defer ts.Close()

	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import * as router from "kitwork/router";
import * as http from "kitwork/http";

router.get((ctx) => {
    const res = http.get("` + ts.URL + `").cache("5m").send().json();
    return ctx.json({ message: res.message });
});
`
	if err := os.WriteFile(filepath.Join(appDir, UIRouterFileName), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello kitwork") {
		t.Fatalf("expected 'hello kitwork' in response body, got: %s", rec.Body.String())
	}
}

func TestRealAppJWTContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import * as router from "kitwork/router";
import { jwt } from "kitwork";

router.get((ctx) => {
    const token = jwt.sign({ user: "admin" }, "secret123");
    const verified = jwt.verify(token, "secret123");
    const forged = jwt.verify(token, "wrong-secret");
    const tampered = jwt.verify(token + "x", "secret123");
    return ctx.json({ user: verified.payload.user, forged: forged, tampered: tampered });
});
`
	if err := os.WriteFile(filepath.Join(appDir, UIRouterFileName), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("expected 'admin' in response body, got: %s", rec.Body.String())
	}
	// A sign->verify round-trip alone is satisfied by a verify that checks nothing. What has to
	// hold is the negative: a wrong secret and a tampered token must both be refused.
	if !strings.Contains(rec.Body.String(), `"forged":null`) {
		t.Fatalf("jwt.verify accepted a token signed with a different secret: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"tampered":null`) {
		t.Fatalf("jwt.verify accepted a tampered token: %s", rec.Body.String())
	}
}

func TestRealAppShortbaseContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import * as router from "kitwork/router";
import { shortbase } from "kitwork";

router.get((ctx) => {
    const code = shortbase.encode(123456);
    const id = shortbase.decode(code);
    return ctx.json({ code: code, id: id });
});
`
	if err := os.WriteFile(filepath.Join(appDir, UIRouterFileName), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	// Round-tripping proves nothing on its own — an encode that returns its input would pass too.
	// The code has to be an actual encoding, i.e. not the digits it came from.
	if strings.Contains(rec.Body.String(), `"code":"123456"`) {
		t.Fatalf("shortbase.encode returned its input unchanged: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"123456"`) {
		t.Fatalf("expected id '123456' in response body, got: %s", rec.Body.String())
	}
}

func TestRealAppScheduleContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import * as router from "kitwork/router";
import * as schedule from "kitwork/schedule";

router.get((ctx) => {
    const list = schedule.list();
    return ctx.json({ count: list.length });
});
`
	if err := os.WriteFile(filepath.Join(appDir, UIRouterFileName), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"count":0`) {
		t.Fatalf("expected count 0 in response body, got: %s", rec.Body.String())
	}
}
