package work

import (
	jwtcap "github.com/kitwork/engine/capabilities/jwt"
)

type JWT = jwtcap.JWTAdapter

func (w *KitWork) JWT() *JWT {
	val := w.Capability("jwt")
	if adapter, ok := val.V.(*jwtcap.JWTAdapter); ok {
		return adapter
	}
	return jwtcap.NewJWTAdapter(w.tenant)
}
