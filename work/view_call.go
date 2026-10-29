package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The bridge of router.view (28/10/2026, KITWORK_ROUTER_VIEW.md, docs/KITWORK_BRIDGE.md): a job of the page, a
// function of the object its function answers with context.bind({ … }), called from the browser as a POST to
// the page's own URL.
//
//	POST /todo?kitwork-call=remove
//	{ "args": [3], "vars": { "title": "" }, "seq": 4, "revs": { "todos": "3f1a…", "left": "9c0e…" } }
//
// The job's name is in the URL, taken out of it before anything reads it (takeViewCall): the guards and the
// view's function read the page's own URL.
//
// The folder's guards and the origin check run as for any POST of the folder. Then the view's function runs
// with the vars the browser sent (context.viewVar, written by the compiler, compiler/router_view.go), the job
// runs on what it bound, the vars are read back (__state), and the function runs again with them. The answer
// is the data the job changed, never the page: what differs between the two runs (view_patch.go), which the
// page, running in the kernel, paints (jit/hydrate/call.js):
//
//	{ "seq": 4, "patch": { "left": 1 }, "lists": { "todos": { "key": "id", "remove": [3] } }, "revs": { … }, "stamp": 1793174400123456 }
//
// revs are the page's revision of each name of its data (view_patch.go): a page that held another value than
// the one the job ran on (another tab changed the data) gets that name whole, so no page is left half new.
// stamp is the moment the data of the answer was read (nextViewStamp): a tab takes no data, from an answer or
// from another tab, read before the data it holds.
//
// A job that sends the page away (context.redirect) answers { seq, redirect }; one that fails answers
// { seq, error: { code, message } } with that code. A job's return value is dropped.
//
// A job of a component of the site the page draws is called the same way, named shop-cart.checkout, with the
// place the instance is called from and its vars (component_call.go).

