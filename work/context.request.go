package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

// --- WRAPPERS CHO JAVASCRIPT ---

type Request struct {
	router *Router
}

func (r *Request) request() *http.Request {
	if r.router == nil {
		return nil
	}
	return r.router.request
}

func (r *Request) url() *url.URL {
	req := r.request()
	if req == nil {
		return &url.URL{}
	}
	return req.URL
}

func (r *Request) Query(key string) value.Value {
	u := r.url()
	if u == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(u.Query().Get(key))
}

func (r *Request) Params(key string) value.Value {
	if v, ok := r.router.params[key]; ok {
		return value.New(v)
	}
	return value.Value{K: value.Nil}
}

func (r *Request) Path() value.Value {
	u := r.url()
	if u == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(u.Path)
}

func (r *Request) Method() value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(req.Method)
}

func (r *Request) Host() value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(req.Host)
}

func (r *Request) Hostname() value.Value {
	host := r.Host().String()
	if strings.Contains(host, ":") {
		h, _, _ := strings.Cut(host, ":")
		return value.New(h)
	}
	return value.New(host)
}

func (r *Request) Header(key string) value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(req.Header.Get(key))
}

func (r *Request) Headers() value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	res := make(map[string]value.Value)
	for k, v := range req.Header {
		if len(v) > 0 {
			res[k] = value.New(v[0])
		}
	}
	return value.New(res)
}

func (r *Request) IP() value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	// Try X-Forwarded-For first
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return value.New(strings.TrimSpace(parts[0]))
	}
	return value.New(req.RemoteAddr)
}

func (r *Request) UserAgent() value.Value {
	return r.Header("User-Agent")
}

func (r *Request) Referer() value.Value {
	return r.Header("Referer")
}

func (r *Request) IsJSON() value.Value {
	ct := r.Header("Content-Type").String()
	return value.New(strings.Contains(strings.ToLower(ct), "application/json"))
}

func (r *Request) IsAJAX() value.Value {
	requestedWith := r.Header("X-Requested-With").String()
	return value.New(strings.ToLower(requestedWith) == "xmlhttprequest")
}

func (r *Request) XHR() value.Value {
	return r.IsAJAX()
}

func (r *Request) Cookie(name string) value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	cookie, err := req.Cookie(name)
	if err != nil {
		return value.Value{K: value.Nil}
	}
	return value.New(cookie.Value)
}

func (r *Request) Cookies() value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	res := make(map[string]value.Value)
	for _, cookie := range req.Cookies() {
		res[cookie.Name] = value.New(cookie.Value)
	}
	return value.New(res)
}

func (r *Request) Scheme() value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	if req.TLS != nil {
		return value.New("https")
	}
	if proto := r.Header("X-Forwarded-Proto").String(); proto != "" {
		return value.New(proto)
	}
	return value.New("http")
}

func (r *Request) Protocol() value.Value {
	return r.Scheme()
}

func (r *Request) Secure() value.Value {
	return value.New(r.Scheme().String() == "https")
}

func (r *Request) URL() value.Value {
	// Reconstruct full URL: scheme://host + RequestURI (path?query)
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(r.Scheme().String() + "://" + r.Host().String() + req.URL.RequestURI())
}

func (r *Request) Href() value.Value {
	return r.URL()
}

func (r *Request) URI() value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(req.URL.RequestURI())
}

func (r *Request) OriginalURL() value.Value {
	return r.URI()
}

func (r *Request) Pattern() value.Value {
	if r.router == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(r.router.Path)
}

func (r *Request) Route() value.Value {
	return r.Pattern()
}

func (r *Request) Page() value.Value {
	if r.router == nil {
		return value.Value{K: value.Nil}
	}

	// Tách pattern thành các segment (ví dụ: ["users", ":id?"])
	patternSegments := strings.Split(strings.Trim(r.router.Path, "/"), "/")
	var resolvedSegments []string

	for _, seg := range patternSegments {
		if strings.HasPrefix(seg, ":") {
			// Đây là tham số (:id hoặc :id?)
			isOptional := strings.HasSuffix(seg, "?")
			name := seg[1:]
			if isOptional {
				name = name[:len(name)-1]
			}

			// Kiểm tra xem tham số này có dữ liệu thực tế không
			if val, ok := r.router.params[name]; ok && val != "" {
				// CÓ dữ liệu -> Chuyển thành folder động [name]
				resolvedSegments = append(resolvedSegments, "["+name+"]")
			} else {
				// KHÔNG có dữ liệu
				if !isOptional {
					// Nếu bắt buộc nhưng thiếu (hiếm khi xảy ra nếu route đã khớp)
					resolvedSegments = append(resolvedSegments, "["+name+"]")
				}
				// Nếu là optional và thiếu -> Bỏ qua segment này (về trang danh sách cha)
			}
		} else {
			// Segment tĩnh (ví dụ: "users")
			resolvedSegments = append(resolvedSegments, seg)
		}
	}

	return value.New(strings.Join(resolvedSegments, "/"))
}

