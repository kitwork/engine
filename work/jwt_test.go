package work

import (
	"testing"

	"github.com/kitwork/engine/value"
)

func TestJWTIntegration(t *testing.T) {
	kw := &KitWork{tenant: &Tenant{}}
	jwt := kw.JWT()
	if jwt == nil {
		t.Fatal("Expected JWT adapter, got nil")
	}

	signed := jwt.Sign(value.NewString("user_456"), value.NewString("my_secret"))
	if signed.K == value.Invalid {
		t.Fatalf("JWT sign failed: %v", signed.V)
	}

	verified := jwt.Verify(signed, value.NewString("my_secret"))
	if verified.K == value.Invalid {
		t.Fatalf("JWT verify failed: %v", verified.V)
	}
}
