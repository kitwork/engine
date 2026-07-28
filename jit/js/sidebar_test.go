package js

import (
	"strings"
	"testing"
)

// The sidebar was written per-site first (huynhnhanquoc.com/_assets/js/sidebar.js) and promoted
// here once its shape had settled. These tests pin what "promoted" has to mean: the engine ships
// it on demand, and the API the existing markup already calls keeps working.

func TestSidebarIsShippedWhenUsed(t *testing.T) {
	html := `<body data-kit-component="sidebar=$sidebar"><button data-kit-click="cycle()">x</button></body>`
	out := Render(html)

	if !strings.Contains(out, `components=component%3Asidebar`) {
		t.Fatalf("sidebar was not injected for markup that uses it:\n%s", out)
	}
	// The versioned alias must ship with it, or data-kit-component="sidebar@v1.0.0" silently
	// resolves to nothing.
	if !strings.Contains(ModulesJS([]string{"sidebar"}), `component("sidebar@v1.0.0"`) {
		t.Error("versioned registration missing")
	}
}

// CONTROL: emit-only-what-is-used is the whole point of the JIT. Without this the test above would
// also pass on a build that inlines every component on every page.
func TestSidebarIsAbsentWhenUnused(t *testing.T) {
	html := `<div data-kit-component="dropdown"><button data-kit-click="toggle()">x</button></div>`
	out := Render(html)

	if strings.Contains(out, `components=component%3Asidebar`) {
		t.Fatal("sidebar shipped to a page that never mentions it")
	}
	if !strings.Contains(out, `components=component%3Adropdown`) {
		t.Fatal("the component that IS used should still ship")
	}
}

// The dashboard's markup already calls cycle/toggle/open/close. Promoting the component must not
// break the page that motivated it, so the old spellings stay alongside the explicit ones.
func TestSidebarKeepsTheAPIExistingMarkupCalls(t *testing.T) {
	src := RuntimeJS([]string{"sidebar"})

	for _, method := range []string{
		"cycle:", "toggle:", "open:", "close:", // what huynhnhanquoc.com already calls
		"expand:", "collapse:", "hide:", // explicit rail control
		"openDrawer:", "closeDrawer:", "toggleDrawer:", // explicit drawer control
		"isExpanded:", "isCollapsed:", "isHidden:", // predicates for data-kit-show
	} {
		if !strings.Contains(src, method) {
			t.Errorf("missing method %q", method)
		}
	}
}

// A rail the user collapsed must still be collapsed after a reload — component state is in-memory,
// so the component has to persist it itself. The drawer must NOT be persisted: a page that opens
// with a mobile overlay already covering it is a bug, not a restored preference.
func TestSidebarPersistsRailButNotDrawer(t *testing.T) {
	src := RuntimeJS([]string{"sidebar"})

	if !strings.Contains(src, "localStorage.setItem") || !strings.Contains(src, "localStorage.getItem") {
		t.Fatal("rail state is not persisted, so a collapsed sidebar reopens expanded")
	}
	if !strings.Contains(src, "drawer: false") {
		t.Error("drawer must start closed on every load")
	}
	// Reading localStorage throws in some privacy modes; an uncaught error there would take the
	// whole kernel down on load.
	if !strings.Contains(src, "try {") {
		t.Error("localStorage access must be guarded")
	}
}
