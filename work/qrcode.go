package work

import (
	qrcap "github.com/kitwork/engine/capabilities/qrcode"
)

type Qrcode = qrcap.QRCodeAdapter

// Qrcode returns a FRESH builder instance on every call to ensure request isolation
// and prevent state bleeding across concurrent requests (e.g. qrcode.napas().template().logo().svg()).
func (w *KitWork) Qrcode() *Qrcode {
	if w == nil {
		return qrcap.NewQRCodeAdapter(nil)
	}
	return qrcap.NewQRCodeAdapter(w.tenant)
}
