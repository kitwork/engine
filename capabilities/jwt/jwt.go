package jwt

import (
	"time"

	"github.com/kitwork/engine/capabilities"
	jwthelper "github.com/kitwork/engine/utilities/jwt"
	"github.com/kitwork/engine/value"
)

type JWTAdapter struct{}

func NewJWTAdapter(capabilities.Scope) *JWTAdapter {
	return &JWTAdapter{}
}

func (j *JWTAdapter) Sign(payloadVal value.Value, secretVal value.Value, opts ...value.Value) value.Value {
	secret := secretVal.Text()
	if secret == "" {
		return value.InvalidFailure(value.InvalidArgumentCode, "jwt sign error: secret key is required")
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

	var dur time.Duration
	if len(opts) > 0 && opts[0].K == value.Map {
		m := opts[0].Map()
		if expOpt, ok := m["expiresIn"]; ok {
			var err error
			if expOpt.K == value.Number {
				dur = time.Duration(expOpt.N) * time.Second
			} else {
				dur, err = jwthelper.ParseExpiresIn(expOpt.Text())
			}
			if err != nil {
				return value.InvalidFailure(value.InvalidArgumentCode, "jwt sign error: invalid expiresIn: "+err.Error())
			}
		}
	}

	token, err := jwthelper.Sign(claims, secret, dur)
	if err != nil {
		return value.InvalidFailure(value.InvalidArgumentCode, "jwt sign error: "+err.Error())
	}

	return value.New(token)
}

func (j *JWTAdapter) Verify(tokenVal value.Value, secretVal value.Value) value.Value {
	token := tokenVal.Text()
	secret := secretVal.Text()

	res := jwthelper.Verify(token, secret)
	return value.New(res)
}

// Decode reads a token's claims without checking it. A token that is not one (not three parts, a
// payload that is not base64 JSON) is INVALID_INPUT, data from outside that cannot be read, as the
// closed set says of a token (14/10/2026: it gave {}). Verify stays data: null for any token it cannot
// trust, a malformed one included, since a visitor's cookie is such a token every day.
func (j *JWTAdapter) Decode(tokenVal value.Value) value.Value {
	token := tokenVal.Text()
	res, err := jwthelper.Decode(token)
	if err != nil {
		return value.InvalidFailure(value.InvalidInputCode, "jwt decode: "+err.Error())
	}
	return value.New(res)
}

func init() {
	capabilities.DefaultRegistry.RegisterWithLifetime("jwt", capabilities.LifetimeApp, func(scope capabilities.Scope) value.Value {
		return value.New(NewJWTAdapter(scope))
	})
}
