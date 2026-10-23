package work

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/kitwork/engine/value"
)

// ── what a routine's function reads from outside (KITWORK_ROUTINE.md mục 3, 4) ───────────────────
//
// A function handed to routine.go or routine.wait runs on another goroutine, in a VM of its own. What it
// reads from outside (its closure: the variables of the handler around it, the file's top level, and the
// objects and arrays they hold) is copied when go() or wait() is called: a copy is the function's alone.
// Without it two functions of one wait writing one outer variable, or one object, would write one Go map
// from two goroutines, a fault no recover() catches, which ends the process and every app in it. With it a
// function's write to an outer variable is never seen by the caller: a routine gives back by return.
//
// The copy is deep and keeps sharing as it was: one object reached twice is one copy reached twice, a
// cycle stays a cycle, two closures over one variable still share it. It walks with a list of its own,
// never Go's stack, so data nested however deep cannot take the host down. Objects of the host (a module,
// a database handle, a routine's handle) are shared: each guards itself. The objects of the request
// (context, request, response, a view being built) are not the routine's: a function of go sees one that
// refuses everything, since the request may be over when it runs; a function of wait sees one that reads
// one function at a time and refuses to answer (routineRequestView). A method of one of them read without
// its call (const header = context.header) is seen with its object, through the same view.

// routineCopy is one copy: what is copied already, and what is left to fill.
type routineCopy struct {
	maps    map[uintptr]map[string]value.Value
	arrays  map[*[]value.Value]*[]value.Value
	lambdas map[*value.Lambda]*value.Lambda
	steps   []routineCopyStep
	// request is how the objects of the request are seen; nil leaves them as they are (a result read
	// again from a handle holds none of the request's but views already).
	request *routineRequestRules
}

// routineCopyStep is a container copied but not filled yet: one of the three is set.
type routineCopyStep struct {
	fromMap    map[string]value.Value
	toMap      map[string]value.Value
	fromArray  []value.Value
	toArray    *[]value.Value
	fromLambda *value.Lambda
	toLambda   *value.Lambda
}

func newRoutineCopy(request *routineRequestRules) *routineCopy {
	return &routineCopy{
		maps:    map[uintptr]map[string]value.Value{},
		arrays:  map[*[]value.Value]*[]value.Value{},
		lambdas: map[*value.Lambda]*value.Lambda{},
		request: request,
	}
}

// copyRoutineLambda is fn and everything it reaches, copied, with the request's objects seen as request
// says.
func copyRoutineLambda(fn *value.Lambda, request *routineRequestRules) *value.Lambda {
	c := newRoutineCopy(request)
	copied := c.lambda(fn)
	c.fill()
	return copied
}

// copyRoutineValue is item and everything it reaches, copied.
func copyRoutineValue(item value.Value) value.Value {
	c := newRoutineCopy(nil)
	copied := c.value(item)
	c.fill()
	return copied
}

// value is item with its container copied (filled later, by fill).
func (c *routineCopy) value(item value.Value) value.Value {
	switch item.K {
	case value.Map:
		if source, ok := item.V.(map[string]value.Value); ok && source != nil {
			item.V = c.mapOf(source)
		}
	case value.Array:
		switch source := item.V.(type) {
		case *[]value.Value:
			if source != nil {
				item.V = c.arrayOf(source)
			}
		case []value.Value:
			copied := make([]value.Value, len(source))
			c.steps = append(c.steps, routineCopyStep{fromArray: source, toArray: &copied})
			item.V = &copied
		}
	case value.Func:
		switch function := item.V.(type) {
		case *value.Lambda:
			if function != nil {
				item.V = c.lambda(function)
			}
		case *value.BoundMethod:
			// a method of an object of the request read without its call (const header = context.header) is
			// seen as its object is: called, it goes through the object's view
			if c.request != nil && function != nil {
				if seen, ok := c.request.view(function.Object); ok {
					if view, isView := seen.V.(*routineRequestView); isView {
						return view.method(function.Name)
					}
				}
			}
		}
	case value.Struct, value.Proxy:
		if c.request != nil {
			if seen, ok := c.request.view(item); ok {
				return seen
			}
		}
	}
	return item
}

