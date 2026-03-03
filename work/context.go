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
func (c *Context) Status(code int) *Response       { return c.Response().Status(code) }
func (c *Context) Redirect(url string, code ...int) {
	c.Response().Redirect(value.New(url), code...)
}
func (c *Context) File(path string, code ...int) {
	c.Response().File(c.tenant().joinPath(path), code...)
}

func (c *Context) Error(v value.Value) {
	c.request.router.err = fmt.Errorf("%s", v.String())
}

func (c *Context) Param(key string) value.Value { return c.request.Param(key) }
func (c *Context) Query(key string) value.Value { return c.request.Query(key) }

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

func (c *Context) Cookie(name string) value.Value          { return c.request.Cookie(name) }
func (c *Context) Cookies() value.Value                    { return c.request.Cookies() }
func (c *Context) Hostname() value.Value                   { return c.request.Hostname() }
func (c *Context) Secure() value.Value                     { return c.request.Secure() }
func (c *Context) OriginalURL() value.Value                { return c.request.OriginalURL() }
func (c *Context) FullURL() value.Value                    { return c.request.FullURL() }
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
		default:
			args = append(args, value.Value{K: value.Nil})
		}
	}
	return args
}
