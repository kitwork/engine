package vanilla

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckedInKitJSMatchesCanonicalAssembly(t *testing.T) {
	want, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(packageDirectory(t), "kit.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("kit.js is stale; run: go run ./jit/javascript/vanilla/cmd/assemble ./jit/javascript/vanilla/kit.js")
	}
}

func TestCheckedInHydrateKitJSMatchesCanonicalAssembly(t *testing.T) {
	want, err := SourceForProfile(ProfileHydrate)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(packageDirectory(t), "hydrate.kit.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("hydrate.kit.js is stale; run: go run ./jit/javascript/vanilla/cmd/assemble -profile hydrate ./jit/javascript/vanilla/hydrate.kit.js")
	}
}

func TestProfileManifestsAreCompleteAndOrdered(t *testing.T) {
	entries, err := sources.ReadDir("src")
	if err != nil {
		t.Fatal(err)
	}
	optional := map[string]bool{"src/service.js": true}
	if len(entries) != len(hydrateFragments)+len(optional) {
		t.Fatalf("embedded fragment count = %d, hydrate manifest + optional count = %d",
			len(entries), len(hydrateFragments)+len(optional))
	}

	seen := make(map[string]bool, len(hydrateFragments))
	for _, name := range hydrateFragments {
		if seen[name] {
			t.Fatalf("duplicate fragment %q", name)
		}
		seen[name] = true
	}
	for _, entry := range entries {
		name := "src/" + entry.Name()
		if optional[name] {
			if seen[name] {
				t.Fatalf("optional fragment %q entered the base profile", name)
			}
			continue
		}
		if !seen[name] {
			t.Fatalf("embedded fragment %q is absent from the hydrate manifest", name)
		}
	}

	if kitFragments[0] != "src/core.js" || kitFragments[len(kitFragments)-1] != "src/boot.js" {
		t.Fatalf("kit fragment boundary order = %q ... %q", kitFragments[0], kitFragments[len(kitFragments)-1])
	}
	if hydrateFragments[0] != "src/core.js" || hydrateFragments[len(hydrateFragments)-1] != "src/boot.js" {
		t.Fatalf("hydrate fragment boundary order = %q ... %q", hydrateFragments[0], hydrateFragments[len(hydrateFragments)-1])
	}

	wantHydrate := append(append([]string(nil), kitFragments[:len(kitFragments)-1]...),
		"src/morph.js", "src/drive.js", "src/boot.js")
	if !equalStrings(hydrateFragments, wantHydrate) {
		t.Fatalf("hydrate manifest = %q, want base + morph + drive + boot", hydrateFragments)
	}
}

func TestProfilesConcatenateExactFragmentBytes(t *testing.T) {
	for _, profile := range []Profile{ProfileKit, ProfileHydrate} {
		profile := profile
		t.Run(string(profile), func(t *testing.T) {
			fragments, err := FragmentNamesForProfile(profile)
			if err != nil {
				t.Fatal(err)
			}
			var want bytes.Buffer
			for _, name := range fragments {
				source, err := sources.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = want.Write(source)
			}

			got, err := SourceForProfile(profile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want.Bytes()) {
				t.Fatalf("%s profile transformed fragment bytes", profile)
			}
			if ContentHash(got) != ContentHash(want.Bytes()) {
				t.Fatalf("%s profile content hash is not deterministic", profile)
			}
		})
	}
}

func TestProfileAPIIsClosedAndReturnsManifestCopies(t *testing.T) {
	if got, err := ArtifactName(ProfileKit); err != nil || got != "kit.js" {
		t.Fatalf("kit artifact = %q, %v", got, err)
	}
	if got, err := ArtifactName(ProfileHydrate); err != nil || got != "hydrate.kit.js" {
		t.Fatalf("hydrate artifact = %q, %v", got, err)
	}
	if _, err := ArtifactName(Profile("unknown")); err == nil {
		t.Fatal("unknown artifact profile succeeded")
	}
	if _, err := SourceForProfile(Profile("unknown")); err == nil {
		t.Fatal("unknown source profile succeeded")
	}

	copyManifest, err := FragmentNamesForProfile(ProfileKit)
	if err != nil {
		t.Fatal(err)
	}
	copyManifest[0] = "changed"
	again, err := FragmentNamesForProfile(ProfileKit)
	if err != nil {
		t.Fatal(err)
	}
	if again[0] != "src/core.js" {
		t.Fatal("caller mutated the canonical kit manifest")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func packageDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return directory
}
