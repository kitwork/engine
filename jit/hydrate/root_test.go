package hydrate

import (
	"strings"
	"testing"
)

// The runtime has one root, window.kitwork (28/10/2026): the name kit beside it went, and nothing on a page
// reaches the runtime by it.
const oneRootAssertions = `
if (typeof window.kitwork !== "object" || typeof window.kitwork.component !== "function") throw new Error("window.kitwork should be the runtime's root");
if (typeof window.kit !== "undefined" || typeof kit !== "undefined") throw new Error("the runtime should have no root named kit, found " + typeof window.kit);
console.log("one root, window.kitwork");
`

func TestTheRuntimeHasOneRoot(t *testing.T) {
	runNodeDOMScript(t, "one_root.test.js", oneRootAssertions)
}

// The control: a runtime that names its root kit too, as the kernel did until 28/10/2026, is caught.
func TestARuntimeThatNamesKitIsCaught(t *testing.T) {
	out, err := runNodeDOMWith(t, composedRuntime()+"\nwindow.kit = window.kitwork;", oneRootAssertions)
	if err == nil || !strings.Contains(out, "no root named kit") {
		t.Fatalf("a root named kit should be caught; got err=%v\n%s", err, out)
	}
}
