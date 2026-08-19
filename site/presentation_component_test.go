package site_test

import (
	"errors"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/site"
)

func TestPresentationJITComponentSnapshotIsDetachedAndUnique(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	t.Cleanup(appRuntime.Close)
	siteRuntime, err := appRuntime.Site(t.TempDir(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(";kit.component(\"tenant-counter\", {});\n")
	if err := generation.Presentation().AddJITComponent(site.JITComponentSource{
		Name: "tenant-counter", Version: "1.0.0", Filename: "counter.js", JavaScript: source,
	}); err != nil {
		t.Fatal(err)
	}
	source[1] = 'X'
	snapshot := generation.Presentation().Snapshot()
	if len(snapshot.JITComponents) != 1 || snapshot.JITComponents[0].JavaScript[1] != 'k' {
		t.Fatalf("presentation retained caller-owned source: %#v", snapshot.JITComponents)
	}
	snapshot.JITComponents[0].JavaScript[1] = 'Y'
	if got := generation.Presentation().Snapshot().JITComponents[0].JavaScript[1]; got != 'k' {
		t.Fatalf("snapshot mutation reached presentation: got %q", got)
	}

	err = generation.Presentation().AddJITComponent(site.JITComponentSource{
		Name: "tenant-counter", Version: "2.0.0", Filename: "counter-v2.js", JavaScript: []byte(";x\n"),
	})
	if !errors.Is(err, site.ErrDuplicateJITComponent) {
		t.Fatalf("duplicate declaration error = %v", err)
	}

	if _, err := siteRuntime.ActivateGeneration(generation); err != nil {
		t.Fatal(err)
	}
	err = generation.Presentation().AddJITComponent(site.JITComponentSource{
		Name: "late", Version: "1.0.0", Filename: "late.js", JavaScript: []byte(";x\n"),
	})
	if !errors.Is(err, site.ErrInvalidJITComponent) {
		t.Fatalf("frozen declaration error = %v", err)
	}
}

func TestPresentationJITComponentRejectsOversizeSource(t *testing.T) {
	appRuntime := app.NewRuntime("identity-a")
	t.Cleanup(appRuntime.Close)
	siteRuntime, err := appRuntime.Site(t.TempDir(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	err = generation.Presentation().AddJITComponent(site.JITComponentSource{
		Name: "large", Version: "1.0.0", Filename: "large.js",
		JavaScript: make([]byte, site.MaxJITComponentSourceBytes+1),
	})
	if !errors.Is(err, site.ErrJITComponentCapacity) {
		t.Fatalf("oversize declaration error = %v", err)
	}
}
