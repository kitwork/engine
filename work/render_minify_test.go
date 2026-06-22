package work

import (
	"testing"

	"github.com/kitwork/engine/value"
)

// Minify defaults by environment when not set explicitly: OFF on local dev (AllowLocal), ON for a
// server. An explicit render.minify()/minify(false) overrides the environment in either direction.
func TestRenderMinifyDefaultByEnv(t *testing.T) {
	prev := AllowLocal
	defer func() { AllowLocal = prev }()

	// --- not set explicitly → follows the environment ---
	AllowLocal = true // local dev
	if (&Render{}).shouldMinify() {
		t.Error("local dev (unset) should default to NO minify")
	}
	AllowLocal = false // server
	if !(&Render{}).shouldMinify() {
		t.Error("server (unset) should default to minify")
	}

	// --- explicit overrides the environment ---
	AllowLocal = false // server, but user turns it OFF
	if (&Render{}).Minify(value.New(false)).shouldMinify() {
		t.Error("explicit minify(false) must override the server default")
	}
	AllowLocal = true // local, but user turns it ON
	if !(&Render{}).Minify().shouldMinify() {
		t.Error("explicit minify() must override the local default")
	}

	// --- explicit subset by type name counts as "on" ---
	AllowLocal = true
	if !(&Render{}).Minify(value.New("css"), value.New("js")).shouldMinify() {
		t.Error("explicit minify(\"css\",\"js\") should minify")
	}
}
