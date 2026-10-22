package http

import (
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// Module is what `import * as http from "kitwork/http"` gives (KITWORK_HTTP.md, 22/10/2026): one
// function per method, each returning the description of a request (Request) and touching no network.
// Modifiers belong to the request, after the method; .send() is the only place a request goes out.
type Module struct {
	ram, disk ResponseStore
	// selfHost is the Host a self-call ("/path") carries: the site's own domain, so the call reaches
	// the site that made it rather than whichever site answers for 127.0.0.1.
	selfHost string
}

// NewModule is the http module of one site: its RAM and disk tiers (either may be nil) and its domain.
func NewModule(ram, disk ResponseStore, selfHost string) *Module {
	return &Module{ram: ram, disk: disk, selfHost: selfHost}
}

func (m *Module) Get(args ...value.Value) value.Value     { return m.describe("GET", args) }
func (m *Module) Post(args ...value.Value) value.Value    { return m.describe("POST", args) }
func (m *Module) Put(args ...value.Value) value.Value     { return m.describe("PUT", args) }
func (m *Module) Patch(args ...value.Value) value.Value   { return m.describe("PATCH", args) }
func (m *Module) Delete(args ...value.Value) value.Value  { return m.describe("DELETE", args) }
func (m *Module) Head(args ...value.Value) value.Value    { return m.describe("HEAD", args) }
func (m *Module) Options(args ...value.Value) value.Value { return m.describe("OPTIONS", args) }

// describe is http.<method>(url): one URL, nothing else. The body goes in .send(data), URL parameters in
// .query(), headers in .header(); the old second argument of http.post(url, body) is an error naming that.
func (m *Module) describe(method string, args []value.Value) value.Value {
	name := strings.ToLower(method)
	if len(args) == 0 || args[0].K != value.String {
		return value.InvalidFailure(value.InvalidArgumentCode,
			fmt.Sprintf("http.%s cần một URL: http.%s(\"https://…\") hay http.%s(\"/đường-dẫn\")", name, name, name))
	}
	if len(args) > 1 {
		if bodyless(method) || method == "OPTIONS" {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
				"http.%s nhận đúng một URL: tham số URL viết bằng .query({ … }), header bằng .header(tên, giá_trị)", name))
		}
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
			"http.%s nhận đúng một URL; thân đi trong .send(data): http.%s(url).send(data)", name, name))
	}
	target := strings.TrimSpace(args[0].String())
	if !validTarget(target) {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
			"http.%s: %q không phải URL http(s) hay đường dẫn bắt đầu bằng /", name, shownURL(target)))
	}
	return value.New(newRequest(m, method, target, defaultMaxBytes))
}

// validTarget is an absolute http(s) URL with a host, or a path of this site ("/x", never "//host").
func validTarget(target string) bool {
	if strings.HasPrefix(target, "/") {
		if strings.HasPrefix(target, "//") || strings.HasPrefix(target, "/\\") {
			return false
		}
		_, err := neturl.ParseRequestURI(target)
		return err == nil
	}
	parsed, err := neturl.Parse(target)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// MissingMember turns the module's old spellings into errors naming the new one: modifiers come after
// the method (http.cache("5m").get(url) was the old way), and .send() ends the chain.
func (m *Module) MissingMember(name string) value.Value {
	switch strings.ToLower(name) {
	case "cache", "persist", "retry", "timeout", "header", "headers", "maxbytes", "query":
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
			"http.%s(…) đã bỏ: bổ nghĩa đứng sau method, viết http.get(url).%s(…).send()", name, name))
	case "send":
		return value.InvalidFailure(value.InvalidArgumentCode,
			"http.send không có: method trước, .send() ở cuối: http.get(url).send()")
	case "fetch", "request", "execute":
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
			"http.%s không có: viết http.get(url).send()", name))
	}
	return value.Value{K: value.Nil}
}

// bodyless is a method whose request carries no body: GET and HEAD.
func bodyless(method string) bool { return method == "GET" || method == "HEAD" }

// written is v as a program writes it in a message: a string quoted, anything else as its text.
func written(v value.Value) string {
	if v.K == value.String {
		return strconv.Quote(v.String())
	}
	if v.K == value.Nil {
		return "null"
	}
	return v.Text()
}
