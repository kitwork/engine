package work

import "github.com/kitwork/engine/value"

type Response struct {
	data    value.Value
	kind    string
	code    int
	headers map[string]string
}

func (r *Response) JSON(data value.Value) {
	r.data = data
	r.kind = "json"
}

func (r *Response) HTML(data value.Value) {
	r.data = data
	r.kind = "html"
}

func (r *Response) Status(code int) {
	r.code = code
}

func (r *Response) Code() int {
	return r.code
}

func (r *Response) Type() string {
	return r.kind
}

func (r *Response) Data() value.Value {
	return r.data
}

func (r *Response) String() string {
	return r.data.String()
}

func (r *Response) Bytes() []byte {
	return []byte(r.data.String())
}

// func (r *Response) Header(key, val string) {
// 	if r.Headers == nil {
// 		r.Headers = make(map[string]string)
// 	}
// 	r.Headers[key] = val
// }
