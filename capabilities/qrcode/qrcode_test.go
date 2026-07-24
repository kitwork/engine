package qrcode_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/capabilities"
	qrcap "github.com/kitwork/engine/capabilities/qrcode"
	"github.com/kitwork/engine/value"
)

type mockScope struct{}

func (m *mockScope) AppID() string                     { return "app_test" }
func (m *mockScope) Domain() string                    { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB            { return nil }

func TestQRCodeCapability(t *testing.T) {
	scope := &mockScope{}
	val, ok := capabilities.DefaultRegistry.Get("qrcode", scope)
	if !ok {
		t.Fatal("QRCode capability not registered")
	}

	adapter, ok := val.V.(*qrcap.QRCodeAdapter)
	if !ok {
		t.Fatalf("Expected *qrcap.QRCodeAdapter, got %T", val.V)
	}

	res := adapter.Generate(value.NewString("https://kitwork.io"), value.New(200))
	if res.K == value.Invalid {
		t.Fatalf("QRCode generate failed: %v", res.V)
	}
}
