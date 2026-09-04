package kitdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestVerifyContextRejectsNilAndCancellation(t *testing.T) {
	database := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
	defer database.Close()
	if err := database.VerifyContext(nil); err == nil {
		t.Fatal("VerifyContext accepted nil context")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := database.VerifyContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyContext canceled = %v, want context.Canceled", err)
	}
	if err := database.Verify(); err != nil {
		t.Fatalf("Verify compatibility wrapper: %v", err)
	}
}
