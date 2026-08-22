//go:build !stdminify

package javascript

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildStagedMinifiesOnlyCoreAndReaddressesExactBytes(t *testing.T) {
	readableOptions := stagedTestOptions(ProfileHydrate, []string{"dialog", "tabs"})
	readable, err := BuildStaged(readableOptions)
	if err != nil {
		t.Fatal(err)
	}

	minifiedOptions := readableOptions
	minifiedOptions.MinifyCore = true
	minified, err := BuildStaged(minifiedOptions)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := BuildStaged(minifiedOptions)
	if err != nil {
		t.Fatal(err)
	}

	if readable.Hydrate == nil || minified.Hydrate == nil || repeated.Hydrate == nil {
		t.Fatal("Hydrate profile omitted its staged Hydrate artifact")
	}
	for _, core := range []struct {
		name     string
		readable JITArtifact
		minified JITArtifact
	}{
		{name: "runtime", readable: readable.Runtime, minified: minified.Runtime},
		{name: "hydrate", readable: *readable.Hydrate, minified: *minified.Hydrate},
	} {
		t.Logf("%s staged core: readable=%d minified=%d", core.name, core.readable.Size(), core.minified.Size())
		if bytes.Equal(core.readable.Bytes(), core.minified.Bytes()) {
			t.Fatalf("%s bytes were not minified", core.name)
		}
		if core.minified.Size() >= core.readable.Size() {
			t.Fatalf("%s minified size=%d, readable=%d", core.name, core.minified.Size(), core.readable.Size())
		}
		if core.minified.Size()*4 > core.readable.Size()*3 {
			t.Fatalf("%s exceeded the 75%% staged-core size budget: minified=%d readable=%d",
				core.name, core.minified.Size(), core.readable.Size())
		}
		assertStagedArtifactExactIdentity(t, core.minified)
		if core.readable.SHA256() == core.minified.SHA256() ||
			core.readable.Integrity() == core.minified.Integrity() ||
			core.readable.Name() == core.minified.Name() {
			t.Fatalf("%s retained pre-minification identity", core.name)
		}
		if source := core.minified.Bytes(); len(source) < 2 || source[0] != ';' || source[len(source)-1] != '\n' {
			t.Fatalf("%s lost staged fragment framing", core.name)
		}
	}

	if readable.GraphKey() == minified.GraphKey() {
		t.Fatal("minified core reused the readable graph key")
	}
	if readable.Graph.SHA256() == minified.Graph.SHA256() ||
		bytes.Equal(readable.Graph.Bytes(), minified.Graph.Bytes()) {
		t.Fatal("graph was not re-addressed for the minified core identities")
	}
	for _, core := range []JITArtifact{minified.Runtime, *minified.Hydrate} {
		if !bytes.Contains(minified.Graph.Bytes(), []byte(core.SHA256())) ||
			!bytes.Contains(minified.Graph.Bytes(), []byte(core.Integrity())) {
			t.Fatalf("graph does not contain exact %s hash and SRI", core.Role())
		}
	}
	assertStagedArtifactExactIdentity(t, minified.Graph)

	assertStagedArtifactSlicesEqual(t, "services", readable.Services, minified.Services)
	assertStagedArtifactSlicesEqual(t, "components", readable.Components, minified.Components)
	if readable.ComponentsBundle == nil || minified.ComponentsBundle == nil {
		t.Fatal("fixture omitted the common components bundle")
	}
	assertStagedArtifactEqual(t, "components bundle", *readable.ComponentsBundle, *minified.ComponentsBundle)

	for _, component := range readableOptions.Components {
		var artifact JITArtifact
		if component.Name == "app" {
			artifact = stagedArtifactByPackage(t, minified.Components, component.Name)
		} else {
			artifact = *minified.ComponentsBundle
		}
		if count := bytes.Count(artifact.Bytes(), component.Source); count != 1 {
			t.Fatalf("component %s raw source count=%d, want 1", component.Name, count)
		}
		rawHash := ContentHash(component.Source)
		if !bytes.Contains(artifact.Bytes(), []byte(rawHash)) ||
			!bytes.Contains(minified.Graph.Bytes(), []byte(rawHash)) {
			t.Fatalf("component %s lost raw source identity", component.Name)
		}
	}

	if minified.GraphKey() != repeated.GraphKey() {
		t.Fatal("repeated minified build changed the graph key")
	}
	assertStagedArtifactSlicesEqual(t, "repeated delivery", minified.Artifacts(), repeated.Artifacts())
}

