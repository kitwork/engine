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

func TestQRCodeLegacyBuilderContract(t *testing.T) {
	result := NewQRCodeAdapter(nil).
		Data(value.NewString("https://kitwork.io")).
		Center(value.NewString("vietqr")).
		CellColor(value.NewString("#111111")).
		CellSize(value.New(0.9)).
		BorderColor(value.NewString("#ff0000")).
		BorderSize(value.New(2)).
		Padding(value.New(3)).
		Merge(value.New(true)).
		FinderColor(value.NewString("#222222")).
		FinderStroke(value.NewString("#333333")).
		FinderRounded(value.New(0.2)).
		Svg()

	if result.K == value.Invalid {
		t.Fatalf("legacy QR builder chain failed: %v", result.V)
	}
	if !strings.Contains(result.Text(), "<svg") {
		t.Fatalf("expected SVG output, got %q", result.Text())
	}
}
