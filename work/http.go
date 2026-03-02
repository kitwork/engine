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
	timeout time.Duration
	headers map[string]string
}

func (h *HTTP) Timeout(ms int) *HTTP {
	h.timeout = time.Duration(ms) * time.Millisecond
	return h
}

func (h *HTTP) Header(key, val string) *HTTP {
	if h.headers == nil {
		h.headers = make(map[string]string)
	}
	h.headers[key] = val
	return h
}

type HTTPResponse struct {
	Status int
	Body   value.Value
	Error  string
}

// JSON method for javascript: fetch.json()
func (r HTTPResponse) JSON() value.Value {
	var jsonData any
	if err := json.Unmarshal(r.Body.Bytes(), &jsonData); err == nil {
		return value.New(jsonData)
	}
	return value.New(nil)
}

// Support direct .body access if it's already a string or bytes
func (r HTTPResponse) Text() string {
	return r.Body.String()
}

func (h *HTTP) Get(url string) value.Value {
	return h.do("GET", url, value.New(nil))
}

func (h *HTTP) Post(url string, body value.Value) value.Value {
	return h.do("POST", url, body)
}

func (h *HTTP) do(method, url string, body value.Value) value.Value {
	timeout := h.timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	var bodyReader io.Reader
	if !body.IsBlank() {
		if body.K == value.String {
			bodyReader = strings.NewReader(body.String())
		} else {
			b, _ := json.Marshal(body.Interface())
			bodyReader = bytes.NewReader(b)
		}
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return value.New(HTTPResponse{Status: 0, Error: err.Error()})
	}

	// Apply headers
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}

	// Default Content-Type for POST if not set
	if method == "POST" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return value.New(HTTPResponse{Status: 0, Error: err.Error()})
	}
	defer resp.Body.Close()

	resBody, _ := io.ReadAll(resp.Body)

	return value.New(HTTPResponse{
		Status: resp.StatusCode,
		Body:   value.New(resBody),
	})
}

func (w *KitWork) HTTP() *HTTP { return &HTTP{} }
