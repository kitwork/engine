package css

import (
	"strings"
	"testing"
)

func TestFocusVisibleVariant(t *testing.T) {
	out := GenerateJITCached(
		`<button class="focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-lime-600">Open</button>`,
		nil,
	)

	for _, want := range []string{
		`.focus-visible\:outline:focus-visible`,
		`.focus-visible\:outline-2:focus-visible`,
		`.focus-visible\:outline-offset-2:focus-visible`,
		`.focus-visible\:outline-lime-600:focus-visible`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated CSS missing %q in:\n%s", want, out)
		}
	}
}
