package theme

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	out := Render(`<head><script data-kitwork-jit="theme"></script><title>x</title></head>`)
	if strings.Contains(out, `-jit="theme"`) {
		t.Error("marker was not replaced")
	}
	if !strings.Contains(out, `add("dark")`) || !strings.Contains(out, `remove("dark")`) {
		t.Error("pre-paint script not injected (add/remove dark)")
	}
	if !strings.Contains(out, `localStorage.getItem("theme")`) {
		t.Error("pre-paint should read the shared \"theme\" key")
	}

	// No marker → unchanged.
	plain := `<head><title>x</title></head>`
	if Render(plain) != plain {
		t.Error("should be a no-op without the marker")
	}

	// data-kit-jit alias + inner whitespace.
	if v := Render(`<script data-kit-jit="theme">   </script>`); strings.Contains(v, `-jit="theme"`) {
		t.Error("data-kit-jit alias / whitespace not handled")
	}
}
