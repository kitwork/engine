package jwt_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/capabilities"
	jwtcap "github.com/kitwork/engine/capabilities/jwt"
	"github.com/kitwork/engine/value"
)

type mockScope struct{}

func (m *mockScope) AppID() string                     { return "app_test" }
func (m *mockScope) Domain() string                    { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB            { return nil }

func TestJWTCapability(t *testing.T) {
	scope := &mockScope{}
	val, ok := capabilities.DefaultRegistry.Get("jwt", scope)
	if !ok {
		t.Fatal("JWT capability not registered")
	}

	adapter, ok := val.V.(*jwtcap.JWTAdapter)
	if !ok {
		t.Fatalf("Expected *jwtcap.JWTAdapter, got %T", val.V)
	}

	signed := adapter.Sign(value.NewString("user_123"), value.NewString("secret_key"))
	if signed.K == value.Invalid {
		t.Fatalf("JWT sign failed: %v", signed.V)
	}

	verified := adapter.Verify(signed, value.NewString("secret_key"))
	if verified.K == value.Invalid {
		t.Fatalf("JWT verify failed: %v", verified.V)
	}
}
