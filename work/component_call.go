package work

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/ui"
	"github.com/kitwork/engine/value"
)

// A job of a component of the site, through the bridge (28/10/2026, KITWORK_COMPONENTS.md mục 8,
// docs/KITWORK_BRIDGE.md). The component is defined in the site's components.work,
//
//	import * as component from "kitwork/component";
//	component.define("shop-cart", (context) => {
//	  const { shippingFee = 15000 } = context.attributes();
//	  var items = [];
//	  const checkout = () => { … items = []; };
//	  return context.bind({ items, checkout });
//	});
//
// and its job is called as a job of the page is, a POST to the page's URL, named by the component and the job,
// with the place the instance is called from (data-kitwork-at, ui/component_live.go) and the instance's vars:
//
//	POST /cart?kitwork-call=shop-cart.checkout
//	{ "args": [], "vars": { …the page's }, "seq": 5, "revs": { …the page's }, "at": "c1", "scope": { "items": [ … ] } }
//
// The folder's guards and the origin check run as for any POST of it, then the view's function. The server
// draws the page from what it bound to find the instance at that place, the component it is and the inputs
// its call passes there: never what the browser sends, so no one changes an input to act on what is not
// theirs. The page does not draw that component there: 404, as for a job the page does not have. Then the
// component's function runs with those inputs and the instance's vars, the job runs on what it bound, the vars
// are read back (__state), the view's function and the component's run again, and the answer is what changed:
// the page's data as for a job of the page (with its revisions, on a page that holds its data in the browser),
// and the instance's under its place.
//
//	{ "seq": 5, "patch": { "count": 2 }, "revs": { "count": "9c0e…" }, "components": { "c1": { "patch": { "items": [], "done": true } } } }

// componentModule is "kitwork/component" (KITWORK_ENGINE.md mục 6): define, which only the loader of a
// component's job reads (componentDefines); anywhere else it says where a component is defined.
type componentModule struct {
	defines *componentDefines
}

// componentDefines collects the component.define calls of components.work as the loader runs it.
type componentDefines struct {
	functions map[string]*value.Lambda
}

// Component is the module "kitwork/component".
func (w *KitWork) Component() *componentModule {
	return &componentModule{defines: w.componentDefines}
}

// Define is component.define("shop-cart", (context) => { … }): the function of the component of that name. It
// answers nothing, as customElements.define does.
func (m *componentModule) Define(name value.Value, fn value.Value) value.Value {
	if m.defines == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, "component.define viết trong components.work ở gốc site: engine đọc nó ở đó, khi trang gọi một việc của component (KITWORK_COMPONENTS.md mục 2)")
	}
	lambda, _ := fn.V.(*value.Lambda)
	if !name.IsString() || fn.K != value.Func || lambda == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, `component.define nhận tên của thẻ và một hàm: component.define("shop-cart", (context) => { … })`)
	}
	if _, taken := m.defines.functions[name.Text()]; taken {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("component.define(%q, …) viết hai lần: một tên một định nghĩa", name.Text()))
	}
	m.defines.functions[name.Text()] = lambda
	return value.Value{K: value.Nil}
}

// componentContext is the context a component's function gets for a call of one of its jobs: the request's,
// with the inputs of the instance (context.attributes()), its vars as the browser sent them (read through
// context.viewVar, which the compiler writes for each var, compiler/router_view.go) and the object it binds.
// The page's parts (title, meta, status) are not a component's.
type componentContext struct {
	*Context
	attributes value.Value
	vars       map[string]value.Value
	bound      value.Value
}

// Attributes is context.attributes(): the inputs of the instance, read where its call stands on the page.
func (c *componentContext) Attributes() value.Value { return c.attributes }

// CallOnly says attributes is called, never read as a value.
func (c *componentContext) CallOnly(name string) bool { return name == "attributes" }

// Bind is the answer of a component's function, context.bind({ … }): the names that leave it.
func (c *componentContext) Bind(args ...value.Value) value.Value {
	for _, arg := range args {
		if arg.IsMap() {
			c.bound = arg
		}
	}
	return c.bound
}

// ViewVar is the value of a var of the component: what the instance holds as the browser sent it, initial
// when it sent none.
func (c *componentContext) ViewVar(name value.Value, initial value.Value) value.Value {
	if sent, ok := c.vars[name.Text()]; ok && name.IsString() {
		return sent
	}
	return initial
}

func (c *componentContext) Title(value.Value) value.Value  { return componentPagePart("title") }
func (c *componentContext) Meta(value.Value) value.Value   { return componentPagePart("meta") }
func (c *componentContext) Status(value.Value) value.Value { return componentPagePart("status") }

