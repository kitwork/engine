package ui

import (
	logic "github.com/kitwork/engine/compiler"
)

// What the page reads of router.view's function (28/10/2026, KITWORK_ROUTER_VIEW.md): the jobs of its bind and
// its vars. A page whose bind holds either runs in the kernel (page_live.go); work/view_call.go answers its jobs.

// viewBridgeNames are the jobs and the vars of a router.work's router.view: the names of its bind whose
// value is a function (written there, or a const of the function holding one), and the vars at the top of
// its function (var x = …, which parses as the word var and an assignment).
func viewBridgeNames(statements []logic.Statement) (jobs, vars map[string]bool) {
	jobs, vars = map[string]bool{}, map[string]bool{}
	for _, statement := range statements {
		fn := routerViewFunction(statement)
		if fn == nil || fn.Body == nil {
			continue
		}
		functions := map[string]bool{}
		body := fn.Body.Statements
		for i, s := range body {
			switch n := s.(type) {
			case *logic.VarStatement:
				if _, ok := n.Value.(*logic.FunctionLiteral); ok && len(n.Names) == 1 {
					functions[n.Names[0].Value] = true
				}
			case *logic.ExpressionStatement:
				if name, ok := n.Expression.(*logic.Identifier); ok && name.Value == "var" && i+1 < len(body) {
					if line, ok := body[i+1].(*logic.ExpressionStatement); ok {
						if assign, ok := line.Expression.(*logic.AssignmentExpression); ok {
							if target, ok := assign.Name.(*logic.Identifier); ok {
								vars[target.Value] = true
							}
						}
					}
				}
			}
		}
		for _, s := range body {
			ret, ok := s.(*logic.ReturnStatement)
			if !ok {
				continue
			}
			call, ok := ret.ReturnValue.(*logic.MethodCallExpression)
			if !ok || call.Method == nil || call.Method.Value != "bind" || len(call.Arguments) != 1 {
				continue
			}
			object, ok := call.Arguments[0].(*logic.ObjectLiteral)
			if !ok {
				continue
			}
			for _, entry := range object.Entries {
				key := ""
				switch k := entry.Key.(type) {
				case *logic.Identifier:
					key = k.Value
				case *logic.Literal:
					if k.Value.IsString() {
						key = k.Value.Text()
					}
				}
				if key == "" || entry.IsSpread {
					continue
				}
				switch v := entry.Value.(type) {
				case *logic.FunctionLiteral:
					jobs[key] = true
				case *logic.Identifier:
					if functions[v.Value] {
						jobs[key] = true
					}
				}
			}
		}
	}
	return jobs, vars
}

// routerViewFunction is the function of router.view(fn) a line begins with, the rules chained after it or
// not; nil for any other line.
func routerViewFunction(statement logic.Statement) *logic.FunctionLiteral {
	line, ok := statement.(*logic.ExpressionStatement)
	if !ok {
		return nil
	}
	current := line.Expression
	for {
		switch node := current.(type) {
		case *logic.ChainExpression:
			current = node.Expression
		case *logic.MethodCallExpression:
			if isIdentifier(node.Object, "router") {
				if node.Method == nil || node.Method.Value != "view" || len(node.Arguments) != 1 {
					return nil
				}
				fn, _ := node.Arguments[0].(*logic.FunctionLiteral)
				return fn
			}
			current = node.Object
		default:
			return nil
		}
	}
}
