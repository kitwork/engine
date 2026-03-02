package work

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

type HTTP struct {
	tenant  *Tenant
	url     string
	headers map[string]string
	body    []byte
	timeout time.Duration

	response *HTTPResponse
}

type HTTPResponse struct {
	status int
	body   []byte
	resp   *http.Response
	err    error
}

func (r *HTTPResponse) Status() int  { return r.status }
func (r *HTTPResponse) Ok() bool     { return r.status >= 200 && r.status < 300 }
func (r *HTTPResponse) Text() string { return string(r.body) }
func (r *HTTPResponse) Error() string {
	if r.err != nil {
		return r.err.Error()
	}
	return ""
}
func (r *HTTPResponse) Header(key string) string {
	if r.resp != nil {
		return r.resp.Header.Get(key)
	}
	return ""
}

func (r *HTTPResponse) JSON() value.Value {
	var v value.Value
	v.UnmarshalJSON(r.body)
	return v
}

func (h *HTTP) Header(key, val string) *HTTP {
	newH := *h
	newH.headers = make(map[string]string)
	for k, v := range h.headers {
		newH.headers[k] = v
	}
	newH.headers[key] = val
	return &newH
}

func (h *HTTP) Type(t string) *HTTP {
	switch t {
	case "json":
		t = "application/json"
	case "form":
		t = "application/x-www-form-urlencoded"
	case "text":
		t = "text/plain"
	case "html":
		t = "text/html"
	case "xml":
		t = "application/xml"
	}
	return h.Header("Content-Type", t)
}

func (h *HTTP) Query(key, val string) *HTTP {
	newH := *h
	if strings.Contains(newH.url, "?") {
		newH.url += "&" + key + "=" + val
	} else {
		newH.url += "?" + key + "=" + val
	}
	return &newH
}

func (h *HTTP) Body(v value.Value) *HTTP {
	newH := *h
	if v.K == value.String {
		newH.body = []byte(v.String())
	} else if v.K == value.Bytes {
		newH.body = v.Bytes()
	} else if !v.IsBlank() {
		b, _ := json.Marshal(v)
		newH.body = b
		if _, ok := newH.headers["Content-Type"]; !ok {
			return newH.Header("Content-Type", "application/json")
		}
	}
	return &newH
}

func (h *HTTP) Timeout(d string) *HTTP {
	dur, err := time.ParseDuration(d)
	if err != nil {
		return h
	}
	newH := *h
	newH.timeout = dur
	return &newH
}

func (h *HTTP) Fetch(urlVal value.Value, options ...value.Value) *HTTPResponse {
	method := "GET"
	url := urlVal.String()
	activeH := *h

	if len(options) > 0 && options[0].IsMap() {
		opts := options[0].Map()
		if m, ok := opts["method"]; ok {
			method = m.String()
		}
		if hVal, ok := opts["headers"]; ok && hVal.IsMap() {
			for k, v := range hVal.Map() {
				activeH.headers[k] = v.String()
			}
		}
		if b, ok := opts["body"]; ok {
			return activeH.Body(b).Send(method, url)
		}
	}

	return activeH.Send(method, url)
}

func (h *HTTP) Send(method string, url string) *HTTPResponse {
	req, err := http.NewRequest(method, url, bytes.NewReader(h.body))
	if err != nil {
		hResp := &HTTPResponse{err: err}
		h.response = hResp
		return hResp
	}

	for k, v := range h.headers {
		req.Header.Set(k, v)
	}

	timeout := 30 * time.Second
	if h.timeout > 0 {
		timeout = h.timeout
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		hResp := &HTTPResponse{err: err}
		h.response = hResp
		return hResp
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	hResp := &HTTPResponse{
		status: resp.StatusCode,
		body:   respBody,
		resp:   resp,
	}

	h.response = hResp
	return hResp
}

func (h *HTTP) Get(url string, options ...value.Value) *HTTPResponse {
	return h.Fetch(value.New(url), h.withMethod("GET", options...)...)
}

func (h *HTTP) Post(url string, options ...value.Value) *HTTPResponse {
	return h.Fetch(value.New(url), h.withMethod("POST", options...)...)
}

func (h *HTTP) Put(url string, options ...value.Value) *HTTPResponse {
	return h.Fetch(value.New(url), h.withMethod("PUT", options...)...)
}

func (h *HTTP) Patch(url string, options ...value.Value) *HTTPResponse {
	return h.Fetch(value.New(url), h.withMethod("PATCH", options...)...)
}

func (h *HTTP) Delete(url string, options ...value.Value) *HTTPResponse {
	return h.Fetch(value.New(url), h.withMethod("DELETE", options...)...)
}

func (h *HTTP) withMethod(method string, options ...value.Value) []value.Value {
	var opts map[string]value.Value
	if len(options) > 0 && options[0].IsMap() {
		opts = options[0].Map()
	} else {
		opts = make(map[string]value.Value)
	}
	opts["method"] = value.New(method)
	return []value.Value{value.New(opts)}
}

func (w *KitWork) HTTP() *HTTP { return &HTTP{tenant: w.tenant} }
