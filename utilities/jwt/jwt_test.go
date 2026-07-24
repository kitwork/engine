package jwt_test

import (
	"testing"
	"time"

	"github.com/kitwork/engine/utilities/jwt"
)

func TestJWTSignAndVerify(t *testing.T) {
	claims := map[string]interface{}{
		"sub":  "user_123",
		"role": "admin",
	}
	secret := "super-secret-key"

	token, err := jwt.Sign(claims, secret, time.Hour)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	result := jwt.Verify(token, secret)
	if !result.Valid {
		t.Fatalf("Verify expected valid, got error: %s", result.Error)
	}
	if result.Payload["sub"] != "user_123" {
		t.Errorf("Expected sub 'user_123', got '%v'", result.Payload["sub"])
	}

	// Verify with wrong secret
	invalidRes := jwt.Verify(token, "wrong-key")
	if invalidRes.Valid {
		t.Error("Expected verify to fail with wrong secret, but succeeded")
	}

	// Decode without verification
	decoded, err := jwt.Decode(token)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if decoded["sub"] != "user_123" {
		t.Errorf("Expected decoded sub 'user_123', got '%v'", decoded["sub"])
	}
}
