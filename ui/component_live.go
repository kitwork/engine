package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	logic "github.com/kitwork/engine/compiler"
)

// A component of the site whose jobs go through the bridge (28/10/2026, KITWORK_COMPONENTS.md mục 8,
// docs/KITWORK_BRIDGE.md). Its function answers context.bind({ … }) and that object holds a job or a var:
//
//	component.define("shop-cart", (context) => {
//	  const { shippingFee = 15000 } = context.attributes();
//	  var items = [];
//	  const checkout = () => { … };
//	  return context.bind({ items, shippingFee, checkout });
//	});
//
// Then the browser runs it as the kernel runs one of Kitwork's (delivered): its host carries
// data-kitwork-component and the place it is called from, data-kitwork-at (an id fixed when the page is
// built), and what is written inside it reads its names live. Each job is a function call.js registers
// with the kernel; calling it is a POST to the page's URL naming shop-cart.checkout and that place, and the
// answer is the data of the instance the job changed (work/component_call.go). Its inputs are never sent:
// the server draws the page again to read them where the call stands (Page.Instances).
//
// As the page is drawn, the server runs the function of every component of the site whose logic answers a
// bind (Request.Component): a const computed on the server is right at the first paint, and the host of one
// the browser runs carries the data of its bind for the kernel to start the instance from (instancePairs).
// Drawn without (Page.Instances, a test), a name takes the value its source writes out, nil for one computed
// as it runs. A call of it inside a for (one instance per row, found by a fingerprint) is not there yet.

// logicNames is what a component's logic declares, as the page reads it from its source.
type logicNames struct {
	state    map[string]any
	bound    bool
	jobs     map[string]bool
	vars     map[string]bool
	inputs   map[string]any
	required map[string]bool
	// problem is the first name of the bind the bridge cannot carry (a var bound under another name, a spread):
	// an error once the component is live
	problem *logicProblem
}

// logicProblem is an error in a logic file, at a byte of its source.
type logicProblem struct {
	position int32
	message  string
}

// live reports whether the logic binds a job or a var: the browser runs it.
func (l *logicNames) live() bool {
	return l != nil && l.bound && (len(l.jobs) > 0 || len(l.vars) > 0)
}

// take sets what the logic declares on the component.
func (d *componentDef) take(names *logicNames) {
	if names == nil {
		return
	}
	d.state, d.bound = names.state, names.bound
	d.jobs, d.vars, d.inputs, d.required = names.jobs, names.vars, names.inputs, names.required
}

// live reports whether the component is the site's and its bind holds a job or a var: the browser runs it,
// its jobs through the bridge.
func (d *componentDef) live() bool {
	return d.kitwork == nil && d.logic != "" && d.bound && (len(d.jobs) > 0 || len(d.vars) > 0)
}

// takes reports whether an attribute at the call names something the component takes in: what
// context.attributes() takes apart, or, for a function that answers no bind, a name it declares.
func (d *componentDef) takes(name string) bool {
	if d.bound {
		_, ok := d.inputs[name]
		return ok
	}
	_, ok := d.state[name]
	return ok
}

