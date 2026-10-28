package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The bridge of router.view (28/10/2026, KITWORK_ROUTER_VIEW.md): a job of the page, a function of the object
// its function answers with context.bind({ … }), called from the browser as a POST to the page's own URL.
//
//	POST /todo
//	Kitwork-Call: remove
//	{ "args": [3], "vars": { "title": "" } }
//
// The folder's guards and the origin check run as for any POST of the folder. Then the view's function runs
// with the vars the browser sent (context.viewVar, written by the compiler, compiler/router_view.go), the job
// runs on what it bound, the vars are read back (__state), and the function runs again to draw the page the
// job left. The answer is JSON: the page's HTML for the browser to morph, and the vars the job changed;
// a job that fails answers { error: { code, message } } with that code. A job's return value is dropped.

// ViewCallHeader names the job of a call; the page's URL is the address (KITWORK_ROUTER_VIEW.md câu 7b is
// still open between this header and a query).
const ViewCallHeader = "Kitwork-Call"

// viewCallLimit is the most a call's body may hold: the arguments and the page's vars.
const viewCallLimit = 1 << 20

// viewVar is the value of the page's var name while a job runs: what the browser sent for it, the initial
// value when it sent none or when no job runs (the page is drawn).
func (c *Context) viewVar(name value.Value, initial value.Value) value.Value {
	if c == nil || c.request == nil || !name.IsString() {
		return initial
	}
	call := c.router().call
	if call == nil {
		return initial
	}
	if sent, ok := call.vars[name.Text()]; ok {
		return sent
	}
	return initial
}

// viewCall is one call of a job of the page: its name, its arguments and the vars the page held, as the
// browser sent them; none of them trusted, like any argument.
type viewCall struct {
	name string
	args []value.Value
	vars map[string]value.Value
}

// readViewCall reads the call of a POST that names a job; an error is the request's own (400).
func readViewCall(r *http.Request) (*viewCall, error) {
	name := r.Header.Get(ViewCallHeader)
	if !jobName(name) {
		return nil, fmt.Errorf("%s không phải tên một việc", ViewCallHeader)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, viewCallLimit+1))
	if err != nil {
		return nil, fmt.Errorf("không đọc được lời gọi")
	}
	if len(body) > viewCallLimit {
		return nil, fmt.Errorf("lời gọi quá %d byte", viewCallLimit)
	}
	var sent struct {
		Args []any          `json:"args"`
		Vars map[string]any `json:"vars"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &sent); err != nil {
			return nil, fmt.Errorf("lời gọi không phải JSON { args, vars }")
		}
	}
	call := &viewCall{name: name, vars: map[string]value.Value{}}
	for _, arg := range sent.Args {
		call.args = append(call.args, value.New(arg))
	}
	for key, v := range sent.Vars {
		if visibleName(key) {
			call.vars[key] = value.New(v)
		}
	}
	return call, nil
}

// jobName reports whether name can be a job's: a name the page reads, letters, digits, _ and $, at most 64.
func jobName(name string) bool {
	if name == "" || len(name) > 64 || !visibleName(name) {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// serveViewCall runs the job of reqRouter.call on what the view's function just bound, then draws the page
// again; called after the function answered context.bind({ … }), on the VM it ran on.
func (t *Tenant) serveViewCall(vm *runtime.VM, ctxObj *Context, reqRouter *Router, node *RouteNode, leaf *FolderRouter, handler *value.Lambda) {
	call := reqRouter.call
	response := reqRouter.response
	data := response.Data()
	var job, state *value.Lambda
	if data.IsMap() {
		fields := data.Map()
		if v, ok := fields[call.name]; ok && v.K == value.Func {
			job, _ = v.V.(*value.Lambda)
		}
		if v, ok := fields[compiler.ViewStateName]; ok && v.K == value.Func {
			state, _ = v.V.(*value.Lambda)
		}
	}
	if job == nil {
		answerViewCallError(reqRouter, http.StatusNotFound, fmt.Sprintf("trang này không có việc %s", call.name))
		return
	}
	if out := vm.ExecuteLambda(job, call.args); out.K == value.Invalid {
		t.recordRuntimeFailure(reqRouter, leaf.bytecode, "view", out)
		status, message := http.StatusInternalServerError, out.Text()
		if failure, ok := out.Failure(); ok {
			message = failure.Message
			if failure.Code == value.NotFoundCode {
				status = http.StatusNotFound
			}
		}
		answerViewCallError(reqRouter, status, message)
		return
	}
	// the vars after the job: the page keeps what it holds, but for what the job changed
	vars := make(map[string]value.Value, len(call.vars))
	for name, v := range call.vars {
		vars[name] = v
	}
	changed := map[string]value.Value{}
	if state != nil {
		if after := vm.ExecuteLambda(state, nil); after.IsMap() {
			for name, v := range after.Map() {
				if sent, had := call.vars[name]; !had || !sameJSON(sent, v) {
					changed[name] = v
				}
				vars[name] = v
			}
		}
	}
	// the page the job left: the view's function again, holding those vars
	reqRouter.call = &viewCall{vars: vars}
	response.data, response.kind = value.Value{K: value.Nil}, ""
	response.order = responseOrder{}
	reqRouter.viewBuilder = nil
	if res := t.execTree(vm, leaf, handler, ctxObj); res.K == value.Invalid || response.Kind() != "render" {
		if res.K == value.Invalid {
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "view", res)
		}
		answerViewCallError(reqRouter, http.StatusInternalServerError, "trang không vẽ lại được sau việc "+call.name)
		return
	}
	t.serveUIAnswer(vm, ctxObj, reqRouter, node, handler)
	if reqRouter.err != nil {
		return
	}
	html := response.Data().Text()
	response.data, response.kind = value.Value{K: value.Nil}, ""
	response.JSON(value.New(map[string]value.Value{"html": value.New(html), "vars": value.New(changed)}))
}

// answerViewCallError answers a call with { error: { code, message } } and code as its status: what the
// page reads as job.error.
func answerViewCallError(reqRouter *Router, code int, message string) {
	response := reqRouter.response
	response.data, response.kind = value.Value{K: value.Nil}, ""
	response.JSON(value.New(map[string]value.Value{
		"error": value.New(map[string]value.Value{"code": value.New(code), "message": value.New(message)}),
	}), code)
}

// sameJSON reports whether two values write the same JSON.
func sameJSON(a, b value.Value) bool {
	x, errX := a.MarshalJSON()
	y, errY := b.MarshalJSON()
	return errX == nil && errY == nil && bytes.Equal(x, y)
}
