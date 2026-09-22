package work

import (
	"fmt"
	"sync"

	hydrate "github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/value"
)

// validateCache caches compiled rules (rule source → IR). Rules are code — a handful per tenant —
// so one process-wide map with no eviction is fine, and a request never pays the parse twice.
var validateCache sync.Map // string → any (nil = known-bad rule)

// Validate is ctx.validate(rule[, data]) / req.validate(rule[, data]): the server compiles a rule
// once (cached) and evaluates it (hydrate.Eval, budgeted) against the submitted data. It is the
// half that decides — a browser can be told anything, so the verdict is made here.
//
// It used to be the server end of a pair: data-kit-validate walked the compiled IR of the SAME
// rule while the visitor typed. That client directive is gone (22/09, 0 sites used it; required /
// pattern / type give the same live feedback natively, and the kernel's submit gate still blocks a
// form holding data-state="invalid"). The rule language here is unchanged.
//
//	ctx.validate("password.length >= 6 && confirm == password")   // scope = form body (JSON body if the request is JSON)
//	ctx.validate(rule, { password: p, confirm: c })               // explicit scope
//
// Fail-closed: no rule, an uncompilable rule, or an evaluation error all return false — a typo in
// a rule must never let data through.
func (r *Request) Validate(vals ...value.Value) value.Value {
	if len(vals) == 0 || !vals[0].IsString() || vals[0].String() == "" {
		return value.New(false)
	}
	rule := vals[0].String()

	node, ok := validateCache.Load(rule)
	if !ok {
		compiled, err := hydrate.Compile(rule)
		if err != nil {
			fmt.Printf("[validate] rule does not compile: %v\n", err)
			compiled = nil
		}
		validateCache.Store(rule, compiled)
		node = compiled
	}
	if node == nil {
		return value.New(false)
	}

	var scope map[string]any
	if len(vals) > 1 && vals[1].IsMap() {
		scope = valueMapToScope(vals[1])
	} else {
		scope = r.validationScope()
	}

	out, err := hydrate.Eval(node, scope)
	if err != nil {
		fmt.Printf("[validate] eval: %v\n", err)
		return value.New(false)
	}
	return value.New(hydrate.Truthy(out))
}

// Validate on the context is the same call — handlers written as (ctx) => … and (req, res) => …
// both reach it.
func (c *Context) Validate(vals ...value.Value) value.Value { return c.request.Validate(vals...) }

// validationScope builds the Eval scope from the request: the JSON body when the request is JSON,
// otherwise the parsed form (POST fields + query). Form values are strings — the same shape the
// client walker read from input.value, so both ends judge identical data.
func (r *Request) validationScope() map[string]any {
	if r.IsJSON().Truthy() {
		if j := r.JSON(); j.IsMap() {
			return valueMapToScope(j)
		}
	}
	return valueMapToScope(r.FormParams())
}

func valueMapToScope(v value.Value) map[string]any {
	m := v.Map()
	scope := make(map[string]any, len(m))
	for k, item := range m {
		scope[k] = item.Interface()
	}
	return scope
}
