package work

// What the asking side of a request may cost and claim (KITWORK_ROUTER §5, 20/10/2026).
//
// Every read of a request body has a ceiling: 1 MiB by default (request.body(), request.json()),
// raised per read with request.json({ limit: "4mb" }) up to the host's ceiling, MaxBodyBytes (the
// server's own, http.MaxBytesHandler in kitwork.go; app.web({ maxBody })). A body past the ceiling is
// not read further, and the request answers 413. Reading the body twice gives the same value.
//
// The client's address and scheme believe a proxy's headers (X-Forwarded-For, X-Forwarded-Proto) only
// when the app says it runs behind one it trusts (TrustProxyHeaders, trust_proxy); else they are the
// connection's address and its TLS. One rule for request.ip, request.scheme, request.secure, the
// cookie's Secure, the page's origin and the rate limits.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// DefaultBodyLimit is the ceiling of a body read that names none.
const DefaultBodyLimit int64 = 1 << 20

// MaxBodyBytes is the host's ceiling on a request body: the server stops reading past it, and no read
// may ask for more. Set from the app's maxBody (kitwork.go); 32 MiB when it says none.
var MaxBodyBytes int64 = 32 << 20

// errBodyTooLarge is a body past its ceiling.
var errBodyTooLarge = errors.New("request body too large")

// ParseByteSize reads a size: a number of bytes, or a number with a unit, "512kb", "4mb", "1gb"
// (1024-based, as MiB).
func ParseByteSize(v value.Value) (int64, error) {
	if v.IsNumeric() {
		if v.N <= 0 {
			return 0, fmt.Errorf("size must be positive: %v", v.N)
		}
		return int64(v.N), nil
	}
	return parseByteSizeText(v.Text())
}

// ParseByteSizeOf is ParseByteSize of a configuration's plain value (a number, a string, a Value).
func ParseByteSizeOf(v any) (int64, error) {
	switch size := v.(type) {
	case value.Value:
		return ParseByteSize(size)
	case string:
		return parseByteSizeText(size)
	case float64:
		return ParseByteSize(value.New(size))
	case int:
		return ParseByteSize(value.New(size))
	case int64:
		return ParseByteSize(value.New(float64(size)))
	}
	return 0, fmt.Errorf("không đọc được cỡ %v: viết như \"32mb\"", v)
}

func parseByteSizeText(text string) (int64, error) {
	s := strings.ToLower(strings.TrimSpace(text))
	units := []struct {
		suffix string
		size   int64
	}{{"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"b", 1}}
	for _, unit := range units {
		if strings.HasSuffix(s, unit.suffix) {
			number, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, unit.suffix)), 64)
			if err != nil || number <= 0 {
				return 0, fmt.Errorf("không đọc được cỡ %q: viết như \"512kb\", \"4mb\"", text)
			}
			return int64(number * float64(unit.size)), nil
		}
	}
	number, err := strconv.ParseInt(s, 10, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("không đọc được cỡ %q: viết như \"512kb\", \"4mb\"", text)
	}
	return number, nil
}

// readBody reads the request's body once, at most limit bytes, and keeps it: a second read gives the
// same bytes. A body past limit is not read further: errBodyTooLarge, and the request answers 413.
func (r *Request) readBody(limit int64) ([]byte, error) {
	if r.bodyRead {
		if r.bodyErr == nil && int64(len(r.body)) > limit {
			return nil, r.tooLarge()
		}
		return r.body, r.bodyErr
	}
	req := r.request()
	if req == nil || req.Body == nil {
		r.bodyRead = true
		return nil, nil
	}
	if limit > MaxBodyBytes {
		limit = MaxBodyBytes
	}
	r.bodyRead = true
	if req.ContentLength > limit {
		r.bodyErr = r.tooLarge()
		return nil, r.bodyErr
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, limit+1))
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytes), err == nil && int64(len(body)) > limit:
		r.bodyErr = r.tooLarge()
		return nil, r.bodyErr
	case err != nil:
		r.bodyErr = err
		return nil, err
	}
	r.body = body
	req.Body = io.NopCloser(bytes.NewReader(body)) // ParseForm and the like read it again
	return body, nil
}

