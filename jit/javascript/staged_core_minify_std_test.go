//go:build stdminify

package javascript

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildStagedStdMinifyKeepsExplicitReadableCorePolicy(t *testing.T) {
	readableOptions := stagedTestOptions(ProfileHydrate, []string{"dialog", "tabs"})
	readable, err := BuildStaged(readableOptions)
	if err != nil {
		t.Fatal(err)
	}

	passthroughOptions := readableOptions
	passthroughOptions.MinifyCore = true
	passthrough, err := BuildStaged(passthroughOptions)
	if err != nil {
		t.Fatal(err)
	}

	if readable.GraphKey() != passthrough.GraphKey() {
		t.Fatal("stdminify passthrough changed the graph key")
	}
	left := readable.Artifacts()
	right := passthrough.Artifacts()
	if len(left) != len(right) {
		t.Fatalf("stdminify artifact count=%d, want %d", len(right), len(left))
	}
	for index := range left {
		if left[index].SHA256() != right[index].SHA256() || left[index].Integrity() != right[index].Integrity() ||
			left[index].Name() != right[index].Name() || !bytes.Equal(left[index].Bytes(), right[index].Bytes()) {
			t.Fatalf("stdminify changed %s artifact bytes or identity", left[index].Role())
		}
	}

	malformed := []byte("; function broken( {\n")
	output, err := prepareStagedCoreSource(JITRoleRuntime, malformed, true)
	if err != nil {
		t.Fatalf("explicit stdminify passthrough returned an error: %v", err)
	}
	if !bytes.Equal(output, malformed) {
		t.Fatalf("explicit stdminify passthrough changed malformed source: %q", output)
	}
}

func TestBuildStagedStdMinifyStillRejectsPreparedCorePolicyMismatch(t *testing.T) {
	prepared, err := prepareStagedCoreArtifacts(ProfileHydrate, true)
	if err != nil {
		t.Fatal(err)
	}
	options := stagedTestOptions(ProfileHydrate, []string{"dialog", "tabs"})
	if _, err := buildStaged(options, &prepared); err == nil ||
		!strings.Contains(err.Error(), "prepared staged core does not match build policy") {
		t.Fatalf("stdminify policy mismatch error=%v", err)
	}
}
