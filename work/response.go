package work

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	filehelper "github.com/kitwork/engine/utilities/file"
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

	// ctx is the request's context, whose deferred view the head methods (.title(), .image(), …)
	// write to; nil for a response no request holds
	ctx *Context

	// uiPage is the page notFound() or error(…) asks for, and uiMessage what error(…) said: in a
	// Kitwork UI site the end of the request draws the nearest notfound.ui or error.ui (answerUIPage)
	uiPage    string
	uiMessage string

	// order is where the response stands in a router.work chain (response_order.go)
	order responseOrder
}

func (r *Response) IsSend() bool {
	return r.kind != "" || !r.data.IsBlank()
}

func (r *Response) IsError() bool { return r.kind == "error" }

// Every edit stands before the answer (KITWORK_ROUTER §6, 20/10/2026): context.title("…").status(201)
// .type("image/png") … edit the response and return it; an answer — json, view, html, text, svg,
// file, redirect, notFound, error — makes it and ends the chain, and returns the response too, so
// `return context.json(data)` is the answer. In router.work the order is checked (response_order.go):
// an edit after the answer, two answers, the page's text before an answer other than view, a .type()
// against json or view, are errors. A template site keeps every way it has.
//
// A number after the value is the status, the old way of a template site (context.json(data, 201)).
func (r *Response) answer(data value.Value, kind string, options []any) *Response {
	r.Return(data, kind)
	for _, option := range options {
		switch choice := option.(type) {
		case int:
			r.code = choice
		case float64: // a program's number (value.transformArg)
			r.code = int(choice)
		case value.Value:
			if choice.IsNumber() {
				r.code = int(choice.N)
			}
		}
	}
	return r
}

func (r *Response) JSON(data value.Value, options ...any) (*Response, error) {
	if err := r.answering("json"); err != nil {
		return r, err
	}
	return r.answer(data, "json", options), nil
}

func (r *Response) Return(data value.Value, kind string, code ...int) {
	r.data = data
	r.kind = kind
	if len(code) > 0 {
		r.code = code[0]
	}
}

func (r *Response) Send(data value.Value, options ...interface{}) *Response {
	r.data = data
	if len(options) == 0 {
		if !data.IsBlank() {
			if r.contentType != "" {
				r.kind = "typed"
			} else {
				r.kind = "" // Clear kind to let responder guess from data
			}
		}
		return r
	}
	for _, opt := range options {
		switch v := opt.(type) {
		case int:
			r.code = v
		case float64:
			r.code = int(v)
		case string:
			r.kind = v
		case value.Value:
			if v.IsNumber() {
				r.code = int(v.N)
			} else if v.IsString() {
				r.kind = v.String()
			}
		}
	}
	return r
}

func (r *Response) String(data string, code ...int) *Response {
	r.Return(value.New(data), "text", code...)
	return r
}

// File answers with a file of kitwork/file, served from the disk when the answer is written (mục 9: Range,
// ETag, Last-Modified, the media type by the extension unless .type() came first): its path, written as
// the module's reads write it (context.file("exports/a.csv"), context.file("app/shop.com/assets/a.pdf")),
// or a handle of it (context.file(file.open("uploads/").segment(id))). Or it answers with bytes the
// handler holds, in the media type .type() set, else application/octet-stream:
// context.type("image/png").file(bytes). A string is always a path (23/10/2026: until then a string was
// refused, naming the handle); text goes through .text() or .html(). Every site, a template's too.
//
// What it is given is read before it answers: a path the module refuses, or anything but a path, a handle
// or bytes, fails without answering, so the handler may still answer otherwise (safe(() => …)).
func (r *Response) File(data value.Value, options ...any) (*Response, error) {
	kind := "file"
	switch _, isHandle := data.V.(*fileHandle); {
	case data.K == value.Bytes:
		kind = "bytes"
	case isHandle && data.K == value.Proxy:
	case data.K == value.String:
		// a path of kitwork/file, read as file.open reads it: no app/ is the data zone, app/… the code zone; a
		// path the module refuses fails here in its words, a file out of reach answers 404 when it is served
		handle, err := r.fileHandle(data.Text())
		if err != nil {
			return r, err
		}
		data = value.Value{K: value.Proxy, V: handle}
	default:
		return r, fmt.Errorf("`.file()` nhận đường dẫn của kitwork/file, handle của nó, hay bytes: `context.file(\"exports/bao-cao.csv\")`, `context.file(file.open(\"uploads/\").segment(id))`, `context.type(\"image/png\").file(bytes)`; trả lời bằng chữ thì `.text(…)` (KITWORK_FILE.md mục 9)")
	}
	if err := r.answering("file"); err != nil {
		return r, err
	}
	return r.answer(data, kind, options), nil
}

