package work

import (
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

	response *Response
	request  *http.Request

	params map[string]string
	err    error // Biến lưu lỗi để truyền giữa các công đoạn

	// Cache configuration
	cacheTTL       time.Duration
	staticTTL      time.Duration
	benchmarkCount int // Số lần chạy lặp để đo hiệu năng

	// Rate Limit configuration
	hasLimit    bool
	limitRate   int
	limitPeriod time.Duration
}

func (r *Router) Benchmark(v value.Value) *Router {
	r.benchmarkCount = int(v.N)
	return r
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

	// // 2.5 Bơm Headers (nếu có)
	// if r.response.headers != nil {
	// 	for k, v := range r.response.headers {
	// 		w.Header().Set(k, v)
	// 	}
	// }

	// 3. Xử lý phản hồi dựa trên kind
	switch kind {
	case "render":
		result, err := r.render()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(result)
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

func (r *Router) render() ([]byte, error) {

	render := NewRender(r.tenant).
		Page(value.New(r.request.URL.Path)).
		Layout(value.New(r.response.page.layout)).
		Template(value.New(r.response.page.template)).
		Bind(r.response.data)
	return []byte(render.String()), nil
}

func (r *Router) New(method, path string) *Router {
	newRoute := *r
	newRoute.middlewares = append([]*value.Lambda(nil), r.middlewares...)
	newRoute.guards = append([]*value.Lambda(nil), r.guards...)

	fullPath := "/" + strings.Trim(r.basePath, "/") + "/" + strings.Trim(path, "/")
	fullPath = strings.ReplaceAll(fullPath, "//", "/")

	newRoute.Method = method
	newRoute.Path = fullPath
	newRoute.handle = nil
	newRoute.response = &Response{}

	r.tenant.routes.Insert(method, fullPath, &newRoute)
	return &newRoute
}

func (r *Router) Get(path string) *Router  { return r.New("GET", path) }
func (r *Router) Post(path string) *Router { return r.New("POST", path) }

// Notfound registers the 404 fallback. The engine routes to a NOTFOUND-method
// route when nothing else matches (see Tenant.Serve) — no "/*-must-be-last" trap.
//
//	router.notfound("*")         // render the REQUESTED path's page (section-aware
//	                             // shell, falling through to notfound.kitwork.html) @ 404
//	router.notfound("/oops")     // always render /oops @ 404
//	router.notfound((ctx) => …)  // full control: a handler (status it yourself)
//
// The string forms are view routes (no handler, no VM, like .view()) that respond 404.
func (r *Router) Notfound(args ...value.Value) *Router {
	route := r.New("NOTFOUND", "/*")
	if len(args) == 1 && args[0].K == value.Func {
		return route.Handle(args[0]) // handler form: caller owns the response
	}
	route.isView = true
	route.isNotfound = true
	route.viewArgs = args
	return route
}

// Context registers tenant-wide defaults applied to ctx.view() rendering:
//
//	router.context({ render })          // an explicit Render object (value.New → {K:Struct, V:*Render})
//	router.context({ minify: true })    // collapse whitespace + inline CSS on every rendered page
//	router.context({ jit: true })       // inline per-page JIT CSS (vs router.jit() = cached /jitcss service)
//
// minify / jit are convenience flags applied to the view render — the explicit one if given,
// otherwise a zero-config default (rooted at views/) created on the spot — so a bare
// router.context({ minify: true }) works with no render wiring.
func (r *Router) Context(cfg value.Value) *Router {
	if !cfg.IsMap() {
		return r
	}
	m := cfg.Map()
	if rv, ok := m["render"]; ok {
		if rd, ok := rv.V.(*Render); ok {
			r.tenant.viewRender = rd
		}
	}
	if v, ok := m["minify"]; ok {
		r.viewRenderDefaults().minify = v.Truthy()
	}
	if v, ok := m["jit"]; ok {
		r.viewRenderDefaults().jitEnabled = v.Truthy()
	}
	return r
}

// viewRenderDefaults returns the tenant's view render, lazily creating a zero-config one
// (views/ root, "notfound" fallback) if none was registered — so the minify/jit shorthands
// in Context have a render to attach to even without router.context({ render }).
func (r *Router) viewRenderDefaults() *Render {
	if r.tenant.viewRender == nil {
		rd := NewRender(r.tenant)
		rd.directory = "views"
		rd.notfound = "notfound"
		r.tenant.viewRender = rd
	}
	return r.tenant.viewRender
}

// Page registers a GET route that renders its page — the route-first declaration of
// a static page. The first arg is ALWAYS the ROUTE path; an optional second arg is the
// page file when route ≠ page. Render runs with no handler and no VM.
//
//	router.page("/about")       // GET /about → app/about/page.kitwork.html
//	router.page("/", "/home")   // route ≠ page → 2nd arg is the page
//	router.page("/docs/:site?") // dynamic; page derived from the route
//
// It is exactly router.get(path).view(pageFile?). For per-request data use
// router.get(path).handle((ctx) => ctx.view(data)).
func (r *Router) Page(args ...value.Value) *Router {
	if len(args) == 0 || !args[0].IsString() {
		return r // route path required
	}
	route := r.New("GET", args[0].String())
	route.isView = true
	route.viewArgs = args[1:]
	return route
}

// View ends a BUILT route by rendering a page — no JS handler, no VM:
//
//	router.get("/about").view()     // → app/about/page.kitwork.html
//	router.get("/").view("/home")   // route ≠ page → arg is the page
//
// Args match ctx.view(): a string is the page, a map is binding. To declare a page
// route in one call use router.page(path); for per-request data use
// .handle((ctx) => ctx.view(data)).
func (r *Router) View(args ...value.Value) *Router {
	r.isView = true
	r.viewArgs = args
	return r
}

// Jit declares the site-wide JIT CSS feature: a single, browser-cached stylesheet built
// from the utility classes used across the tenant's templates, served at /jitcss (or a
// custom path), and auto-linked into every rendered page. Declare it once at boot:
//
//	router.jit()                              // serve at /jitcss + auto-link in <head>
//	router.jit("/styles.css")                 // custom path
//	router.jit({ path: "/css", inject: false }) // serve only; place the <link> yourself
//
// This is SERVICE mode — one shared, cached request for the whole site. Contrast with
// render.jit(), which INLINES a per-page <style>. The route serves CSS in Go (no VM).
func (r *Router) Jit(args ...value.Value) *Router {
	path := JITStylesheetPath
	inject := true
	if len(args) > 0 {
		a := args[0]
		if a.IsString() {
			if p := a.String(); p != "" {
				path = p
			}
		} else if a.IsMap() {
			m := a.Map()
			if p, ok := m["path"]; ok && p.String() != "" {
				path = p.String()
			}
			if v, ok := m["inject"]; ok {
				inject = v.Truthy()
			}
		}
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Surface an obvious clash instead of failing silently: the routes table is last-write-
	// wins, so if another GET route already owns this path one of them will be shadowed.
	if r.tenant.routes != nil {
		if rt, _ := r.tenant.routes.Match("GET", path); rt != nil && !rt.isJIT {
			fmt.Printf("[router.jit] GET %s is already registered by another route — it will be shadowed\n", path)
		}
	}

	route := r.New("GET", path)
	route.isJIT = true

	// Auto-link uses the route's CANONICAL path (New normalizes basePath + slashes), so the
	// injected <link href> can never diverge from the route that actually serves the CSS —
	// e.g. router.group("/assets").jit("/css") serves AND links /assets/css. Set once at boot.
	r.tenant.jitRoute = route.Path
	r.tenant.jitInject = inject
	return route
}

func (r *Router) Handle(l value.Value) *Router { r.handle, _ = l.V.(*value.Lambda); return r }
func (r *Router) Guard(l value.Value) *Router {
	if l.K == value.Func {
		if lambda, ok := l.V.(*value.Lambda); ok {
			r.guards = append(r.guards, lambda)
		}
	}
	return r
}
func (r *Router) Use(l value.Value) *Router {
	if l.K == value.Func {
		if lambda, ok := l.V.(*value.Lambda); ok {
			r.middlewares = append(r.middlewares, lambda)
		}
	}
	return r
}
func (r *Router) Middleware(l value.Value) *Router {
	return r.Use(l)
}
func (r *Router) Start(v value.Value) *Router   { r.start, _ = v.V.(*value.Lambda); return r }
func (r *Router) Then(v value.Value) *Router    { r.then, _ = v.V.(*value.Lambda); return r }
func (r *Router) Catch(v value.Value) *Router   { r.catch, _ = v.V.(*value.Lambda); return r }
func (r *Router) Finally(v value.Value) *Router { r.final, _ = v.V.(*value.Lambda); return r }

func (r *Router) Group(prefix string) *Router {
	newGroup := *r
	newGroup.basePath = "/" + strings.Trim(prefix, "/")
	return &newGroup
}

func (r *Router) File(path string) *Router {
	r.response.File(r.tenant.resolve(path))
	return r
}

func (r *Router) Response(data value.Value, options ...interface{}) *Router {

	// 2. Nếu không, coi là phản hồi dữ liệu tĩnh
	r.response.Send(data, options...)
	return r
}

func (r *Router) Directory(path string) *Router {
	r.response.Directory(r.tenant.resolve(path))
	return r
}

func (r *Router) Redirect(url string) *Router {
	r.response.Redirect(value.New(url))
	return r
}

func (r *Router) Cache(v value.Value) *Router {
	d, err := time.ParseDuration(v.String())
	if err == nil {
		r.cacheTTL = d
	}
	return r
}

func (r *Router) Limit(args ...value.Value) *Router {
	if len(args) == 0 {
		return r
	}

	firstArg := args[0]

	// Case 1: Object/Map parameter, e.g., .limit({ rate: 10, period: "1s" }) or .limit({ rate: 10, second: 1 })
	if firstArg.IsMap() {
		m := firstArg.Map()
		rateVal, okRate := m["rate"]
		periodVal, okPeriod := m["period"]

		var rate int
		var period time.Duration
		var err error
		var hasPeriod bool

		if okRate {
			// Parse Rate
			if rateVal.IsNumeric() {
				rate = int(rateVal.Float())
			} else {
				rate, _, err = parseLimitStr(rateVal.String() + "/1s")
			}
		}

		if okPeriod {
			hasPeriod = true
			// Parse Period
			if periodVal.K == value.Duration {
				period = time.Duration(int64(periodVal.N))
			} else if periodVal.IsString() {
				period, err = time.ParseDuration(periodVal.String())
			} else if periodVal.IsNumeric() {
				period = time.Duration(periodVal.Float()) * time.Second
			}
		} else {
			// Check for other keys like second, seconds, minute, minutes, hour, hours, day, days
			unitKeys := []struct {
				names []string
				unit  time.Duration
			}{
				{[]string{"second", "seconds"}, time.Second},
				{[]string{"minute", "minutes"}, time.Minute},
				{[]string{"hour", "hours"}, time.Hour},
				{[]string{"day", "days"}, 24 * time.Hour},
			}

			for _, uk := range unitKeys {
				for _, name := range uk.names {
					if unitVal, ok := m[name]; ok {
						if unitVal.IsNumeric() {
							period = time.Duration(unitVal.Float()) * uk.unit
							hasPeriod = true
							break
						}
					}
				}
				if hasPeriod {
					break
				}
			}
		}

		if err == nil && rate > 0 && period > 0 && hasPeriod {
			r.hasLimit = true
			r.limitRate = rate
			r.limitPeriod = period
			return r
		}
	}

	// Case 2: Multiple parameters, e.g., .limit(10, "1s") or .limit(10, 1) or .limit(10, time.Second)
	if len(args) >= 2 {
		rateArg := args[0]
		periodArg := args[1]

		var rate int
		var period time.Duration
		var err error

		// Parse Rate
		if rateArg.IsNumeric() {
			rate = int(rateArg.Float())
		} else {
			rate, _, err = parseLimitStr(rateArg.String() + "/1s")
		}

		// Parse Period
		if periodArg.K == value.Duration {
			period = time.Duration(int64(periodArg.N))
		} else if periodArg.IsString() {
			period, err = time.ParseDuration(periodArg.String())
		} else if periodArg.IsNumeric() {
			period = time.Duration(periodArg.Float()) * time.Second
		}

		if err == nil && rate > 0 && period > 0 {
			r.hasLimit = true
			r.limitRate = rate
			r.limitPeriod = period
			return r
		}
	}

	// Case 3: Single string parameter, e.g., .limit("10/s")
	rate, period, err := parseLimitStr(firstArg.String())
	if err == nil {
		r.hasLimit = true
		r.limitRate = rate
		r.limitPeriod = period
	} else {
		fmt.Printf("[Router.Limit] Error parsing rate limit: %v\n", err)
	}

	return r
}

func parseLimitStr(s string) (int, time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid format: must be <rate>/<period>")
	}

	var rate int
	_, err := fmt.Sscanf(parts[0], "%d", &rate)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid rate: %w", err)
	}

	periodStr := strings.TrimSpace(parts[1])
	if periodStr == "" {
		return 0, 0, fmt.Errorf("empty period")
	}

	// If period has no number, e.g. "s", "m", prepend "1"
	hasDigit := false
	for _, char := range periodStr {
		if char >= '0' && char <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		periodStr = "1" + periodStr
	}

	periodStr = strings.ReplaceAll(periodStr, "seconds", "s")
	periodStr = strings.ReplaceAll(periodStr, "second", "s")
	periodStr = strings.ReplaceAll(periodStr, "sec", "s")
	periodStr = strings.ReplaceAll(periodStr, "minutes", "m")
	periodStr = strings.ReplaceAll(periodStr, "minute", "m")
	periodStr = strings.ReplaceAll(periodStr, "min", "m")
	periodStr = strings.ReplaceAll(periodStr, "hours", "h")
	periodStr = strings.ReplaceAll(periodStr, "hour", "h")
	periodStr = strings.ReplaceAll(periodStr, "hr", "h")

	d, err := time.ParseDuration(periodStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid duration unit: %w", err)
	}

	return rate, d, nil
}

func (r *Router) Static(v value.Value) *Router {
	var durationStr string
	if v.K == value.Map {
		m := v.Map()
		if d, ok := m["duration"]; ok {
			durationStr = d.String()
		}
	} else {
		durationStr = v.String()
	}

	d, err := time.ParseDuration(durationStr)
	if err == nil {
		r.staticTTL = d
	}
	return r
}

type StaticCacheMeta struct {
	Status      int               `json:"status"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers"`
	ExpireAt    time.Time         `json:"expire_at"`
}

func (r *Router) getStaticCachePath() (string, error) {
	req := r.request
	if req == nil {
		return "", fmt.Errorf("request is nil")
	}

	keySource := r.Method + ":" + req.URL.Path + "?" + req.URL.RawQuery
	hasher := md5.New()
	hasher.Write([]byte(keySource))
	hashStr := hex.EncodeToString(hasher.Sum(nil))

	subDir := hashStr[:2]
	fileName := hashStr[2:]

	cacheDir := r.tenant.resolve(".static", subDir)
	return filepath.Join(cacheDir, fileName), nil
}

func (r *Router) serveStaticCache(w http.ResponseWriter, req *http.Request) bool {
	if r.staticTTL <= 0 {
		return false
	}

	basePath, err := r.getStaticCachePath()
	if err != nil {
		return false
	}

	mu := getFileLock(basePath)
	mu.RLock()
	hasReadLock := true
	defer func() {
		if hasReadLock {
			mu.RUnlock()
		}
	}()

	// 1. Check if Gzip is supported and try to serve .static.gz first
	gzipSupported := strings.Contains(req.Header.Get("Accept-Encoding"), "gzip")
	var file *os.File
	var openErr error
	usingGzip := false

	if gzipSupported {
		file, openErr = os.Open(basePath + ".static.gz")
		if openErr == nil {
			usingGzip = true
		}
	}

	// 2. Fall back to uncompressed .static if gzip was not supported or failed to open
	if !usingGzip {
		file, openErr = os.Open(basePath + ".static")
		if openErr != nil {
			return false
		}
	}
	defer file.Close()

	// 3. Read first 10 bytes for metadata length
	headerBuf := make([]byte, 10)
	_, err = io.ReadFull(file, headerBuf)
	if err != nil {
		return false
	}
	var L int
	_, err = fmt.Sscanf(string(headerBuf), "%010d", &L)
	if err != nil {
		return false
	}

	// 4. Read L bytes of metadata
	metaBuf := make([]byte, L)
	_, err = io.ReadFull(file, metaBuf)
	if err != nil {
		return false
	}

	// 5. Parse metadata
	var meta StaticCacheMeta
	if err := json.Unmarshal(metaBuf, &meta); err != nil {
		return false
	}

	// 6. Check expiration. If expired, clean up both files
	if time.Now().After(meta.ExpireAt) {
		file.Close()

		mu.RUnlock()
		hasReadLock = false

		mu.Lock()
		os.Remove(basePath + ".static")
		os.Remove(basePath + ".static.gz")
		mu.Unlock()

		return false
	}

	// 7. Set Headers & Status Code
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}
	for k, v := range meta.Headers {
		w.Header().Set(k, v)
	}

	// Set Content-Encoding header if serving pre-compressed content
	if usingGzip {
		w.Header().Set("Content-Encoding", "gzip")
	}

	w.WriteHeader(meta.Status)

	// 8. Stream Body directly to writer (Zero-Memory Copy!)
	io.Copy(w, file)
	return true
}