func TestPrepareStagedCoreSourceIsDetachedAndStrict(t *testing.T) {
	readable := []byte("; (function () { var marker = 1; })();\n")
	detached, err := prepareStagedCoreSource(JITRoleRuntime, readable, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(detached, readable) {
		t.Fatalf("readable staged source changed: %q", detached)
	}
	detached[2] = 'X'
	if readable[2] == 'X' {
		t.Fatal("readable staged source shares caller-owned bytes")
	}
	readable[3] = 'Y'
	if detached[3] == 'Y' {
		t.Fatal("caller mutation changed detached staged source")
	}

	if _, err := prepareStagedCoreSource(JITRoleRuntime, []byte("; function broken( {\n"), true); err == nil ||
		!strings.Contains(err.Error(), "minify staged runtime") {
		t.Fatalf("malformed strict staged source error=%v", err)
	}
}

func TestBuildStagedRejectsMismatchedPreparedCorePolicy(t *testing.T) {
	minifiedHydrate, err := prepareStagedCoreArtifacts(ProfileHydrate, true)
	if err != nil {
		t.Fatal(err)
	}
	readableOptions := stagedTestOptions(ProfileHydrate, []string{"dialog", "tabs"})
	if _, err := buildStaged(readableOptions, &minifiedHydrate); err == nil ||
		!strings.Contains(err.Error(), "prepared staged core does not match build policy") {
		t.Fatalf("minification-policy mismatch error=%v", err)
	}

	minifiedKit, err := prepareStagedCoreArtifacts(ProfileKit, true)
	if err != nil {
		t.Fatal(err)
	}
	minifiedOptions := readableOptions
	minifiedOptions.MinifyCore = true
	if _, err := buildStaged(minifiedOptions, &minifiedKit); err == nil ||
		!strings.Contains(err.Error(), "prepared staged core does not match build policy") {
		t.Fatalf("profile mismatch error=%v", err)
	}
}

func assertStagedArtifactExactIdentity(t *testing.T, artifact JITArtifact) {
	t.Helper()
	source := artifact.Bytes()
	sum := sha256.Sum256(source)
	wantIntegrity := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
	if artifact.SHA256() != ContentHash(source) {
		t.Fatalf("%s hash does not identify exact bytes", artifact.Name())
	}
	if artifact.Integrity() != wantIntegrity {
		t.Fatalf("%s SRI=%q, want %q", artifact.Name(), artifact.Integrity(), wantIntegrity)
	}
	if artifact.Name() != artifact.SHA256()+"."+artifact.Suffix()+".js" {
		t.Fatalf("artifact name=%q does not match hash and suffix", artifact.Name())
	}
}

func assertStagedArtifactSlicesEqual(t *testing.T, label string, left, right []JITArtifact) {
	t.Helper()
	if len(left) != len(right) {
		t.Fatalf("%s artifact count=%d, want %d", label, len(right), len(left))
	}
	for index := range left {
		assertStagedArtifactEqual(t, label, left[index], right[index])
	}
}

func assertStagedArtifactEqual(t *testing.T, label string, left, right JITArtifact) {
	t.Helper()
	if left.Role() != right.Role() || left.Package() != right.Package() || left.Version() != right.Version() ||
		left.Suffix() != right.Suffix() || left.SHA256() != right.SHA256() ||
		left.Integrity() != right.Integrity() || left.Name() != right.Name() ||
		!bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatalf("%s changed artifact %s/%s", label, left.Role(), left.Package())
	}
}
