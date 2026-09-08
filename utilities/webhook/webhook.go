// Package webhook verifies Standard Webhooks HMAC-SHA256 request signatures.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

type VerifyOptions struct {
	Tolerance  time.Duration
	SecretMode string
}

type VerifyResult struct {
	Valid     bool   `json:"valid"`
	Error     string `json:"error"`
	KeyMode   string `json:"keyMode"`
	Timestamp int64  `json:"timestamp"`
}

func Verify(body []byte, id, timestamp, signatures, secret string, options VerifyOptions) VerifyResult {
	return VerifyAt(body, id, timestamp, signatures, secret, options, time.Now())
}

func VerifyAt(body []byte, id, timestamp, signatures, secret string, options VerifyOptions, now time.Time) VerifyResult {
	id = strings.TrimSpace(id)
	timestamp = strings.TrimSpace(timestamp)
	signatures = strings.TrimSpace(signatures)
	secret = strings.TrimSpace(secret)
	if id == "" || timestamp == "" || signatures == "" {
		return VerifyResult{Error: "missing webhook signature headers"}
	}
	if secret == "" {
		return VerifyResult{Error: "webhook secret is not configured"}
	}

	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return VerifyResult{Error: "invalid webhook timestamp"}
	}
	tolerance := options.Tolerance
	if tolerance <= 0 {
		tolerance = 5 * time.Minute
	}
	delta := now.Unix() - unix
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > tolerance {
		return VerifyResult{Error: "webhook timestamp is outside the allowed window", Timestamp: unix}
	}

	message := []byte(id + "." + timestamp + "." + string(body))
	modes := []string{normalizeMode(options.SecretMode)}
	if modes[0] == "either" {
		modes = []string{"raw", "standard"}
	}

	for _, mode := range modes {
		key, keyErr := signatureKey(secret, mode)
		if keyErr != "" {
			continue
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(message)
		expected := mac.Sum(nil)
		if matchesSignature(signatures, expected) {
			return VerifyResult{Valid: true, KeyMode: mode, Timestamp: unix}
		}
	}

	return VerifyResult{Error: "webhook signature does not match", Timestamp: unix}
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "raw", "either":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "standard"
	}
}

func signatureKey(secret, mode string) ([]byte, string) {
	if mode == "raw" {
		return []byte(secret), ""
	}
	encoded := secret
	if strings.HasPrefix(encoded, "whsec_") {
		encoded = strings.TrimPrefix(encoded, "whsec_")
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil || len(decoded) == 0 {
		return nil, "invalid standard webhook secret"
	}
	return decoded, ""
}

func matchesSignature(header string, expected []byte) bool {
	for _, candidate := range strings.Fields(header) {
		parts := strings.SplitN(strings.TrimSpace(candidate), ",", 2)
		if len(parts) != 2 || parts[0] != "v1" {
			continue
		}
		actual, err := base64.StdEncoding.DecodeString(parts[1])
		if err == nil && hmac.Equal(actual, expected) {
			return true
		}
	}
	return false
}