// fileHandle is the handle of kitwork/file a path written in .file() names, in the app of the request.
func (r *Response) fileHandle(written string) (*fileHandle, error) {
	if r.ctx == nil || r.ctx.request == nil || r.ctx.request.router == nil || r.ctx.request.router.tenant == nil {
		return nil, fmt.Errorf("`.file(%q)`: không có app của request để tìm file", written)
	}
	path, failed := filehelper.Parse(written)
	if failed != nil {
		return nil, failed
	}
	return &fileHandle{tenant: r.ctx.request.router.tenant, path: path}, nil
}

func (r *Response) Directory(path string, code ...int) *Response {
	r.Return(value.New(path), "directory", code...)
	return r
}

// Redirect sends to url; in router.work with 302 when no .status() came before (the responder turns
// any other into 303, the way of a template site).
func (r *Response) Redirect(url value.Value, options ...any) (*Response, error) {
	if err := r.answering("redirect"); err != nil {
		return r, err
	}
	if r.ordered() {
		if r.code != 0 && (r.code < 300 || r.code > 399) {
			// router.work: a status that is not a redirect's is an error, not a quiet 303 (§6)
			return r, fmt.Errorf("`.status(%d)` trước `.redirect()`: chuyển hướng là 3xx, `context.status(301).redirect(…)` (301, 302, 303, 307, 308); không có `.status()` thì 302", r.code)
		}
		if r.code == 0 {
			r.code = http.StatusFound
		}
	}
	return r.answer(url, "redirect", options), nil
}

func (r *Response) HTML(data value.Value, options ...any) (*Response, error) {
	if err := r.answering("html"); err != nil {
		return r, err
	}
	return r.answer(data, "html", options), nil
}

func (r *Response) SVG(data value.Value, options ...any) (*Response, error) {
	if err := r.answering("svg"); err != nil {
		return r, err
	}
	return r.answer(data, "svg", options), nil
}

// CSS responds with Content-Type: text/css — pair with render.css() to serve a stylesheet
// from a handler: res.css(render.css()). Works with .cache()/.static() like any response.
func (r *Response) CSS(data value.Value, options ...any) *Response {
	return r.answer(data, "css", options)
}

func (r *Response) Bytes(data []byte, options ...any) *Response {
	return r.answer(value.New(data), "bytes", options)
}

func (r *Response) Text(data value.Value, options ...any) (*Response, error) {
	if err := r.answering("text"); err != nil {
		return r, err
	}
	return r.answer(data, "text", options), nil
}

func (r *Response) Render(data value.Value, options ...any) *Response {
	return r.answer(data, "render", options)
}

func (r *Response) Error(data value.Value, code ...int) (*Response, error) {
	if err := r.answering("error"); err != nil {
		return r, err
	}
	r.ErrorString(data.String(), code...)
	r.uiPage, r.uiMessage = uiErrorPage, errorMessage(data)
	return r, nil
}

func (r *Response) ErrorString(data string, code ...int) *Response {
	r.Return(value.New(data), "error", code...)
	return r
}

func (r *Response) HelloWorld() *Response {
	r.String("Hello World")
	return r
}

func (r *Response) View(data value.Value, options ...any) (*Response, error) {
	if r.ctx != nil && r.ctx.request != nil {
		if err := r.ctx.router().refuseGuardView(); err != nil {
			return r, err
		}
	}
	if err := r.answering("view"); err != nil {
		return r, err
	}
	return r.answer(data, "render", options), nil
}

func (r *Response) NotFound() (*Response, error) {
	if err := r.answering("notFound"); err != nil {
		return r, err
	}
	r.ErrorString("404 Not Found", 404)
	r.uiPage = uiNotFoundPage
	return r, nil
}

func (r *Response) Status(code int) (*Response, error) {
	if err := r.editing("status"); err != nil {
		return r, err
	}
	r.code = code
	return r, nil
}

// setType sets the media type an answer goes out with (.type()). XML, CSV, calendar, and vendor
// formats all share this path instead of growing one response method per representation.
func (r *Response) setType(mediaType string) {
	mediaType = strings.TrimSpace(mediaType)
	if mediaType != "" && strings.Contains(mediaType, "/") && !strings.ContainsAny(mediaType, "\r\n") {
		r.contentType = mediaType
	}
}

// mediaType is the Content-Type the answer goes out with: what .type() set when it did, else its
// kind's own.
func (r *Response) mediaType(kind string) string {
	if r.contentType != "" {
		return r.contentType
	}
	return kind
}

