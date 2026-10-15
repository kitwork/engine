package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/utilities/sse"
	"github.com/kitwork/engine/value"
)

var staticFileLocks sync.Map

func getFileLock(path string) *sync.RWMutex {
	val, _ := staticFileLocks.LoadOrStore(path, &sync.RWMutex{})
	return val.(*sync.RWMutex)
}

func (w *KitWork) Router() *Router { return &Router{tenant: w.tenant} }

func NewRouter(tenant *Tenant) *Router {
	return &Router{
		tenant: tenant,
	}
}

type Router struct {
	tenant   *Tenant
	Method   string
	Path     string
	basePath string

	start       *value.Lambda
	middlewares []*value.Lambda
	guards      []*value.Lambda

	handle *value.Lambda
	then   *value.Lambda
	catch  *value.Lambda
	final  *value.Lambda

	// View route: render a page directly (no JS handler, no VM). Set by .view().
	isView     bool
	isNotfound bool // a view route that responds 404 (set by .notfound())
	viewArgs   []value.Value

	// JIT stylesheet route: serve the tenant's site-wide JIT CSS (no JS handler, no VM).
	// Set by .jit(); the path defaults to /jitcss.
	isJIT bool
	// (isIcons and isLogo — the shared stylesheet routes router.icons() and router.logo() served at
	// /jiticons and /jitlogo — went with those two methods in the tree router, 09/07/2026; a page's
	// icons and logos are inlined into it, and nothing set or read these fields since.)

	response       *Response
	request        *http.Request
	responseWriter http.ResponseWriter

	params map[string]string
	err    error // Biến lưu lỗi để truyền giữa các công đoạn
	// failure is the runtime failure behind err when a run ended in one (failed), what a .error()
	// handler's error parameter describes (errorObject).
	failure value.Value
	failed  bool

	requestID string

	// Cache configuration
	cacheTTL       time.Duration
	staticTTL      time.Duration
	benchmarkCount int // Số lần chạy lặp để đo hiệu năng

	// Rate Limit configuration — a list of rules ({type, rate, period}). Multiple rules, even
	// the same type with different periods (e.g. 100/s + 1000/min per IP), all apply.
	limitRules []rateRule

	cors *CorsOptions

	// Metadata and application level bindings
	meta value.Value

	// treeRender, when set, is the render a filesystem-routed (tree) request uses for ctx.view():
	// it is pointed at the resolved folder so page/index/slots resolve by that folder's chain.
	treeRender *plannedRenderer

	// viewBuilder is the DEFERRED view result for a filesystem-routed request (see viewbuilder.go).
	// Handlers accumulate binding + meta on it; the tree lifecycle renders it once at the end.
	viewBuilder *ViewBuilder
	// chainMeta is the meta declared via router.meta() merged down the folder chain (root→leaf),
	// the inherited base every page's $.meta starts from.
	chainMeta map[string]value.Value
	// chainJsonld is the router.jsonld() nodes accumulated down the folder chain (root→leaf); the
	// view appends its own ctx.jsonld() nodes and renders them all into <head>.
	chainJsonld []value.Value
	// transition is router.transition() of the nearest folder up the chain that declared one: how
	// <content /> changes when a link moves here without a reload. "" is "none".
	transition string
}

// rateRule is one rate-limit rule. Type is the key dimension ("ip" | "user" | "browser" |
// "global" aggregate); Scope is the blast radius ("tenant" (default) | "server" = a bucket
// shared across ALL tenants, in the host limiter store).
type rateRule struct {
	Type   string
	Rate   int
	Period time.Duration
	Scope  string
}

// --- ENGINE LOGIC ---

