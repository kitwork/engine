package work

import (
	"github.com/kitwork/engine/value"
)

type Response struct {
	data value.Value
	kind string
	code int
	// headers map[string]string
}

func (r *Response) IsSend() bool {
	return r.kind != "" || !r.data.IsBlank()
}

func (r *Response) IsError() bool { return r.kind == "error" }

func (r *Response) JSON(data value.Value, code ...int) {
	r.Return(data, "json", code...)
}

func (r *Response) Return(data value.Value, kind string, code ...int) {
	r.data = data
	r.kind = kind
	if len(code) > 0 {
		r.code = code[0]
	}
}

func (r *Response) Send(data value.Value, options ...interface{}) {
	r.data = data
	if len(options) == 0 {
		if !data.IsBlank() {
			r.kind = "" // Clear kind to let responder guess from data
		}
		return
	}
	for _, opt := range options {
		switch v := opt.(type) {
		case int:
			r.code = v
		case string:
			r.kind = v
		case value.Value:
			// Xử lý nếu tham số đến từ JavaScript VM
			if v.IsNumber() {
				r.code = int(v.N)
			} else if v.IsString() {
				r.kind = v.String()
			}
		}
	}
}

func (r *Response) String(data string, code ...int) {
	r.Return(value.New(data), "text", code...)
}

func (r *Response) File(path string, code ...int) {
	r.Return(value.New(path), "file", code...)
}

func (r *Response) Directory(path string, code ...int) {
	r.Return(value.New(path), "directory", code...)
}

func (r *Response) Redirect(url value.Value, code ...int) {
	r.Return(url, "redirect", code...)
}

func (r *Response) HTML(data value.Value, code ...int) {
	r.Return(data, "html", code...)
}

func (r *Response) Image(data []byte, code ...int) {
	r.Return(value.New(data), "image", code...)
}

func (r *Response) Bytes(data []byte, code ...int) {
	r.Return(value.New(data), "bytes", code...)
}

func (r *Response) Text(data value.Value, code ...int) {
	r.Return(data, "text", code...)
}

func (r *Response) Error(data value.Value, code ...int) {
	r.ErrorString(data.String(), code...)
}

func (r *Response) ErrorString(data string, code ...int) {
	r.Return(value.New(data), "error", code...)
}

func (r *Response) HelloWorld() {
	r.String("Hello World")
}

func (r *Response) NotFound() {
	r.Error(value.New("404 Not Found"), 404)
}

func (r *Response) Status(code int) *Response {
	r.code = code
	return r
}

func (r *Response) Code() int {
	return r.code
}

func (r *Response) toBytes() []byte {
	return []byte(r.data.String())
}

func (r *Response) Kind() string      { return r.kind }
func (r *Response) Data() value.Value { return r.data }
