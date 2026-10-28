package work

import (
	sbcap "github.com/kitwork/engine/capabilities/shortbase"
)

type Shortbase = sbcap.ShortbaseAdapter

func (w *KitWork) Shortbase() *Shortbase {
	val := w.Capability("shortbase")
	if adapter, ok := val.V.(*sbcap.ShortbaseAdapter); ok {
		return adapter
	}
	return sbcap.NewShortbaseAdapter(w.tenant)
}
