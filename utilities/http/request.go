package http

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	stdhttp "net/http"
	neturl "net/url"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

const (
	// defaultTimeout is how long one attempt may take when .timeout() does not say.
	defaultTimeout = 10 * time.Second
	// defaultMaxBytes caps the body a program reads when .maxBytes() does not say (10 MiB).
	defaultMaxBytes int64 = 10 << 20
	// HostMaxBytes is the most .maxBytes() may raise the cap to (64 MiB), and router.proxy's cap.
	HostMaxBytes int64 = 64 << 20
)

// Request is the description of one request (KITWORK_HTTP.md, 22/10/2026): http.get(url) makes it,
// modifiers return a changed copy, and .send() sends it, every time it is called. Nothing else sends:
// there is no sending at the end of a statement and none on reading a field (the ways of 14/10/2026,
// removed). A copy per modifier keeps a description a program reuses unchanged:
//
//	const feed = http.get(url).timeout("5s");
//	const first = feed.send();
//	const again = feed.header("Accept", "application/json").send();
type Request struct {
	module *Module // nil for a request Go made (NewRequest): no cache tiers, no site Host
	method string
	target string // absolute http(s) URL, or "/path" of this site
	header stdhttp.Header
	query  map[string]string

	timeout  time.Duration // per attempt
	retry    int           // attempts after the first
	maxBytes int64

	cache, persist       bool
	cacheTTL, persistTTL time.Duration
}

func newRequest(m *Module, method, target string, maxBytes int64) *Request {
	return &Request{
		module: m, method: method, target: target,
		header: stdhttp.Header{}, query: map[string]string{},
		timeout: defaultTimeout, maxBytes: maxBytes,
	}
}

// NewRequest is a request Go describes (router.proxy): no cache tiers, maxBytes the cap on the body it
// reads (HostMaxBytes at most; 0 or less is HostMaxBytes). Send it with SendGo.
func NewRequest(method, target string, maxBytes int64) (*Request, error) {
	target = strings.TrimSpace(target)
	if !validTarget(target) {
		return nil, fmt.Errorf("%q is not an http(s) URL or a path", shownURL(target))
	}
	if maxBytes <= 0 || maxBytes > HostMaxBytes {
		maxBytes = HostMaxBytes
	}
	return newRequest(nil, strings.ToUpper(method), target, maxBytes), nil
}

func (r *Request) clone() *Request {
	next := *r
	next.header = r.header.Clone()
	next.query = make(map[string]string, len(r.query))
	for name, text := range r.query {
		next.query[name] = text
	}
	return &next
}

// ---- modifiers: each returns a changed copy, or the failure naming how to write it ----

// Header sets one header. A later value replaces an earlier one of the same name, whatever its case.
func (r *Request) Header(args ...value.Value) value.Value {
	if len(args) != 2 {
		return invalid(".header() cần tên và giá trị: .header(\"Accept\", \"application/json\")")
	}
	next := r.clone()
	if failure := next.setHeader("header", args[0], args[1]); failure.Failed() {
		return failure
	}
	return value.New(next)
}

// Headers sets several headers, merged with those set before.
func (r *Request) Headers(args ...value.Value) value.Value {
	if len(args) != 1 || args[0].K != value.Map {
		return invalid(".headers() nhận một object: .headers({ Accept: \"application/json\" })")
	}
	next := r.clone()
	fields := args[0].Map()
	for _, name := range sortedNames(fields) {
		if failure := next.setHeader("headers", value.New(name), fields[name]); failure.Failed() {
			return failure
		}
	}
	return value.New(next)
}

// setHeader checks a header before it is kept: a name of token characters, a value with no line break
// or NUL in it, so data cannot add a header of its own (header injection).
func (r *Request) setHeader(modifier string, name, val value.Value) value.Value {
	if name.K != value.String || !validHeaderName(name.String()) {
		return invalid(fmt.Sprintf(".%s(): tên header %s không hợp lệ: chỉ chữ, số và ! # $ %% & ' * + - . ^ _ ` | ~",
			modifier, written(name)))
	}
	var text string
	switch val.K {
	case value.String:
		text = val.String()
	case value.Number, value.Bool:
		text = val.Text()
	default:
		return invalid(fmt.Sprintf(".%s(): giá trị của header %q là chuỗi, số hay boolean, không phải %s",
			modifier, name.String(), kindName(val)))
	}
	if strings.ContainsAny(text, "\r\n\x00") {
		return invalid(fmt.Sprintf(".%s(): giá trị của header %q có xuống dòng (\\r, \\n) hay NUL: không được, để chống chèn header",
			modifier, name.String()))
	}
	r.header.Set(name.String(), text)
	return value.Value{}
}

