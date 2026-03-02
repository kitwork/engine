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

// --- SHORTCUTS CHO JAVASCRIPT (Fluid API) ---

func (c *Context) JSON(v value.Value, code ...int) *Context { c.Response().JSON(v, code...); return c }
func (c *Context) HTML(v value.Value, code ...int) *Context { c.Response().HTML(v, code...); return c }
func (c *Context) Text(v value.Value, code ...int) *Context { c.Response().Text(v, code...); return c }
func (c *Context) Status(code int) *Context                 { c.Response().Status(code); return c }
func (c *Context) Redirect(url string, code ...int) *Context {
	c.Response().Redirect(value.New(url), code...)
	return c
}
func (c *Context) File(path string, code ...int) *Context {
	c.Response().File(c.tenant().joinPath(path), code...)
	return c
}

func (c *Context) Abort(code int, message ...string) *Context {
	msg := "Operation Aborted"
	if len(message) > 0 {
		msg = message[0]
	}
	c.Response().ErrorString(msg, code)
	return c
}

func (c *Context) Error(v value.Value) *Context {
	c.request.router.err = fmt.Errorf("%s", v.String())
	return c
}

func (c *Context) GetError() value.Value {
	if c.request.router.err == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(c.request.router.err.Error())
}

func (c *Context) Param(key string) value.Value { return c.request.Param(key) }
func (c *Context) Query(key string) value.Value { return c.request.Query(key) }

func (c *Context) argsLambda(lambda *value.Lambda) []value.Value {
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
