package javascript

import (
	"errors"
	"testing"
)

func TestClientComponentsDoNotEnterTheManagedDeliveryGraph(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAssetStore(composer, AssetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	delivery, err := store.ComposeHTML([]byte(`<section data-kit-component="tenant-card"></section>`))
	if err != nil {
		t.Fatal(err)
	}
	artifacts := delivery.Artifacts()
	if len(artifacts) != 2 || artifacts[0].Role != JITRoleRuntime || artifacts[1].Role != JITRoleGraph {
		t.Fatalf("local-only delivery artifacts = %#v, want runtime and empty managed graph", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.Role == JITRoleComponent || artifact.Role == JITRoleComponents {
			t.Fatalf("local component leaked into managed artifact %#v", artifact)
		}
	}
}

func TestPreparedDocumentsDifferingOnlyByClientComponentsShareManagedGraph(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAssetStore(composer, AssetLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	alphaSource := []byte(`<section data-kit-component="tenant-alpha"></section>`)
	betaSource := []byte(`<section data-kit-component="tenant-beta"></section>`)
	alpha, err := ScanHTML(alphaSource)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ScanHTML(betaSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareGeneration([]ScanResult{alpha, beta}); err != nil {
		t.Fatal(err)
	}
	alphaDelivery, err := store.ComposeHTML(alphaSource)
	if err != nil {
		t.Fatal(err)
	}
	betaDelivery, err := store.ComposeHTML(betaSource)
	if err != nil {
		t.Fatal(err)
	}
	if !sameDelivery(alphaDelivery, betaDelivery) {
		t.Fatalf("local-only documents produced different managed graphs: %s != %s",
			alphaDelivery.GraphKey(), betaDelivery.GraphKey())
	}
}

func TestClientComponentCannotShadowManagedCatalog(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`<section data-kit-component="dialog"></section>`,
		`<section data-kit-component="app" data-kit-as="$app"></section>`,
	} {
		if _, err := composer.ComposeHTML([]byte(source)); !errors.Is(err, ErrInvalidComponentUse) {
			t.Errorf("ComposeHTML(%q) error = %v, want managed-shadow rejection", source, err)
		}
	}
}

func TestUnknownManagedComponentRemainsStrictWhileUnversionedIsClient(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := composer.ComposeHTML([]byte(`<section data-kit-component="tenant-unknown"></section>`)); err != nil {
		t.Fatalf("unversioned client component error = %v", err)
	}
	_, err = composer.ComposeHTML([]byte(`<section data-kit-component="tenant-unknown@1.0.0"></section>`))
	if !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("ComposeHTML() error = %v, want ErrModuleNotFound", err)
	}
}