// Query sets URL parameters, merged with those set before (a later name wins), encoded when sent.
func (r *Request) Query(args ...value.Value) value.Value {
	if len(args) != 1 || args[0].K != value.Map {
		return invalid(".query() nhận một object: .query({ page: 1, search: \"kitwork\" })")
	}
	next := r.clone()
	fields := args[0].Map()
	for _, name := range sortedNames(fields) {
		field := fields[name]
		switch field.K {
		case value.String:
			next.query[name] = field.String()
		case value.Number, value.Bool:
			next.query[name] = field.Text()
		default:
			// null, a list, a nested object: how to encode them is still open (mục 11), so no guess
			return invalid(fmt.Sprintf(".query(): %q là %s; .query() nhận chuỗi, số hay boolean (null, mảng, object lồng chưa có)",
				name, kindName(field)))
		}
	}
	return value.New(next)
}

// Timeout is the most one attempt may take: "500ms", "10s", "2m". A number of milliseconds, the old
// spelling, is an error naming the new one.
func (r *Request) Timeout(args ...value.Value) value.Value {
	if len(args) != 1 {
		return invalid(".timeout() nhận một hạn: .timeout(\"5s\")")
	}
	limit := args[0]
	switch limit.K {
	case value.Number:
		return invalid(fmt.Sprintf(".timeout(%s): hạn viết bằng chữ, như .timeout(\"5s\") hay .timeout(\"500ms\"); số mili giây đã bỏ",
			limit.Text()))
	case value.String:
		if d, err := parseDuration(limit.String()); err == nil && d > 0 {
			next := r.clone()
			next.timeout = d
			return value.New(next)
		}
	}
	return invalid(fmt.Sprintf(".timeout(%s): không đọc được hạn: viết như \"500ms\", \"10s\", \"2m\"", written(limit)))
}

// Retry is how many attempts follow the first when there is no answer or a 5xx: .retry(3) is four in
// all. POST and PATCH retry only with an Idempotency-Key header, checked at .send().
func (r *Request) Retry(args ...value.Value) value.Value {
	if len(args) != 1 || args[0].K != value.Number || args[0].N < 0 || args[0].N != math.Trunc(args[0].N) {
		return invalid(".retry() nhận số lần thử thêm, một số nguyên từ 0: .retry(3) là bốn lần tổng cộng")
	}
	next := r.clone()
	next.retry = int(args[0].N)
	return value.New(next)
}

// MaxBytes caps the body read into memory: a size ("1kb", "50mb") or a number of bytes, up to the
// host's 64 MiB. Past the cap the result is TOO_LARGE, its status kept.
func (r *Request) MaxBytes(args ...value.Value) value.Value {
	if len(args) != 1 {
		return invalid(".maxBytes() nhận một cỡ: .maxBytes(\"50mb\")")
	}
	size, readable := int64(0), false
	switch args[0].K {
	case value.Number:
		if args[0].N > 0 && args[0].N == math.Trunc(args[0].N) {
			size, readable = int64(args[0].N), true
		}
	case value.String:
		size, readable = parseByteSize(args[0].String())
	}
	if !readable {
		return invalid(fmt.Sprintf(".maxBytes(%s): không đọc được cỡ: viết như \"512kb\", \"50mb\" hay một số byte",
			written(args[0])))
	}
	if size > HostMaxBytes {
		return invalid(fmt.Sprintf(".maxBytes(%s): vượt trần của host, %s", written(args[0]), sizeText(HostMaxBytes)))
	}
	next := r.clone()
	next.maxBytes = size
	return value.New(next)
}

// Cache keeps an ok answer of a GET in RAM: http.get(url).cache("5m").send().
func (r *Request) Cache(args ...value.Value) value.Value {
	return r.store("cache", args)
}

// Persist keeps an ok answer of a GET on disk, and gives an expired copy back when the live request
// fails: http.get(url).persist("1d").send().
func (r *Request) Persist(args ...value.Value) value.Value {
	return r.store("persist", args)
}

func (r *Request) store(modifier string, args []value.Value) value.Value {
	if r.method != "GET" {
		return invalid(fmt.Sprintf(".%s() chỉ cho GET: http.get(url).%s(\"5m\").send(); http.%s(…) không lưu câu trả lời",
			modifier, modifier, strings.ToLower(r.method)))
	}
	ttl, failure := ttlOf(modifier, args)
	if failure.Failed() {
		return failure
	}
	next := r.clone()
	if modifier == "cache" {
		next.cache, next.cacheTTL = true, ttl
	} else {
		next.persist, next.persistTTL = true, ttl
	}
	return value.New(next)
}

