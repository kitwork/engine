package javascript

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func tenantCounterPackage(source []byte) ComponentPackage {
	return ComponentPackage{Name: "tenant-counter", Version: "1.2.3", Source: source}
}

func TestTenantComponentCatalogResolvesInlineAndLegacyExactVersion(t *testing.T) {
	source := []byte(";kit.component(\"tenant-counter\", { count: 0 });\n")
	composer, err := NewDefaultComposer(tenantCounterPackage(source))
	if err != nil {
		t.Fatal(err)
	}
	if !composer.HasManagedComponent("tenant-counter") || !composer.HasManagedComponent("dialog") || composer.HasManagedComponent("unknown") {
		t.Fatal("managed component membership omitted overlay/embedded identity")
	}
	// The catalog must retain the constructor snapshot, not caller-owned bytes.
	source[1] = 'X'
	inline, err := composer.ComposeHTML([]byte(`<main data-kit-component="tenant-counter@1.2.3"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	exact, err := composer.ComposeHTML([]byte(`<main data-kit-component="tenant-counter" data-kit-version="1.2.3"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if inline.ContentHash != exact.ContentHash || !bytes.Equal(inline.JavaScript, exact.JavaScript) {
		t.Fatal("inline exact and legacy separate exact versions resolved differently")
	}
	if !bytes.Contains(exact.JavaScript, []byte(`kit.component("tenant-counter"`)) ||
		bytes.Contains(exact.JavaScript, []byte(`Xit.component("tenant-counter"`)) {
		t.Fatal("composer did not retain detached tenant JavaScript")
	}
	_, err = composer.ComposeHTML([]byte(`<main data-kit-component="tenant-counter" data-kit-version="1.2.4"></main>`))
	if !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("mismatched explicit version error = %v", err)
	}
}

func TestTenantComponentCatalogRejectsShadowReservedAndDuplicates(t *testing.T) {
	source := []byte(";kit.component(\"x\", {});\n")
	_, err := NewDefaultComposer(ComponentPackage{Name: "dialog", Version: "9.0.0", Source: source})
	if !errors.Is(err, ErrComponentShadow) {
		t.Fatalf("embedded shadow error = %v", err)
	}
	_, err = NewDefaultComposer(ComponentPackage{Name: "runtime", Version: "1.0.0", Source: source})
	if !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("reserved name error = %v", err)
	}
	_, err = NewDefaultComposer(
		ComponentPackage{Name: "tenant-x", Version: "1.0.0", Source: source},
		ComponentPackage{Name: "tenant-x", Version: "2.0.0", Source: source},
	)
	if !errors.Is(err, ErrInvalidModule) || err == nil || !strings.Contains(err.Error(), "duplicate component") {
		t.Fatalf("duplicate tenant component error = %v", err)
	}
}

func TestTenantComponentCatalogEmitsOnlyWhenDocumentUsesIt(t *testing.T) {
	source := []byte(";kit.component(\"tenant-counter\", { count: 0 });\n")
	store, err := NewDefaultAssetStore(tenantCounterPackage(source))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	unusedHTML := []byte(`<main data-kit-scope="ready: true"></main>`)
	usedHTML := []byte(`<main data-kit-component="tenant-counter@1.2.3"></main>`)
	unused, err := ScanHTML(unusedHTML)
	if err != nil {
		t.Fatal(err)
	}
	used, err := ScanHTML(usedHTML)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareGeneration([]ScanResult{unused, used}); err != nil {
		t.Fatal(err)
	}
	unusedDelivery, err := store.ComposeHTML(unusedHTML)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range unusedDelivery.Artifacts() {
		if artifact.Role == JITRoleComponent || artifact.Role == JITRoleComponents {
			t.Fatalf("unused tenant component was emitted: %+v", artifact)
		}
	}
	usedDelivery, err := store.ComposeHTML(usedHTML)
	if err != nil {
		t.Fatal(err)
	}
	componentCount := 0
	for _, artifact := range usedDelivery.Artifacts() {
		if artifact.Role == JITRoleService {
			t.Fatalf("v1 tenant component unexpectedly declared a service: %+v", artifact)
		}
		if artifact.Role != JITRoleComponent {
			continue
		}
		componentCount++
		if artifact.Suffix != "tenant-counter" {
			t.Fatalf("component suffix = %q", artifact.Suffix)
		}
		stored, ok := store.Lookup(artifact.ContentHash)
		if !ok || !bytes.Contains(stored.JavaScript, source) {
			t.Fatal("tenant component artifact omitted snapshotted source")
		}
	}
	if componentCount != 1 {
		t.Fatalf("tenant component artifacts = %d, want 1", componentCount)
	}
}

func TestClientComponentCannotShadowEmbeddedOrTenantCatalog(t *testing.T) {
	composer, err := NewDefaultComposer(tenantCounterPackage([]byte(";kit.component(\"tenant-counter\", {});\n")))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dialog", "tenant-counter"} {
		_, err := composer.ComposeHTML([]byte(`<main data-kit-component="` + name + `"></main>`))
		if !errors.Is(err, ErrInvalidComponentUse) || !strings.Contains(err.Error(), "shadows the managed catalog") {
			t.Fatalf("local %s collision error = %v", name, err)
		}
	}
	if _, err := composer.ComposeHTML([]byte(`<main data-kit-component="page-widget"></main>`)); err != nil {
		t.Fatalf("unknown client component failed: %v", err)
	}
	_, err = composer.ComposeHTML([]byte(`<main data-kit-component="page-widget@1.0.0"></main>`))
	if !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown managed component error = %v", err)
	}
}