// The page's text — title, description, image, meta, and the head names the engine has (url,
// language, jsonld) — goes to the deferred view the answer draws. Each is an edit: it returns the
// response and stands before the answer, which in router.work is .view() (response_order.go).
// og:type is .meta({ type: "article" }).
func (r *Response) viewOf() *ViewBuilder {
	if r.ctx == nil {
		return nil
	}
	return r.ctx.viewBuilder()
}

func (r *Response) head(name string, set func(*ViewBuilder)) (*Response, error) {
	if err := r.editing(name); err != nil {
		return r, err
	}
	if builder := r.viewOf(); builder != nil {
		set(builder)
	}
	return r, nil
}

func (r *Response) Title(v value.Value) (*Response, error) {
	return r.head("title", func(b *ViewBuilder) { b.Title(v) })
}
func (r *Response) Description(v value.Value) (*Response, error) {
	return r.head("description", func(b *ViewBuilder) { b.Description(v) })
}

// Image is the page's head image (og:image, twitter:image), not an answer: bytes go out with
// context.type("image/png").file(bytes).
func (r *Response) Image(v value.Value) (*Response, error) {
	return r.head("image", func(b *ViewBuilder) { b.Image(v) })
}
func (r *Response) Url(v value.Value) (*Response, error) {
	return r.head("url", func(b *ViewBuilder) { b.Url(v) })
}
func (r *Response) Language(v value.Value) (*Response, error) {
	return r.head("language", func(b *ViewBuilder) { b.Language(v) })
}
func (r *Response) Meta(v value.Value) (*Response, error) {
	return r.head("meta", func(b *ViewBuilder) { b.Meta(v) })
}
func (r *Response) Bind(v value.Value) *Response {
	if builder := r.viewOf(); builder != nil {
		builder.Bind(v)
	}
	return r
}
func (r *Response) Jsonld(args ...value.Value) (*Response, error) {
	return r.head("jsonld", func(b *ViewBuilder) { b.Jsonld(args...) })
}

// Header sets a response header. Request headers remain available through ctx.header(name).
func (r *Response) Header(name, data string) (*Response, error) {
	if err := r.editing("header"); err != nil {
		return r, err
	}
	r.setHeader(name, data)
	return r, nil
}

// Headers sets the headers of an object, one Header each: context.headers({ "X-A": "1", … }).
func (r *Response) Headers(headers value.Value) (*Response, error) {
	if err := r.editing("headers"); err != nil {
		return r, err
	}
	if !headers.IsMap() {
		return r, fmt.Errorf("`.headers()` nhận một object tên → giá trị: `context.headers({ \"Cache-Control\": \"no-store\" })`")
	}
	for name, data := range headers.Map() {
		r.setHeader(name, data.Text())
	}
	return r, nil
}

func (r *Response) setHeader(name, data string) {
	name = http.CanonicalHeaderKey(strings.TrimSpace(name))
	data = strings.TrimSpace(data)
	if name == "" || strings.ContainsAny(name+data, "\r\n") {
		return
	}
	if r.headers == nil {
		r.headers = make(map[string]string)
	}
	r.headers[name] = data
}

// headerMap is a copy of the headers set, for the engine (the cache keeps them).
func (r *Response) headerMap() map[string]string {
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
		Secure:   r.overHTTPS(), // Secure when the request came over https (KITWORK_ROUTER §6)
	}
	applyCookieOptions(cookie, options...)
	r.cookies = append(r.cookies, cookie)
	return r
}

// Cookie sets a cookie on the response and returns it, an edit before the answer:
// response.cookie("seen", "1").json(data) — and response.cookie("seen", null) deletes it. Reading a
// cookie is the request's (request.cookie("seen")).
func (r *Response) Cookie(name string, args ...value.Value) (*Response, error) {
	if err := r.editing("cookie"); err != nil {
		return r, err
	}
	if len(args) == 0 {
		return r, nil
	}
	if r.inUISite() {
		if err := r.checkCookieOptions(name, args[1:]); err != nil {
			return r, err
		}
	}
	if args[0].K == value.Nil {
		return r.DeleteCookie(name, args[1:]...), nil
	}
	return r.SetCookie(name, args[0], args[1:]...), nil
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
		Secure:   r.overHTTPS(),
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
	if v, ok := opts["maxAge"]; ok {
		if v.IsNumber() {
			cookie.MaxAge = int(v.N) // seconds
		} else if span, err := ParseDuration(v.Text()); err == nil && span > 0 {
			cookie.MaxAge = int(span / time.Second) // a span: "30d"
		}
	}
	if v, ok := opts["expires"]; ok {
		if at, ok := cookieExpires(v); ok {
			cookie.Expires = at
		}
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
