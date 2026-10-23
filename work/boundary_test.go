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
import * as file from "kitwork/file";

router.get((ctx) => {
    return ctx.file(file.open(ctx.query("f")));
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

	rec := getBoundary(t, tenant, "app/localhost/own.txt")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "OWN-FILE") {
		t.Fatalf("ctx.file must serve the app's own file; got %d %q", rec.Code, rec.Body.String())
	}
}

func TestTenantPreparesCanonicalPathBoundaries(t *testing.T) {
	_, tenant := buildBoundaryFixture(t, boundaryFileRouter)
	if tenant.siteBoundary == nil || tenant.appBoundary == nil {
		t.Fatal("tenant did not prepare generation path boundaries")
	}
}

// The boundary is the app IDENTITY, not the domain folder (STABILITY.md §1). A file of the app's folder
// outside the domain's is the code zone's, app/shared.txt (KITWORK_FILE.md mục 1), and must keep working —
// this is what stops the fix from being quietly over-restrictive.
func TestContextFileAllowsIdentityLevelShare(t *testing.T) {
	_, tenant := buildBoundaryFixture(t, boundaryFileRouter)

	rec := getBoundary(t, tenant, "app/shared.txt")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "IDENTITY-SHARED") {
		t.Fatalf("identity-level share must remain reachable; got %d %q", rec.Code, rec.Body.String())
	}
}

// Reading another app's file is a cross-tenant isolation break. The handler builds its path from
// request input, which is an ordinary download pattern — the ENGINE has to hold the line.
func TestContextFileRejectsCrossAppTraversal(t *testing.T) {
	_, tenant := buildBoundaryFixture(t, boundaryFileRouter)

	for _, path := range []string{"../../victim-corp/victim.com/secret.txt", "app/../victim-corp/victim.com/secret.txt"} {
		rec := getBoundary(t, tenant, path)
		if strings.Contains(rec.Body.String(), "TOP-SECRET-CROSS-TENANT") {
			t.Fatalf("ctx.file escaped the app root: %q", rec.Body.String())
		}
		// file.open refuses the path as written, before anything is touched: the same failure whether the
		// file exists or not, so it confirms nothing
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: expected the handler's failure at file.open, got %d", path, rec.Code)
		}
	}
	// a handle that passes as written but leads nowhere the zone reaches answers 404, as a missing file
	for _, path := range []string{"app/.env", "app/localhost/missing.txt"} {
		if rec := getBoundary(t, tenant, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404 (a denial must not confirm the file exists), got %d", path, rec.Code)
		}
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

	contextRec := getBoundary(t, tenant, "app/localhost/external/secret.txt")
	if strings.Contains(contextRec.Body.String(), "TOP-SECRET-CROSS-TENANT") {
		t.Fatal("ctx.file followed a symlink outside the app")
	}
}

const boundaryUploadRouter = `
import { router } from "kitwork";
import * as file from "kitwork/file";

router.post((ctx) => {
    const saved = file.open("uploads/").segment(ctx.query("name")).write(ctx.request.file("doc"));
    return ctx.json({ saved: saved.path });
});
`

func postUpload(t *testing.T, tenant *Tenant, name, filename string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("doc", filename)
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("UPLOADED-PAYLOAD"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "http://localhost/?name="+name, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	return rec
}

// CONTROL: an upload into the app's data must still succeed (KITWORK_FILE.md mục 9).
func TestUploadWritesIntoTheAppsData(t *testing.T) {
	root, tenant := buildBoundaryFixture(t, boundaryUploadRouter)

	rec := postUpload(t, tenant, "report.bin", "report.bin")
	if !strings.Contains(rec.Body.String(), `"saved":"uploads/report.bin"`) {
		t.Fatalf("upload inside the app must succeed; got %d %q", rec.Code, rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(root, "acme", ".database", "files", "uploads", "report.bin"))
	if err != nil || string(content) != "UPLOADED-PAYLOAD" {
		t.Fatalf("uploaded file not written into the app's data: %q %v", content, err)
	}
}

// An escaping upload destination is a WRITE primitive: dropping a file onto another app's
// router.kitwork.js would hand it execution. segment() takes one name, so a name from the request never
// climbs, and the client's file name is never a path. Nothing may be created outside the app.
func TestUploadRefusesEscapingName(t *testing.T) {
	root, tenant := buildBoundaryFixture(t, boundaryUploadRouter)

	rec := postUpload(t, tenant, "../../../victim-corp/victim.com/planted.bin", "../../planted.bin")
	if strings.Contains(rec.Body.String(), "saved") {
		t.Fatalf("escaping upload reported success: %q", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "victim-corp", "victim.com", "planted.bin")); err == nil {
		t.Fatal("upload was written into another app's directory")
	}
}

// A folder of the app's data that leads into another app through a link (a symlink, or on Windows a
// junction) is refused by the zone's os.Root when the upload is written: nothing lands there.
//
// CONTROL: with Zone.Write writing the machine path first (os.MkdirAll and os.WriteFile of the app's
// folder, .database/files and the names joined, as file.write did before 22/10/2026), the upload followed
// the junction: "upload followed a link into another app".
func TestUploadRefusesLinkedDestination(t *testing.T) {
	root, tenant := buildBoundaryFixture(t, boundaryUploadRouter)
	data := filepath.Join(root, "acme", ".database", "files")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "victim-corp", "victim.com")
	linkFolder(t, target, filepath.Join(data, "uploads"))

	rec := postUpload(t, tenant, "planted.bin", "planted.bin")
	if strings.Contains(rec.Body.String(), "saved") {
		t.Fatalf("linked upload reported success: %q", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(target, "planted.bin")); err == nil {
		t.Fatal("upload followed a link into another app")
	}
}
