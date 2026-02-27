package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

func newExecutionContext(e *Engine) *ExecutionContext {
	ctx := &ExecutionContext{
		machine:    runtime.New(nil, nil),
		task:       &work.Task{},
		reqCtx:     &work.Request{},
		argsBuffer: make([]value.Value, 0, 10),
	}

	// Map Builtins to Context
	ctx.jsonFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) > 0 {
			ctx.task.JSON(args[0])
		}
		return value.New(ctx.task)
	})
	ctx.htmlFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) >= 1 {
			ctx.task.HTML(args[0], args[1:]...)
		}
		return value.New(ctx.task)
	})

	// Parallel implementation
	ctx.parallelFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.NewNull()
		}
		arg := args[0]
		if e.Config.Debug {
			fmt.Printf("[Parallel Debug] Input Type: %s, IsMap: %v\n", arg.K.String(), arg.IsMap())
		}

		if arg.IsArray() {
			arr := arg.Array()
			results := make([]value.Value, len(arr))
			var wg sync.WaitGroup
			for i, v := range arr {
				if sFn, ok := v.V.(*value.Script); ok {
					wg.Add(1)
					go func(idx int, fn *value.Script) {
						defer wg.Done()
						r := e.ExecuteLambda(ctx.task.Work, fn, ctx.reqCtx.Request, ctx.reqCtx.Writer, nil)
						results[idx] = r.Value
					}(i, sFn)
				} else {
					results[i] = v
				}
			}
			wg.Wait()
			return value.New(results)
		} else if arg.IsMap() {
			m := arg.Map()
			results := make(map[string]value.Value)

			// Tạm thời chạy TUẦN TỰ để đảm bảo Memory Safety cho các Object phức tạp
			for k, v := range m {
				if sFn, ok := v.V.(*value.Script); ok {
					r := e.ExecuteLambda(ctx.task.Work, sFn, ctx.reqCtx.Request, ctx.reqCtx.Writer, nil)
					results[k] = r.Value
				} else {
					results[k] = v
				}
			}

			return value.New(results)
		}
		return value.NewNull()
	})

	ctx.goFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) > 0 {
			if sFn, ok := args[0].V.(*value.Script); ok {
				go e.ExecuteLambda(ctx.task.Work, sFn, ctx.reqCtx.Request, ctx.reqCtx.Writer, nil)
			}
		}
		return value.NewNull()
	})

	ctx.deferFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) > 0 {
			if sFn, ok := args[0].V.(*value.Script); ok {
				ctx.machine.Defer(sFn)
			}
		}
		return value.NewNull()
	})

	ctx.doneFn = value.NewFunc(func(args ...value.Value) value.Value {
		ctx.task.Done(args...)
		ctx.machine.Stop()
		return value.NewNull()
	})

	ctx.failFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) > 0 {
			ctx.task.Fail(args[0])
		}
		ctx.machine.Stop()
		return value.NewNull()
	})

	// engine object for chaining
	var runtimeObj map[string]value.Value
	runtimeObj = map[string]value.Value{
		"source": value.NewFunc(func(args ...value.Value) value.Value {
			for _, arg := range args {
				e.Config.Sources = append(e.Config.Sources, arg.Text())
			}
			return value.New(runtimeObj)
		}),
		"debug": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 {
				e.Config.Debug = args[0].IsTrue()
			}
			return value.New(runtimeObj)
		}),
	}

	ctx.engineFn = value.New(map[string]value.Value{
		"run": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 {
				arg := args[0]
				if arg.IsMap() {
					cfg, _ := arg.Interface().(map[string]any)
					if p, ok := cfg["port"].(float64); ok {
						e.Config.Port = int(p)
					}
					if d, ok := cfg["debug"].(bool); ok {
						e.Config.Debug = d
					}
					if s, ok := cfg["source"].(string); ok {
						e.Config.Sources = append(e.Config.Sources, s)
					}
					if ss, ok := cfg["source"].([]any); ok {
						for _, s := range ss {
							if str, ok := s.(string); ok {
								e.Config.Sources = append(e.Config.Sources, str)
							}
						}
					}
				} else if arg.IsNumeric() {
					e.Config.Port = int(arg.N)
				}
			}
			return value.New(runtimeObj)
		}),
	})

	ctx.nowFn = value.NewFunc(func(args ...value.Value) value.Value { return ctx.task.Now() })

	// industrial db proxy: hỗ trợ cả db() và db.from()
	dbHandler := &dbProxyHandler{ec: ctx}
	ctx.dbFn = value.Value{K: value.Proxy, V: dbHandler}

	ctx.payloadFn = value.NewFunc(func(args ...value.Value) value.Value { return ctx.reqCtx.Payload() })
	ctx.logFn = value.NewFunc(func(args ...value.Value) value.Value {
		ctx.task.Log(args...)
		return value.NewNull()
	})
	ctx.fetchFn = value.NewFunc(func(args ...value.Value) value.Value { return value.New(ctx.task.Fetch()) })
	ctx.cacheFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.NewNull()
		}
		key := args[0].Text()

		// Pattern: cache(key) -> Get only
		if len(args) == 1 {
			if val, ok := work.GetCache(key); ok {
				return val
			}
			return value.NewNull()
		}

		// Pattern: cache(key, value, ttl) OR cache(key, ttl, callback)
		if len(args) >= 3 {
			arg1 := args[1]
			arg2 := args[2]

			// Helper to parse TTL
			parseTTL := func(v value.Value) time.Duration {
				var d time.Duration
				switch {
				case v.IsNumeric():
					d = time.Duration(v.Float()) * time.Second
				case v.IsString():
					str := v.Text()
					if strings.HasSuffix(str, "d") {
						if days, err := strconv.Atoi(strings.TrimSuffix(str, "d")); err == nil {
							d = time.Duration(days) * 24 * time.Hour
						}
					} else {
						if parsed, err := time.ParseDuration(str); err == nil {
							d = parsed
						}
					}
				}
				return d
			}

			// Case A: cache(key, ttl, callback)
			if arg2.IsCallable() {
				if val, ok := work.GetCache(key); ok {
					return val
				}
				if sFn, ok := arg2.V.(*value.Script); ok {
					res := e.ExecuteLambda(ctx.task.Work, sFn, ctx.reqCtx.Request, ctx.reqCtx.Writer, nil)
					ttl := parseTTL(arg1)
					if ttl > 0 {
						work.SetCache(key, res.Value, ttl)
					}
					return res.Value
				}
			} else {
				// Case B: cache(key, value, ttl)
				ttl := parseTTL(arg2)
				if ttl > 0 {
					work.SetCache(key, arg1, ttl)
				}
				return arg1
			}
		}

		return value.NewNull()
	})

	ctx.queryFn = value.NewFunc(func(args ...value.Value) value.Value {
		if ctx.reqCtx.Request == nil {
			return value.NewNull()
		}

		// Always get from Request (Go caches URL.Query anyway)
		r := ctx.reqCtx.Request
		if len(args) == 0 {
			res := make(map[string]value.Value)
			for k, v := range r.URL.Query() {
				if len(v) > 0 {
					res[k] = value.New(v[0])
				}
			}
			return value.New(res)
		}
		key := args[0].Text()
		return value.New(r.URL.Query().Get(key))
	})

	type contextKey string
	const bodyCacheKey contextKey = "kitwork_body"

	ctx.bodyFn = value.NewFunc(func(args ...value.Value) value.Value {
		// Priority 1: Check pre-parsed body in reqCtx
		if ctx.reqCtx.Body.K == value.Map {
			if len(args) == 0 {
				return ctx.reqCtx.Body
			}
			return ctx.reqCtx.Body.Get(args[0].Text())
		}

		if ctx.reqCtx.Request == nil {
			return value.NewNull()
		}

		// Priority 2: Try to get from Request Context cache
		if cached := ctx.reqCtx.Request.Context().Value(bodyCacheKey); cached != nil {
			b := cached.(value.Value)
			if len(args) == 0 {
				return b
			}
			return b.Get(args[0].Text())
		}

		// Parse body ONCE and store back into Request Context
		if ctx.reqCtx.Request.Body != nil {
			var bodyData map[string]any
			json.NewDecoder(ctx.reqCtx.Request.Body).Decode(&bodyData)
			res := make(map[string]value.Value)
			for k, v := range bodyData {
				res[k] = value.New(v)
			}
			bodyVal := value.New(res)

			// Store in context for next time
			importCtx := context.WithValue(ctx.reqCtx.Request.Context(), bodyCacheKey, bodyVal)
			ctx.reqCtx.Request = ctx.reqCtx.Request.WithContext(importCtx)

			if len(args) == 0 {
				return bodyVal
			}
			return bodyVal.Get(args[0].Text())
		}

		return value.NewNull()
	})

	ctx.paramsFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.New(ctx.reqCtx.Params)
		}
		key := args[0].Text()
		if val, ok := ctx.reqCtx.Params[key]; ok {
			return val
		}
		return value.NewNull()
	})

	ctx.cookieFn = value.NewFunc(func(args ...value.Value) value.Value {
		// GET Cookie
		if len(args) == 1 {
			if ctx.reqCtx.Request != nil {
				c, err := ctx.reqCtx.Request.Cookie(args[0].Text())
				if err == nil {
					return value.New(c.Value)
				}
			}
			return value.NewNull()
		}

		// SET Cookie
		if len(args) >= 2 && ctx.reqCtx.Writer != nil {
			name := args[0].Text()
			val := args[1].Text()
			c := &http.Cookie{
				Name:  name,
				Value: val,
				Path:  "/", // Default path
			}

			// Options handling
			if len(args) > 2 && args[2].IsMap() {
				opts := args[2].Map()
				if v, ok := opts["path"]; ok {
					c.Path = v.Text()
				}
				if v, ok := opts["domain"]; ok {
					c.Domain = v.Text()
				}
				if v, ok := opts["maxAge"]; ok {
					c.MaxAge = int(v.N)
				}
				if v, ok := opts["secure"]; ok {
					c.Secure = v.Truthy()
				}
				if v, ok := opts["httpOnly"]; ok {
					c.HttpOnly = v.Truthy()
				}
			}
			http.SetCookie(ctx.reqCtx.Writer, c)
		}
		return value.NewNull()
	})

	var responseObj map[string]value.Value
	responseObj = map[string]value.Value{
		"status": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 {
				ctx.task.Status(int(args[0].N))
			}
			return value.New(responseObj)
		}),
		"header": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) >= 2 {
				ctx.task.Header(args[0].Text(), args[1].Text())
			}
			return value.New(responseObj)
		}),
		"redirect": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 && ctx.reqCtx.Request != nil && ctx.reqCtx.Writer != nil {
				url := args[0].Text()
				code := http.StatusFound
				if len(args) > 1 {
					code = int(args[1].N)
				}
				http.Redirect(ctx.reqCtx.Writer, ctx.reqCtx.Request, url, code)
			}
			return value.New(responseObj)
		}),
		"json":   ctx.jsonFn,
		"html":   ctx.htmlFn,
		"cookie": ctx.cookieFn,
	}
	ctx.machine.Vars["response"] = value.New(responseObj)

	requestHeaderFn := value.NewFunc(func(args ...value.Value) value.Value {
		if r := ctx.reqCtx.Request; r != nil {
			if len(args) == 0 {
				res := make(map[string]value.Value)
				for k, v := range r.Header {
					if len(v) > 0 {
						res[k] = value.New(v[0])
					}
				}
				return value.New(res)
			}
			key := args[0].Text()
			return value.New(r.Header.Get(key))
		}
		return value.NewNull()
	})

	ctx.machine.Vars["request"] = value.New(map[string]value.Value{
		"header": requestHeaderFn,
		"query":  ctx.queryFn,
		"body":   ctx.bodyFn,
		"params": ctx.paramsFn,
		"cookie": ctx.cookieFn,
	})

	// Legacy aliases
	ctx.machine.Vars["status"] = responseObj["status"]
	ctx.machine.Vars["redirect"] = responseObj["redirect"]
	ctx.machine.Vars["header"] = requestHeaderFn

	ctx.workFn = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.NewNull()
		}
		name := args[0].Text()
		if e.Config.Debug {
			fmt.Printf("[Trigger work()] Called for: %s\n", name)
		}
		if w, ok := e.Registry[name]; ok {
			return value.New(w)
		}
		w := work.New(name, "", "", "")
		e.Registry[name] = w
		return value.New(w)
	})

	// Pre-inject into machine
	ctx.machine.Vars["json"] = ctx.jsonFn
	ctx.machine.Vars["html"] = ctx.htmlFn
	ctx.machine.Vars["query"] = ctx.queryFn
	ctx.machine.Vars["body"] = ctx.bodyFn
	ctx.machine.Vars["params"] = ctx.paramsFn
	ctx.machine.Vars["payload"] = ctx.payloadFn
	ctx.machine.Vars["work"] = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.NewNull()
		}
		name := args[0].Text()

		// 1. Exact match
		if w, ok := e.Registry[name]; ok {
			return value.New(w)
		}

		// 2. Case-insensitive match
		for k, w := range e.Registry {
			if strings.EqualFold(k, name) {
				return value.New(w)
			}
		}

		w := work.New(name, "", "", "")
		e.Registry[name] = w
		return value.New(w)
	})
	ctx.machine.Vars["cookie"] = ctx.cookieFn
	ctx.machine.Vars["engine"] = ctx.engineFn
	ctx.machine.Vars["now"] = ctx.nowFn
	// Wrap core services into Smart Proxies (Go Template Style)
	ctx.machine.Vars["db"] = value.Value{K: value.Proxy, V: &dbProxyHandler{ec: ctx}}
	ctx.machine.Vars["log"] = value.Value{K: value.Proxy, V: &genericServiceProxy{fn: ctx.logFn}}
	ctx.machine.Vars["fetch"] = value.Value{K: value.Proxy, V: &genericServiceProxy{fn: ctx.fetchFn}}
	ctx.machine.Vars["cache"] = value.Value{K: value.Proxy, V: &genericServiceProxy{fn: ctx.cacheFn}}
	ctx.machine.Vars["done"] = ctx.doneFn
	ctx.machine.Vars["fail"] = ctx.failFn

	ctx.machine.Vars["defer"] = ctx.deferFn
	ctx.machine.Vars["random"] = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.New(rand.Float64())
		}
		arg := args[0]
		// 1. Array: pick random item
		if arg.K == value.Array {
			ptr := arg.V.(*[]value.Value)
			arr := *ptr
			if len(arr) == 0 {
				return value.NewNull()
			}
			return arr[rand.Intn(len(arr))]
		}
		// 2. Number: random range
		if arg.K == value.Number {
			if len(args) == 1 {
				// random(max) -> 0..max-1
				if arg.N <= 0 {
					return value.New(0)
				}
				return value.New(float64(rand.Intn(int(arg.N))))
			}
			// random(min, max) -> min..max-1
			min := int(arg.N)
			max := int(args[1].N)
			if max <= min {
				return value.New(float64(min))
			}
			return value.New(float64(rand.Intn(max-min) + min))
		}
		return value.New(rand.Float64())
	})
	ctx.machine.Vars["readfile"] = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.NewNull()
		}
		path := args[0].Text()
		content, err := os.ReadFile(path)
		if err != nil {
			return value.NewNull()
		}
		return value.New(string(content))
	})

	return ctx
}