func componentPagePart(word string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("context.%s là của trang, không của component: component không có phần của trang (KITWORK_COMPONENTS.md mục 3)", word))
}

// componentRun is one run of a component's function: the VM it ran on, where its jobs run, the program of
// components.work, and what it bound.
type componentRun struct {
	vm       *runtime.VM
	bytecode *compiler.Bytecode
	bound    value.Value
}

// runComponent runs components.work (logic) for one call and the function it defines for name, with the inputs
// of the instance and its vars. A failure is answered as the call's error by the caller: status and message.
func (t *Tenant) runComponent(reqRouter *Router, ctxObj *Context, logic, name string, inputs map[string]any, vars map[string]value.Value) (*componentRun, int, string) {
	file, status, message := t.loadComponents(reqRouter, logic)
	if file == nil {
		return nil, status, message
	}
	return t.callComponent(reqRouter, ctxObj, file, logic, name, inputs, vars)
}

// componentFile is components.work run for one request: the VM its functions run on, its program, and the
// function each component.define gave a name.
type componentFile struct {
	vm        *runtime.VM
	bytecode  *compiler.Bytecode
	functions map[string]*value.Lambda
}

// loadComponents runs components.work (logic) in a VM of its own for the request, gathering its defines.
func (t *Tenant) loadComponents(reqRouter *Router, logic string) (*componentFile, int, string) {
	bc, err := t.componentProgram(logic)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Sprintf("%s không biên dịch được: %v", t.relativeToSite(logic), err)
	}
	defines := &componentDefines{functions: map[string]*value.Lambda{}}
	vm := runtime.New(bc.Program)
	t.prepareExecutionVM(vm, t.vm.Globals, t.vm.Builtins, reqRouter.requestScope)
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(&KitWork{tenant: t, vm: vm, requestScope: reqRouter.requestScope, componentDefines: defines})
	})
	vm.Builtins[0] = kitworkFunc
	vm.Globals[kitwork] = kitworkFunc
	vm.MaxEnergy = t.MaxEnergy
	if out := vm.Run(); out.K == value.Invalid {
		t.recordRuntimeFailure(reqRouter, bc, "component", out)
		return nil, http.StatusInternalServerError, out.Text()
	}
	return &componentFile{vm: vm, bytecode: bc, functions: defines.functions}, 0, ""
}

// callComponent runs the function components.work defines for name, on the VM it was loaded in, with the inputs
// of the instance and its vars (nil as the page is drawn: each var takes its first value).
func (t *Tenant) callComponent(reqRouter *Router, ctxObj *Context, file *componentFile, logic, name string, inputs map[string]any, vars map[string]value.Value) (*componentRun, int, string) {
	vm, bc := file.vm, file.bytecode
	fn := file.functions[name]
	if fn == nil {
		return nil, http.StatusNotFound, fmt.Sprintf("%s không có component.define(%q, …)", t.relativeToSite(logic), name)
	}
	context := &componentContext{Context: ctxObj, attributes: value.New(inputs), vars: vars}
	if out := vm.ExecuteLambda(fn, []value.Value{value.New(context)}); out.K == value.Invalid {
		t.recordRuntimeFailure(reqRouter, bc, "component", out)
		status, message := http.StatusInternalServerError, out.Text()
		if failure, ok := out.Failure(); ok {
			message = failure.Message
		}
		return nil, status, message
	}
	if !context.bound.IsMap() {
		return nil, http.StatusInternalServerError, fmt.Sprintf("hàm của %s không trả context.bind({ … })", name)
	}
	return &componentRun{vm: vm, bytecode: bc, bound: context.bound}, 0, ""
}

// componentRenderer runs, for one request, the function of each component of the site the page draws whose
// logic answers a bind (ui.Request.Component, KITWORK_COMPONENTS.md mục 9): a const computed on the server is
// right at the first paint. Each components.work is loaded once for the request; each call runs with the inputs
// it passes where it stands and the request's context (the cookies, the session of who looks). The first failure
// fails the request, as a failure of the view's function does.
type componentRenderer struct {
	t          *Tenant
	reqRouter  *Router
	ctx        *Context
	files      map[string]*componentFile
	converters []*uiConverter
	failure    error
}

func newComponentRenderer(t *Tenant, reqRouter *Router) *componentRenderer {
	return &componentRenderer{t: t, reqRouter: reqRouter, ctx: &Context{request: &Request{router: reqRouter}},
		files: map[string]*componentFile{}}
}

