package work

import (
	"time"

	jwthelper "github.com/kitwork/engine/helpers/jwt"
	"github.com/kitwork/engine/value"
)

type JWT struct {
	tenant *Tenant
}

func (w *KitWork) JWT() *JWT {
	return &JWT{tenant: w.tenant}
}

type JWTVerifyResult = jwthelper.VerifyResult

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

func (j *JWT) Verify(tokenVal value.Value, secretVal value.Value) value.Value {
	tokenStr := tokenVal.Text()
	secret := secretVal.Text()

	res := jwthelper.Verify(tokenStr, secret)
	return value.New(res)
}

func (j *JWT) Decode(tokenVal value.Value) value.Value {
	claims, err := jwthelper.Decode(tokenVal.Text())
	if err != nil {
		return value.NewNil()
	}
	return value.New(claims)
}