// lockViewCalls waits for the calls of a page's jobs before this one and returns what lets the next one go.
// The page's function runs before its job and the job writes on what it read: two calls that both read before
// either wrote would keep one write of the two (twenty adds at once kept two rows). One call at a time per
// page, from the run before the job to the run after it, the guards and the answer included; drawing the page
// waits for nothing, nor does a refresh, nor a job that changes nothing but the page's vars (the compiler's
// word, compiler/view_read_only.go, 29/10/2026): they write nothing a job writes on, and a search typed while
// another person's tick is out goes at once. A page's jobs are the page's to order; writes of two pages to the
// same data are the store's (an atomic statement), not this lock's.
func (t *Tenant) lockViewCalls(leaf *FolderRouter) func() {
	lock, _ := t.viewCalls.LoadOrStore(viewPageKey(leaf), &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// lastViewStamp is the last stamp nextViewStamp gave.
var lastViewStamp atomic.Int64

// nextViewStamp is the moment the data of a page of router.view is read, in microseconds, taken before the run
// that reads it: never twice the same in the process, never smaller than one given before (across a restart,
// the clock's word). A job that writes takes its stamp after its write, under the page's lock; every run takes
// its own before it reads, locked or not: so of two answers of one page the one with the greater stamp read its
// data after the other's job wrote. The tabs of a browser tell each other the data an answer brought, and a
// tab takes it only when its stamp is greater than the one of the data it holds (call.js), however late a
// message or an answer arrives.
func nextViewStamp() int64 {
	for {
		last := lastViewStamp.Load()
		now := time.Now().UnixMicro()
		if now <= last {
			now = last + 1
		}
		if lastViewStamp.CompareAndSwap(last, now) {
			return now
		}
	}
}

// ViewCallParameter names the job of a call in the page's URL, POST /todo?kitwork-call=remove
// (KITWORK_ROUTER_VIEW.md câu 7b, 28/10/2026): the URL, not a header, because the shell of a native app passes
// a request's method, URL and body as a browser does, and not always its headers.
const ViewCallParameter = "kitwork-call"

// ViewRefreshParameter asks, with no job, for the data of a page that changed since the revisions it sends,
// POST /todo?kitwork-refresh=1: a tab seen again after another changed the data (call.js). It goes the way of
// a call, through the folder's guards and the page's lock, and runs the view's function once.
const ViewRefreshParameter = "kitwork-refresh"

// viewPageKey names a page of router.view among a tenant's: its folder.
func viewPageKey(leaf *FolderRouter) string {
	if leaf == nil || leaf.node == nil {
		return ""
	}
	return leaf.node.relPath()
}

// takeViewCall takes out of a POST's URL the job it names (kitwork-call) or its ask for what changed
// (kitwork-refresh), before anything reads the URL: the guards and the view's function read the page's own.
// call is true when the URL names a job, refresh when it asks with none.
func takeViewCall(r *http.Request) (name string, call, refresh bool) {
	found := takeFromQuery(r, ViewCallParameter, ViewRefreshParameter)
	name, call = found[ViewCallParameter]
	_, refresh = found[ViewRefreshParameter]
	return name, call, refresh && !call
}

// takeFromQuery takes keys out of a request's query, the rest as it was written, and answers the value of each
// it found ("" for one with none); nil when it found none.
func takeFromQuery(r *http.Request, keys ...string) map[string]string {
	if !strings.Contains(r.URL.RawQuery, "kitwork-") {
		return nil
	}
	var found map[string]string
	kept := make([]string, 0, 4)
	for _, part := range strings.Split(r.URL.RawQuery, "&") {
		key, v, _ := strings.Cut(part, "=")
		if name, err := url.QueryUnescape(key); err == nil && slices.Contains(keys, name) {
			if found == nil {
				found = map[string]string{}
			}
			found[name], _ = url.QueryUnescape(v)
			continue
		}
		kept = append(kept, part)
	}
	if found != nil {
		r.URL.RawQuery = strings.Join(kept, "&")
		r.RequestURI = r.URL.RequestURI()
	}
	return found
}

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
// answer. A job of a component (shop-cart.checkout) also names the place its instance is called from (at)
// and carries the instance's vars (scope).
type viewCall struct {
	name  string
	args  []value.Value
	vars  map[string]value.Value
	seq   float64
	at    string
	scope map[string]value.Value
	// revs are the revisions the page holds, nil when it sent none
	revs map[string]string
	// refresh: no job, the page asks for the data that changed since its revisions (kitwork-refresh)
	refresh bool
	// marks are the keys the page holds tickets for, made lately, "" when it holds none (view_changes.go)
	marks string
	// readOnly: the job changes nothing but the page's vars (FolderMethod.readOnly): its call holds no lock and
	// tells no tab
	readOnly bool
	// scopeRevs are the revisions of the data of the instance a component's job is called on, as the tab holds
	// them (the components block, then the answers); nil when it sent none
	scopeRevs map[string]string
}

// readViewCall reads the call of a POST whose URL named a job (name) or asked for what changed (refresh); an
// error is the request's own (400).
func readViewCall(r *http.Request, name string, refresh bool) (*viewCall, error) {
	if !refresh && !jobName(name) {
		return nil, fmt.Errorf("%s=%q không phải tên một việc", ViewCallParameter, name)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, viewCallLimit+1))
	if err != nil {
		return nil, fmt.Errorf("không đọc được lời gọi")
	}
	if len(body) > viewCallLimit {
		return nil, fmt.Errorf("lời gọi quá %d byte", viewCallLimit)
	}
	var sent struct {
		Args  []any             `json:"args"`
		Vars  map[string]any    `json:"vars"`
		Seq   float64           `json:"seq"`
		At    string            `json:"at"`
		Scope map[string]any    `json:"scope"`
		Revs  map[string]string `json:"revs"`
		Marks string            `json:"marks"`
		// ScopeRevs are the revisions of the instance's data the tab holds (component_call.go)
		ScopeRevs map[string]string `json:"scopeRevs"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &sent); err != nil {
			return nil, fmt.Errorf("lời gọi không phải JSON { args, vars, seq }")
		}
	}
	call := &viewCall{name: name, vars: map[string]value.Value{}, seq: sent.Seq, revs: sent.Revs, refresh: refresh, marks: sent.Marks}
	for _, arg := range sent.Args {
		call.args = append(call.args, value.New(arg))
	}
	for key, v := range sent.Vars {
		if visibleName(key) {
			call.vars[key] = value.New(v)
		}
	}
	if strings.Contains(name, ".") {
		// a job of a component: the place of its instance on the page, and the instance's vars
		if !componentPlace(sent.At) {
			return nil, fmt.Errorf("việc %s của component cần chỗ gọi của instance (at)", name)
		}
		call.at, call.scope, call.scopeRevs = sent.At, map[string]value.Value{}, sent.ScopeRevs
		for key, v := range sent.Scope {
			if visibleName(key) {
				call.scope[key] = value.New(v)
			}
		}
	}
	return call, nil
}

// componentPlace reports whether at can be the place a component is called from, as the page writes it
// (ui callPlace): c, a hash in digits and lowercase letters, and a dash and a number when two hash the same.
func componentPlace(at string) bool {
	if len(at) < 2 || len(at) > 16 || at[0] != 'c' {
		return false
	}
	hash, suffix, two := strings.Cut(at[1:], "-")
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') {
			return false
		}
	}
	if two {
		for _, r := range suffix {
			if r < '0' || r > '9' {
				return false
			}
		}
		return suffix != "" && hash != ""
	}
	return hash != ""
}

// jobName reports whether name can be a job's: a name the page reads, letters, digits, _ and $, at most 64; or
// a job of a component, its name, a dot and the job's (shop-cart.checkout).
func jobName(name string) bool {
	if component, job, ok := strings.Cut(name, "."); ok {
		return len(component) <= 64 && siteComponentName(component) && jobName(job)
	}
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
	if call.refresh {
		t.serveViewRefresh(vm, reqRouter, leaf)
		return
	}
	if call.at != "" {
		t.serveComponentCall(vm, ctxObj, reqRouter, node, leaf, handler) // a job of a component (component_call.go)
		return
	}
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
	isVar := map[string]bool{}
	for name, v := range call.vars {
		vars[name] = v
		isVar[name] = true
	}
	if state != nil {
		if after := vm.ExecuteLambda(state, nil); after.IsMap() {
			for name, v := range after.Map() {
				vars[name] = v
				isVar[name] = true
			}
		}
	}
	// the page the job left: the view's function again, holding those vars, past the guards again
	reqRouter.call = &viewCall{vars: vars, seq: call.seq}
	if !t.guardsAgain(vm, ctxObj, reqRouter, leaf, call.seq) {
		return
	}
	stamp := nextViewStamp() // before the run that reads the data the answer brings, under the page's lock
	reqRouter.reads = map[string]bool{}
	if res := t.execTree(vm, leaf, handler, ctxObj); res.K == value.Invalid || response.Kind() != "render" {
		if res.K == value.Invalid {
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "view", res)
		}
		answerViewCallError(reqRouter, http.StatusInternalServerError, "trang không vẽ lại được sau việc "+call.name)
		return
	}
	after := viewSnapshot(response.Data())
	patch, lists := viewPatch(before, after, t.viewListKeys(node))
	answer := map[string]any{"seq": call.seq, "stamp": stamp}
	t.answerTickets(answer, reqRouter, leaf, call.marks)
	if call.revs != nil {
		if revs := staleRevs(call.revs, before, after, isVar, patch, lists); len(revs) > 0 {
			answer["revs"] = revs
		}
		t.rowsSince(leaf, call.revs, patch, lists) // a list the tab held older comes by row, when kept (view_rows.go)
	}
	t.keepViewRows(leaf, after)
	if len(patch) > 0 {
		answer["patch"] = patch
	}
	if len(lists) > 0 {
		answer["lists"] = lists
	}
	// the tabs holding a ticket for the page ask for what changed (view_changes.go); a job that changes only the
	// vars changed no data another tab shows, however its own page's differs (a search's results)
	if !call.readOnly && viewDataChanged(before, after, isVar) {
		t.tellViewChanged(leaf, stamp)
	}
	answerViewCall(reqRouter, answer)
}

// guardsAgain runs the guards of the branch and of router.view before the run after a job
// (KITWORK_ROUTER_VIEW.md câu 5f, 5g), with the cookies the job set or removed as the browser will send them
// next: signing out on a page that wants a session sends the page to the guard's /login. True when they let
// the page be drawn again. Otherwise the call is answered and no data of the page goes: { seq, redirect } for a
// guard that sends the page away, the page follows; { seq, reload } for one that refuses (false,
// status(…).json(…)) or fails, the page opens its URL again and meets the guard's answer there. The cookies
// the job set go with either answer.
func (t *Tenant) guardsAgain(vm *runtime.VM, ctxObj *Context, reqRouter *Router, leaf *FolderRouter, seq float64) bool {
	if reqRouter.request != nil {
		cookiesAsSentNext(reqRouter.request, reqRouter.response.cookies)
	}
	pass := func(folder *FolderRouter, guard *value.Lambda) bool {
		clearViewAnswer(reqRouter)
		return t.runStage(vm, folder, guard, ctxObj, reqRouter, true)
	}
	ok := true
	for _, node := range reqRouter.callChain {
		if node == nil || node.folder == nil {
			continue
		}
		for _, guard := range node.folder.guards {
			if ok && !pass(node.folder, guard) {
				ok = false
			}
		}
	}
	for _, guard := range reqRouter.callGuards {
		if ok && !pass(leaf, guard) {
			ok = false
		}
	}
	response := reqRouter.response
	if ok {
		clearViewAnswer(reqRouter)
		return true
	}
	answer := map[string]any{"seq": seq, "reload": true}
	if response.Kind() == "redirect" {
		answer = map[string]any{"seq": seq, "redirect": response.Data().Text()}
	}
	reqRouter.err = nil
	response.uiPage, response.uiMessage = "", ""
	answerViewCall(reqRouter, answer)
	return false
}

// cookiesAsSentNext writes into a request's Cookie header the cookies its response set or removed, as the
// browser will send them with its next request: what the run after a job reads.
func cookiesAsSentNext(req *http.Request, set []*http.Cookie) {
	if len(set) == 0 {
		return
	}
	values, order := map[string]string{}, []string{}
	for _, c := range req.Cookies() {
		if _, seen := values[c.Name]; !seen {
			order = append(order, c.Name)
		}
		values[c.Name] = c.Value
	}
	for _, c := range set {
		if c.MaxAge < 0 {
			delete(values, c.Name)
			continue
		}
		if _, seen := values[c.Name]; !seen {
			order = append(order, c.Name)
		}
		values[c.Name] = c.Value
	}
	var b strings.Builder
	for _, name := range order {
		v, kept := values[name]
		if !kept {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString((&http.Cookie{Name: name, Value: v}).String())
	}
	req.Header.Set("Cookie", b.String())
}

// serveViewRefresh answers a page that asks for what changed since its revisions: each name of its data
// (a var's aside) whose revision is not the page's, whole, with its new revision; nothing when it holds the data
// as it is. Its stamp was taken before the view's function ran, under the page's lock (router_serve.go); its
// tickets are for what that run read (view_changes.go).
func (t *Tenant) serveViewRefresh(vm *runtime.VM, reqRouter *Router, leaf *FolderRouter) {
	call := reqRouter.call
	data := reqRouter.response.Data()
	now := viewSnapshot(data)
	vars := map[string]bool{}
	if data.IsMap() {
		if v, ok := data.Map()[compiler.ViewStateName]; ok && v.K == value.Func {
			if state, ok := v.V.(*value.Lambda); ok {
				if held := vm.ExecuteLambda(state, nil); held.IsMap() {
					for name := range held.Map() {
						vars[name] = true
					}
				}
			}
		}
	}
	patch, revs := map[string]any{}, map[string]string{}
	for name, v := range now {
		if vars[name] {
			continue
		}
		if rev := revOf(v); call.revs[name] != rev {
			patch[name], revs[name] = v, rev
		}
	}
	for name := range call.revs {
		if _, kept := now[name]; !kept {
			patch[name], revs[name] = nil, ""
		}
	}
	answer := map[string]any{"seq": call.seq, "stamp": reqRouter.viewStamp}
	t.answerTickets(answer, reqRouter, leaf, call.marks)
	lists := map[string]any{}
	t.rowsSince(leaf, call.revs, patch, lists) // a list the tab holds at a kept revision comes by row (view_rows.go)
	t.keepViewRows(leaf, now)
	if len(revs) > 0 {
		answer["patch"], answer["revs"] = patch, revs
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

// siteComponentName reports whether name is a component of the site's: lowercase words and digits joined by
// single dashes, at least one dash (ui componentName, siteName).
func siteComponentName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' || !strings.Contains(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
