package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
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

	serverPort := 0
	if GetServerPort != nil {
		serverPort = GetServerPort()
	}

	var isLocalRequest bool
	if strings.HasPrefix(url, "/") {
		isLocalRequest = true
		port := 8080
		if serverPort > 0 {
			port = serverPort
		}
		url = fmt.Sprintf("http://127.0.0.1:%d%s", port, url)
	}

	req, err := stdhttp.NewRequest(method, url, bodyReader)
	if err != nil {
		return value.New(Response{Status: 0, Error: err.Error()})
	}

	for k, v := range h.headers {
		req.Header.Set(k, v)
	}

	if method == "POST" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	allowLocal := false
	if IsLocalAllowed != nil {
		allowLocal = IsLocalAllowed()
	}

	transport := sharedTransport
	if isLocalRequest || allowLocal {
		transport = localTransport
	}

	var client *stdhttp.Client
	if h.timeout > 0 {
		client = &stdhttp.Client{
			Transport: transport,
			Timeout:   h.timeout,
		}
	} else {
		if isLocalRequest || allowLocal {
			client = localClient
		} else {
			client = sharedClient
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return value.New(Response{Status: 0, Error: err.Error()})
	}
	defer resp.Body.Close()

	resBody, _ := io.ReadAll(resp.Body)

	return value.New(Response{
		Status: resp.StatusCode,
		Body:   value.New(resBody),
	})
}
