package work

import (
	"encoding/json"
	"net/http"

	"github.com/kitwork/engine/value"
)

type Response struct {
	data    value.Value
	kind    string
	code    int
	headers map[string]string

	send bool // Cờ đánh dấu đã có phản hồi (Returning)
}

// IsSend kiểm tra xem đã gửi phản hồi chưa
func (r *Response) IsSend() bool  { return r.send }
func (r *Response) IsError() bool { return r.kind == "error" }

func (r *Response) JSON(data value.Value, code ...int) *Response {
	return r.Send("json", data, code...)
}

func (r *Response) Send(kind string, data value.Value, code ...int) *Response {
	if !r.send {
		r.send = true
		if len(code) > 0 {
			r.code = code[0]
		}
		r.kind = kind
		r.data = data
	}
	return r
}

func (r *Response) String(data string, code ...int) *Response {
	return r.Send("text", value.New(data), code...)
}

func (r *Response) File(path string, code ...int) *Response {
	return r.Send("file", value.New(path), code...)
}

func (r *Response) Directory(path string, code ...int) *Response {
	return r.Send("directory", value.New(path), code...)
}

func (r *Response) Redirect(url value.Value, code ...int) *Response {
	return r.Send("redirect", url, code...)
}

func (r *Response) HTML(data value.Value, code ...int) *Response {
	return r.Send("html", data, code...)
}

func (r *Response) Image(data []byte, code ...int) *Response {
	return r.Send("image", value.New(data), code...)
}

func (r *Response) Bytes(data []byte, code ...int) *Response {
	return r.Send("bytes", value.New(data), code...)
}

func (r *Response) Text(data value.Value, code ...int) *Response {
	return r.Send("text", data, code...)
}

func (r *Response) Error(data value.Value, code ...int) *Response {
	return r.ErrorString(data.String(), code...)
}

func (r *Response) ErrorString(data string, code ...int) *Response {
	return r.Send("error", value.New(data), code...)
}

func (r *Response) HelloWorld() *Response {
	return r.String("Hello World")
}

func (r *Response) NotFound() *Response {
	if r.send {
		return r
	}

	return r.Error(value.New("404 Not Found"), 404)
}

func (r *Response) Status(code int) *Response {
	if r.send {
		return r
	}
	r.code = code
	return r
}

func (r *Response) Code() int {
	return r.code
}

func (r *Response) toBytes() []byte {
	return []byte(r.data.String())
}

func (r *Response) toJson() ([]byte, error) {
	if r.kind != "json" {
		return nil, nil
	}
	return json.Marshal(r.data)
}

func (r *Response) Type() string { return r.kind }

func (r *Response) Write(w http.ResponseWriter, req *http.Request) error {
	if r.kind == "" {
		http.NotFound(w, req)
		return nil
	}

	if r.code == 0 {
		r.code = 200
	}

	switch r.kind {
	case "redirect":
		http.Redirect(w, req, r.data.String(), http.StatusSeeOther)
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(r.Code())
		w.Write(r.toBytes())
	case "file":
		http.ServeFile(w, req, r.data.String())
	case "directory":
		http.FileServer(http.Dir(r.data.String())).ServeHTTP(w, req)
	case "bytes":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(r.Code())
		w.Write(r.data.Bytes())
	case "image":
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(r.Code())
		w.Write(r.data.Bytes())
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(r.Code())
		b, err := r.toJson()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return err
		}
		w.Write(b)
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(r.Code())
		w.Write(r.toBytes())
	case "error":
		w.WriteHeader(r.Code())
		w.Write(r.toBytes())
	default:
		http.NotFound(w, req)
	}
	return nil
}
