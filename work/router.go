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
	"time"

	"github.com/kitwork/engine/value"
)

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

	guards []*value.Lambda
	handle *value.Lambda
	then   *value.Lambda
	catch  *value.Lambda
	final  *value.Lambda

	response *Response
	request  *http.Request

	params map[string]string
	err    error // Biến lưu lỗi để truyền giữa các công đoạn

	// Cache configuration
	cacheTTL       time.Duration
	staticTTL      time.Duration
	benchmarkCount int // Số lần chạy lặp để đo hiệu năng
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
			if r.response.Code() == 0 {
				r.response.Status(500)
			}
		} else if !data.IsBlank() {
			if data.K == value.Map || data.K == value.Array {
				kind = "json"
			} else {
				kind = "html"
			}
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

func (r *Router) Handle(l value.Value) *Router { r.handle, _ = l.V.(*value.Lambda); return r }
func (r *Router) Guard(l value.Value) *Router {
	if l.K == value.Func {
		if lambda, ok := l.V.(*value.Lambda); ok {
			r.guards = append(r.guards, lambda)
		}
	}
	return r
}
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
		os.Remove(basePath + ".static")
		os.Remove(basePath + ".static.gz")
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

	// 2. Write the compressed .static.gz file
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


