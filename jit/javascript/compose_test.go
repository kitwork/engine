package javascript

import (
	"bytes"
	"errors"
	"testing"
)

func TestComposerAdaptsScopesToPublicProfiles(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	kitBundle, err := composer.ComposeHTML([]byte(`<main data-kit-scope="count: 0"><b data-kit-text="count"></b></main>`))
	if err != nil {
		t.Fatal(err)
	}
	kitArtifact, err := Build(BuildOptions{Profile: ProfileKit})
	if err != nil {
		t.Fatal(err)
	}
	if kitBundle.Profile != ProfileKit || kitBundle.Release != ReleaseVersion ||
		kitBundle.ContentHash != kitArtifact.SHA256() || !bytes.Equal(kitBundle.JavaScript, kitArtifact.Bytes()) {
		t.Fatal("scope-only composition did not use the exact public Kit artifact")
	}

	hydrateBundle, err := composer.ComposeStandalone(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	hydrateArtifact, err := Build(BuildOptions{Profile: ProfileHydrate})
	if err != nil {
		t.Fatal(err)
	}
	if hydrateBundle.Profile != ProfileHydrate || hydrateBundle.Release != ReleaseVersion ||
		hydrateBundle.ContentHash != hydrateArtifact.SHA256() || !bytes.Equal(hydrateBundle.JavaScript, hydrateArtifact.Bytes()) {
		t.Fatal("application composition did not use the exact public Hydrate artifact")
	}
}

func TestComposerClosesCurrentProgressBarPackage(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := composer.ComposeHTML([]byte(`<main data-kit-component="progress-bar@2.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := composer.ComposeHTML([]byte(`<main data-kit-component="progress-bar" data-kit-version="2.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if canonical.ContentHash != legacy.ContentHash || !bytes.Equal(canonical.JavaScript, legacy.JavaScript) {
		t.Fatal("canonical and legacy progress-bar@2.0.0 pins resolved differently")
	}
	for _, marker := range [][]byte{
		[]byte(`kit.service("progress"`),
		[]byte(`kit.component("progress-bar"`),
		[]byte(`components["progress-bar"] = "2.0.0"`),
	} {
		if !bytes.Contains(canonical.JavaScript, marker) {
			t.Fatalf("closed artifact omitted %q", marker)
		}
	}
	if canonical.ContentHash != ContentHash(canonical.JavaScript) {
		t.Fatal("adapter content hash differs from exact artifact bytes")
	}

	older, err := composer.ComposeStandalone([]ComponentRef{{Name: "progress-bar", Version: "1.2.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if older.ContentHash == canonical.ContentHash {
		t.Fatal("different exact component versions shared an artifact identity")
	}
	_, err = composer.ComposeStandalone([]ComponentRef{
		{Name: "progress-bar", Version: "1.2.0"},
		{Name: "progress-bar", Version: "2.0.0"},
	}, false)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version conflict error=%v", err)
	}
}

func TestComposerFailsClosedForUnsupportedCatalog(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, unsupported := range []string{"counter", "combobox", "tab"} {
		_, err := composer.ComposeHTML([]byte(`<main data-kit-component="` + unsupported + `@1.0.0"></main>`))
		if !errors.Is(err, ErrModuleNotFound) {
			t.Fatalf("unsupported component %q error=%v", unsupported, err)
		}
	}
	static, err := composer.ComposeHTML([]byte(`<main>Static</main>`))
	if err != nil || !static.Empty() || static.ContentHash != "" {
		t.Fatalf("static composition=%+v error=%v", static, err)
	}
}

func TestComposerStandaloneSelectionIsDeterministic(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	left, err := composer.ComposeStandalone([]ComponentRef{{Name: "progress-bar", Version: "2.0.0"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	right, err := composer.ComposeStandalone([]ComponentRef{{Name: "progress-bar", Version: "2.0.0"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if left.ContentHash != right.ContentHash || !bytes.Equal(left.JavaScript, right.JavaScript) {
		t.Fatal("application union depends on scan order")
	}
}
