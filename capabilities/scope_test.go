package capabilities

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanPathBoundaryEnforcement(t *testing.T) {
	base := t.TempDir()

	// Safe path inside boundary
	safe, err := CleanPath(base, "sub", "file.txt")
	if err != nil {
		t.Fatalf("Expected clean path success, got error: %v", err)
	}
	if !strings.HasPrefix(safe, filepath.Clean(base)) {
		t.Fatalf("Expected safe path inside base, got %s", safe)
	}

	// Escaping path using ..
	_, err = CleanPath(base, "..", "etc", "passwd")
	if err == nil {
		t.Fatal("Expected error when path escapes boundary via .., got nil")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Expected permission denied error, got %v", err)
	}
}