func (r *Router) saveStaticCache() {
	if r.staticTTL <= 0 || r.err != nil || !r.response.IsSend() {
		return
	}

	basePath, err := r.getStaticCachePath()
	if err != nil {
		return
	}

	mu := getFileLock(basePath)
	mu.Lock()
	defer mu.Unlock()

	// Ensure directory exists
	os.MkdirAll(filepath.Dir(basePath), 0755)

	// Extract Content-Type
	contentType := ""
	kind := r.response.Kind()
	switch kind {
	case "json":
		contentType = "application/json; charset=utf-8"
	case "html", "render":
		contentType = "text/html; charset=utf-8"
	case "text":
		contentType = "text/plain; charset=utf-8"
	case "css":
		contentType = "text/css; charset=utf-8"
	case "bytes":
		contentType = "application/octet-stream"
	case "image":
		contentType = "image/png"
	}

	meta := StaticCacheMeta{
		Status:      r.response.Code(),
		ContentType: contentType,
		Headers:     make(map[string]string),
		ExpireAt:    time.Now().Add(r.staticTTL),
	}
	if meta.Status == 0 {
		meta.Status = 200
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return
	}
	L := len(metaBytes)

	// Get body bytes
	var bodyBytes []byte
	if kind == "render" {
		bodyBytes, _ = r.render()
	} else if kind == "json" {
		bodyBytes, _ = json.Marshal(r.response.Data())
	} else {
		bodyBytes = r.response.toBytes()
	}

	// 1. Write the standard .static file (uncompressed)
	fileRaw, err := os.Create(basePath + ".static")
	if err == nil {
		headerStr := fmt.Sprintf("%010d", L)
		fileRaw.Write([]byte(headerStr))
		fileRaw.Write(metaBytes)
		fileRaw.Write(bodyBytes)
		fileRaw.Close()
	}

	// 2. Write the compressed .static.gz file only if content is compressible and large enough
	// Compressible: html, json, text, render. Threshold: > 1024 bytes (1 KB)
	shouldCompress := len(bodyBytes) > 1024 && (kind == "html" || kind == "json" || kind == "render" || kind == "text" ||
		strings.Contains(contentType, "text/") || strings.Contains(contentType, "json"))

	if shouldCompress {
		fileGzip, err := os.Create(basePath + ".static.gz")
		if err == nil {
			headerStr := fmt.Sprintf("%010d", L)
			fileGzip.Write([]byte(headerStr))
			fileGzip.Write(metaBytes)

			// Compress body bytes using gzip writer
			gw := gzip.NewWriter(fileGzip)
			_, errGz := gw.Write(bodyBytes)
			if errGz == nil {
				gw.Close()
			}
			fileGzip.Close()
		}
	}
}
