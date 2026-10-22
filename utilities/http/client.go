package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	neturl "net/url"
	"strings"

	"github.com/kitwork/engine/value"
)

// guardedClient sends every request to an absolute URL, through the SSRF guard. It has no time limit
// of its own: each attempt carries its .timeout() as a context deadline.
var guardedClient = &stdhttp.Client{Transport: guardedTransport}

// attempt is one try of the request: its answer read whole (up to maxBytes), or why there was none.
func (r *Request) attempt(ctx context.Context, target string, body []byte, contentType string) Outcome {
	attemptContext, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	address, client, self := target, guardedClient, strings.HasPrefix(target, "/")
	if self {
		authority := selfAuthority()
		address = "http://" + authority + target
		client = selfClient(authority, r.selfHost())
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := stdhttp.NewRequestWithContext(attemptContext, r.method, address, reader)
	if err != nil {
		return Outcome{Code: value.NetworkCode, Message: shownError(err)}
	}
	for name, values := range r.header {
		request.Header[name] = append([]string(nil), values...)
	}
	if contentType != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", contentType)
	}
	if host := r.selfHost(); self && host != "" {
		request.Host = host
	}

	response, err := client.Do(request)
	if err != nil {
		return noAnswer(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, r.maxBytes+1))
	if err != nil {
		return noAnswer(err)
	}
	if int64(len(data)) > r.maxBytes {
		return Outcome{
			Status: response.StatusCode, Header: response.Header, Code: value.TooLargeCode,
			Message: fmt.Sprintf("Response body exceeds %s (.maxBytes)", sizeText(r.maxBytes)),
		}
	}
	outcome := Outcome{Status: response.StatusCode, Header: response.Header, Body: data}
	if !outcome.Ok() {
		outcome.Message = stdhttp.StatusText(response.StatusCode)
		if outcome.Message == "" {
			outcome.Message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
	}
	return outcome
}

// selfHost is the Host of this request's self-calls: its site's domain, or none for a Go request.
func (r *Request) selfHost() string {
	if r.module == nil {
		return ""
	}
	return r.module.selfHost
}

// selfAuthority is where a self-call goes: this host's own port on 127.0.0.1 (8080 when none is set).
func selfAuthority() string {
	port := 8080
	if GetServerPort != nil && GetServerPort() > 0 {
		port = GetServerPort()
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// selfClient carries a self-call past the SSRF guard. A redirect is followed only on the same port,
// and keeps the site's Host; one that leaves (to another host, or https://) is the answer itself, a
// 3xx: the guard is not there to check where it goes.
func selfClient(authority, host string) *stdhttp.Client {
	return &stdhttp.Client{
		Transport: selfTransport,
		CheckRedirect: func(next *stdhttp.Request, via []*stdhttp.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if next.URL.Scheme != "http" || next.URL.Host != authority {
				return stdhttp.ErrUseLastResponse
			}
			if host != "" {
				next.Host = host
			}
			return nil
		},
	}
}

// noAnswer is the outcome of a request that got no answer: its code, and Kitwork's sentence for it.
func noAnswer(err error) Outcome {
	code := failureCode(err)
	message := shownError(err)
	if code == value.TimeoutCode {
		message = "Request timed out"
	}
	return Outcome{Code: code, Message: message}
}

// failureCode is the code of a request that got no answer: DENIED when the SSRF guard refused the
// address, value.CancelledCode when the caller's context was cancelled, TIMEOUT past its time limit,
// NETWORK for any other (DNS, TLS, a refused or broken connection).
func failureCode(err error) string {
	var denied *deniedAddress
	var timeout interface{ Timeout() bool }
	switch {
	case errors.As(err, &denied):
		return value.DeniedCode
	case errors.Is(err, context.Canceled):
		return value.CancelledCode
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return value.TimeoutCode
	}
	return value.NetworkCode
}

// shownError is the text of a request's failure as the program reads it (result.error, which a site
// may hand a browser): Go names the URL in it, and a URL can carry a secret, so the URL loses its user
// information, query and fragment ("Get \"https://api.example.com/v1\": dial tcp …").
func shownError(err error) string {
	var failed *neturl.Error
	if errors.As(err, &failed) {
		shown := *failed
		shown.URL = shownURL(failed.URL)
		return shown.Error()
	}
	return err.Error()
}

// shownURL is raw without its user information, query and fragment. raw may not parse (the error of
// a URL that is not one), so it is cut as text.
func shownURL(raw string) string {
	if cut := strings.IndexAny(raw, "?#"); cut >= 0 {
		raw = raw[:cut]
	}
	if scheme := strings.Index(raw, "://"); scheme >= 0 {
		rest := raw[scheme+3:]
		authority := rest
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			authority = rest[:slash]
		}
		if at := strings.LastIndexByte(authority, '@'); at >= 0 {
			raw = raw[:scheme+3] + rest[at+1:]
		}
	}
	return raw
}
