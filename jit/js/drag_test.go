package js

import (
	"strings"
	"testing"
)

// data-kitwork-drag is delivered like intersect-enter: a page that writes it gets the kernel and the
// drag file; a page that only prints it in an example gets neither; a page with neither directive
// gets no directive file at all.
func TestDragIsDeliveredWhereWritten(t *testing.T) {
	roles, paths := deliveredFiles(t, Deliver(`<html><head></head><body><header data-kitwork-component="titlebar" data-kitwork-drag="true"><button data-kitwork-drag="false"></button></header></body></html>`, Options{}))
	if strings.Join(roles, ",") != "runtime,directive,component" || !strings.HasPrefix(paths[1], "/_kitwork/directives/drag.") {
		t.Fatalf("a title bar page: roles %v paths %v", roles, paths)
	}
	if asset, exact := AssetAt(paths[1]); asset == nil || !exact {
		t.Errorf("%s does not round-trip through AssetAt", paths[1])
	}
	if _, paths := deliveredFiles(t, Deliver(`<html><head></head><body><button data-kitwork-event="click: n++"></button></body></html>`, Options{})); len(paths) != 1 {
		t.Errorf("CONTROL: a page without data-kitwork-drag gets the kernel alone, got %v", paths)
	}
	if out := Deliver(`<html><head></head><body><pre><code>data-kitwork-drag="true"</code></pre></body></html>`, Options{}); strings.Contains(out, "<script") {
		t.Errorf("CONTROL: a page that only prints the directive gets no script:\n%s", out)
	}
}