func (r *Request) Benchmark(vals ...value.Value) value.Value {
	if len(vals) < 2 {
		return value.New(map[string]interface{}{"error": "Benchmark requires iterations and a callback"})
	}
	count := int(vals[0].N)
	lambda, ok := vals[1].V.(*value.Lambda)
	if !ok {
		return value.New(map[string]interface{}{"error": "Second argument must be a function"})
	}

	// 1. Chuẩn bị đo lường bộ nhớ
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	// 2. Thực thi và đo thời gian
	vm := r.router.tenant.vm
	hArgs := []value.Value{} // Callback thường không có tham số

	start := time.Now()
	for i := 0; i < count; i++ {
		vm.ExecuteLambda(lambda, hArgs)
	}
	duration := time.Since(start)

	// 3. Kết thúc đo lường
	runtime.ReadMemStats(&m2)

	// 4. Tính toán các chỉ số "khủng"
	allocBytes := m2.TotalAlloc - m1.TotalAlloc
	gcCycles := m2.NumGC - m1.NumGC
	ops := float64(count) / duration.Seconds()

	res := make(map[string]interface{})
	res["iterations"] = count
	res["duration"] = duration.String()
	res["ops_per_sec"] = fmt.Sprintf("%.0f", ops)
	res["avg_latency"] = (duration / time.Duration(count)).String()

	// Memory stats
	res["memory"] = map[string]interface{}{
		"total_alloc_mb": fmt.Sprintf("%.2f MB", float64(allocBytes)/1024/1024),
		"alloc_per_op":   fmt.Sprintf("%d bytes", allocBytes/uint64(count)),
		"gc_cycles":      gcCycles,
	}

	return value.New(res)
}

func (r *Request) Body() value.Value {
	req := r.request()
	if req == nil || req.Body == nil {
		return value.Value{K: value.Nil}
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return value.Value{K: value.Nil}
	}

	// Restore body
	req.Body = io.NopCloser(bytes.NewBuffer(body))

	return value.New(string(body))
}

func (r *Request) JSON() value.Value {
	body := r.Body().String()
	if body == "" {
		return value.Value{K: value.Nil}
	}

	var data interface{}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return value.Value{K: value.Nil}
	}

	return value.New(data)
}

func (r *Request) FormValue(key string) value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(req.FormValue(key))
}

func (r *Request) FormParams() value.Value {
	req := r.request()
	if req == nil {
		return value.Value{K: value.Nil}
	}
	if err := req.ParseForm(); err != nil {
		return value.Value{K: value.Nil}
	}
	res := make(map[string]value.Value)
	for k, v := range req.Form {
		if len(v) > 0 {
			res[k] = value.New(v[0])
		}
	}
	return value.New(res)
}

func (r *Request) MultipartForm() (*multipart.Form, error) {
	req := r.request()
	if req == nil {
		return nil, http.ErrNoLocation
	}
	if err := req.ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	return req.MultipartForm, nil
}

func (r *Request) SaveFile(fieldName string, destPath string) value.Value {
	req := r.request()
	if req == nil {
		return value.New(false)
	}

	file, header, err := req.FormFile(fieldName)
	if err != nil {
		return value.New(false)
	}
	defer file.Close()

	fullDest := r.router.tenant.joinPath(destPath)
	// Create directory if not exists
	dir := filepath.Dir(fullDest)
	if filepath.Ext(fullDest) == "" {
		// If destPath is a directory
		dir = fullDest
		os.MkdirAll(dir, 0755)
		fullDest = filepath.Join(dir, header.Filename)
	} else {
		os.MkdirAll(dir, 0755)
	}

	out, err := os.Create(fullDest)
	if err != nil {
		return value.New(false)
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	return value.New(err == nil)
}
