package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// A router chain that names a method the engine does not have — `.themes()` written against an
// engine that has not shipped it yet, a typo, a method from a newer engine — must not fail
// silently. Before this, the unknown member read as nil, the call on nil yielded nil, and every
// declaration AFTER it on the chain (.javascript(), .css({...}), .highlight()) was dropped without
// a word: the site lost its client runtime and its design tokens and nothing was logged. Now the
// chain survives the call, what follows still lands, and boot prints one line naming the method.
func TestRouterUnknownMethodIsReported(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `
import { router } from "kitwork";

router
    .title("Before")
    .nosuchmethod({ dark: true })
    .description("After — must still be declared");

router.get((ctx) => ctx.html("<h1>ok</h1>"));
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("a router written ahead of its engine must still serve: %v", err)
	}
	folder := tenant.routeTree().root.ensureFolder(tenant)
	if folder.meta["description"].Text() != "After — must still be declared" {
		t.Fatalf("the declaration after the unknown call was lost: %v", folder.meta)
	}
	if got := folder.unknownMethodWarning(); !strings.Contains(got, "router.nosuchmethod()") || !strings.Contains(got, "ignored") {
		t.Fatalf("the boot warning must name the unknown method: %q", got)
	}
}

// CONTROL: the same fixture without the unknown call boots and both declarations land.
func TestRouterKnownChainStillBoots(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `
import { router } from "kitwork";

router
    .title("Before")
    .description("After");

router.get((ctx) => ctx.html("<h1>ok</h1>"));
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("control chain must serve: %d %q", rec.Code, rec.Body.String())
	}
	meta := tenant.routeTree().root.ensureFolder(tenant).meta
	if meta["title"].Text() != "Before" || meta["description"].Text() != "After" {
		t.Fatalf("control chain did not land both declarations: %v", meta)
	}
}

// The chain must survive the unknown call: what comes AFTER it still lands, so the error names one
// missing method and nothing else is lost.
func TestRouterUnknownMethodKeepsTheChainAlive(t *testing.T) {
	fr := &FolderRouter{methods: map[string]*FolderMethod{}, outputs: map[string]*FolderMethod{}, meta: map[string]value.Value{}}
	router := value.New(fr)

	missing := router.Get("nosuchmethod")
	if missing.K != value.Func {
		t.Fatalf("an unknown member must resolve to a callable that records it, got kind %v", missing.K)
	}
	next := missing.Call("nosuchmethod", value.New(map[string]value.Value{"dark": value.New(true)}))
	if next.V != fr {
		t.Fatalf("the unknown call must return the router itself so the chain continues, got %#v", next.V)
	}
	next.Get("title").Call("title", value.New("After"))
	if fr.meta["title"].Text() != "After" {
		t.Fatalf("a declaration after the unknown call was lost: %v", fr.meta)
	}
	if got := fr.unknownMethodWarning(); !strings.Contains(got, "router.nosuchmethod()") {
		t.Fatalf("the warning must name the method: %q", got)
	}
}
