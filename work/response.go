package work

import "github.com/kitwork/engine/value"

type Response struct {
	Data    value.Value
	Type    string
	Code    int
	Headers map[string]string
}

func (r *Response) JSON(data value.Value) {
	r.Data = data
	r.Type = "json"
}

func (r *Response) HTML(data value.Value) {
	r.Data = data
	r.Type = "html"
}

func (r *Response) Status(code int) {
	r.Code = code
}

func (r *Response) Header(key, val string) {
	if r.Headers == nil {
		r.Headers = make(map[string]string)
	}
	r.Headers[key] = val
}