type dbProxyHandler struct {
	ec *ExecutionContext
}

func (h *dbProxyHandler) OnGet(key string) value.Value {
	// Start a fresh query and wrap it in a queryProxyHandler
	query := h.ec.task.DB()
	query.SetExecutor(h.ec.machine)

	handler := &queryProxyHandler{ec: h.ec, query: query}
	return handler.OnGet(key)
}

func (h *dbProxyHandler) OnCompare(op string, other value.Value) value.Value {
	return value.NewNull()
}

func (h *dbProxyHandler) OnInvoke(method string, args ...value.Value) value.Value {
	// 1. Direct call db() or db("conn")
	if method == "" {
		conn := ""
		if len(args) > 0 {
			conn = args[0].Text()
		}
		query := h.ec.task.DB(conn)
		query.SetExecutor(h.ec.machine)
		return value.Value{K: value.Proxy, V: &queryProxyHandler{ec: h.ec, query: query}}
	}

	// 2. Method call via proxy db.from("user")
	// Start a fresh query and delegate the method call
	query := h.ec.task.DB()
	query.SetExecutor(h.ec.machine)
	handler := &queryProxyHandler{ec: h.ec, query: query}
	return handler.OnInvoke(method, args...)
}

type queryProxyHandler struct {
	ec    *ExecutionContext
	query *work.DBQuery
}

