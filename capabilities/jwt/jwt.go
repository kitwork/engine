package jwt

import (
	"time"

	"github.com/kitwork/engine/capabilities"
	jwthelper "github.com/kitwork/engine/utilities/jwt"
	"github.com/kitwork/engine/value"
)

type JWTAdapter struct {
	scope capabilities.Scope
}

func NewJWTAdapter(scope capabilities.Scope) *JWTAdapter {
	return &JWTAdapter{scope: scope}
}

func (j *JWTAdapter) Sign(payloadVal value.Value, secretVal value.Value, opts ...value.Value) value.Value {
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
				return value.Value{K: value.Invalid, V: "jwt sign error: invalid expiresIn: " + err.Error()}
			}
		}
	}

	token, err := jwthelper.Sign(claims, secret, dur)
	if err != nil {
		return value.Value{K: value.Invalid, V: "jwt sign error: " + err.Error()}
	}

	return value.New(token)
}

func (j *JWTAdapter) Verify(tokenVal value.Value, secretVal value.Value) value.Value {
	token := tokenVal.Text()
	secret := secretVal.Text()

	res := jwthelper.Verify(token, secret)
	return value.New(res)
}

func (j *JWTAdapter) Decode(tokenVal value.Value) value.Value {
	token := tokenVal.Text()
	res, _ := jwthelper.Decode(token)
	return value.New(res)
}

func init() {
	capabilities.DefaultRegistry.Register("jwt", func(scope capabilities.Scope) value.Value {
		return value.New(NewJWTAdapter(scope))
	})
}
