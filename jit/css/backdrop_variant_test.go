package css

import (
	"strings"
	"testing"
)

func TestBackdropVariantTargetsNativeDialogBackdrop(t *testing.T) {
	css, selector, _ := ResolveCore("backdrop:bg-zinc-950/70", nil)
	if css == "" {
		t.Fatal("backdrop color utility did not resolve")
	}
	if !strings.Contains(selector, "::backdrop") {
		t.Fatalf("backdrop variant generated selector %q", selector)
	}
}
