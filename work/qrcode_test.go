package work

import (
	"testing"

	"github.com/kitwork/engine/value"
)

func TestQRCodeIntegration(t *testing.T) {
	kw := &KitWork{tenant: &Tenant{}}
	qr := kw.Qrcode()
	if qr == nil {
		t.Fatal("Expected QRCode adapter, got nil")
	}

	res := qr.Generate(value.NewString("https://kitwork.io"), value.New(256))
	if res.K == value.Invalid {
		t.Fatalf("QRCode generate failed: %v", res.V)
	}
}
