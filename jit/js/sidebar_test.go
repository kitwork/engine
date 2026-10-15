package js

import (
	"strings"
	"testing"
)

// The sidebar was written per-site first (huynhnhanquoc.com/_assets/js/sidebar.js) and promoted
// here once its shape had settled. These tests pin what "promoted" has to mean: the engine ships
// it on demand, and the API the existing markup already calls keeps working.

func TestSidebarIsShippedWhenUsed(t *testing.T) {
	html := `<body data-kit-component="sidebar" data-kit-alias="$sidebar"><button data-kit-click="cycle()">x</button></body>`
	out := deliver(html)

	if !strings.Contains(out, `/_kitwork/components/sidebar.`) {
		t.Fatalf("sidebar was not injected for markup that uses it:\n%s", out)
	}
	// The bare name only: the kernel drops a version written in a host (data-kit-component=
	// "sidebar@v1.0.0") before it looks the blueprint up, so no versioned name is registered.
	if src := ModulesJS([]string{"sidebar"}); !strings.Contains(src, `component("sidebar", `) || strings.Contains(src, `component("sidebar@`) {
		t.Error("the file must register the bare name, and only it")
	}
}

// CONTROL: emit-only-what-is-used is the whole point of the JIT. Without this the test above would
// also pass on a build that inlines every component on every page.
func TestSidebarIsAbsentWhenUnused(t *testing.T) {
	html := `<dialog data-kit-component="dialog"><button data-kit-click="close()">x</button></dialog>`
	out := deliver(html)

	if strings.Contains(out, `/_kitwork/components/sidebar.`) {
		t.Fatal("sidebar shipped to a page that never mentions it")
	}
	if !strings.Contains(out, `/_kitwork/components/dialog.`) {
		t.Fatal("the component that IS used should still ship")
	}
}

// The dashboard's markup calls cycle/toggle and the explicit drawer names. open()/close() went on
// 23/09 (3941bc02) for openDrawer()/closeDrawer(); this test kept listing them and kept passing,
// because it read the whole composed runtime and the kernel has an "open:" of its own. Meanwhile the
// dashboard's mobile close button called close() into nothing. It reads the component's own file
// now (03/10), and the dashboard calls closeDrawer().
func TestSidebarKeepsTheAPIExistingMarkupCalls(t *testing.T) {
	src := ModulesJS([]string{"sidebar"})

	for _, method := range []string{
		"cycle:", "toggle:", // what huynhnhanquoc.com already calls
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
	src := ModulesJS([]string{"sidebar"})

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
