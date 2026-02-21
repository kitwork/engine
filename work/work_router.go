package work

import (
	"fmt"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

// StaticRoute defines a single API endpoint or view path
type StaticRoute struct {
	Method         string
	Path           string
	HandledBy      *Work
	Fn             *value.Script
	Template       *Template
	Redirect       *Redirect
	Handler        *value.Script
	IsRaw          bool
	BenchmarkIters int // Nếu > 0, chạy chế độ Benchmark
	IsJIT          bool
	CacheDuration  time.Duration
	Middlewares    []*value.Script
}

// RouterCore stores the networking state
type RouterCore struct {
	Routes            []*StaticRoute
	LastRoute         *StaticRoute
	Prefix            string
	GlobalMiddlewares []*value.Script
}

// Router registers a generic route
// Kept on Work for backward compatibility and config loading
func (w *Work) Router(args ...value.Value) *Work {
	if len(args) < 2 {
		return w
	}
	method := strings.ToUpper(args[0].Text())
	path := args[1].Text()

	// Apply route prefix if set
	if w.CoreRouter.Prefix != "" {
		if strings.HasSuffix(w.CoreRouter.Prefix, "/") && strings.HasPrefix(path, "/") {
			path = w.CoreRouter.Prefix + path[1:]
		} else if !strings.HasSuffix(w.CoreRouter.Prefix, "/") && !strings.HasPrefix(path, "/") {
			path = w.CoreRouter.Prefix + "/" + path
		} else {
			path = w.CoreRouter.Prefix + path
		}
	}

	fmt.Printf("[Router] %s: Called for %s %s\n", w.Name, method, path)
	// Check if route already exists
	for i, r := range w.CoreRouter.Routes {
		if r.Method == method && r.Path == path {
			fmt.Printf("[Router] %s: Route exists, moving to end\n", w.Name)
			// Move existing route to the end so .handle() can update it
			rObj := w.CoreRouter.Routes[i]
			w.CoreRouter.Routes = append(w.CoreRouter.Routes[:i], w.CoreRouter.Routes[i+1:]...) // Remove
			w.CoreRouter.Routes = append(w.CoreRouter.Routes, rObj)                             // Re-add at end
			w.CoreRouter.LastRoute = rObj                                                       // Track
			return w
		}
	}
	// Route doesn't exist, add new one
	fmt.Printf("[Router] %s: Adding new route\n", w.Name)
	newRoute := &StaticRoute{Method: method, Path: path}
	w.CoreRouter.Routes = append(w.CoreRouter.Routes, newRoute)
	w.CoreRouter.LastRoute = newRoute // Track
	return w
}

func (w *Work) Get(args ...value.Value) *Work    { return w.routerWithHandler("GET", args...) }
func (w *Work) Post(args ...value.Value) *Work   { return w.routerWithHandler("POST", args...) }
func (w *Work) Put(args ...value.Value) *Work    { return w.routerWithHandler("PUT", args...) }
func (w *Work) Delete(args ...value.Value) *Work { return w.routerWithHandler("DELETE", args...) }

func (w *Work) routerWithHandler(method string, args ...value.Value) *Work {
	if len(args) == 0 {
		return w
	}
	path := args[0].Text()
	w.Router(value.New(method), value.New(path))
	if len(args) > 1 {
		w.Handle(args[1])
	}
	return w
}

func (w *Work) JITCSS(args ...value.Value) *Work {
	if len(args) == 0 {
		return w
	}
	path := args[0].Text()
	w.Router(value.New("GET"), value.New(path))

	// Set IsJIT on the newly added (last) route
	if len(w.CoreRouter.Routes) > 0 {
		last := w.CoreRouter.Routes[len(w.CoreRouter.Routes)-1]
		last.IsJIT = true
	}

	if len(args) > 1 {
		w.Handle(args[1])
	}
	return w
}

// Benchmark configures the last added route to run in benchmark mode
func (w *Work) Benchmark(args ...value.Value) *Work {
	if w.CoreRouter.LastRoute == nil {
		fmt.Println("⚠️ .benchmark() called but no route was added previously")
		return w
	}
	iters := 1000
	if len(args) > 0 {
		iters = int(args[0].N)
	}
	w.CoreRouter.LastRoute.BenchmarkIters = iters
	return w
}

// Prefix sets a standard path prefix for all subsequent routes
func (w *Work) Prefix(args ...value.Value) *Work {
	if len(args) > 0 {
		w.CoreRouter.Prefix = args[0].Text()
	}
	return w
}

// Group is an alias for Prefix in this fluent chained API
func (w *Work) Group(args ...value.Value) *Work {
	return w.Prefix(args...)
}

// Use adds middleware either to the last added route or globally if no route has been set
func (w *Work) Use(args ...value.Value) *Work {
	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			if w.CoreRouter.LastRoute != nil {
				w.CoreRouter.LastRoute.Middlewares = append(w.CoreRouter.LastRoute.Middlewares, sFn)
				fmt.Printf("[Middleware] Added to route %s %s\n", w.CoreRouter.LastRoute.Method, w.CoreRouter.LastRoute.Path)
			} else {
				w.CoreRouter.GlobalMiddlewares = append(w.CoreRouter.GlobalMiddlewares, sFn)
				fmt.Printf("[Middleware] Added globally to Work %s\n", w.Name)
			}
		}
	}
	return w
}

// UseGlobal explicitly adds a middleware to the global work unit instead of the route
func (w *Work) UseGlobal(args ...value.Value) *Work {
	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			w.CoreRouter.GlobalMiddlewares = append(w.CoreRouter.GlobalMiddlewares, sFn)
			fmt.Printf("[Middleware] Added globally to Work %s\n", w.Name)
		}
	}
	return w
}