// tooLarge marks the request to answer 413 and gives the error.
func (r *Request) tooLarge() error {
	if r.router != nil {
		r.router.bodyTooLarge = true
	}
	return errBodyTooLarge
}

// bodyFailure is the failure a program gets for a body it may not read.
func bodyFailure(err error, limit int64) value.Value {
	if errors.Is(err, errBodyTooLarge) {
		return value.InvalidFailure(value.InvalidInputCode, fmt.Sprintf("thân request vượt trần %d byte: request.json({ limit: \"4mb\" }) nâng được tới trần của host (%d byte)", limit, MaxBodyBytes))
	}
	return value.InvalidFailure(value.InvalidInputCode, "không đọc được thân request")
}

// jsonBody decodes the body, read up to limit, as JSON: nil for an empty body; a body that is not JSON
// is nil, as request.json() always gave.
func (r *Request) jsonBody(limit int64) value.Value {
	body, err := r.readBody(limit)
	if err != nil {
		return bodyFailure(err, limit)
	}
	if len(body) == 0 {
		return value.Value{K: value.Nil}
	}
	var data any
	if json.Unmarshal(body, &data) != nil {
		return value.Value{K: value.Nil}
	}
	return value.New(data)
}

// jsonWithChoices is request.json({ limit }) of router.work: the body as JSON, read up to its limit.
func (r *Request) jsonWithChoices(args ...value.Value) value.Value {
	limit := DefaultBodyLimit
	if len(args) > 0 && args[0].IsMap() {
		if given, ok := args[0].Map()["limit"]; ok {
			size, err := ParseByteSize(given)
			if err != nil {
				return value.InvalidFailure(value.InvalidArgumentCode, "request.json({ limit }): "+err.Error())
			}
			if size > MaxBodyBytes {
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("request.json({ limit }): %d byte vượt trần của host (%d byte, app.web({ maxBody }))", size, MaxBodyBytes))
			}
			limit = size
		}
	}
	return r.jsonBody(limit)
}

// clientIP is the client's address: the connection's, or the first X-Forwarded-For / X-Real-IP when
// the app trusts its proxy (TrustProxyHeaders). Without the port.
func clientIP(req *http.Request) string {
	if TrustProxyHeaders {
		if forwarded := req.Header.Get("X-Forwarded-For"); forwarded != "" {
			return strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
		if real := req.Header.Get("X-Real-IP"); real != "" {
			return strings.TrimSpace(real)
		}
	}
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return host
	}
	return req.RemoteAddr
}

// requestScheme is "https" when the connection is TLS, or, when the app trusts its proxy, when
// X-Forwarded-Proto says so; else "http".
func requestScheme(req *http.Request) string {
	if req == nil {
		return "http"
	}
	if req.TLS != nil {
		return "https"
	}
	if TrustProxyHeaders && strings.EqualFold(strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")), "https") {
		return "https"
	}
	return "http"
}

// inUISite reports whether the request is a Kitwork UI site's (router.work).
func (r *Request) inUISite() bool {
	return r.router != nil && uiRoute(r.router.node)
}

// Member is request.json({ limit }) in router.work: a function of the choices (value.OwnMember). A
// template site reads request.json as the property it was.
func (r *Request) Member(name string) (value.Value, bool) {
	if strings.EqualFold(name, "json") && r.inUISite() {
		return value.NewFunc(func(args ...value.Value) value.Value { return r.jsonWithChoices(args...) }), true
	}
	return value.Value{}, false
}

// hiddenUIRequestMembers are what a router.work handler does not see on the request: a benchmark loop
// and the upload API, whose shape is still open (KITWORK_ROUTER §5). A template site keeps them.
var hiddenUIRequestMembers = map[string]bool{"benchmark": true, "savefile": true, "multipartform": true}

func (r *Request) HiddenMember(name string) bool {
	return hiddenUIRequestMembers[strings.ToLower(name)] && r.inUISite()
}

// HiddenMember of the context hides its saveFile shortcut in router.work, as the request's.
func (c *Context) HiddenMember(name string) bool {
	return strings.EqualFold(name, "saveFile") && c.request != nil && c.request.inUISite()
}
