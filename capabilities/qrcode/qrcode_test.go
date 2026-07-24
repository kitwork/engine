package qrcode

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestQRCodeContractRealAppChain(t *testing.T) {
	adapter := NewQRCodeAdapter(nil)

	payment := value.New(map[string]any{
		"bank":    "970422",
		"account": "123456789",
		"amount":  50000,
		"memo":    "ung ho website",
	})

	res := adapter.Napas(payment).
		Template(value.NewString("circular")).
		Logo(value.NewString("vietqr")).
		Svg()

	if res.K == value.Invalid {
		t.Fatalf("QRCode contract chain failed: %v", res.V)
	}

	svgText := res.Text()
	if !strings.Contains(svgText, "<svg") {
		t.Fatalf("Expected SVG string output, got: %s", svgText)
	}
}
