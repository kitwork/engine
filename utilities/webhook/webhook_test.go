package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func TestVerifyRawSecret(t *testing.T) {
	now := time.Unix(1785577257, 0)
	body := []byte(`{"type":"order.paid"}`)
	id := "event-1"
	timestamp := "1785577257"
	mac := hmac.New(sha256.New, []byte("polar_whs_example"))
	_, _ = mac.Write([]byte(id + "." + timestamp + "." + string(body)))
	signature := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	result := VerifyAt(body, id, timestamp, signature, "polar_whs_example", VerifyOptions{SecretMode: "raw"}, now)
	if !result.Valid || result.KeyMode != "raw" {
		t.Fatalf("verify failed: %+v", result)
	}
}

func TestVerifyStandardSecretAndRejectsReplay(t *testing.T) {
	now := time.Unix(1700000000, 0)
	body := []byte("{}")
	id := "event-2"
	timestamp := "1700000000"
	key := []byte("standard-secret-material")
	secret := "whsec_" + base64.RawStdEncoding.EncodeToString(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id + "." + timestamp + "." + string(body)))
	signature := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	valid := VerifyAt(body, id, timestamp, signature, secret, VerifyOptions{}, now)
	if !valid.Valid || valid.KeyMode != "standard" {
		t.Fatalf("standard verify failed: %+v", valid)
	}
	expired := VerifyAt(body, id, timestamp, signature, secret, VerifyOptions{}, now.Add(6*time.Minute))
	if expired.Valid || expired.Error == "" {
		t.Fatalf("expired signature accepted: %+v", expired)
	}
}