// run runs one call: the data of its bind as the page reads it, nil after a failure.
func (c *componentRenderer) run(call ui.Instance) map[string]any {
	if c.failure != nil {
		return nil
	}
	file := c.files[call.Logic]
	if file == nil {
		loaded, _, message := c.t.loadComponents(c.reqRouter, call.Logic)
		if loaded == nil {
			c.failure = uncaught(fmt.Sprintf("component %s: %s", call.Name, message))
			return nil
		}
		file = loaded
		c.files[call.Logic] = file
	}
	run, _, message := c.t.callComponent(c.reqRouter, c.ctx, file, call.Logic, call.Name, call.Inputs, nil)
	if run == nil {
		c.failure = uncaught(fmt.Sprintf("component %s: %s", call.Name, message))
		return nil
	}
	// a function it binds is a job, no data; a function inside its data runs on its VM when the page calls it
	converter := &uiConverter{vm: file.vm}
	c.converters = append(c.converters, converter)
	out := map[string]any{}
	for name, v := range run.bound.Map() {
		if !visibleName(name) || v.K == value.Func {
			continue
		}
		if x, ok := converter.convert(v, 0); ok {
			out[name] = x
		}
		if converter.cycle != nil {
			c.failure = fmt.Errorf("component %s: dữ liệu %s: %s", call.Name, name, converter.cycle.Error())
			return nil
		}
	}
	return out
}

// failed is the failure that ends the request: one a run met, or one that ended a function of a component's
// data the page called as it was drawn.
func (c *componentRenderer) failed() error {
	if c.failure != nil {
		return c.failure
	}
	for _, converter := range c.converters {
		if converter.fatal.Failed() {
			return uncaught(converter.fatal.Text())
		}
	}
	return nil
}

// componentProgram is components.work compiled for the bridge, once per site generation (a change to the
// site's files reloads the generation, and with it this).
func (t *Tenant) componentProgram(logic string) (*compiler.Bytecode, error) {
	plan := t.renderPlan()
	if plan != nil {
		if cached, ok := plan.components.Load(logic); ok {
			return cached.(*compiler.Bytecode), nil
		}
	}
	bc, err := compiler.CompileComponentFile(t.resolveApp(), logic)
	if err != nil {
		return nil, err
	}
	if plan != nil {
		plan.components.Store(logic, bc)
	}
	return bc, nil
}

// relativeToSite names a file of the site for a message.
func (t *Tenant) relativeToSite(file string) string {
	base := strings.TrimRight(strings.ReplaceAll(t.resolve(), "\\", "/"), "/") + "/"
	return strings.TrimPrefix(strings.ReplaceAll(file, "\\", "/"), base)
}

// viewInstances draws the folder's page from the data the view's function bound and gives each component on it
// whose jobs go through the bridge, by its place; nil when the page cannot be had.
func (t *Tenant) viewInstances(vm *runtime.VM, reqRouter *Router, node *RouteNode, handler *value.Lambda, data value.Value) map[string]ui.Instance {
	page := t.viewPage(node)
	if page == nil {
		return nil
	}
	scope, _, err := viewData(vm, handler, data)
	if err != nil {
		return nil
	}
	r := reqRouter.request
	return page.Instances(ui.Request{Path: r.URL.Path, Origin: requestOrigin(r), Scope: scope, Method: r.Method})
}

// viewPage is the folder's compiled .ui page, nil when it cannot be had.
func (t *Tenant) viewPage(node *RouteNode) *ui.Page {
	plan := t.renderPlan()
	if plan == nil || plan.ui == nil || node == nil {
		return nil
	}
	compiled, err := plan.ui.page(node.relPath(), uiOwnPage)
	if err != nil {
		return nil
	}
	return compiled.page
}

