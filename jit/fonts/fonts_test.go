package fonts

import (
	"testing"
)

func TestHasAndNames(t *testing.T) {
	for _, slug := range []string{"outfit", "fira-code"} {
		if !Has(slug) {
			t.Errorf("expected vendored family %q", slug)
		}
	}
	if Has("definitely-not-a-font") {
		t.Error("unknown family reported present")
	}
	if len(Names()) < 2 {
		t.Errorf("expected the vendored families, got %d", len(Names()))
	}
}

// Every face in the catalog must point at a real embedded woff2.
func TestEmbeddedFilesExist(t *testing.T) {
	for slug, fam := range catalog {
		if len(fam.faces) == 0 {
			t.Errorf("family %q has no faces", slug)
		}
		for _, f := range fam.faces {
			if _, err := FS.ReadFile(f.file); err != nil {
				t.Errorf("missing embedded woff2 for %q: %s (%v)", slug, f.file, err)
			}
		}
	}
}
