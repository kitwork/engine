package http

import (
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"strings"

	"github.com/kitwork/engine/value"
)

// Outcome is how one send ended, the Go side of a Result (router.proxy reads it through SendGo).
type Outcome struct {
	// Status is the HTTP status of the answer; 0 when there is none.
	Status int
	// Header is the answer's headers; nil when there is none.
	Header stdhttp.Header
	// Body is the answer's body, read whole; nil when there is none or it was past maxBytes.
	Body []byte
	// Code is Kitwork's code when there is no usable answer: NETWORK, TIMEOUT, DENIED, TOO_LARGE (with
	// the HTTP status kept), or value.CancelledCode; "" when there is one.
	Code string
	// Message is Kitwork's sentence for what went wrong: the HTTP reason of a 4xx or 5xx ("Not Found"),
	// "Request timed out", a network error without the URL's secrets; "" when ok.
	Message string
	// Cached: from the .cache()/.persist() tiers. Stale: an expired disk copy, because the live
	// request failed.
	Cached, Stale bool
}

// Ok is an answer of 200–299 whose body was read.
func (o Outcome) Ok() bool { return o.Code == "" && o.Status >= 200 && o.Status < 300 }

// Result is what .send() returns (KITWORK_HTTP.md mục 8), the same object for every outcome: ok,
// status, code, error, headers, cached, stale, and json(), text(), bytes() to call. The body was read
// when .send() returned; reading it again never sends again.
//
// The seven fields are data (value.OwnMember), not Go methods of no argument: a getter answers a call
// too, and spec mục 8 says error is a property, so upstream.error() is NOT_A_FUNCTION, as { error: "x" }
// .error() is (until 22/10/2026 it gave the sentence).
type Result struct{ outcome Outcome }

// Member answers the result's fields: ok, status, code, error, headers, cached, stale. A name is read
// as written, as an object's key is.
func (r *Result) Member(name string) (value.Value, bool) {
	switch name {
	case "ok":
		// true for an answer of 200–299
		return value.New(r.outcome.Ok()), true
	case "status":
		// the HTTP status, null when there was no answer
		if r.outcome.Status == 0 {
			return value.Value{K: value.Nil}, true
		}
		return value.New(r.outcome.Status), true
	case "code":
		// Kitwork's code when there was no usable answer, null when there was one
		return nullable(r.outcome.Code), true
	case "error":
		// Kitwork's sentence, null when ok
		return nullable(r.outcome.Message), true
	case "headers":
		return r.headers(), true
	case "cached":
		// from the .cache()/.persist() tiers
		return value.New(r.outcome.Cached), true
	case "stale":
		// an expired disk copy, given because the live request failed
		return value.New(r.outcome.Stale), true
	}
	return value.Value{}, false
}

// headers is the answer's headers by lower-case name, several values of one name joined by ", "; null
// when there was no answer.
func (r *Result) headers() value.Value {
	if r.outcome.Header == nil {
		return value.Value{K: value.Nil}
	}
	headers := make(map[string]value.Value, len(r.outcome.Header))
	for name, values := range r.outcome.Header {
		headers[strings.ToLower(name)] = value.New(strings.Join(values, ", "))
	}
	return value.New(headers)
}

// JSON reads the body as JSON. A body that does not parse, or an empty one (204, HEAD), is
// INVALID_INPUT: data from outside that cannot be read. JSON never makes up a value.
func (r *Result) JSON() value.Value {
	body, failure := r.body("json")
	if failure.IsError {
		return failure
	}
	if len(body) == 0 {
		return value.InvalidFailure(value.InvalidInputCode, fmt.Sprintf(
			"json(): thân câu trả lời rỗng (HTTP %d), không phải JSON", r.outcome.Status))
	}
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return value.InvalidFailure(value.InvalidInputCode, "json(): thân câu trả lời không phải JSON: "+err.Error())
	}
	return value.New(data)
}

// Text reads the body as text.
func (r *Result) Text() value.Value {
	body, failure := r.body("text")
	if failure.IsError {
		return failure
	}
	return value.New(string(body))
}

// Bytes is the body's bytes as they came.
func (r *Result) Bytes() value.Value {
	body, failure := r.body("bytes")
	if failure.IsError {
		return failure
	}
	if body == nil {
		body = []byte{}
	}
	return value.New(body)
}

// body is the answer's body, or the failure of reading one that is not there: no answer, or a body
// past maxBytes. The failure carries the result's own code, so safe(() => upstream.json()) reads
// NETWORK when the network was down.
func (r *Result) body(reader string) ([]byte, value.Value) {
	if r.outcome.Code == "" && r.outcome.Status != 0 {
		return r.outcome.Body, value.Value{}
	}
	return nil, value.InvalidFailure(value.ClosedCode(r.outcome.Code), fmt.Sprintf(
		"%s(): không có thân để đọc (%s: %s): kiểm ok trước khi đọc thân", reader, r.outcome.Code, r.outcome.Message))
}

// CallOnly: json, text and bytes run only when called; read, each is the function.
func (r *Result) CallOnly(name string) bool {
	return name == "json" || name == "text" || name == "bytes"
}

// MissingMember names the result's spelling for what it does not have; any other name is null.
func (r *Result) MissingMember(name string) value.Value {
	var message string
	switch strings.ToLower(name) {
	case "body":
		message = ".body không có: đọc thân bằng .bytes(), .text() hay .json()"
	case "data":
		message = ".data không có: đọc thân bằng .json()"
	case "base64":
		message = ".base64 đã bỏ: đọc thân bằng .bytes()"
	case "contenttype":
		message = ".contentType đã bỏ: đọc .headers[\"content-type\"]"
	case "message":
		message = ".message không có: câu báo là .error"
	default:
		return value.Value{K: value.Nil}
	}
	return value.InvalidFailure(value.InvalidArgumentCode, "kết quả của .send(): "+message)
}

func nullable(text string) value.Value {
	if text == "" {
		return value.Value{K: value.Nil}
	}
	return value.New(text)
}
