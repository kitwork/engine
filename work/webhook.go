package work

import webhookcap "github.com/kitwork/engine/capabilities/webhook"

type Webhook = webhookcap.Adapter

func (w *KitWork) Webhook() *Webhook {
	value := w.Capability("webhook")
	if adapter, ok := value.V.(*webhookcap.Adapter); ok {
		return adapter
	}
	return webhookcap.NewAdapter(w.tenant)
}