// boundNames reads a component's function that answers context.bind({ … }) at its end; ok false for one
// that does not. state holds the names of that object with the value the source writes for each, nil for a
// job and for a value only the run knows (the page draws that one from the run, Request.Component).
func boundNames(fn *logic.FunctionLiteral) (*logicNames, bool) {
	if len(fn.Parameters) == 0 || len(fn.Body.Statements) == 0 {
		return nil, false
	}
	context := fn.Parameters[0].Value
	statements := fn.Body.Statements
	ret, ok := statements[len(statements)-1].(*logic.ReturnStatement)
	if !ok {
		return nil, false
	}
	call, ok := ret.ReturnValue.(*logic.MethodCallExpression)
	if !ok || call.Method == nil || call.Method.Value != "bind" || !isIdentifier(call.Object, context) || len(call.Arguments) != 1 {
		return nil, false
	}
	object, ok := call.Arguments[0].(*logic.ObjectLiteral)
	if !ok {
		return nil, false
	}
	names := &logicNames{state: map[string]any{}, bound: true, jobs: map[string]bool{}, vars: map[string]bool{},
		inputs: map[string]any{}, required: map[string]bool{}}
	functions := map[string]bool{}
	consts := map[string]logic.Expression{}
	vars := map[string]logic.Expression{}
	inputOf := map[string]string{} // the local name an input is taken apart into → the input's name
	for i, statement := range statements[:len(statements)-1] {
		switch s := statement.(type) {
		case *logic.VarStatement:
			if s.Pattern != nil && attributesCall(s.Value, context) {
				for _, element := range s.Pattern.Elements {
					if element.Name == nil {
						continue
					}
					key := element.Key
					if key == "" {
						key = element.Name.Value
					}
					inputOf[element.Name.Value] = key
					if element.Default == nil {
						names.inputs[key], names.required[key] = nil, true
						continue
					}
					names.inputs[key], _ = writtenValue(element.Default)
				}
				continue
			}
			if s.DestructMode != logic.DestructNone || len(s.Names) != 1 {
				continue
			}
			if _, ok := s.Value.(*logic.FunctionLiteral); ok {
				functions[s.Names[0].Value] = true
				continue
			}
			consts[s.Names[0].Value] = s.Value
		case *logic.ExpressionStatement:
			// var x = v is the bare word var, then the assignment (var is no keyword of the compiler yet)
			if !isIdentifier(s.Expression, "var") || i+1 >= len(statements)-1 {
				continue
			}
			if line, ok := statements[i+1].(*logic.ExpressionStatement); ok {
				if assign, ok := line.Expression.(*logic.AssignmentExpression); ok {
					if target, ok := assign.Name.(*logic.Identifier); ok {
						vars[target.Value] = assign.Value
					}
				}
			}
		}
	}
	unknown := func(at logic.Token, format string, args ...any) {
		if names.problem == nil {
			names.problem = &logicProblem{position: at.Position, message: fmt.Sprintf(format, args...)}
		}
	}
	for _, entry := range object.Entries {
		if entry.IsSpread {
			unknown(object.Token, "bind của component nhận object viết tại chỗ, từng tên một ({ items, checkout }), không trải (...)")
			continue
		}
		key, at := "", object.Token
		switch k := entry.Key.(type) {
		case *logic.Identifier:
			key, at = k.Value, k.Token
		case *logic.Literal:
			if k.Value.IsString() {
				key, at = k.Value.Text(), k.Token
			}
		}
		if key == "" {
			continue
		}
		switch v := entry.Value.(type) {
		case *logic.FunctionLiteral:
			names.jobs[key], names.state[key] = true, nil
			continue
		case *logic.Identifier:
			switch initial, isVar := vars[v.Value]; {
			case functions[v.Value]:
				names.jobs[key], names.state[key] = true, nil
				continue
			case isVar:
				if key != v.Value {
					unknown(at, "var %s vào bind dưới tên %s: cầu nối gửi một var theo tên của nó; viết { %s }", v.Value, key, v.Value)
					continue
				}
				names.vars[key] = true
				names.state[key], _ = writtenValue(initial)
				continue
			case inputOf[v.Value] != "":
				input := inputOf[v.Value]
				if input != key {
					unknown(at, "đầu vào %s vào bind dưới tên %s: trang vẽ giá trị ở chỗ gọi dưới tên của đầu vào; viết { %s }", input, key, input)
					continue
				}
				names.state[key] = names.inputs[input]
				continue
			case consts[v.Value] != nil:
				names.state[key], _ = writtenValue(consts[v.Value])
				continue
			}
		}
		names.state[key], _ = writtenValue(entry.Value)
	}
	return names, true
}

// attributesCall reports whether e is context.attributes(), context being the function's parameter.
func attributesCall(e logic.Expression, context string) bool {
	call, ok := e.(*logic.MethodCallExpression)
	return ok && call.Method != nil && call.Method.Value == "attributes" && isIdentifier(call.Object, context) && len(call.Arguments) == 0
}

// lineColumn is the line and column of a byte of a source, columns counted in runes like the parser.
func lineColumn(source string, position int32) (int, int) {
	if position < 0 || int(position) > len(source) {
		return 1, 1
	}
	line, column := 1, 1
	for _, r := range source[:position] {
		if r == '\n' {
			line, column = line+1, 1
			continue
		}
		column++
	}
	return line, column
}

// liveSiteCall checks the call of a component the browser runs through the bridge and gives it its place on the
// page: the id its host carries and its jobs name on the wire.
func (c *compiler) liveSiteCall(n *Node, cx ctx, site *callSite) error {
	def := site.def
	if !def.live() {
		return nil
	}
	if def.root != nil {
		return errAt(n, "%s có việc đi qua cầu nối, mà giao diện của nó là %s: lát này chỉ nhận giao diện viết bên trong thẻ (<%s>…</%s>)",
			def.name, rel(c.site, def.ui), def.name, def.name)
	}
	if site.forEach != nil || cx.forAt != nil {
		return errAt(n, "%s có việc đi qua cầu nối, mà chỗ gọi này lặp theo for: mỗi dòng một instance, và server tìm đúng instance đã bấm bằng dấu vân tay của nó, phần engine chưa có. Đặt <%s> ngoài for",
			def.name, def.name)
	}
	if !c.given.view {
		return errAt(n, "%s có việc đi qua cầu nối, cầu nối của router.view: trang này không vẽ bằng router.view((context) => context.bind({ … })) nên việc của %s không có chỗ để gọi",
			def.name, def.name)
	}
	site.at = "c" + strconv.Itoa(c.next("component-call"))
	if c.out.components == nil {
		c.out.components = &liveComponents{defs: map[string]*componentDef{}}
	}
	c.out.components.defs[def.name] = def
	return nil
}

