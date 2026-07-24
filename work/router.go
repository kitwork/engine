package work

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/helpers/sse"
	"github.com/kitwork/engine/render"
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
	// JIT icon stylesheet route: serve the tenant's site-wide icon CSS (masks, no JS handler,
	// no VM). Set by .icons(); the path defaults to /jiticons.
	isIcons bool
	// jitjs runtime route: serve the tenant's site-wide JS runtime (verbs, no JS handler, no VM).
	// Set by .jitjs(); the path defaults to /jitjs.
	isJitjs bool
	// JIT logo stylesheet route: serve the tenant's site-wide brand-logo CSS (Simple Icons masks,
	// no JS handler, no VM). Set by .logo(); the path defaults to /jitlogo.
	isLogo bool

	response       *Response
	request        *http.Request
	responseWriter http.ResponseWriter

	params map[string]string
	err    error // Biến lưu lỗi để truyền giữa các công đoạn

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
	treeRender *render.Render

	// viewBuilder is the DEFERRED view result for a filesystem-routed request (see viewbuilder.go).
	// Handlers accumulate binding + meta on it; the tree lifecycle renders it once at the end.
	viewBuilder *ViewBuilder
	// chainMeta is the meta declared via router.meta() merged down the folder chain (root→leaf),
	// the inherited base every page's $.meta starts from.
	chainMeta map[string]value.Value
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
		if r.err != nil {
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
		// The VM was already returned to the pool before this responder runs (Tenant.Serve), so
		// the long-lived stream below holds NO VM — only this goroutine. See SSE_ARCHITECTURE.md.
		r.streamSSE(w)
		return

	case "redirect":
		http.Redirect(w, request, data.String(), http.StatusSeeOther)
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	case "file":
		http.ServeFile(w, request, data.String())
	case "directory":
		dirPath := strings.TrimSuffix(data.String(), "*")
		dirPath = strings.TrimSuffix(dirPath, "/")

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
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(r.response.Code())
		b, err := json.Marshal(r.response.Data())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(b)
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	case "error":
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	default:
		http.NotFound(w, request)
	}
}

// streamSSE runs the Go-native side of an SSE connection. The engine calls it from Tenant.Serve
// AFTER the request's VM has been returned to the pool, so NO VM is held for the (potentially
// hours-long) connection — only this lightweight HTTP goroutine. It writes the stream headers,
// registers the client, replays missed events (Last-Event-ID), then fans broker messages +
// heartbeats out to the wire until the client disconnects.
func (r *Router) streamSSE(w http.ResponseWriter) {
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	broker.Register(client)

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
	defer func() {
		ticker.Stop()
		broker.Unregister(client)
	}()

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
