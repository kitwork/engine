package work

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

type JWT struct {
	tenant *Tenant
}

func (w *KitWork) JWT() *JWT {
	return &JWT{tenant: w.tenant}
}

type JWTVerifyResult struct {
	Valid   bool                   `json:"valid"`
	Payload map[string]interface{} `json:"payload"`
	Error   string                 `json:"error"`
}

func base64URLEncode(src []byte) string {
	return base64.RawURLEncoding.EncodeToString(src)
}

func base64URLDecode(src string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(src)
}

func computeHMAC256(message, secret []byte) []byte {
	key := hmac.New(sha256.New, secret)
	key.Write(message)
	return key.Sum(nil)
}

func parseExpiresIn(val value.Value) (time.Duration, error) {
	if val.K == value.Number {
		return time.Duration(val.N) * time.Second, nil
	}
	if val.K == value.String {
		str := strings.TrimSpace(val.Text())
		if len(str) == 0 {
			return 0, fmt.Errorf("empty expiration string")
		}
		unit := str[len(str)-1]
		numStr := str[:len(str)-1]
		num, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", numStr)
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
			return 0, fmt.Errorf("unknown unit: %c", unit)
		}
	}
	return 0, fmt.Errorf("unsupported expiresIn type")
}

func (j *JWT) Sign(payloadVal value.Value, secretVal value.Value, opts ...value.Value) value.Value {
	secret := secretVal.Text()
	if secret == "" {
		return value.Value{K: value.Invalid, V: "jwt sign error: secret key is required"}
	}

	claims := make(map[string]interface{})
	if payloadVal.K == value.Map {
		m := payloadVal.Map()
		for k, v := range m {
			claims[k] = v.Interface()
		}
	} else {
		claims["sub"] = payloadVal.Interface()
	}

	// Handle options
	if len(opts) > 0 && opts[0].K == value.Map {
		m := opts[0].Map()
		if expOpt, ok := m["expiresIn"]; ok {
			dur, err := parseExpiresIn(expOpt)
			if err != nil {
				return value.Value{K: value.Invalid, V: "jwt sign error: invalid expiresIn: " + err.Error()}
			}
			claims["exp"] = time.Now().Add(dur).Unix()
		}
	}

	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}

	headerBytes, _ := json.Marshal(header)
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return value.Value{K: value.Invalid, V: "jwt sign error: failed to marshal payload: " + err.Error()}
	}

	headerB64 := base64URLEncode(headerBytes)
	claimsB64 := base64URLEncode(claimsBytes)

	signingInput := headerB64 + "." + claimsB64
	sigBytes := computeHMAC256([]byte(signingInput), []byte(secret))
	sigB64 := base64URLEncode(sigBytes)

	token := signingInput + "." + sigB64
	return value.New(token)
}

func (j *JWT) Verify(tokenVal value.Value, secretVal value.Value) value.Value {
	tokenStr := tokenVal.Text()
	secret := secretVal.Text()

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return value.New(JWTVerifyResult{Valid: false, Error: "invalid token format (expected 3 parts)"})
	}

	signingInput := parts[0] + "." + parts[1]
	sig, err := base64URLDecode(parts[2])
	if err != nil {
		return value.New(JWTVerifyResult{Valid: false, Error: "failed to decode signature"})
	}

	expectedSig := computeHMAC256([]byte(signingInput), []byte(secret))
	if !hmac.Equal(sig, expectedSig) {
		return value.New(JWTVerifyResult{Valid: false, Error: "invalid signature"})
	}

	payloadBytes, err := base64URLDecode(parts[1])
	if err != nil {
		return value.New(JWTVerifyResult{Valid: false, Error: "failed to decode payload"})
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return value.New(JWTVerifyResult{Valid: false, Error: "failed to parse payload json"})
	}

	now := time.Now().Unix()
	if expVal, ok := claims["exp"]; ok {
		if expFloat, ok := expVal.(float64); ok {
			if now > int64(expFloat) {
				return value.New(JWTVerifyResult{Valid: false, Error: "token expired"})
			}
		}
	}
	if nbfVal, ok := claims["nbf"]; ok {
		if nbfFloat, ok := nbfVal.(float64); ok {
			if now < int64(nbfFloat) {
				return value.New(JWTVerifyResult{Valid: false, Error: "token not active yet"})
			}
		}
	}

	return value.New(JWTVerifyResult{Valid: true, Payload: claims})
}

func (j *JWT) Decode(tokenVal value.Value) value.Value {
	tokenStr := tokenVal.Text()
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return value.NewNil()
	}

	payloadBytes, err := base64URLDecode(parts[1])
	if err != nil {
		return value.NewNil()
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return value.NewNil()
	}

	return value.New(claims)
}
