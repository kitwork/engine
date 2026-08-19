package site_test

import (
	"testing"

	"github.com/kitwork/engine/site"
)

// A same public URL pointing at two different disk dirs (e.g. "_assets" and "assets" both → /assets)
// must fail closed, while an identical re-declaration stays idempotent under hot-reload.
func TestAssetMountConflictFailsClosed(t *testing.T) {
	p := &site.Presentation{}

	if err := p.AddAssetMount(site.AssetMount{URL: "assets", Disk: "_assets"}); err != nil {
		t.Fatalf("first mount rejected: %v", err)
	}
	if err := p.AddAssetMount(site.AssetMount{URL: "assets", Disk: "_assets"}); err != nil {
		t.Fatalf("identical mount must be idempotent, got: %v", err)
	}
	if err := p.AddAssetMount(site.AssetMount{URL: "assets", Disk: "assets"}); err == nil {
		t.Fatal("same url + different disk must conflict, got nil")
	}
	if err := p.AddAssetMount(site.AssetMount{URL: "img", Disk: "_img"}); err != nil {
		t.Fatalf("distinct url rejected: %v", err)
	}
}
