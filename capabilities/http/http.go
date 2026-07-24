package http

import (
	"github.com/kitwork/engine/capabilities"
	httputil "github.com/kitwork/engine/utilities/http"
	"github.com/kitwork/engine/value"
)

type HTTPAdapter struct {
	scope  capabilities.Scope
	client *httputil.HTTP
}

func NewHTTPAdapter(scope capabilities.Scope) *HTTPAdapter {
	return &HTTPAdapter{
		scope:  scope,
		client: httputil.NewClient(nil, nil),
	}
}

func (h *HTTPAdapter) Get(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "http: url required"}
	}
	url := args[0].Text()
	resp := h.client.Get(url)
	return value.New(resp)
}

func (h *HTTPAdapter) Post(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "http: url required"}
	}
	urlVal := args[0]
	var bodyVal value.Value
	if len(args) > 1 {
		bodyVal = args[1]
	}
	resp := h.client.Post(urlVal.Text(), bodyVal)
	return value.New(resp)
}

func (h *HTTPAdapter) Fetch(args ...value.Value) value.Value {
	return h.Get(args...)
}

func init() {
	capabilities.DefaultRegistry.Register("http", func(scope capabilities.Scope) value.Value {
		return value.New(NewHTTPAdapter(scope))
	})
}
