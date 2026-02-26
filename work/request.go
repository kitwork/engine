package work

import (
	"net/http"

	"github.com/kitwork/engine/value"
)

type Request struct {
	Request *http.Request
	Writer  http.ResponseWriter

	Params map[string]value.Value // URL Path params like :id
	Query  value.Value
	Body   value.Value
}

func (r *Request) Reset(req *http.Request, writer http.ResponseWriter) {
	r.Request = req
	r.Writer = writer
	r.Query = value.Value{K: value.Nil}
	r.Body = value.Value{K: value.Nil}

	if r.Params == nil {
		r.Params = make(map[string]value.Value)
	} else {
		for k := range r.Params {
			delete(r.Params, k)
		}
	}
}

// Shared empty map to avoid allocation
var zeroPayload = value.New(map[string]value.Value{})

func (r *Request) Payload() value.Value {
	res := make(map[string]value.Value)

	// 1. Path Params (high priority)
	for k, v := range r.Params {
		res[k] = v
	}

	// 2. Query Params
	if r.Query.K == value.Map {
		if m, ok := r.Query.V.(map[string]value.Value); ok {
			for k, v := range m {
				res[k] = v
			}
		}
	}

	// 3. Body Params
	if r.Body.K == value.Map {
		if m, ok := r.Body.V.(map[string]value.Value); ok {
			for k, v := range m {
				res[k] = v
			}
		}
	}

	if len(res) == 0 {
		return zeroPayload
	}

	return value.New(res)
}
