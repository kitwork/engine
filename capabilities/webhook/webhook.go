package webhook

import (
	"time"

	"github.com/kitwork/engine/capabilities"
	webhookutil "github.com/kitwork/engine/utilities/webhook"
	"github.com/kitwork/engine/value"
)

type Adapter struct{}

func NewAdapter(capabilities.Scope) *Adapter { return &Adapter{} }

func (a *Adapter) Verify(bodyVal, headersVal, secretVal value.Value, options ...value.Value) value.Value {
	headers := headersVal.Map()
	id := mapText(headers, "id", "webhook-id")
	timestamp := mapText(headers, "timestamp", "webhook-timestamp")
	signature := mapText(headers, "signature", "webhook-signature")
	opts := webhookutil.VerifyOptions{}
	if len(options) > 0 && options[0].K == value.Map {
		values := options[0].Map()
		if item, ok := values["toleranceSeconds"]; ok && item.IsNumber() {
			opts.Tolerance = time.Duration(item.Int()) * time.Second
		}
		if item, ok := values["secretMode"]; ok {
			opts.SecretMode = item.Text()
		}
	}
	result := webhookutil.Verify([]byte(bodyVal.String()), id, timestamp, signature, secretVal.Text(), opts)
	return value.New(result)
}

func mapText(values map[string]value.Value, keys ...string) string {
	for _, key := range keys {
		if item, ok := values[key]; ok {
			return item.Text()
		}
	}
	return ""
}

func init() {
	capabilities.DefaultRegistry.RegisterWithLifetime("webhook", capabilities.LifetimeApp, func(scope capabilities.Scope) value.Value {
		return value.New(NewAdapter(scope))
	})
}
