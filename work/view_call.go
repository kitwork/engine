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

// The bridge of router.view (28/10/2026, KITWORK_ROUTER_VIEW.md, docs/KITWORK_BRIDGE.md): a job of the page, a
// function of the object its function answers with context.bind({ … }), called from the browser as a POST to
// the page's own URL.
//
//	POST /todo
//	Kitwork-Call: remove
//	{ "args": [3], "vars": { "title": "" }, "seq": 4 }
//
// The folder's guards and the origin check run as for any POST of the folder. Then the view's function runs
// with the vars the browser sent (context.viewVar, written by the compiler, compiler/router_view.go), the job
// runs on what it bound, the vars are read back (__state), and the function runs again with them. The answer
// is the data the job changed, never the page: what differs between the two runs (view_patch.go), which the
// page, running in the kernel, paints (jit/hydrate/call.js):
//
//	{ "seq": 4, "patch": { "left": 1 }, "lists": { "todos": { "key": "id", "remove": [3] } } }
//
// A job that sends the page away (context.redirect) answers { seq, redirect }; one that fails answers
// { seq, error: { code, message } } with that code. A job's return value is dropped.

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
// browser sent them, none of them trusted, like any argument; seq is the call's number, sent back with its
// answer.
type viewCall struct {
	name string
	args []value.Value
	vars map[string]value.Value
	seq  float64
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
		Seq  float64        `json:"seq"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &sent); err != nil {
			return nil, fmt.Errorf("lời gọi không phải JSON { args, vars, seq }")
		}
	}
	call := &viewCall{name: name, vars: map[string]value.Value{}, seq: sent.Seq}
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

// serveViewCall runs the job of reqRouter.call on what the view's function just bound, runs the function
// again and answers what changed; called after the function answered context.bind({ … }), on the VM it ran on.
func (t *Tenant) serveViewCall(vm *runtime.VM, ctxObj *Context, reqRouter *Router, node *RouteNode, leaf *FolderRouter, handler *value.Lambda) {
	call := reqRouter.call
	response := reqRouter.response
	data := response.Data()
	before := viewSnapshot(data) // as JSON reads it back: a job that changes a value in place still differs
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
	// the job answers nothing but a redirect: what the view's function answered is set aside
	clearViewAnswer(reqRouter)
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
	if response.Kind() == "redirect" {
		// the job sends the page away (KITWORK_ROUTER_VIEW.md câu 6b): the page follows, nothing is run again;
		// a cookie the job set goes with the answer
		answerViewCall(reqRouter, map[string]any{"seq": call.seq, "redirect": response.Data().Text()})
		return
	}
	// the vars after the job: the page keeps what it holds, but for what the job changed
	vars := make(map[string]value.Value, len(call.vars))
	for name, v := range call.vars {
		vars[name] = v
	}
	if state != nil {
		if after := vm.ExecuteLambda(state, nil); after.IsMap() {
			for name, v := range after.Map() {
				vars[name] = v
			}
		}
	}
	// the page the job left: the view's function again, holding those vars
	reqRouter.call = &viewCall{vars: vars, seq: call.seq}
	clearViewAnswer(reqRouter)
	if res := t.execTree(vm, leaf, handler, ctxObj); res.K == value.Invalid || response.Kind() != "render" {
		if res.K == value.Invalid {
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "view", res)
		}
		answerViewCallError(reqRouter, http.StatusInternalServerError, "trang không vẽ lại được sau việc "+call.name)
		return
	}
	patch, lists := viewPatch(before, viewSnapshot(response.Data()), t.viewListKeys(node))
	answer := map[string]any{"seq": call.seq}
	if len(patch) > 0 {
		answer["patch"] = patch
	}
	if len(lists) > 0 {
		answer["lists"] = lists
	}
	answerViewCall(reqRouter, answer)
}

// clearViewAnswer sets aside what the response was answered with, for the next run to answer.
func clearViewAnswer(reqRouter *Router) {
	response := reqRouter.response
	response.data, response.kind, response.code = value.Value{K: value.Nil}, "", 0
	response.order = responseOrder{}
	reqRouter.viewBuilder = nil
}

// viewListKeys is the key of each list the folder's page draws by key (ui Page.ListKeys), nil when the page
// cannot be had: its lists then change whole.
func (t *Tenant) viewListKeys(node *RouteNode) map[string]string {
	plan := t.renderPlan()
	if plan == nil || plan.ui == nil || node == nil {
		return nil
	}
	compiled, err := plan.ui.page(node.relPath(), uiOwnPage)
	if err != nil || compiled.page == nil {
		return nil
	}
	return compiled.page.ListKeys()
}

// answerViewCall answers a call with JSON, 200.
func answerViewCall(reqRouter *Router, answer map[string]any) {
	b, err := json.Marshal(answer)
	if err != nil {
		answerViewCallError(reqRouter, http.StatusInternalServerError, "không viết được câu trả lời thành JSON: "+err.Error())
		return
	}
	response := reqRouter.response
	response.data, response.kind = value.Value{K: value.Nil}, ""
	response.contentType = "application/json; charset=utf-8"
	response.Bytes(b, http.StatusOK)
}

// answerViewCallError answers a call with { seq, error: { code, message } } and code as its status: what the
// page reads as job.error.
func answerViewCallError(reqRouter *Router, code int, message string) {
	var seq float64
	if reqRouter.call != nil {
		seq = reqRouter.call.seq
	}
	response := reqRouter.response
	response.data, response.kind = value.Value{K: value.Nil}, ""
	response.JSON(value.New(map[string]value.Value{
		"seq":   value.New(seq),
		"error": value.New(map[string]value.Value{"code": value.New(code), "message": value.New(message)}),
	}), code)
}