// ---- sending ----

// Send sends the request, with data as its body: an object or a list as JSON (Content-Type
// application/json unless the program set one), a string as UTF-8 text, bytes as they are, nothing
// for no body. It returns a Result whatever happened on the network: ok says which. Only a mistake of
// the program is a failure: a body on GET or HEAD, a retried POST without an Idempotency-Key, a body of
// another kind.
func (r *Request) Send(args ...value.Value) value.Value {
	name := strings.ToLower(r.method)
	if len(args) > 1 {
		return invalid(fmt.Sprintf(".send() nhận nhiều nhất một thân: http.%s(url).send(data)", name))
	}
	data := value.Value{K: value.Nil}
	if len(args) == 1 {
		data = args[0]
	}
	if bodyless(r.method) && !data.IsBlank() {
		return invalid(fmt.Sprintf("http.%s(…).send() không nhận thân: tham số URL viết bằng .query({ … })", name))
	}
	if r.retry > 0 && (r.method == "POST" || r.method == "PATCH") && r.header.Get("Idempotency-Key") == "" {
		return invalid(fmt.Sprintf("%s thử lại có thể ghi hai lần: thêm header Idempotency-Key, như .header(\"Idempotency-Key\", key), hay bỏ .retry()",
			r.method))
	}
	body, contentType, failure := encodeBody(data)
	if failure.Failed() {
		return failure
	}
	outcome := r.run(context.Background(), body, contentType)
	if outcome.Code == value.CancelledCode {
		// the request's own cancellation ends the run, as everywhere (KITWORK_LANG.md mục 6)
		return value.InvalidFailure(value.CancelledCode, outcome.Message)
	}
	return value.New(&Result{outcome: outcome})
}

// SendGo sends the request for a Go caller and hands back how it ended. ctx may cancel it.
func (r *Request) SendGo(ctx context.Context) Outcome {
	if ctx == nil {
		ctx = context.Background()
	}
	return r.run(ctx, nil, "")
}

// UnsentFailure is the failure of a request a route's function returned without sending it: until
// 22/10/2026 `return http.get(url)` sent it at the return, and the router now answers with nothing
// unless this names .send(). ok is false for any other value.
func UnsentFailure(v value.Value) (failure value.Value, ok bool) {
	request, isRequest := v.V.(*Request)
	if v.K != value.Struct || !isRequest {
		return value.Value{}, false
	}
	method := strings.ToLower(request.method)
	return invalid(fmt.Sprintf("hàm của route trả về http.%s(…) chưa gửi: .send() là chỗ duy nhất gửi request, viết const upstream = http.%s(url).send(); rồi trả lời từ upstream, như context.json(upstream.json())",
		method, method)), true
}

// HiddenMember keeps SendGo from a program: Go's way in, not Kitwork's.
func (r *Request) HiddenMember(name string) bool { return strings.EqualFold(name, "sendgo") }

// unsentReads is what a program wrote on a request that was never sent (it used to send then), and how
// the result of .send() spells it today: a field the result has, a reader to call, or the new spelling
// of a field the result no longer has (mục 8).
var unsentReads = map[string]string{
	"ok": "upstream.ok", "status": "upstream.status", "code": "upstream.code", "error": "upstream.error",
	"cached": "upstream.cached", "stale": "upstream.stale",
	"json": "upstream.json()", "text": "upstream.text()", "bytes": "upstream.bytes()",
	"body": "upstream.bytes(), upstream.text() hay upstream.json()", "data": "upstream.json()",
	"base64": "upstream.bytes()", "contenttype": "upstream.headers[\"content-type\"]", "message": "upstream.error",
}

// MissingMember names the new spelling for the ways that are gone: reading a result on a request that
// was never sent (it used to send then), and the verbs that are not .send().
func (r *Request) MissingMember(name string) value.Value {
	method := strings.ToLower(r.method)
	folded := strings.ToLower(name)
	if spelled, ok := unsentReads[folded]; ok {
		return invalid(fmt.Sprintf("http.%s(…) chưa gửi nên chưa có %s: kết quả là của .send(), viết const upstream = http.%s(url).send(); rồi đọc %s",
			method, name, method, spelled))
	}
	switch folded {
	case "then", "catch", "fetch", "execute", "request", "commit", "fire":
		return invalid(fmt.Sprintf("http.%s(…).%s không có: .send() là chỗ duy nhất gửi request: http.%s(url).send()",
			method, name, method))
	}
	return value.Value{K: value.Nil}
}

