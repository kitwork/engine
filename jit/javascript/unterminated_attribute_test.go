package javascript

import (
	"testing"
	"time"
)

// An authored attribute that never closes its quote drags the quote state across the rest of
// the document, so a tag's "end" lands far away and the attribute walk meets bytes no real tag
// contains — `>` where a name should start. Every attribute loop must still make progress;
// one that did not spun forever inside InjectDelivery at boot, with a live host that accepted
// connections and answered none of them.
func TestInjectDeliveryTerminatesOnUnterminatedAttribute(t *testing.T) {
	source := []byte(`<!doctype html><html><head><meta charset="utf-8"></head><body>
<section><a class="group">
<span class="flex h-10 w-10 rounded-xl bg-canvas hover:bg-warning-soft hover:text-warning><i
class=" logo-ubuntu block h-5 w-5" aria-hidden="true"></i></span>
<span class="flex"><i class="icon-brand-windows block h-5 w-5" aria-hidden="true"></i></span>
<div data-kit-component="dropdown@2.0.0"><button data-kit-click="toggle()">x</button></div>
</a></section></body></html>`)
	done := make(chan error, 1)
	go func() {
		_ = hasRuntimeMarkerAttribute(source)
		_, err := deliveryHeadOffset(source)
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the delivery scanners did not terminate on an unterminated attribute")
	}
}