// missingInputs tells an input the component requires that its call does not pass.
func (c *compiler) missingInputs(n *Node, def *componentDef, passed map[string]bool) error {
	if !def.bound {
		return nil
	}
	var missing []string
	for name := range def.required {
		if !passed[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return errAt(n, "<%s> thiếu đầu vào %s: %s khai nó trong context.attributes() mà không có mặc định",
		def.name, strings.Join(missing, ", "), def.name)
}

// liveComponents are the components of the site a page holds that the browser runs: what call.js registers
// with the kernel, written once at the end of <content /> as data-kitwork-components.
type liveComponents struct {
	defs map[string]*componentDef
}

// write writes each component's starting state (the values its source writes out), its jobs and its vars.
func (l *liveComponents) write(b *strings.Builder) {
	if l == nil || len(l.defs) == 0 {
		return
	}
	out := make(map[string]any, len(l.defs))
	for name, def := range l.defs {
		state := map[string]any{}
		jobs, vars := []string{}, []string{}
		for key, v := range def.state {
			if def.jobs[key] {
				jobs = append(jobs, key)
				continue
			}
			state[key] = v
			if def.vars[key] {
				vars = append(vars, key)
			}
		}
		sort.Strings(jobs)
		sort.Strings(vars)
		out[name] = map[string]any{"state": state, "jobs": jobs, "vars": vars}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return
	}
	b.WriteString(`<script type="application/json" data-kitwork-components>`)
	b.Write(data) // json.Marshal writes <, > and & as <…: nothing here closes the script
	b.WriteString(`</script>`)
}

// Instance is one call of a component the browser runs, as one render of the page draws it: its name, the
// file of its logic, and the inputs the call passes, read where it stands.
type Instance struct {
	Name   string
	Logic  string
	Inputs map[string]any
}

// Instances draws the page for a request and gives each component it draws whose jobs go through the bridge,
// by the place it is called from (data-kitwork-at): how the server finds the inputs of the instance a job is
// called on, never from what the browser sends (work/component_call.go).
func (p *Page) Instances(req Request) map[string]Instance {
	instances := map[string]Instance{}
	p.renderWith(req, instances)
	return instances
}

// runInstance runs the component of a call for the request (Request.Component), with the inputs the call
// passes; nil when the page is drawn without (Page.Instances, a test) or the call is no such component.
func (o *elemOp) runInstance(r *run, values []any) map[string]any {
	if o.instanceOf == nil || r.component == nil {
		return nil
	}
	inputs := make(map[string]any, len(o.bindings))
	for i, b := range o.bindings {
		inputs[b.name] = values[i]
	}
	return r.component(Instance{Name: o.instanceOf.name, Logic: o.instanceOf.logic, Inputs: inputs})
}

// instancePairs writes, for the kernel to start the instance from, every name of the data of its bind as its
// function gave it this request, over the values its source writes out (call.js registers those): a const
// computed on the server, a var's first value, an input.
func (o *elemOp) instancePairs(own map[string]any) string {
	names := make([]string, 0, len(own))
	for name := range own {
		if _, bound := o.instanceOf.state[name]; bound && !o.instanceOf.jobs[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+": "+literal(own[name]))
	}
	return strings.Join(pairs, "; ")
}

// noteInstance records the instance of a call as the page draws it. One under an if that is false for this
// render is none: on a page that runs in the kernel such an if is drawn hidden, so that the browser can show it
// without a reload, but the page does not draw it for this request, and its jobs are not this caller's to call
// (<box if="isAdmin"><admin-tools>…</admin-tools></box>). Once the data makes the if true, the page the server
// draws for the call holds it, and its jobs answer.
func (o *elemOp) noteInstance(r *run, values []any) {
	if o.at == "" || r.instances == nil || r.falseIfs > 0 {
		return
	}
	inputs := make(map[string]any, len(o.bindings))
	for i, b := range o.bindings {
		inputs[b.name] = values[i]
	}
	r.instances[o.at] = Instance{Name: o.instanceOf.name, Logic: o.instanceOf.logic, Inputs: inputs}
}

// readsLiveSite reports whether an expression reads a name of a site component the browser runs.
func (f *frame) readsLiveSite(e *expr) bool {
	for _, name := range usedNames(e.ir) {
		if d := f.declaring(name); d != nil && d.component != nil && d.component.live() {
			return true
		}
	}
	return false
}
