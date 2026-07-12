package work

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/value"
)

// Context HUB
// Context HUB - Lớp vỏ bọc để JS code nhìn quen thuộc
type Context struct {
	request *Request
}

func (c *Context) router() *Router { return c.request.router }
func (c *Context) tenant() *Tenant { return c.router().tenant }

func (c *Context) Request() *Request   { return c.request }
func (c *Context) Response() *Response { return c.router().response }

func (c *Context) Req() *Request  { return c.Request() }
func (c *Context) Res() *Response { return c.Response() }

// --- SHORTCUTS CHO JAVASCRIPT (Fluid API) ---

func (c *Context) JSON(v value.Value, code ...int) { c.Response().JSON(v, code...) }
func (c *Context) HTML(v value.Value, code ...int) { c.Response().HTML(v, code...) }
func (c *Context) Text(v value.Value, code ...int) { c.Response().Text(v, code...) }
func (c *Context) CSS(v value.Value, code ...int)  { c.Response().CSS(v, code...) }
func (c *Context) Status(code int) *Response       { return c.Response().Status(code) }
func (c *Context) Type(mediaType string) *Response { return c.Response().Type(mediaType) }
func (c *Context) Redirect(url string, code ...int) {
	c.Response().Redirect(value.New(url), code...)
}
func (c *Context) File(path string, code ...int) {
	c.Response().File(c.tenant().resolve(path), code...)
}

func (c *Context) Error(v value.Value) {
	c.request.router.err = fmt.Errorf("%s", v.String())
}

func (c *Context) Params(key string) value.Value { return c.request.Params(key) }
func (c *Context) Query(key string) value.Value  { return c.request.Query(key) }

func (c *Context) Path() value.Value           { return c.request.Path() }
func (c *Context) Method() value.Value         { return c.request.Method() }
func (c *Context) Host() value.Value           { return c.request.Host() }
func (c *Context) IP() value.Value             { return c.request.IP() }
func (c *Context) Body() value.Value           { return c.request.Body() }
func (c *Context) JSONBody() value.Value       { return c.request.JSON() }
func (c *Context) Header(k string) value.Value { return c.request.Header(k) }
func (c *Context) Headers() value.Value        { return c.request.Headers() }
func (c *Context) UserAgent() value.Value      { return c.request.UserAgent() }
func (c *Context) IsAJAX() value.Value         { return c.request.IsAJAX() }
func (c *Context) IsJSON() value.Value         { return c.request.IsJSON() }

// Cookie is the context-level cookie shorthand:
//
//	ctx.cookie("name")                 read
//	ctx.cookie("name", value)          set with defaults
//	ctx.cookie("name", value, options) set with options
//	ctx.cookie("name", null, options)  delete
//
// Request.Cookie remains read-only. SetCookie and DeleteCookie stay as explicit aliases.
func (c *Context) Cookie(name string, args ...value.Value) value.Value {
	if len(args) == 0 {
		return c.request.Cookie(name)
	}
	data := args[0]
	options := args[1:]
	if data.K == value.Nil {
		c.Response().DeleteCookie(name, options...)
		return value.NewNil()
	}
	c.Response().SetCookie(name, data, options...)
	return data
}

func (c *Context) Cookies() value.Value { return c.request.Cookies() }
func (c *Context) SetCookie(name string, data value.Value, options ...value.Value) *Response {
	return c.Response().SetCookie(name, data, options...)
}
func (c *Context) DeleteCookie(name string, options ...value.Value) *Response {
	return c.Response().DeleteCookie(name, options...)
}
func (c *Context) Hostname() value.Value                   { return c.request.Hostname() }
func (c *Context) Secure() value.Value                     { return c.request.Secure() }
func (c *Context) OriginalURL() value.Value                { return c.request.OriginalURL() }
func (c *Context) URL() value.Value                        { return c.request.URL() }
func (c *Context) Href() value.Value                       { return c.request.Href() }
func (c *Context) URI() value.Value                        { return c.request.URI() }
func (c *Context) Pattern() value.Value                    { return c.request.Pattern() }
func (c *Context) Route() value.Value                      { return c.request.Route() }
func (c *Context) SaveFile(field, dest string) value.Value { return c.request.SaveFile(field, dest) }

func (c *Context) arguments(lambda *value.Lambda) []value.Value {
	if lambda == nil {
		return nil
	}
	ctxVal := value.New(c)
	args := make([]value.Value, 0, len(lambda.Params))

	for _, name := range lambda.Params {
		lower := strings.ToLower(name)
		switch lower {
		case "ctx", "context":
			args = append(args, ctxVal)
		case "req", "request":
			args = append(args, value.New(c.Request()))
		case "res", "response":
			args = append(args, value.New(c.Response()))
		case "sse":
			args = append(args, value.New(&SseHelper{tenant: c.tenant(), context: c}))
		case "err", "error", "e":
			if c.router().err != nil {
				args = append(args, value.New(c.router().err.Error()))
			} else {
				args = append(args, value.Value{K: value.Nil})
			}
		default:
			args = append(args, value.Value{K: value.Nil})
		}
	}
	return args
}
