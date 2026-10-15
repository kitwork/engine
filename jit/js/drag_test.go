package js

import (
	"strings"
	"testing"
)

// data-kit-drag is delivered like data-kit-intersect: a page that writes it gets the kernel and the
// drag file; a page that only prints it in an example gets neither; a page with neither directive
// gets no directive file at all.
func TestDragIsDeliveredWhereWritten(t *testing.T) {
	roles, paths := deliveredFiles(t, Deliver(`<html><head></head><body><header data-kit-component="titlebar" data-kit-drag="true"><button data-kit-drag="false"></button></header></body></html>`, nil, Options{}))
	if strings.Join(roles, ",") != "runtime,directive,component" || !strings.HasPrefix(paths[1], "/_kitwork/directives/drag.") {
		t.Fatalf("a title bar page: roles %v paths %v", roles, paths)
	}
	if asset, exact := AssetAt(paths[1], nil); asset == nil || !exact {
		t.Errorf("%s does not round-trip through AssetAt", paths[1])
	}
	if _, paths := deliveredFiles(t, Deliver(`<html><head></head><body><button data-kit-click="n++"></button></body></html>`, nil, Options{})); len(paths) != 1 {
		t.Errorf("CONTROL: a page without data-kit-drag gets the kernel alone, got %v", paths)
	}
	if out := Deliver(`<html><head></head><body><pre><code>data-kit-drag="true"</code></pre></body></html>`, nil, Options{}); strings.Contains(out, "<script") {
		t.Errorf("CONTROL: a page that only prints the directive gets no script:\n%s", out)
	}
}
