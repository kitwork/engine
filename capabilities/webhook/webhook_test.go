package webhook_test

import (
	"testing"

	"github.com/kitwork/engine/capabilities"
	webhookcap "github.com/kitwork/engine/capabilities/webhook"
)

func TestCapabilityRegisters(t *testing.T) {
	value, ok := capabilities.DefaultRegistry.Get("webhook", nil)
	if !ok {
		t.Fatal("webhook capability is not registered")
	}
	if _, ok := value.V.(*webhookcap.Adapter); !ok {
		t.Fatalf("adapter type = %T", value.V)
	}
}
