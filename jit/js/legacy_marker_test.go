package js

import (
	"strings"
	"testing"
)

// A page rendered before the rename (10/10/2026) carries its kernel as data-kitwork-jit="runtime"; it
// already has the runtime and must not get a second one. Control: the same page without that tag
// does get the kernel.
func TestDeliverLeavesAPageWithTheOldKernelTagAlone(t *testing.T) {
	old := `<html><head><script data-kitwork-jit="runtime" src="/kit/kernel.0000000000000000.js" defer></script></head><body><b data-kit-text="'x'">x</b></body></html>`
	if Deliver(old, nil, Options{}) != old {
		t.Error("a page that already loads the kernel under the old name got a second kernel")
	}
	fresh := strings.Replace(old, `<script data-kitwork-jit="runtime" src="/kit/kernel.0000000000000000.js" defer></script>`, "", 1)
	if out := Deliver(fresh, nil, Options{}); !strings.Contains(out, `<script data-kitwork-generated="runtime"`) {
		t.Errorf("control: the page without a kernel must get one, under today's name:\n%s", out)
	}
}
