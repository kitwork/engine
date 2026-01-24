package work

import (
	"fmt"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// StaticRoute đại diện cho một rule điều hướng tĩnh
type StaticRoute struct {
	Method  string
	Path    string
	Handler *value.ScriptFunction
}

// Work là Blueprint (Bản thiết kế) - IMMUTABLE
type Work struct {
	Name       string
	Routes     []*StaticRoute
	Retries    int
	TimeoutDur time.Duration
	Ver        string
	Bytecode   *compiler.Bytecode
}

func (w *Work) LoadFromConfig(data map[string]any) {
	fmt.Printf("DEBUG: Loading Work Config: %+v\n", data)
	if name, ok := data["name"].(string); ok {
		w.Name = name
	}
	if ver, ok := data["version"].(string); ok {
		w.Ver = ver
	}
	if routes, ok := data["routes"].([]any); ok {
		for _, r := range routes {
			if rm, ok := r.(map[string]any); ok {
				method, _ := rm["method"].(string)
				path, _ := rm["path"].(string)
				w.Router(method, path)
			}
		}
	}
}

func NewWork(name string) *Work {
	return &Work{Name: name}
}

func (w *Work) Router(method, path string) *Work {
	method = strings.ToUpper(method)
	fmt.Printf("[Router] %s: Called for %s %s\n", w.Name, method, path)
	// Check if route already exists
	for i, r := range w.Routes {
		if r.Method == method && r.Path == path {
			fmt.Printf("[Router] %s: Route exists, moving to end\n", w.Name)
			// Move existing route to the end so .handle() can update it
			w.Routes = append(w.Routes[:i], w.Routes[i+1:]...) // Remove
			w.Routes = append(w.Routes, r)                     // Re-add at end
			return w
		}
	}
	// Route doesn't exist, add new one
	fmt.Printf("[Router] %s: Adding new route\n", w.Name)
	w.Routes = append(w.Routes, &StaticRoute{Method: method, Path: path})
	return w
}

func (w *Work) Handle(fn value.Value) *Work {
	if len(w.Routes) > 0 {
		lastRoute := w.Routes[len(w.Routes)-1]
		if sFn, ok := fn.V.(*value.ScriptFunction); ok {
			fmt.Printf("[Handle] %s: Setting handler for %s %s with Address: %d (was: %v)\n",
				w.Name, lastRoute.Method, lastRoute.Path, sFn.Address, lastRoute.Handler)
			lastRoute.Handler = sFn
		} else {
			fmt.Printf("[Handle] %s: WARNING: fn.V is not *ScriptFunction, type: %T\n", w.Name, fn.V)
		}
	} else {
		fmt.Printf("[Handle] %s: WARNING: No routes to attach handler to\n", w.Name)
	}
	return w
}

func (w *Work) Retry(times int, _ string) *Work {
	w.Retries = times
	return w
}

func (w *Work) Version(v string) *Work {
	w.Ver = v
	return w
}

// Task đại diện cho một phiên thực thi (Mutable)
type Task struct {
	Work     *Work
	Params   map[string]value.Value
	Response value.Value
	ResType  string
	Config   map[string]string
}

func (t *Task) Reset(w *Work) {
	t.Work = w
	t.Response = value.Value{K: value.Nil}
	t.ResType = "json"

	if t.Params != nil {
		for k := range t.Params {
			delete(t.Params, k)
		}
	} else {
		t.Params = make(map[string]value.Value)
	}

	if t.Config != nil {
		for k := range t.Config {
			delete(t.Config, k)
		}
	} else {
		t.Config = make(map[string]string)
	}
}

func (t *Task) JSON(val value.Value) {
	t.Response = val
	t.ResType = "json"
}

func (t *Task) HTML(template value.Value, data ...value.Value) {
	if len(data) > 0 {
		res := make(map[string]value.Value)
		res["template"] = template
		res["data"] = data[0]
		t.Response = value.New(res)
	} else {
		t.Response = template
	}
	t.ResType = "html"
}

func (t *Task) Now() value.Value     { return value.New(time.Now()) }
func (t *Task) DB() *DBQuery         { return NewDBQuery() }
func (t *Task) HTTP() *HTTPClient    { return NewHTTPClient(t) }
func (t *Task) Payload() value.Value { return value.New(t.Params) }
func (t *Task) Log(args ...value.Value) {
	fmt.Printf("[%s] [%s] ", time.Now().Format("15:04:05"), t.Work.Name)
	for _, arg := range args {
		fmt.Print(arg.Text(), " ")
	}
	fmt.Println()
}
func (t *Task) Print(args ...value.Value) {
	for _, arg := range args {
		fmt.Print(arg.Text(), " ")
	}
	fmt.Println()
}