func (h *queryProxyHandler) OnGet(key string) value.Value {
	lowerKey := strings.ToLower(key)
	// Special handling for entry-point methods to ensure they are always captured
	if lowerKey == "from" || lowerKey == "table" {
		return value.NewFunc(func(args ...value.Value) value.Value {
			return h.OnInvoke(lowerKey, args...)
		})
	}

	vQuery := value.New(h.query)
	// Priority 1: Check if it's a method on DBQuery via reflection
	attr := vQuery.Get(key)
	if attr.K != value.Func {
		// Auto-correct case: take -> Take
		attr = vQuery.Get(strings.Title(key))
	}
	if attr.K == value.Func {
		// Return a wrapped function that routes through OnInvoke
		return value.NewFunc(func(args ...value.Value) value.Value {
			return h.OnInvoke(key, args...)
		})
	}

	// Priority 2: If we are accessing a property (db.user), assume it's a table name
	if h.query.GetTable() == "" {
		h.query.Table(key)
	}

	// Always return the proxy to allow further chaining
	return value.Value{K: value.Proxy, V: h}
}

func (h *queryProxyHandler) OnInvoke(method string, args ...value.Value) value.Value {
	vQuery := value.New(h.query)

	lowerMethod := strings.ToLower(method)
	if lowerMethod == "from" || lowerMethod == "table" {
		if len(args) > 0 {
			h.query.Table(args[0].Text())
			return value.Value{K: value.Proxy, V: h}
		}
	}

	// Handle direct call on proxy: db.from("user") or db.table("user")
	if method == "" {
		// If table is still empty and we have a string arg, it's likely db.from("user") case
		if len(args) == 1 && h.query.GetTable() == "" {
			h.query.Table(args[0].Text())
			return value.Value{K: value.Proxy, V: h}
		}
		return vQuery
	}

	// Try Invoke directly
	res := vQuery.Invoke(method, args...)
	if res.K == value.Invalid || res.K == value.Nil {
		// Auto-correct case: take -> Take
		res = vQuery.Invoke(strings.Title(method), args...)
	}

	// If the result is a *DBQuery, wrap it back in a proxy for continuous chaining
	if res.K == value.Struct {
		if _, ok := res.V.(*work.DBQuery); ok {
			return value.Value{K: value.Proxy, V: h}
		}
	}

	return res
}

func (h *queryProxyHandler) OnCompare(op string, other value.Value) value.Value {
	return value.NewNull()
}

type genericServiceProxy struct {
	fn value.Value // The original service function (ctx.logFn, etc)
}

func (h *genericServiceProxy) OnGet(key string) value.Value {
	// Equivalent to service().key access
	serviceInstance := h.fn.Call("", []value.Value{}...)
	return serviceInstance.Get(key)
}

func (h *genericServiceProxy) OnInvoke(method string, args ...value.Value) value.Value {
	// If calling the proxy directly, call the underlying function
	if method == "" {
		return h.fn.Call("", args...)
	}
	// If calling a method (rare for the entry proxy), invoke on instance
	serviceInstance := h.fn.Call("", []value.Value{}...)
	return serviceInstance.Invoke(method, args...)
}

func (h *genericServiceProxy) OnCompare(op string, other value.Value) value.Value {
	return value.NewNull()
}
