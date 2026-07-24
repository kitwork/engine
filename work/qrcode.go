package work

import (
	qrcap "github.com/kitwork/engine/capabilities/qrcode"
)

type Qrcode = qrcap.QRCodeAdapter

func (w *KitWork) Qrcode() *Qrcode {
	val := w.Capability("qrcode")
	if adapter, ok := val.V.(*qrcap.QRCodeAdapter); ok {
		return adapter
	}
	return qrcap.NewQRCodeAdapter(w.tenant)
}
