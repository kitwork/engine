package work

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDiscoverLegacySites(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "identity-a", "legacy.example")
	current := filepath.Join(root, "identity-a", "current.example")
	for _, dir := range []string{legacy, current} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(legacy, "app.kitwork.js"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, RouterFileName), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := DiscoverLegacySites(root)
	if !slices.Equal(got, []string{"legacy.example"}) {
		t.Fatalf("unexpected legacy sites: %v", got)
	}
}
