package work

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildBoundaryFixture lays out two DIFFERENT apps under one root:
//
//	<root>/acme/localhost/     ← the tenant under test
//	<root>/acme/shared.txt     ← identity-level share (same app, outside the domain folder)
//	<root>/victim-corp/…       ← another app entirely
//
// so a test can tell apart the two things that look alike lexically: leaving the DOMAIN folder
// (legitimate — _core/ and siblings live there) and leaving the APP (never legitimate).
func buildBoundaryFixture(t *testing.T, routerScript string) (root string, tenant *Tenant) {
	t.Helper()
	root = t.TempDir()

	appDir := filepath.Join(root, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "own.txt"), []byte("OWN-FILE"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "acme", "shared.txt"), []byte("IDENTITY-SHARED"), 0644); err != nil {
		t.Fatal(err)
	}

	victimDir := filepath.Join(root, "victim-corp", "victim.com")
	if err := os.MkdirAll(victimDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victimDir, "secret.txt"), []byte("TOP-SECRET-CROSS-TENANT"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant = NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	return root, tenant
}

const boundaryFileRouter = `
import { router } from "kitwork";

router.get((ctx) => {
    return ctx.file(ctx.query("f"));
});
`

func getBoundary(t *testing.T, tenant *Tenant, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/?f="+query, nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	return rec
}

// CONTROL: ctx.file must keep working for the app's own files. Without this, the rejection tests
// below would also pass on a build where ctx.file is simply broken.
func TestContextFileServesOwnFile(t *testing.T) {
	_, tenant := buildBoundaryFixture(t, boundaryFileRouter)

	rec := getBoundary(t, tenant, "own.txt")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "OWN-FILE") {
		t.Fatalf("ctx.file must serve the app's own file; got %d %q", rec.Code, rec.Body.String())
	}
}

// The boundary is the app IDENTITY, not the domain folder (STABILITY.md §1). Leaving the domain
// folder to reach an identity-level share is legitimate and must keep working — this is what stops
// the fix from being quietly over-restrictive.
func TestContextFileAllowsIdentityLevelShare(t *testing.T) {
	_, tenant := buildBoundaryFixture(t, boundaryFileRouter)

	rec := getBoundary(t, tenant, "../shared.txt")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "IDENTITY-SHARED") {
		t.Fatalf("identity-level share must remain reachable; got %d %q", rec.Code, rec.Body.String())
	}
}

// Reading another app's file is a cross-tenant isolation break. The handler builds its path from
// request input, which is an ordinary download pattern — the ENGINE has to hold the line.
func TestContextFileRejectsCrossAppTraversal(t *testing.T) {
	_, tenant := buildBoundaryFixture(t, boundaryFileRouter)

	rec := getBoundary(t, tenant, "../../victim-corp/victim.com/secret.txt")
	if strings.Contains(rec.Body.String(), "TOP-SECRET-CROSS-TENANT") {
		t.Fatalf("ctx.file escaped the app root: %q", rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (a denial must not confirm the file exists), got %d", rec.Code)
	}
}

func TestStaticAndContextFileRejectCrossAppSymlink(t *testing.T) {
	root, tenant := buildBoundaryFixture(t, boundaryFileRouter)
	link := filepath.Join(root, "acme", "localhost", "external")
	target := filepath.Join(root, "victim-corp", "victim.com")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	staticReq := httptest.NewRequest(http.MethodGet, "http://localhost/external/secret.txt", nil)
	staticRec := httptest.NewRecorder()
	tenant.Serve(staticRec, staticReq)
	if strings.Contains(staticRec.Body.String(), "TOP-SECRET-CROSS-TENANT") {
		t.Fatal("zero-VM static serving followed a symlink outside the site")
	}

	contextRec := getBoundary(t, tenant, "external/secret.txt")
	if strings.Contains(contextRec.Body.String(), "TOP-SECRET-CROSS-TENANT") {
		t.Fatal("ctx.file followed a symlink outside the app")
	}
}

const boundaryUploadRouter = `
import { router } from "kitwork";

router.post((ctx) => {
    const saved = ctx.request.saveFile("doc", ctx.query("dest"));
    return ctx.json({ saved: saved });
});
`

func postUpload(t *testing.T, tenant *Tenant, dest, filename string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("doc", filename)
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("UPLOADED-PAYLOAD"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "http://localhost/?dest="+dest, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	return rec
}

// CONTROL: a normal upload inside the app must still succeed.
func TestSaveFileAcceptsDestinationInsideApp(t *testing.T) {
	root, tenant := buildBoundaryFixture(t, boundaryUploadRouter)

	rec := postUpload(t, tenant, "uploads/report.txt", "report.txt")
	if !strings.Contains(rec.Body.String(), "true") {
		t.Fatalf("upload inside the app must succeed; got %q", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "acme", "localhost", "uploads", "report.txt")); err != nil {
		t.Fatalf("uploaded file not written inside the app: %v", err)
	}
}

// An escaping upload destination is a WRITE primitive: dropping a file onto another app's
// router.kitwork.js would hand it execution. Nothing may be created outside the app.
func TestSaveFileRejectsEscapingDestination(t *testing.T) {
	root, tenant := buildBoundaryFixture(t, boundaryUploadRouter)

	rec := postUpload(t, tenant, "../../victim-corp/victim.com/planted.txt", "planted.txt")
	if strings.Contains(rec.Body.String(), "true") {
		t.Fatalf("escaping upload reported success: %q", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "victim-corp", "victim.com", "planted.txt")); err == nil {
		t.Fatal("upload was written into another app's directory")
	}
}

func TestSaveFileRejectsSymlinkedDestination(t *testing.T) {
	root, tenant := buildBoundaryFixture(t, boundaryUploadRouter)
	link := filepath.Join(root, "acme", "localhost", "external")
	target := filepath.Join(root, "victim-corp", "victim.com")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	rec := postUpload(t, tenant, "external/planted.txt", "planted.txt")
	if strings.Contains(rec.Body.String(), "true") {
		t.Fatalf("symlinked upload reported success: %q", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(target, "planted.txt")); err == nil {
		t.Fatal("upload followed a symlink into another app")
	}
}