// run is one send: the cache tiers of a GET, then the attempts, then the stale copy when they failed.
func (r *Request) run(ctx context.Context, body []byte, contentType string) Outcome {
	target := r.finalURL()
	var ram, disk ResponseStore
	if r.module != nil && r.method == "GET" {
		if r.cache {
			ram = r.module.ram
		}
		if r.persist {
			disk = r.module.disk
		}
	}
	key := ""
	if ram != nil || disk != nil {
		key = requestKey(target, r.header)
		if ram != nil {
			if snapshot, ok := ram.Load(key); ok {
				return storedOutcome(snapshot, false)
			}
		}
		if disk != nil {
			if snapshot, ok := disk.Load(key); ok {
				if ram != nil { // a disk hit refills RAM for the next reader
					ram.Save(key, snapshot, r.cacheTTL)
				}
				return storedOutcome(snapshot, false)
			}
		}
	}

	var outcome Outcome
	for attempt := 0; attempt <= r.retry; attempt++ {
		if attempt > 0 && !pause(ctx, backoff(attempt-1)) {
			outcome = noAnswer(ctx.Err())
			break
		}
		outcome = r.attempt(ctx, target, body, contentType)
		if !retryable(outcome) {
			break // an answer below 500 is definite: retrying a 404 only wastes time
		}
	}

	if key != "" {
		switch {
		case outcome.Ok():
			snapshot := Snapshot{Status: outcome.Status, Body: outcome.Body, ContentType: outcome.Header.Get("Content-Type")}
			if ram != nil {
				ram.Save(key, snapshot, r.cacheTTL)
			}
			if disk != nil {
				disk.Save(key, snapshot, r.persistTTL)
			}
		case retryable(outcome) && disk != nil:
			if snapshot, ok := disk.LoadStale(key); ok {
				return storedOutcome(snapshot, true)
			}
		}
	}
	return outcome
}

// finalURL is the target with .query() encoded after it (names sorted), before any fragment.
func (r *Request) finalURL() string {
	if len(r.query) == 0 {
		return r.target
	}
	values := neturl.Values{}
	for name, text := range r.query {
		values.Set(name, text)
	}
	base, fragment, hasFragment := strings.Cut(r.target, "#")
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
		if strings.HasSuffix(base, "?") || strings.HasSuffix(base, "&") {
			separator = ""
		}
	}
	target := base + separator + values.Encode()
	if hasFragment {
		target += "#" + fragment
	}
	return target
}

// retryable is an outcome worth another attempt: no answer from the network (NETWORK, TIMEOUT) or a
// 5xx. DENIED, TOO_LARGE and every answer below 500 are definite.
func retryable(o Outcome) bool {
	if o.Code != "" {
		return o.Status == 0 && (o.Code == value.NetworkCode || o.Code == value.TimeoutCode)
	}
	return o.Status >= 500
}

// backoff is the wait before attempt n+2: 100ms, 200ms, 400ms… (at most 6.4s), the upper half of it
// drawn at random (jitter), so clients that failed together do not retry together.
func backoff(attempt int) time.Duration {
	if attempt > 6 {
		attempt = 6
	}
	step := (100 * time.Millisecond) << uint(attempt)
	return step/2 + rand.N(step/2)
}

// pause waits d, or less when ctx ends first (false then).
func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// encodeBody is the body of .send(data) and the Content-Type it implies.
func encodeBody(data value.Value) ([]byte, string, value.Value) {
	switch data.K {
	case value.Map, value.Array:
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, "", value.InvalidFailure(value.InvalidArgumentCode, ".send(): thân không viết được thành JSON: "+err.Error())
		}
		return encoded, "application/json", value.Value{}
	case value.String:
		return []byte(data.String()), "", value.Value{}
	case value.Bytes:
		return data.Bytes(), "", value.Value{}
	}
	if data.IsBlank() {
		return nil, "", value.Value{}
	}
	return nil, "", invalid(fmt.Sprintf(".send() nhận object, mảng, chuỗi hay bytes, không phải %s: một số viết trong object, như .send({ n: 1 })",
		kindName(data)))
}

// validHeaderName is a header name of token characters (RFC 9110).
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func sortedNames(fields map[string]value.Value) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// kindName names a value's kind in a message.
func kindName(v value.Value) string {
	switch v.K {
	case value.Nil:
		return "null"
	case value.Array:
		return "mảng"
	case value.Map:
		return "object"
	case value.Func:
		return "hàm"
	case value.Bytes:
		return "bytes"
	}
	return v.TypeOf()
}

func invalid(message string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, message)
}
