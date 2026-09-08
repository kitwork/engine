package javascript

import (
	"strings"
	"testing"
)

// data-kitwork-highlight is the single authored name allowed inside the
// engine-emitted namespace: the highlight pass is server-only, so the attribute
// is declared where the work happens. Every OTHER data-kitwork-* name must stay
// rejected, or the namespace stops meaning anything.
func TestScannerAcceptsAuthoredHighlightAttribute(t *testing.T) {
	markup := `<pre><code data-kitwork-highlight="go">package main</code></pre>`
	if _, err := ScanHTML([]byte(markup)); err != nil {
		t.Fatalf("the scanner rejected an authored highlight slot: %v", err)
	}
}

func TestScannerStillRejectsOtherEngineNamespaceAttributes(t *testing.T) {
	for _, name := range []string{"data-kitwork-click", "data-kitwork-hash", "data-kitwork-highlighter", "data-kitwork-slot"} {
		markup := `<div ` + name + `="x"></div>`
		_, err := ScanHTML([]byte(markup))
		if err == nil {
			t.Fatalf("%s was accepted from an author; the engine namespace guard is no longer holding", name)
		}
		if !strings.Contains(err.Error(), "engine-emitted namespace") {
			t.Fatalf("%s was rejected for the wrong reason: %v", name, err)
		}
	}
}

// The slot marker is how an author pins WHERE the engine writes its own output
// — jit/theme has documented `<script data-kitwork-jit="theme"></script>` since
// it shipped. Rejecting it made that mechanism unusable on any site with staged
// delivery enabled, which is a contradiction inside the engine, not a rule.
func TestScannerAcceptsAuthoredSlotMarkers(t *testing.T) {
	for _, markup := range []string{
		`<style data-kitwork-jit="css"></style>`,
		`<script data-kitwork-jit="theme"></script>`,
		`<link data-kitwork-jit="manifest">`,
	} {
		if _, err := ScanHTML([]byte(markup)); err != nil {
			t.Fatalf("the scanner rejected an authored slot marker %q: %v", markup, err)
		}
	}
}

// The slot marker and the forged delivery marker are the same attribute name,
// so the rule has to read the tag and the role — not just the name. A <script>
// claiming a delivery role would smuggle code into a pipeline whose whole point
// is content-addressed integrity.
func TestScannerRejectsForgedDeliveryRolesButAllowsSlots(t *testing.T) {
	for _, forged := range []string{
		`<script data-kitwork-jit="runtime"></script>`,
		`<script data-kitwork-jit="hydrate"></script>`,
		`<script data-kitwork-jit="component"></script>`,
		`<script data-kitwork-jit="service"></script>`,
	} {
		if _, err := ScanHTML([]byte(forged)); err == nil {
			t.Fatalf("an authored delivery marker was accepted: %s", forged)
		}
	}
	for _, slot := range []string{
		`<style data-kitwork-jit="css"></style>`,
		`<link data-kitwork-jit="manifest">`,
		`<script data-kitwork-jit="theme"></script>`,
	} {
		if _, err := ScanHTML([]byte(slot)); err != nil {
			t.Fatalf("a slot marker was rejected: %s (%v)", slot, err)
		}
	}
}
