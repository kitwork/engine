package compiler

import "github.com/kitwork/engine/value"

// ViewStateName is the hidden name the object of router.view's context.bind({ … }) gains: a function that
// reads the page's vars back after a job ran (work/view_call.go). A name the page never reads: names that
// begin with __ are not page data.
const ViewStateName = "__state"

// rewriteRouterViews readies each router.view((context) => { … }) of a router.work for the bridge (28/10/2026,
// KITWORK_ROUTER_VIEW.md). A var at the top of its function takes what the page holds when a job is called,
//
//	var title = "";   →   var title = context.viewVar("title", "");
//
// and the object it binds gains __state, () => ({ title }), read after the job: the vars the job changed go
// back to the page. The function's first parameter is the context it reads them off; a function without one
// has no state of the page. router.view is given the jobs that change nothing but the vars, read as the author
// wrote them (view_read_only.go): router.view(fn, { __readOnly: ["search"] }). Nothing else of the file changes.
func rewriteRouterViews(prog *Program) {
	for _, statement := range prog.Statements {
		line, ok := statement.(*ExpressionStatement)
		if !ok {
			continue
		}
		call := routerViewCall(line.Expression)
		if call == nil {
			continue
		}
		fn, _ := call.Arguments[0].(*FunctionLiteral)
		if names := readOnlyJobs(fn); len(names) > 0 {
			call.Arguments = append(call.Arguments, readOnlyArgument(call.Token, names))
		}
		rewriteViewFunction(fn)
	}
}

// rewriteComponentDefines readies each component.define("shop-cart", (context) => { … }) of a components.work
// the same way (28/10/2026, KITWORK_COMPONENTS.md mục 8): a var at the top of its function takes what the
// instance holds when a job of it is called, var items = [] becoming var items = context.viewVar("items", []),
// and the object it binds gains __state. The context is the component's (work/component_call.go).
func rewriteComponentDefines(prog *Program) {
	for _, statement := range prog.Statements {
		line, ok := statement.(*ExpressionStatement)
		if !ok {
			continue
		}
		if fn := componentDefineFunction(line.Expression); fn != nil {
			rewriteViewFunction(fn)
		}
	}
}

// componentDefineFunction is the function written in component.define("name", fn) at the head of a line; nil
// for any other line, and for a function defined elsewhere and passed by its name.
func componentDefineFunction(e Expression) *FunctionLiteral {
	var object Expression
	var method *Identifier
	var arguments []Expression
	switch call := e.(type) {
	case *MethodCallExpression:
		if call.Method == nil {
			return nil
		}
		object, method, arguments = call.Object, call.Method, call.Arguments
	case *CallExpression:
		member, ok := call.Function.(*MemberExpression)
		if !ok || member.Property == nil {
			return nil
		}
		object, method, arguments = member.Object, member.Property, call.Arguments
	default:
		return nil
	}
	name, ok := object.(*Identifier)
	if !ok || name.Value != "component" || method.Value != "define" || len(arguments) != 2 {
		return nil
	}
	fn, _ := arguments[1].(*FunctionLiteral)
	return fn
}

// routerViewFunction is the function of router.view(fn) at the head of a line, the rules chained after it
// (.guard(…), .limit(…)) or not; nil for any other line.
func routerViewFunction(e Expression) *FunctionLiteral {
	call := routerViewCall(e)
	if call == nil {
		return nil
	}
	fn, _ := call.Arguments[0].(*FunctionLiteral)
	return fn
}

// routerViewCall is the call router.view(fn) at the head of a line, written with one argument, a function; nil
// for any other line.
func routerViewCall(e Expression) *MethodCallExpression {
	for {
		switch node := e.(type) {
		case *ChainExpression:
			e = node.Expression
		case *MethodCallExpression:
			if name, ok := node.Object.(*Identifier); ok && name.Value == "router" {
				if node.Method == nil || node.Method.Value != "view" || len(node.Arguments) != 1 {
					return nil
				}
				if _, ok := node.Arguments[0].(*FunctionLiteral); !ok {
					return nil
				}
				return node
			}
			e = node.Object
		default:
			return nil
		}
	}
}

// rewriteViewFunction does the rewrite on one function of router.view.
func rewriteViewFunction(fn *FunctionLiteral) {
	if fn == nil || fn.Body == nil || len(fn.Parameters) == 0 {
		return
	}
	context := fn.Parameters[0]
	statements := fn.Body.Statements
	var names []string
	for i, statement := range statements {
		// var x = v is the bare word var, then the assignment (var is no keyword of the compiler yet)
		if !bareVar(statement) || i+1 >= len(statements) {
			continue
		}
		line, ok := statements[i+1].(*ExpressionStatement)
		if !ok {
			continue
		}
		assign, ok := line.Expression.(*AssignmentExpression)
		if !ok || assign.Update != NotAnUpdate {
			continue
		}
		name, ok := assign.Name.(*Identifier)
		if !ok {
			continue
		}
		assign.Value = &MethodCallExpression{
			Token:     name.Token,
			Object:    &Identifier{Token: context.Token, Value: context.Value},
			Method:    &Identifier{Token: name.Token, Value: "viewVar"},
			Arguments: []Expression{stringLiteral(name.Token, name.Value), assign.Value},
		}
		names = append(names, name.Value)
	}
	if len(names) == 0 {
		return
	}
	for _, statement := range statements {
		ret, ok := statement.(*ReturnStatement)
		if !ok {
			continue
		}
		object := boundObject(ret.ReturnValue)
		if object == nil {
			continue
		}
		state := &ObjectLiteral{Token: object.Token}
		for _, name := range names {
			state.Entries = append(state.Entries, ObjectEntry{
				Key:   stringLiteral(object.Token, name),
				Value: &Identifier{Token: object.Token, Value: name},
			})
		}
		object.Entries = append(object.Entries, ObjectEntry{
			Key: stringLiteral(object.Token, ViewStateName),
			Value: &FunctionLiteral{
				Token: object.Token,
				Body:  &BlockStatement{Token: object.Token, Statements: []Statement{&ReturnStatement{Token: object.Token, ReturnValue: state}}},
			},
		})
	}
}

// bareVar reports whether the statement is the word var alone, as `var x = 1` parses today.
func bareVar(statement Statement) bool {
	line, ok := statement.(*ExpressionStatement)
	if !ok {
		return false
	}
	name, ok := line.Expression.(*Identifier)
	return ok && name.Value == "var"
}

// boundObject is the object of .bind({ … }) that ends a return, context.title(…).bind({ … }) included.
func boundObject(e Expression) *ObjectLiteral {
	call, ok := e.(*MethodCallExpression)
	if !ok || call.Method == nil || call.Method.Value != "bind" || len(call.Arguments) != 1 {
		return nil
	}
	object, _ := call.Arguments[0].(*ObjectLiteral)
	return object
}

// stringLiteral is the string text as a literal at the place of token.
func stringLiteral(token Token, text string) *Literal {
	v := value.New(text)
	return &Literal{Token: tokenWithValue(token, String, v), Value: v}
}
