// Package jwt provides pure Go JWT token signing, verification, and decoding (HS256).
package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// VerifyResult contains the outcome of a JWT validation attempt.
type VerifyResult struct {
	Valid   bool                   `json:"valid"`
	Payload map[string]interface{} `json:"payload"`
	Error   string                 `json:"error"`
}

// Base64URLEncode encodes bytes into raw URL-safe base64 string.
func Base64URLEncode(src []byte) string {
	return base64.RawURLEncoding.EncodeToString(src)
}

// Base64URLDecode decodes a raw URL-safe base64 string into bytes.
func Base64URLDecode(src string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(src)
}

// ComputeHMAC256 calculates the HMAC-SHA256 signature for a message and secret.
func ComputeHMAC256(message, secret []byte) []byte {
	key := hmac.New(sha256.New, secret)
	key.Write(message)
	return key.Sum(nil)
}

// ParseExpiresIn parses standard expiration strings (e.g. "1h", "30m", "7d", "3600s") or seconds.
func ParseExpiresIn(str string) (time.Duration, error) {
	str = strings.TrimSpace(str)
	if len(str) == 0 {
		return 0, errors.New("empty expiration string")
	}
	unit := str[len(str)-1]
	numStr := str[:len(str)-1]
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		// Fallback: try parsing entire string as seconds
		numSec, errSec := strconv.ParseFloat(str, 64)
		if errSec == nil {
			return time.Duration(numSec) * time.Second, nil
		}
		return 0, fmt.Errorf("invalid number: %s", str)
	}
	switch unit {
	case 's', 'S':
		return time.Duration(num) * time.Second, nil
	case 'm', 'M':
		return time.Duration(num) * time.Minute, nil
	case 'h', 'H':
		return time.Duration(num) * time.Hour, nil
	case 'd', 'D':
		return time.Duration(num) * 24 * time.Hour, nil
	default:
		// Fallback: try parsing whole string as seconds
		if numSec, errSec := strconv.ParseFloat(str, 64); errSec == nil {
			return time.Duration(numSec) * time.Second, nil
		}
		return 0, fmt.Errorf("unknown unit: %c", unit)
	}
}

// Sign creates an HS256 JWT token using claims, secret, and optional expiration duration (0 = no exp).
func Sign(claims map[string]interface{}, secret string, dur time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("secret key is required")
	}
	if claims == nil {
		claims = make(map[string]interface{})
	}

	if dur > 0 {
		claims["exp"] = time.Now().Add(dur).Unix()
	}

	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}

	headerBytes, _ := json.Marshal(header)
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	headerB64 := Base64URLEncode(headerBytes)
	claimsB64 := Base64URLEncode(claimsBytes)

	signingInput := headerB64 + "." + claimsB64
	sigBytes := ComputeHMAC256([]byte(signingInput), []byte(secret))
	sigB64 := Base64URLEncode(sigBytes)

	return signingInput + "." + sigB64, nil
}

// Verify validates an HS256 JWT token against the given secret key.
func Verify(tokenStr, secret string) VerifyResult {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return VerifyResult{Valid: false, Error: "invalid token format (expected 3 parts)"}
	}

	signingInput := parts[0] + "." + parts[1]
	sig, err := Base64URLDecode(parts[2])
	if err != nil {
		return VerifyResult{Valid: false, Error: "failed to decode signature"}
	}

	expectedSig := ComputeHMAC256([]byte(signingInput), []byte(secret))
	if !hmac.Equal(sig, expectedSig) {
		return VerifyResult{Valid: false, Error: "invalid signature"}
	}

	payloadBytes, err := Base64URLDecode(parts[1])
	if err != nil {
		return VerifyResult{Valid: false, Error: "failed to decode payload"}
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return VerifyResult{Valid: false, Error: "failed to parse payload json"}
	}

	now := time.Now().Unix()
	if expVal, ok := claims["exp"]; ok {
		if expFloat, ok := expVal.(float64); ok {
			if now > int64(expFloat) {
				return VerifyResult{Valid: false, Error: "token expired"}
			}
		}
	}
	if nbfVal, ok := claims["nbf"]; ok {
		if nbfFloat, ok := nbfVal.(float64); ok {
			if now < int64(nbfFloat) {
				return VerifyResult{Valid: false, Error: "token not active yet"}
			}
		}
	}

	return VerifyResult{Valid: true, Payload: claims}
}

// Decode extracts claims from a JWT token string without signature verification.
func Decode(tokenStr string) (map[string]interface{}, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}

	payloadBytes, err := Base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse payload json: %w", err)
	}

	return claims, nil
}
