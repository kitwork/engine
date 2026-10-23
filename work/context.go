package work

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// Context HUB
// Context HUB - Lớp vỏ bọc để JS code nhìn quen thuộc
type Context struct {
	request *Request
}

func (c *Context) router() *Router { return c.request.router }
func (c *Context) tenant() *Tenant { return c.router().tenant }

func (c *Context) Request() *Request { return c.request }
func (c *Context) Response() *Response {
	response := c.router().response
	if response.ctx == nil {
		response.ctx = c // its head methods (.title(), .image()) write to this request's view
	}
	return response
}

func (c *Context) Req() *Request  { return c.Request() }
func (c *Context) Res() *Response { return c.Response() }

// --- SHORTCUTS CHO JAVASCRIPT (Fluid API) ---

// The shortcuts are the request's one response's: context.json(data) is context.response.json(data),
// context.status(201) is context.response.status(201), the same response and the same order
// (response_order.go).
func (c *Context) JSON(v value.Value, options ...any) (*Response, error) {
	return c.Response().JSON(v, options...)
}
func (c *Context) HTML(v value.Value, options ...any) (*Response, error) {
	return c.Response().HTML(v, options...)
}
func (c *Context) Text(v value.Value, options ...any) (*Response, error) {
	return c.Response().Text(v, options...)
}
func (c *Context) SVG(v value.Value, options ...any) (*Response, error) {
	return c.Response().SVG(v, options...)
}
func (c *Context) CSS(v value.Value, options ...any) *Response {
	return c.Response().CSS(v, options...)
}
func (c *Context) Status(code int) (*Response, error) { return c.Response().Status(code) }
func (c *Context) Redirect(url string, options ...any) (*Response, error) {
	return c.Response().Redirect(value.New(url), options...)
}

// File answers with a file of kitwork/file, served from the disk (context.file(file.open("exports/a.csv")),
// KITWORK_FILE.md mục 9), or with bytes the handler made (context.type("image/png").file(bytes)).
func (c *Context) File(data value.Value, options ...any) (*Response, error) {
	return c.Response().File(data, options...)
}

// Error answers the request with an error of the site's: its .error() hook, or in router.work the
// nearest error.ui with 500 (answerUIPage).
func (c *Context) Error(v value.Value) (*Response, error) {
	response := c.Response()
	if err := response.answering("error"); err != nil {
		return response, err
	}
	c.request.router.err = fmt.Errorf("%s", v.String())
	response.uiPage, response.uiMessage = uiErrorPage, errorMessage(v)
	return response, nil
}

func (c *Context) Params(key string) value.Value { return c.request.Params(key) }
func (c *Context) Query(key string) value.Value  { return c.request.Query(key) }

func (c *Context) Path() value.Value     { return c.request.Path() }
func (c *Context) Method() value.Value   { return c.request.Method() }
func (c *Context) Host() value.Value     { return c.request.Host() }
func (c *Context) IP() value.Value       { return c.request.IP() }
func (c *Context) Body() value.Value     { return c.request.Body() }
func (c *Context) JSONBody() value.Value { return c.request.JSON() }
func (c *Context) JSONBodyLimit(limit int) value.Value {
	return c.request.JSONLimit(limit)
}

// Header reads a header of the request (context.header("Authorization")), or, with a value, sets one
// on the response, an edit before the answer (context.header("X-A", "1").json(data)).
func (c *Context) Header(k string, v ...value.Value) (any, error) {
	if len(v) == 0 {
		return c.request.Header(k), nil
	}
	return c.Response().Header(k, v[0].Text())
}

// Headers reads the request's headers, or, given an object, sets each on the response, an edit
// before the answer (context.headers({ "Cache-Control": "no-store" }).json(data)).
func (c *Context) Headers(v ...value.Value) (any, error) {
	if len(v) == 0 {
		return c.request.Headers(), nil
	}
	return c.Response().Headers(v[0])
}
func (c *Context) UserAgent() value.Value { return c.request.UserAgent() }
func (c *Context) IsAJAX() value.Value    { return c.request.IsAJAX() }
func (c *Context) IsJSON() value.Value    { return c.request.IsJSON() }

// Cookie is the context-level cookie shorthand:
//
//	ctx.cookie("name")                 read
//	ctx.cookie("name", value)          set with defaults
//	ctx.cookie("name", value, options) set with options
//	ctx.cookie("name", null, options)  delete
//
// Request.Cookie remains read-only. SetCookie and DeleteCookie stay as explicit aliases.
//
// In router.work setting one is an edit of the response, before the answer, and gives the response
// to go on with: context.cookie("seen", "1").json(data).
func (c *Context) Cookie(name string, args ...value.Value) (any, error) {
	if len(args) == 0 {
		return c.request.Cookie(name), nil
	}
	response := c.Response()
	if response.inUISite() {
		return response.Cookie(name, args...)
	}
	data := args[0]
	options := args[1:]
	if data.K == value.Nil {
		response.DeleteCookie(name, options...)
		return value.NewNil(), nil
	}
	response.SetCookie(name, data, options...)
	return data, nil
}

func (c *Context) Cookies() value.Value { return c.request.Cookies() }
func (c *Context) SetCookie(name string, data value.Value, options ...value.Value) *Response {
	return c.Response().SetCookie(name, data, options...)
}
func (c *Context) DeleteCookie(name string, options ...value.Value) *Response {
	return c.Response().DeleteCookie(name, options...)
}
func (c *Context) Hostname() value.Value    { return c.request.Hostname() }
func (c *Context) Secure() value.Value      { return c.request.Secure() }
func (c *Context) OriginalURL() value.Value { return c.request.OriginalURL() }
func (c *Context) URL() value.Value         { return c.request.URL() }
func (c *Context) Href() value.Value        { return c.request.Href() }
func (c *Context) URI() value.Value         { return c.request.URI() }
func (c *Context) Pattern() value.Value     { return c.request.Pattern() }
func (c *Context) Route() value.Value       { return c.request.Route() }

func (c *Context) arguments(lambda *value.Lambda) []value.Value {
	if lambda == nil {
		return nil
	}
	ctxVal := value.New(c)
	args := make([]value.Value, 0, len(lambda.Params))

	for _, name := range lambda.Params {
		if compiler.DestructuredEntries(name) != nil {
			// ({ request, response }) => …: the parameter is the context, taken apart in the body
			args = append(args, ctxVal)
			continue
		}
		lower := strings.ToLower(name)
		switch lower {
		case "ctx", "context":
			args = append(args, ctxVal)
		case "req", "request":
			args = append(args, value.New(c.Request()))
		case "res", "response":
			args = append(args, value.New(c.Response()))
		case "err", "error", "e":
			args = append(args, c.router().errorObject())
		default:
			args = append(args, value.Value{K: value.Nil})
		}
	}
	return args
}
