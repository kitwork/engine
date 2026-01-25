package work

import (
	"fmt"
	"net/http"
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
				w.Router(value.New(method), value.New(path))
			}
		}
	}
}

func NewWork(name string) *Work {
	return &Work{Name: name}
}

func (w *Work) Router(args ...value.Value) *Work {
	if len(args) < 2 {
		return w
	}
	method := strings.ToUpper(args[0].Text())
	path := args[1].Text()
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

// Task đại diện cho một phiên thực thi (Mutable Context)
type Task struct {
	Work    *Work
	Request *http.Request
	Writer  http.ResponseWriter

	Params map[string]value.Value // URL Path params like :id

	Response value.Value
	ResType  string
	Config   map[string]string
}

func (t *Task) Reset(w *Work) {
	t.Work = w
	t.Request = nil
	t.Writer = nil
	t.Response = value.Value{K: value.Nil}
	t.ResType = "json"

	if t.Params == nil {
		t.Params = make(map[string]value.Value)
	} else {
		for k := range t.Params {
			delete(t.Params, k)
		}
	}

	if t.Config == nil {
		t.Config = make(map[string]string)
	} else {
		for k := range t.Config {
			delete(t.Config, k)
		}
	}
}

func (t *Task) SetRequest(r *http.Request, w http.ResponseWriter) {
	t.Request = r
	t.Writer = w
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

func (t *Task) Now() value.Value  { return value.New(time.Now()) }
func (t *Task) DB() *DBQuery      { return NewDBQuery() }
func (t *Task) HTTP() *HTTPClient { return NewHTTPClient(t) }

func (t *Task) GetQuery() value.Value  { return value.NewNull() }
func (t *Task) SetQuery(v value.Value) {}
func (t *Task) GetBody() value.Value   { return value.NewNull() }
func (t *Task) SetBody(v value.Value)  {}
func (t *Task) GetParams() value.Value { return value.New(t.Params) }

func (t *Task) Payload() value.Value {
	res := make(map[string]value.Value)
	for k, v := range t.Params {
		res[k] = v
	}
	return value.New(res)
}
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
