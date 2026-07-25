package hydrate

import (
	"strings"
	"testing"
)

const sidebarTag = `<header data-kit-component="sidebar=$sidebar" data-kit-bind="{ 'data-state': status, 'data-open': drawer }" class="w-64">`

// The whole point: a collapsed sidebar must arrive collapsed, not arrive expanded and snap.
func TestPreRenderBindBakesRestoredState(t *testing.T) {
	state := map[string]map[string]any{"sidebar": {"status": "collapsed"}}
	out := PreRenderBind(sidebarTag, state)

	if !strings.Contains(out, `data-state="collapsed"`) {
		t.Fatalf("restored state was not baked into the tag:\n%s", out)
	}
	// drawer is absent from the cookie: it evaluates as missing, which the client renders as no
	// attribute. Baking data-open="" would open a mobile overlay on first paint.
	if strings.Contains(out, `data-open="`) {
		t.Errorf("a key the cookie never carried was baked anyway:\n%s", out)
	}
	// Everything the author wrote must survive.
	if !strings.Contains(out, `class="w-64"`) || !strings.Contains(out, `data-kit-component="sidebar=$sidebar"`) {
		t.Errorf("authored attributes were damaged:\n%s", out)
	}
}

// CONTROL: with no cookie there is nothing to restore and the markup must come through untouched —
// otherwise the assertions above would also hold on a pass that rewrites tags arbitrarily.
func TestPreRenderBindIsANoOpWithoutState(t *testing.T) {
	if out := PreRenderBind(sidebarTag, nil); out != sidebarTag {
		t.Fatalf("markup changed with no state:\n%s", out)
	}
	other := map[string]map[string]any{"palette": {"open": true}}
	if out := PreRenderBind(sidebarTag, other); out != sidebarTag {
		t.Fatalf("another component's state leaked into this tag:\n%s", out)
	}
}

// false must REMOVE the attribute, matching the client. A drawer bound to false that renders as
// data-open="" would cover the page on load.
func TestPreRenderBindFalseRemovesAttribute(t *testing.T) {
	state := map[string]map[string]any{"sidebar": {"status": "expanded", "drawer": false}}
	out := PreRenderBind(sidebarTag, state)

	if strings.Contains(out, `data-open="`) {
		t.Fatalf("false should remove the attribute:\n%s", out)
	}
	if !strings.Contains(out, `data-state="expanded"`) {
		t.Fatalf("string value missing:\n%s", out)
	}
}

// An attribute the author already wrote is replaced, not duplicated — two data-state attributes is
// invalid markup and the browser would keep the wrong one.
func TestPreRenderBindReplacesAuthoredAttribute(t *testing.T) {
	tag := `<header data-kit-component="sidebar" data-state="expanded" data-kit-bind="{ 'data-state': status }">`
	out := PreRenderBind(tag, map[string]map[string]any{"sidebar": {"status": "hidden"}})

	if strings.Count(out, "data-state=") != 1 {
		t.Fatalf("expected exactly one data-state:\n%s", out)
	}
	if !strings.Contains(out, `data-state="hidden"`) {
		t.Fatalf("restored value should win:\n%s", out)
	}
}

// A cookie is attacker-controllable, so its value must never be able to close the tag or add one.
func TestPreRenderBindEscapesCookieValues(t *testing.T) {
	state := map[string]map[string]any{"sidebar": {"status": `"><script>alert(1)</script>`}}
	out := PreRenderBind(sidebarTag, state)

	if strings.Contains(out, "<script>") {
		t.Fatalf("cookie value escaped the attribute:\n%s", out)
	}
}

func TestComponentNameStripsVersionAndAlias(t *testing.T) {
	for decl, want := range map[string]string{
		"sidebar":                "sidebar",
		"sidebar=$sidebar":       "sidebar",
		"sidebar@v1.0.0":         "sidebar",
		"sidebar@v1.0.0=$handle": "sidebar",
	} {
		if got := ComponentName(decl); got != want {
			t.Errorf("%q → %q, want %q", decl, got, want)
		}
	}
}

func TestStateFromCookies(t *testing.T) {
	state := StateFromCookies(map[string]string{
		"kitwork.sidebar": "status=collapsed&count=3&pinned=true",
		"session":         "abc", // not ours — must be ignored
	})

	sb := state["sidebar"]
	if sb == nil {
		t.Fatal("sidebar state missing")
	}
	if sb["status"] != "collapsed" {
		t.Errorf("status = %v", sb["status"])
	}
	// Types matter: an expression comparing count > 2 must see a number, not "3".
	if sb["count"] != float64(3) {
		t.Errorf("count = %v (%T), want number", sb["count"], sb["count"])
	}
	if sb["pinned"] != true {
		t.Errorf("pinned = %v (%T), want bool", sb["pinned"], sb["pinned"])
	}
	if _, leaked := state["session"]; leaked {
		t.Error("a non-kitwork cookie was treated as component state")
	}
}
