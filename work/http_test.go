package work

import (
	"testing"
)

func TestHTTPIntegration(t *testing.T) {
	kw := &KitWork{tenant: &Tenant{}}
	h := kw.HTTP()
	if h == nil {
		t.Fatal("Expected HTTP adapter, got nil")
	}
}