func (r *Router) responder(w http.ResponseWriter) {
	kind := r.response.Kind()
	data := r.response.Data()
	request := r.request

	// 1. Tự động xác định kind nếu trống dựa trên data hoặc lỗi
	if kind == "" {
		if r.err != nil && isUncaught(r.err) {
			// an error nothing caught is the server's: 500, whatever status the handler had set for the
			// answer it did not finish, written by writeFailure
			kind = "failure"
			r.response.Return(value.Value{K: value.Nil}, kind, http.StatusInternalServerError)
		} else if r.err != nil {
			kind = "error"
			data = value.New(r.err.Error())
			r.response.Return(data, kind)
			if r.response.Code() == 0 {
				r.response.Status(500)
			}
		} else if !data.IsBlank() {
			if data.K == value.Map || data.K == value.Array {
				kind = "json"
			} else {
				kind = "html"
			}
			r.response.Return(data, kind)
		}
	}

	// 2. Mặc định Status 200 nếu chưa có
	if r.response.Code() == 0 {
		r.response.Status(200)
	}

	if r.cors != nil {
		writeCorsHeaders(r.cors, w, request)
	}
	r.response.writeCookies(w)
	r.response.writeHeaders(w)
	if kind == "failure" {
		r.writeFailure(w, r.response.Code())
		return
	}
	if requestNotModified(request, w.Header()) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// // 2.5 Bơm Headers (nếu có)
	// if r.response.headers != nil {
	// 	for k, v := range r.response.headers {
	// 		w.Header().Set(k, v)
	// 	}
	// }

	// 3. Xử lý phản hồi dựa trên kind

	switch kind {
	case "sse":
		r.streamSSE(w, nil)
		return

	case "redirect":
		// ctx.redirect(url, 301) means 301. Only a redirect status is honoured; anything
		// else — the response's 200 default included — falls back to 303, the one that
		// turns a POST into a GET.
		code := r.response.Code()
		if code < 300 || code > 399 {
			code = http.StatusSeeOther
		}
		http.Redirect(w, request, data.String(), code)
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	case "file":
		// Boundary check at the point of USE, so it covers every producer of a file response
		// (ctx.file, response.file, …) rather than trusting each one. A handler that builds its
		// path from request input — ctx.file(ctx.query("f")) is an ordinary download pattern —
		// must not be able to read another app's files. 404, not 403: a denial must not reveal
		// whether the out-of-bounds file exists.
		if !r.tenant.insideAppRoot(data.String()) {
			http.NotFound(w, request)
			return
		}
		http.ServeFile(w, request, data.String())
	case "directory":
		dirPath := strings.TrimSuffix(data.String(), "*")
		dirPath = strings.TrimSuffix(dirPath, "/")
		if !r.tenant.insideAppRoot(dirPath) {
			http.NotFound(w, request)
			return
		}

		prefix := strings.TrimSuffix(r.Path, "*")
		http.StripPrefix(prefix, http.FileServer(http.Dir(dirPath))).ServeHTTP(w, request)
	case "bytes":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(r.response.Code())
		w.Write(data.Bytes())
	case "image":
		// TODO: Tự động nhận diện mime-type từ bytes
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(r.response.Code())
		w.Write(data.Bytes())
	case "svg":
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write([]byte(data.String()))
	case "typed":
		w.Header().Set("Content-Type", r.response.ContentType())
		w.WriteHeader(r.response.Code())
		if request.Method != http.MethodHead {
			w.Write(r.response.toBytes())
		}
	case "css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	case "json":
		// encoded before the status goes out: a value JSON cannot carry (NaN, Infinity) is a failure
		// of the request, answered as one, not a 200 with an error after it
		b, err := json.Marshal(r.response.Data())
		if err != nil {
			r.err = uncaught("không viết được câu trả lời thành JSON: " + err.Error())
			var number *json.UnsupportedValueError
			if errors.As(err, &number) {
				r.err = uncaught("không viết được câu trả lời thành JSON: JSON không có số " + number.Str + " (vô hạn hoặc không phải số)")
			}
			r.tenant.recordFailure(r, "json", r.err)
			r.writeFailure(w, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(b)
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := r.response.toBytes()
		allowThemePrepaint(w.Header(), html)
		w.WriteHeader(r.response.Code())
		w.Write(html)
	case "error":
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	default:
		http.NotFound(w, request)
	}
}

// uncaughtError is a failure no code of the site caught: a runtime error of the VM, whose text is its
// whole diagnostic (message, stack, file:line:column, byte offsets), a page that does not compile, a
// value the engine could not write. Its text is for the site's author: the log record keeps it whole
// (recordRuntimeFailure, recordFailure), and a visitor reads it only while developing (AllowLocal);
// in production the answer is the status and the request id (writeFailure). An error the site chose
// to show is not one: ctx.error("…"), a guard that rejected the request.
type uncaughtError struct{ text string }

func (e *uncaughtError) Error() string { return e.text }

func uncaught(text string) error { return &uncaughtError{text: text} }

func isUncaught(err error) bool {
	var failure *uncaughtError
	return errors.As(err, &failure)
}

// errorObject is what the parameter err, error or e of a .error() handler receives: the error that
// ended the request, in the shape safe(() => …) gives an error, { message, code, at } (14/10/2026;
// until then the string of the whole diagnostic, stack included). A runtime failure keeps its code and
// its place; a failure safe never catches (ENERGY_LIMIT…) shows its own code. An error the site set
// (ctx.error("…")) is FAIL, a guard that rejected the request DENIED, an answer the engine could not
// write ERROR; none of them has a place. null when nothing failed.
func (r *Router) errorObject() value.Value {
	if r == nil || r.err == nil {
		return value.Value{K: value.Nil}
	}
	if r.failed {
		return runtime.FailureObject(r.failure)
	}
	code := value.DefaultFailureCode
	switch {
	case r.err == errRequestRejected:
		code = value.DeniedCode
	case !isUncaught(r.err):
		code = value.FailCode
	}
	return runtime.FailureObject(value.InvalidFailure(code, r.err.Error()))
}

// errRequestRejected is the error of a guard that returned false.
var errRequestRejected = errors.New("request rejected")

// fail answers the request with an error nothing caught, at status (500; 502 for a proxy whose
// upstream failed). The router keeps the error for .error() and the log; the responder writes
// writeFailure.
func (r *Router) fail(err error, status int) {
	if !isUncaught(err) {
		err = uncaught(err.Error())
	}
	r.err = err
	r.response.Return(value.Value{K: value.Nil}, "failure", status)
}

// writeFailure writes the answer of an error nothing caught. While developing (AllowLocal) it is the
// error's whole text, as router.view and a .ui page answered theirs; in production a visitor reads
// only the status and the request id the log record carries (X-Request-ID): a page for a browser,
// JSON for a client that asked for JSON, text for any other.
func (r *Router) writeFailure(w http.ResponseWriter, status int) {
	writeFailure(w, r.request, status, r.requestID, r.err)
}

func writeFailure(w http.ResponseWriter, request *http.Request, status int, id string, err error) {
	header := w.Header()
	// nothing the handler set for the answer it did not finish describes this one
	for _, name := range []string{"Content-Encoding", "Content-Length", "Content-Disposition", "ETag", "Last-Modified"} {
		header.Del(name)
	}
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	if AllowLocal && err != nil {
		header.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		io.WriteString(w, err.Error())
		return
	}
	title := http.StatusText(status)
	switch failureFormat(request) {
	case "html":
		header.Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(w, failurePage, status, html.EscapeString(title), html.EscapeString(id))
	case "json":
		body, _ := json.Marshal(struct {
			Error     string `json:"error"`
			Status    int    `json:"status"`
			RequestID string `json:"requestId"`
		}{title, status, id})
		header.Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		w.Write(body)
	default:
		header.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(w, "%s\nRequest ID: %s\n", title, id)
	}
}

// failurePage is the page a browser reads for an error nothing caught, in production: the status and
// the request id, nothing of the site's code.
const failurePage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex"><title>%[1]d %[2]s</title></head>
<body><h1>%[2]s</h1><p>Request ID: <code>%[3]s</code></p></body>
</html>
`

// failureFormat is how a failure is written for the client that asked: "html" for a browser, "json"
// for a client that accepts or sends JSON, "text" for any other.
func failureFormat(request *http.Request) string {
	if request == nil {
		return "text"
	}
	accept := strings.ToLower(request.Header.Get("Accept"))
	switch {
	case strings.Contains(accept, "text/html"):
		return "html"
	case strings.Contains(accept, "json"),
		strings.Contains(strings.ToLower(request.Header.Get("Content-Type")), "json"):
		return "json"
	}
	return "text"
}

// WriteSiteFailure answers a request no site could take: the site's generation did not prepare (a
// file that does not compile, a boot that failed) or no site is there. The log record keeps the error
// whole with the request id the answer carries (X-Request-ID); the answer is writeFailure's, the
// error itself only while developing.
func WriteSiteFailure(w http.ResponseWriter, request *http.Request, status int, err error) {
	id := requestID(request)
	w.Header().Set("X-Request-ID", id)
	message := ""
	if err != nil {
		message = err.Error()
	}
	level, said := slog.LevelError, "Kitwork site could not answer"
	if errors.Is(err, fs.ErrNotExist) {
		// no site is there: any Host a client sends reaches this, so it is not an error of a site
		level, said = slog.LevelWarn, "Kitwork site not found"
	}
	slog.Log(
		request.Context(),
		level,
		said,
		"request_id", id,
		"host", request.Host,
		"method", request.Method,
		"path", request.URL.Path,
		"status", status,
		"message", message,
	)
	writeFailure(w, request, status, id, uncaught(message))
}

// streamSSE runs the Go-native side of an SSE connection. Once the site broker
// accepts the client, handoff closes the request scope and releases its VM and
// generation ownership. Only the HTTP goroutine and site broker remain for the
// potentially hours-long connection.
func (r *Router) streamSSE(w http.ResponseWriter, handoff func()) {
	client, ok := r.response.Data().V.(*SSEClient)
	if !ok {
		http.Error(w, "invalid sse client", http.StatusInternalServerError)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Active-connection cap (DoS protection) — each open stream costs a goroutine + broker slot.
	broker := r.tenant.SSEBroker()
	maxConn := client.MaxConnections
	if maxConn <= 0 {
		maxConn = 1000
	}
	if broker.ClientCount() >= maxConn {
		http.Error(w, "too many concurrent connections", http.StatusTooManyRequests)
		return
	}

	if !broker.Register(client) {
		http.Error(w, "streaming unavailable", http.StatusServiceUnavailable)
		return
	}
	defer broker.Unregister(client)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if handoff != nil {
		handoff()
	}

	// Send clientSessionId init event
	initPayload, err := sse.FormatSSEPayload("", "init", map[string]string{"clientSessionId": client.ID})
	if err == nil {
		w.Write(initPayload)
	}

	// Last-Event-ID recovery (header on auto-reconnect, or ?lastEventId= for manual control).
	lastEventID := r.request.Header.Get("Last-Event-ID")
	if lastEventID == "" {
		lastEventID = r.request.URL.Query().Get("lastEventId")
	}
	if lastEventID != "" {
		broker.Replay(client, lastEventID)
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	notify := r.request.Context().Done()
	flusher.Flush()

	for {
		select {
		case <-notify:
			return // client closed the tab / network dropped
		case msg, open := <-client.SendChan:
			if !open {
				return // broker stopped (tenant evicted)
			}
			w.Write(msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

type CorsOptions struct {
	// Origins specifies the allowed origins.
	// Use "*" to allow all origins.
	Origins []string

	// Methods specifies the allowed HTTP methods.
	Methods []string

	// Headers specifies the allowed request headers.
	Headers []string

	// Expose specifies the response headers that browsers
	// are allowed to expose to JavaScript.
	Expose []string

	// Credentials indicates whether the browser may send
	// credentials such as cookies or HTTP authentication.
	Credentials bool

	// MaxAge specifies how long the browser may cache
	// the result of a preflight request.
	MaxAge time.Duration
}

func writeCorsHeaders(opts *CorsOptions, w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}

	allowedOrigin := ""
	for _, o := range opts.Origins {
		if o == "*" {
			allowedOrigin = "*"
			break
		}
		if o == origin {
			allowedOrigin = origin
			break
		}
	}

	if allowedOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		if opts.Credentials && allowedOrigin != "*" {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
	}

	if len(opts.Headers) > 0 {
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(opts.Headers, ", "))
	}
	if len(opts.Methods) > 0 {
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(opts.Methods, ", "))
	}
	if opts.MaxAge > 0 {
		w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(opts.MaxAge)))
	}
}