// serveComponentCall runs a job of a component the page draws (reqRouter.call names it component.job, at its
// place), after the view's function answered context.bind({ … }) on vm.
func (t *Tenant) serveComponentCall(vm *runtime.VM, ctxObj *Context, reqRouter *Router, node *RouteNode, leaf *FolderRouter, handler *value.Lambda) {
	call := reqRouter.call
	response := reqRouter.response
	component, name, _ := strings.Cut(call.name, ".")
	data := response.Data()
	before := viewSnapshot(data)
	// the page's vars: its own, never sent whole, never given a revision (view_patch.go staleRevs)
	pageVars := map[string]bool{}
	for key := range call.vars {
		pageVars[key] = true
	}
	if data.IsMap() {
		if v, ok := data.Map()[compiler.ViewStateName]; ok && v.K == value.Func {
			if fn, _ := v.V.(*value.Lambda); fn != nil {
				if held := vm.ExecuteLambda(fn, nil); held.IsMap() {
					for key := range held.Map() {
						pageVars[key] = true
					}
				}
			}
		}
	}
	instance, found := t.viewInstances(vm, reqRouter, node, handler, data)[call.at]
	if !found || instance.Name != component {
		answerViewCallError(reqRouter, http.StatusNotFound, fmt.Sprintf("trang này không vẽ %s ở chỗ %s", component, call.at))
		return
	}
	run, status, message := t.runComponent(reqRouter, ctxObj, instance.Logic, component, instance.Inputs, call.scope)
	if run == nil {
		answerViewCallError(reqRouter, status, message)
		return
	}
	fields := run.bound.Map()
	var job, state *value.Lambda
	if v, ok := fields[name]; ok && v.K == value.Func {
		job, _ = v.V.(*value.Lambda)
	}
	if v, ok := fields[compiler.ViewStateName]; ok && v.K == value.Func {
		state, _ = v.V.(*value.Lambda)
	}
	if job == nil {
		answerViewCallError(reqRouter, http.StatusNotFound, fmt.Sprintf("%s không có việc %s", component, name))
		return
	}
	mineBefore := viewSnapshot(run.bound)
	clearViewAnswer(reqRouter)
	if out := run.vm.ExecuteLambda(job, call.args); out.K == value.Invalid {
		t.recordRuntimeFailure(reqRouter, run.bytecode, "component", out)
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
		answerViewCall(reqRouter, map[string]any{"seq": call.seq, "redirect": response.Data().Text()})
		return
	}
	// the instance's vars after the job: what it holds, but for what the job changed
	vars := make(map[string]value.Value, len(call.scope))
	for key, v := range call.scope {
		vars[key] = v
	}
	if state != nil {
		if after := run.vm.ExecuteLambda(state, nil); after.IsMap() {
			for key, v := range after.Map() {
				vars[key] = v
			}
		}
	}
	// the page the job left: the view's function again, holding the page's vars as they were, past the guards
	// again (view_call.go)
	reqRouter.call = &viewCall{vars: call.vars, seq: call.seq}
	if !t.guardsAgain(vm, ctxObj, reqRouter, leaf, call.seq) {
		return
	}
	stamp := nextViewStamp() // before the run that reads the page's data, under the page's lock (view_call.go)
	reqRouter.reads = map[string]bool{}
	if res := t.execTree(vm, leaf, handler, ctxObj); res.K == value.Invalid || response.Kind() != "render" {
		if res.K == value.Invalid {
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "view", res)
		}
		answerViewCallError(reqRouter, http.StatusInternalServerError, "trang không vẽ lại được sau việc "+call.name)
		return
	}
	after := response.Data()
	answer := map[string]any{"seq": call.seq}
	// the page's data the job changed, for a page that holds its data in the browser (one that runs in the
	// kernel), as a job of the page answers it (view_call.go): by name, a list by row, and whole for a name the
	// page held at another revision than the run before the job; a page the server draws whole holds none
	if page := t.viewPage(node); page != nil && page.Live() {
		answer["stamp"] = stamp // a tab takes no data read before the data it holds (call.js)
		t.answerTickets(answer, reqRouter, leaf, call.marks)
		now := viewSnapshot(after)
		if viewDataChanged(before, now, pageVars) {
			t.tellViewChanged(leaf, stamp) // the tabs listening to the page ask for what changed (view_listen.go)
		}
		patch, lists := viewPatch(before, now, t.viewListKeys(node))
		if call.revs != nil {
			if revs := staleRevs(call.revs, before, now, pageVars, patch, lists); len(revs) > 0 {
				answer["revs"] = revs
			}
		}
		if len(patch) > 0 {
			answer["patch"] = patch
		}
		if len(lists) > 0 {
			answer["lists"] = lists
		}
	}
	// the instance again, its inputs read where it stands on the page as it is now; a page that no longer
	// draws it there has nothing of it to change
	if now, ok := t.viewInstances(vm, reqRouter, node, handler, after)[call.at]; ok && now.Name == component {
		again, status, message := t.runComponent(reqRouter, ctxObj, now.Logic, component, now.Inputs, vars)
		if again == nil {
			answerViewCallError(reqRouter, status, message)
			return
		}
		if mine, _ := viewPatch(mineBefore, viewSnapshot(again.bound), nil); len(mine) > 0 {
			answer["components"] = map[string]any{call.at: map[string]any{"patch": mine}}
		}
	}
	answerViewCall(reqRouter, answer)
}
