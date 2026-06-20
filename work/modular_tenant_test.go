package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeMod(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// A REAL tenant built from modules via import/export, served over HTTP through
// the same path the engine uses (Run() to register, then Serve()).
//
//	app.kitwork.js          → imports the route module (side-effect)
//	routes/hello.kitwork.js → imports `router` (kitwork) + greet from a sibling
//	                          lib file, registers GET /hello
//	lib/greet.kitwork.js    → exports the helper
//
// If the request to /hello returns the lib's output, the whole modular
// architecture (file-to-file import/export) works end to end.
func TestModularTenantServesViaImports(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-modular-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")

	writeMod(t, filepath.Join(dir, "lib", "greet.kitwork.js"),
		`export const greet = (name) => "Hello, " + name + "!";`)

	writeMod(t, filepath.Join(dir, "routes", "hello.kitwork.js"),
		`import router from "kitwork/router";`+"\n"+
			`import { greet } from "../lib/greet.kitwork.js";`+"\n"+
			`router.get("/hello").handle((response) => { return response.text(greet("world")); });`)

	writeMod(t, filepath.Join(dir, "app.kitwork.js"),
		`import "./routes/hello.kitwork.js";`)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("modular tenant failed to compile/run: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/hello", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "Hello, world!" {
		t.Fatalf("body = %q, want %q", got, "Hello, world!")
	}
}
