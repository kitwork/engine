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
	AllowLocal = true
	defer func() { AllowLocal = false }()

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
import { router, http } from "kitwork";

router.get((ctx) => {
    const res = http.get("` + ts.URL + `").cache("5m").json();
    return ctx.json({ message: res.message });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
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

func TestRealAppDatabaseContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router, sqlite } from "kitwork";

router.get((ctx) => {
    sqlite.exec("CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT, price INTEGER)");
    sqlite.table("items").create({ name: "laptop", price: 1200 });
    sqlite.atomic((tx) => {
        tx.table("items").create({ name: "camera", price: 900 });
    });
    const item = sqlite.table("items").where(row => row.name == "camera").first();
    return ctx.json({ name: item.name, price: item.price });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
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
	if !strings.Contains(rec.Body.String(), `"name":"camera"`) || !strings.Contains(rec.Body.String(), `"price":900`) {
		t.Fatalf("database lambda/transaction did not execute on the request VM: %s", rec.Body.String())
	}
}

func TestRealAppCollectionContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")

	postsDir := filepath.Join(appDir, "_collection", "posts")
	if err := os.MkdirAll(postsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(postsDir, "welcome.md"), []byte("---\ntitle: Welcome\nstatus: published\npublishedAt: \"2026-01-01\"\n---\nHello World"), 0644); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router, collection } from "kitwork";

router.get((ctx) => {
    const posts = collection.open("posts").all();
    return ctx.json({ list: posts });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
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
	if !strings.Contains(rec.Body.String(), "welcome") {
		t.Fatalf("expected 'welcome' in response body, got: %s", rec.Body.String())
	}
}

func TestRealAppJWTContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router, jwt } from "kitwork";

router.get((ctx) => {
    const token = jwt.sign({ user: "admin" }, "secret123");
    const verified = jwt.verify(token, "secret123");
    const forged = jwt.verify(token, "wrong-secret");
    const tampered = jwt.verify(token + "x", "secret123");
    return ctx.json({ user: verified.payload.user, forged: forged, tampered: tampered });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
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
import { router, shortbase } from "kitwork";

router.get((ctx) => {
    const code = shortbase.encode(123456);
    const id = shortbase.decode(code);
    return ctx.json({ code: code, id: id });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
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

func TestRealAppCronContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router, cron } from "kitwork";

router.get((ctx) => {
    const list = cron.list();
    return ctx.json({ count: list.length });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
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
