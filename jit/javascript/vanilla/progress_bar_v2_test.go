package vanilla

import (
	"bytes"
	"testing"
)

const progressBar120SHA256 = "6e435370cf74e0b66dae5352481ff1435707d78c99f92895992295b6497e5553"

func TestProgressBar200IsPresentationOnly(t *testing.T) {
	source := readVanillaFile(t, "component", "progress-bar", "2.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' {
		t.Fatal("progress-bar@2.0.0 is not a sealable classic script")
	}
	if got := bytes.Count(source, []byte(`kit.component("progress-bar"`)); got != 1 {
		t.Fatalf("progress-bar@2.0.0 registration count = %d, want 1", got)
	}
	for _, required := range []string{
		`visible: false`, `value: null`, `kit.progress.subscribe(`,
		`progress.phase === "start"`, `progress.phase === "progress"`,
		`progress.phase === "finish"`, `progress.outcome === "loaded"`,
		`scope.value = 100`, `}, 300)`, `clearTimeout(hideTimer)`, `unsubscribe()`,
	} {
		if !bytes.Contains(source, []byte(required)) {
			t.Fatalf("progress-bar@2.0.0 lost %s", required)
		}
	}
	for _, forbidden := range []string{
		"WeakMap", "manualSequence", "kit.progress.snapshot", "kit:navigation",
		"document.", "fetch(", "start: function", "set: function", "inc: function",
		"done: function", "reset: function", "status:", "message:", "source:",
	} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("progress-bar@2.0.0 contains non-presentation behavior %q", forbidden)
		}
	}

	historical := readVanillaFile(t, "component", "progress-bar", "1.2.0.js")
	if got := ContentHash(historical); got != progressBar120SHA256 {
		t.Fatalf("immutable progress-bar@1.2.0 SHA-256 = %s, want %s", got, progressBar120SHA256)
	}
}