func (c *routineCopy) mapOf(source map[string]value.Value) map[string]value.Value {
	key := reflect.ValueOf(source).Pointer()
	if copied, ok := c.maps[key]; ok {
		return copied
	}
	copied := make(map[string]value.Value, len(source))
	c.maps[key] = copied
	c.steps = append(c.steps, routineCopyStep{fromMap: source, toMap: copied})
	return copied
}

func (c *routineCopy) arrayOf(source *[]value.Value) *[]value.Value {
	if copied, ok := c.arrays[source]; ok {
		return copied
	}
	items := make([]value.Value, len(*source))
	copied := &items
	c.arrays[source] = copied
	c.steps = append(c.steps, routineCopyStep{fromArray: *source, toArray: copied})
	return copied
}

func (c *routineCopy) lambda(source *value.Lambda) *value.Lambda {
	if source == nil {
		return nil
	}
	if copied, ok := c.lambdas[source]; ok {
		return copied
	}
	copied := &value.Lambda{
		Address:      source.Address,
		Name:         source.Name,
		SourceFile:   source.SourceFile,
		SourceLine:   source.SourceLine,
		SourceColumn: source.SourceColumn,
		Params:       source.Params, // never written after the lambda is made
		Program:      source.Program,
	}
	c.lambdas[source] = copied
	c.steps = append(c.steps, routineCopyStep{fromLambda: source, toLambda: copied})
	return copied
}

// fill fills every container copied, and those it reaches, until none is left.
func (c *routineCopy) fill() {
	for len(c.steps) > 0 {
		step := c.steps[len(c.steps)-1]
		c.steps = c.steps[:len(c.steps)-1]
		switch {
		case step.toMap != nil:
			for key, item := range step.fromMap {
				step.toMap[key] = c.value(item)
			}
		case step.toArray != nil:
			for index, item := range step.fromArray {
				(*step.toArray)[index] = c.value(item)
			}
		case step.toLambda != nil:
			if step.fromLambda.Scope != nil {
				step.toLambda.Scope = c.mapOf(step.fromLambda.Scope)
			}
			step.toLambda.Parent = c.lambda(step.fromLambda.Parent)
		}
	}
}

// ── the request, as a routine's function sees it ──────────────────────────────────────────────────

// routineRequestRules is how the functions of one go() or one wait() see the request's objects. detached
// is go's: nothing of the request is read. Otherwise lock is the wait's, one for all its functions: they
// run at once, and the request's objects keep what they read once (the body, the form), unguarded.
type routineRequestRules struct {
	detached bool
	lock     *sync.Mutex
}

// view is item as the routine sees it, when item is an object of the request.
func (r *routineRequestRules) view(item value.Value) (value.Value, bool) {
	if seen, ok := item.V.(*routineRequestView); ok {
		if r.detached && !seen.detached {
			// a function of go started inside a function of wait: the request may be over when it runs
			return value.Value{K: value.Proxy, V: &routineRequestView{target: seen.target, what: seen.what, detached: true}}, true
		}
		// a wait inside a function of wait: its functions read under the same lock as their siblings'
		return item, true
	}
	what := requestObjectName(item)
	if what == "" {
		return item, false
	}
	return value.Value{K: value.Proxy, V: &routineRequestView{target: item, what: what, detached: r.detached, lock: r.lock}}, true
}

// The objects of the request give their methods read without a call as values that name them
// (value.BoundMethod): the copy then sees const header = context.header as context's, not as a Go
// function it cannot tell from any other.
func init() {
	value.BindMethods((*Context)(nil), (*Request)(nil), (*Response)(nil), (*ViewBuilder)(nil))
}

