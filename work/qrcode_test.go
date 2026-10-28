package work

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestQRCodeIntegrationRealAppContract(t *testing.T) {
	kw := &KitWork{tenant: &Tenant{}}
	qr1 := kw.Qrcode()
	qr2 := kw.Qrcode()

	if qr1 == qr2 {
		t.Fatal("Expected fresh QRCode adapter instances for request isolation")
	}

	payment := value.New(map[string]any{
		"bank":    "970422",
		"account": "000012345",
		"amount":  100000,
		"memo":    "test donate",
	})

	svgRes := qr1.Napas(payment).
		Template(value.NewString("circular")).
		Logo(value.NewString("vietqr")).
		Svg()

	if svgRes.K == value.Invalid {
		t.Fatalf("QRCode Napas chain failed: %v", svgRes.V)
	}
	if !strings.Contains(svgRes.Text(), "<svg") {
		t.Fatalf("Expected valid SVG output, got: %s", svgRes.Text())
	}
}
