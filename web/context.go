package web

import (
	"net/http"

	"github.com/kitwork/engine/value"
)

type Context struct {
	Request  *http.Request
	Response http.ResponseWriter
	Params   map[string]string
}

func NewContext(w http.ResponseWriter, req *http.Request) *Context {
	return &Context{
		Request:  req,
		Response: w,
		Params:   make(map[string]string),
	}
}

func (c *Context) Param(key string) string {
	return c.Params[key]
}

func (c *Context) Query(key string) string {
	if c.Request == nil || c.Request.URL == nil {
		return ""
	}
	return c.Request.URL.Query().Get(key)
}

func (c *Context) JSON(status int, v any) value.Value {
	if c.Response != nil {
		c.Response.Header().Set("Content-Type", "application/json")
		c.Response.WriteHeader(status)
	}
	return value.New(v)
}
