package work

import (
	"net/http"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

type page struct {
	template string
	layout   string
}

type Response struct {
	data        value.Value
	kind        string
	code        int
	contentType string
	headers     map[string]string

	page    *page
	cookies []*http.Cookie
}

func (r *Response) IsSend() bool {
	return r.kind != "" || !r.data.IsBlank()
}

func (r *Response) IsError() bool { return r.kind == "error" }

func (r *Response) JSON(data value.Value, code ...int) {
	r.Return(data, "json", code...)
}

func (r *Response) Return(data value.Value, kind string, code ...int) {
	r.data = data
	r.kind = kind
	if len(code) > 0 {
		r.code = code[0]
	}
}

func (r *Response) Send(data value.Value, options ...interface{}) {
	r.data = data
	if len(options) == 0 {
		if !data.IsBlank() {
			if r.contentType != "" {
				r.kind = "typed"
			} else {
				r.kind = "" // Clear kind to let responder guess from data
			}
		}
		return
	}
	for _, opt := range options {
		switch v := opt.(type) {
		case int:
			r.code = v
		case string:
			r.kind = v
		case value.Value:
			// Xử lý nếu tham số đến từ JavaScript VM
			if v.IsNumber() {
				r.code = int(v.N)
			} else if v.IsString() {
				r.kind = v.String()
			}
		}
	}
}

func (r *Response) String(data string, code ...int) {
	r.Return(value.New(data), "text", code...)
}

func (r *Response) File(path string, code ...int) {
	r.Return(value.New(path), "file", code...)
}

func (r *Response) Directory(path string, code ...int) {
	r.Return(value.New(path), "directory", code...)
}

func (r *Response) Redirect(url value.Value, code ...int) {
	r.Return(url, "redirect", code...)
}

func (r *Response) HTML(data value.Value, code ...int) {
	r.Return(data, "html", code...)
}

func (r *Response) Image(data []byte, code ...int) {
	r.Return(value.New(data), "image", code...)
}

func (r *Response) SVG(data value.Value, code ...int) {
	r.Return(data, "svg", code...)
}

// CSS responds with Content-Type: text/css — pair with render.css() to serve a stylesheet
// from a handler: res.css(render.css()). Works with .cache()/.static() like any response.
func (r *Response) CSS(data value.Value, code ...int) {
	r.Return(data, "css", code...)
}

func (r *Response) Bytes(data []byte, code ...int) {
	r.Return(value.New(data), "bytes", code...)
}

func (r *Response) Text(data value.Value, code ...int) {
	r.Return(data, "text", code...)
}

func (r *Response) Render(data value.Value, code ...int) {
	r.Return(data, "render", code...)
}

func (r *Response) Error(data value.Value, code ...int) {
	r.ErrorString(data.String(), code...)
}

func (r *Response) ErrorString(data string, code ...int) {
	r.Return(value.New(data), "error", code...)
}

func (r *Response) HelloWorld() {
	r.String("Hello World")
}

func (r *Response) View(data value.Value, code ...int) {
	r.Return(data, "render", code...)
}

func (r *Response) NotFound() {
	r.Error(value.New("404 Not Found"), 404)
}

func (r *Response) Status(code int) *Response {
	r.code = code
	return r
}

// Type selects the media type used by Send. XML, CSV, calendar, and vendor formats all share
// this path instead of growing one response method per representation.
func (r *Response) Type(mediaType string) *Response {
	mediaType = strings.TrimSpace(mediaType)
	if mediaType != "" && strings.Contains(mediaType, "/") && !strings.ContainsAny(mediaType, "\r\n") {
		r.contentType = mediaType
	}
	return r
}

// Header sets a response header. Request headers remain available through ctx.header(name).
func (r *Response) Header(name, data string) *Response {
	name = http.CanonicalHeaderKey(strings.TrimSpace(name))
	data = strings.TrimSpace(data)
	if name == "" || strings.ContainsAny(name+data, "\r\n") {
		return r
	}
	if r.headers == nil {
		r.headers = make(map[string]string)
	}
	r.headers[name] = data
	return r
}

func (r *Response) Headers() map[string]string {
	out := make(map[string]string, len(r.headers))
	for name, data := range r.headers {
		out[name] = data
	}
	return out
}

// SetCookie queues an HTTP cookie for the response. Cookies are HttpOnly and SameSite=Lax by
// default; callers may override path, domain, secure, httpOnly, sameSite, and maxAge.
func (r *Response) SetCookie(name string, data value.Value, options ...value.Value) *Response {
	cookie := &http.Cookie{
		Name:     name,
		Value:    data.String(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	applyCookieOptions(cookie, options...)
	r.cookies = append(r.cookies, cookie)
	return r
}

// DeleteCookie expires a cookie immediately while preserving path/domain matching options.
func (r *Response) DeleteCookie(name string, options ...value.Value) *Response {
	cookie := &http.Cookie{
		Name:     name,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
	}
	applyCookieOptions(cookie, options...)
	cookie.Value = ""
	cookie.MaxAge = -1
	cookie.Expires = time.Unix(1, 0).UTC()
	r.cookies = append(r.cookies, cookie)
	return r
}

func applyCookieOptions(cookie *http.Cookie, options ...value.Value) {
	if len(options) == 0 || options[0].K != value.Map {
		return
	}
	opts := options[0].Map()
	if v, ok := opts["path"]; ok && v.String() != "" {
		cookie.Path = v.String()
	}
	if v, ok := opts["domain"]; ok {
		cookie.Domain = v.String()
	}
	if v, ok := opts["secure"]; ok {
		cookie.Secure = v.Truthy()
	}
	if v, ok := opts["httpOnly"]; ok {
		cookie.HttpOnly = v.Truthy()
	}
	if v, ok := opts["maxAge"]; ok && v.IsNumber() {
		cookie.MaxAge = int(v.N)
	}
	if v, ok := opts["sameSite"]; ok {
		switch strings.ToLower(v.String()) {
		case "strict":
			cookie.SameSite = http.SameSiteStrictMode
		case "none":
			cookie.SameSite = http.SameSiteNoneMode
		case "default":
			cookie.SameSite = http.SameSiteDefaultMode
		default:
			cookie.SameSite = http.SameSiteLaxMode
		}
	}
}

func (r *Response) writeCookies(w http.ResponseWriter) {
	for _, cookie := range r.cookies {
		http.SetCookie(w, cookie)
	}
}

func (r *Response) writeHeaders(w http.ResponseWriter) {
	for name, data := range r.headers {
		w.Header().Set(name, data)
	}
}

func requestNotModified(request *http.Request, headers http.Header) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}
	if etag := headers.Get("ETag"); etag != "" {
		if candidate := request.Header.Get("If-None-Match"); candidate != "" {
			for _, item := range strings.Split(candidate, ",") {
				if strings.TrimSpace(item) == etag || strings.TrimSpace(item) == "*" {
					return true
				}
			}
			return false
		}
	}
	lastModified := headers.Get("Last-Modified")
	if lastModified == "" {
		return false
	}
	since := request.Header.Get("If-Modified-Since")
	modified, modifiedErr := http.ParseTime(lastModified)
	requested, requestErr := http.ParseTime(since)
	return modifiedErr == nil && requestErr == nil && !modified.After(requested.Add(time.Second))
}

func (r *Response) Template(index string) *Response {
	if r.page == nil {
		r.page = &page{}
	}
	r.page.template = index
	return r
}

func (r *Response) Layout(layout string) *Response {
	if r.page == nil {
		r.page = &page{}
	}
	r.page.layout = layout
	return r
}

func (r *Response) Code() int {
	return r.code
}

func (r *Response) toBytes() []byte {
	// if r.kind == "render" {
	// 	// Trường hợp A: Dùng một bộ Render đã config sẵn (như 'home')
	// 	if r.renderer != nil {
	// 		return []byte(r.renderer.tmpl(r.data.Interface()))
	// 	}

	// 	// Trường hợp B: Dùng cấu hình ad-hoc qua .template().layout()
	// 	if r.page != nil {
	// 		// Tạo một renderer tạm thời dựa trên cấu hình trong r.page
	// 		// Note: r.router and r.router.tenant.Render() are placeholders.
	// 		// You would need to ensure r.router is initialized and provides a way to get a Renderer.
	// 		if r.router != nil {
	// 			engine := r.router.tenantRender() // Assuming tenantRender returns a Renderer
	// 			if r.page.template != "" {
	// 				engine.Template(value.New(r.page.template))
	// 			}
	// 			if r.page.layout != "" {
	// 				engine.Layout(value.New(r.page.layout))
	// 			}
	// 			return []byte(engine.tmpl(r.data.Interface()))
	// 		}
	// 	}
	// }
	return []byte(r.data.String())
}

func (r *Response) Kind() string        { return r.kind }
func (r *Response) Data() value.Value   { return r.data }
func (r *Response) ContentType() string { return r.contentType }
