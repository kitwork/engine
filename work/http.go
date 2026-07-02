package work

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/kitwork/engine/value"
)

var AllowLocal bool
var ServerPort int

var sharedTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip != nil {
				if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
					return fmt.Errorf("SSRF prevention: connection to private/local space is blocked (%s)", host)
				}
			}
			return nil
		},
	}).DialContext,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxIdleConnsPerHost: 100,
}

var localTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxIdleConnsPerHost: 100,
}

var sharedClient = &http.Client{
	Transport: sharedTransport,
	Timeout:   10 * time.Second,
}

var localClient = &http.Client{
	Transport: localTransport,
	Timeout:   10 * time.Second,
}

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

// Base64 method for javascript: fetch.base64()
func (r HTTPResponse) Base64() string {
	b := r.Body.Bytes()
	b64 := base64.StdEncoding.EncodeToString(b)

	var mimeType string
	if len(b) >= 4 && bytes.HasPrefix(b, []byte("\x89PNG")) {
		mimeType = "image/png"
	} else if len(b) >= 3 && bytes.HasPrefix(b, []byte("\xff\xd8\xff")) {
		mimeType = "image/jpeg"
	} else if len(b) >= 6 && (bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a"))) {
		mimeType = "image/gif"
	} else if bytes.Contains(b, []byte("<svg")) {
		mimeType = "image/svg+xml"
	}

	if mimeType != "" {
		return fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
	}
	return b64
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

	var isLocalRequest bool
	if strings.HasPrefix(url, "/") {
		isLocalRequest = true
		port := 8080
		if ServerPort > 0 {
			port = ServerPort
		}
		url = fmt.Sprintf("http://127.0.0.1:%d%s", port, url)
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

	transport := sharedTransport
	if isLocalRequest || AllowLocal {
		transport = localTransport
	}

	var client *http.Client
	if h.timeout > 0 {
		client = &http.Client{
			Transport: transport,
			Timeout:   h.timeout,
		}
	} else {
		if isLocalRequest || AllowLocal {
			client = localClient
		} else {
			client = sharedClient
		}
	}

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

func (r HTTPResponse) Ok() bool {
	return r.Status >= 200 && r.Status < 300
}

func (w *KitWork) HTTP() *HTTP { return &HTTP{} }

// globalFetch implements the browser-compatible global fetch(url, options) function.
func globalFetch(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.New(HTTPResponse{Status: 0, Error: "fetch: url is required"})
	}
	urlStr := args[0].Text()

	h := &HTTP{}
	method := "GET"
	var body value.Value

	if len(args) > 1 && args[1].IsMap() {
		opts := args[1].Map()
		if m, ok := opts["method"]; ok {
			method = strings.ToUpper(m.String())
		}
		if b, ok := opts["body"]; ok {
			body = b
		}
		if t, ok := opts["timeout"]; ok {
			if t.IsNumeric() {
				h.timeout = time.Duration(t.N) * time.Millisecond
			} else {
				if d, err := ParseDuration(t.String()); err == nil {
					h.timeout = d
				}
			}
		}
		if hdrs, ok := opts["headers"]; ok && hdrs.IsMap() {
			h.headers = make(map[string]string)
			for k, v := range hdrs.Map() {
				h.headers[k] = v.String()
			}
		}
	}

	return h.do(method, urlStr, body)
}