// requestObjectName is how a program names an object of the request ("context", "request", "response",
// the "view" a template site builds), "" for any other value.
func requestObjectName(item value.Value) string {
	if item.K != value.Struct {
		return ""
	}
	switch item.V.(type) {
	case *Context:
		return "context"
	case *Request:
		return "request"
	case *Response:
		return "response"
	case *ViewBuilder:
		return "view"
	}
	return ""
}

// routineContextAnswers are the members of context that answer the request or edit its answer: a
// function of wait leaves them to the caller. header, headers and cookie answer only with a value to set.
var routineContextAnswers = map[string]bool{
	"json": true, "html": true, "text": true, "svg": true, "css": true, "status": true, "redirect": true,
	"file": true, "error": true, "view": true, "bind": true, "data": true, "meta": true, "jsonld": true,
	"title": true, "description": true, "image": true, "notfound": true, "type": true, "setcookie": true,
	"deletecookie": true, "savefile": true, "response": true, "res": true,
}

// routineRequestView is an object of the request as a function of a routine sees it.
type routineRequestView struct {
	target   value.Value
	what     string
	detached bool
	lock     *sync.Mutex
}

// refused is the error of using member of the request from a routine's function, "" when it may be read.
func (v *routineRequestView) refused(member string, args []value.Value) string {
	if v.detached {
		return fmt.Sprintf("%s.%s trong hàm của routine.go: `go` chạy sau khi request xong: lấy giá trị cần trước rồi đưa vào", v.what, member)
	}
	folded := strings.ToLower(member)
	answers := false
	switch v.what {
	case "response", "view":
		answers = true
	case "context":
		answers = routineContextAnswers[folded] ||
			(folded == "header" && len(args) > 1) || (folded == "headers" && len(args) > 0) || (folded == "cookie" && len(args) > 1)
	case "request":
		answers = folded == "savefile"
	}
	if answers {
		return fmt.Sprintf("%s.%s trong hàm của routine.wait: trả lời ở hàm gọi routine.wait, sau khi nó trả kết quả", v.what, member)
	}
	for _, argument := range args {
		if lambda, ok := argument.V.(*value.Lambda); ok && lambda != nil {
			return fmt.Sprintf("%s.%s(hàm) trong hàm của routine.wait: một hàm của request chỉ chạy ở hàm gọi routine.wait", v.what, member)
		}
	}
	return ""
}

func (v *routineRequestView) OnGet(key string) value.Value {
	if message := v.refused(key, nil); message != "" {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	return v.seen(key, v.locked(func() value.Value { return v.target.Get(key) }))
}

func (v *routineRequestView) OnInvoke(method string, args ...value.Value) value.Value {
	if message := v.refused(method, args); message != "" {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	return v.seen(method, v.locked(func() value.Value { return v.target.Invoke(method, args...) }))
}

func (v *routineRequestView) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

// method is member of the object read without its call, as the routine sees it: called, it is a call of
// member through this view, under its rules.
func (v *routineRequestView) method(member string) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value { return v.OnInvoke(member, args...) })
}

// locked runs read under the wait's lock, released whatever read does (a host call that panics ends the
// function, never its siblings).
func (v *routineRequestView) locked(read func() value.Value) value.Value {
	v.lock.Lock()
	defer v.lock.Unlock()
	return read()
}

// seen is what a read gave, as the routine sees it: an object of the request is a view under the same
// rules, a method of one read without its call (context.query) runs under the lock when called.
func (v *routineRequestView) seen(member string, read value.Value) value.Value {
	if what := requestObjectName(read); what != "" {
		return value.Value{K: value.Proxy, V: &routineRequestView{target: read, what: what, lock: v.lock}}
	}
	if read.K == value.Func {
		if _, isLambda := read.V.(*value.Lambda); !isLambda {
			method := read
			return value.NewFunc(func(args ...value.Value) value.Value {
				if message := v.refused(member, args); message != "" {
					return value.InvalidFailure(value.InvalidArgumentCode, message)
				}
				return v.seen(member, v.locked(func() value.Value { return method.Call(member, args...) }))
			})
		}
	}
	return read
}
