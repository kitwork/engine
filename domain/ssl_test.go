package domain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestHostPolicySitesFolder proves the single-tenant AutoSSL rule: a host is allowed when a
// matching folder exists under SitesDir (drop-a-folder → cert), including the www→apex mapping,
// while an unknown host with no folder (and no DB registration) is rejected.
func TestHostPolicySitesFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "allowed.vn"), 0o755); err != nil {
		t.Fatal(err)
	}

	prevDir, prevAllows := SitesDir, Allows
	SitesDir, Allows = dir, nil
	defer func() { SitesDir, Allows = prevDir, prevAllows }()

	ctx := context.Background()
	if err := HostPolicy(ctx, "allowed.vn"); err != nil {
		t.Errorf("allowed.vn should be permitted via its sites folder, got %v", err)
	}
	if err := HostPolicy(ctx, "www.allowed.vn"); err != nil {
		t.Errorf("www.allowed.vn should map to the apex folder, got %v", err)
	}
	if err := HostPolicy(ctx, "denied.vn"); err == nil {
		t.Error("denied.vn has no folder and no DB record — should be rejected")
	}

	// With SitesDir disabled, even an existing folder name is not special.
	SitesDir = ""
	if err := HostPolicy(ctx, "allowed.vn"); err == nil {
		t.Error("with SitesDir empty, allowed.vn should fall through to rejection")
	}
}
