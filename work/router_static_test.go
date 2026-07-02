package work

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// .static() requires an explicit duration (no silent "forever"). It accepts the human units Go's
// ParseDuration lacks (d/w/mo/y) and leaves the route uncached on a missing/bad value.
func TestStaticDurations(t *testing.T) {
	// Blank argument → NOT cached (must be explicit).
	noArg := &Router{}
	if got := noArg.Static(value.Value{}); got != noArg {
		t.Error("static() must return the router for chaining")
	}
	if noArg.staticTTL != 0 {
		t.Errorf("static() no-arg must NOT cache, got TTL %v", noArg.staticTTL)
	}

	cases := map[string]time.Duration{
		"30s": 30 * time.Second,
		"10m": 10 * time.Minute,
		"1h":  time.Hour,
	}
	for in, want := range cases {
		r := &Router{}
		r.Static(value.New(in))
		if r.staticTTL != want {
			t.Errorf("static(%q) = %v, want %v", in, r.staticTTL, want)
		}
	}

	// Unparseable → not cached (warns, no panic).
	bad := &Router{}
	bad.Static(value.New("nonsense"))
	if bad.staticTTL != 0 {
		t.Errorf("static(\"nonsense\") must not cache, got %v", bad.staticTTL)
	}

	// Map form { duration: "1h" }.
	rm := &Router{}
	rm.Static(value.New(map[string]value.Value{"duration": value.New("1h")}))
	if rm.staticTTL != time.Hour {
		t.Errorf("static({duration:1h}) = %v, want 1h", rm.staticTTL)
	}
}

// static-mtime: the template signature must change when a template is edited, so the static cache
// invalidates instead of serving stale content.
func TestTemplateSignatureChanges(t *testing.T) {
	base := t.TempDir()
	page := filepath.Join(base, "page.kitwork.html")
	if err := os.WriteFile(page, []byte("<p>v1</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig1 := templateSignature(base)

	// Edit the template (bump mtime + size).
	time.Sleep(15 * time.Millisecond)
	if err := os.WriteFile(page, []byte("<p>v2 — longer</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig2 := templateSignature(base)

	if sig1 == sig2 {
		t.Error("templateSignature must change after a template edit (static cache would go stale)")
	}
}
