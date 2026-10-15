package css

import (
	"strings"
	"testing"
)

// The engine's grouped form names a class it toggles as the key of a pair:
// data-kitwork-class="text-red-500: over". The kernel adds it later; its rule has to be in the
// stylesheet already, or the page goes unstyled the moment the condition flips.
func TestJITEmitsTheClassesAToggleNames(t *testing.T) {
	html := `<b class="flex" data-kitwork-bind="text: total" data-kitwork-class="text-red-500: over; bg-green-100: paid">0</b>`
	css := GenerateJITCached(html, nil)
	for _, want := range []string{".text-red-500", ".bg-green-100", ".flex"} {
		if !strings.Contains(css, want) {
			t.Errorf("%s was not emitted\n--- css ---\n%s", want, css)
		}
	}
	// control: a class named only inside a condition or a value, in a style pair, or in the retired
	// bind form (class.<name> in data-kitwork-bind, which the kernel no longer toggles) is not a class
	// the page uses
	quiet := GenerateJITCached(`<b class="flex" data-kitwork-class="flex: mode == 'text-red-500'" data-kitwork-bind="title: 'underline'; class.bg-green-100: paid" data-kitwork-style="class.italic: x">0</b>`, nil)
	for _, unwanted := range []string{".text-red-500", ".bg-green-100", ".underline", ".italic"} {
		if strings.Contains(quiet, unwanted) {
			t.Errorf("%s is not toggled by any data-kitwork-class, yet was emitted\n--- css ---\n%s", unwanted, quiet)
		}
	}
	if !strings.Contains(quiet, ".flex") {
		t.Errorf("the static class is still emitted\n--- css ---\n%s", quiet)
	}
}
