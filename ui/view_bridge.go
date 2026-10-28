package ui

import (
	"regexp"
	"strings"

	logic "github.com/kitwork/engine/compiler"
)

// The page side of router.view's bridge (28/10/2026, KITWORK_ROUTER_VIEW.md; work/view_call.go is the server's).
// Until the page runs in the kernel (KITWORK_UI.md mục 15, "trang chạy ở client"), a page drawn by router.view
// reaches its jobs through one small file, /_kitwork/call.<hash>.js (jit/hydrate/call.js):
//
//	<button click="remove(todo.id)">        →  data-kitwork-job-click="remove" data-kitwork-args-click="[3]"
//	<input value="{{ title }}">             →  data-kitwork-var="title"
//
// An event outside every component that calls a job of the page, job(a, b), is written as the job's name and
// its arguments, read on the server where the element is drawn (a row's todo.id is that row's), as JSON. A
// field whose value is a var of the view's function is marked with the var's name: the file sends what each
// marked field holds as the page's vars, and writes back the vars the job changed. Any other event of the
// page still waits for the page to run in the kernel.

// jobCallRe is an event that calls one function: name(arguments), a ; after it or not.
var jobCallRe = regexp.MustCompile(`^\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(([\s\S]*)\)\s*;?\s*$`)

// bareNameRe is an attribute value that is one name in {{ }}: {{ title }}.
var bareNameRe = regexp.MustCompile(`^\s*\{\{\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\}\}\s*$`)

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

// viewJobEvents writes the events of an element that call a job of router.view's page as the bridge reads
// them, and returns the others. Outside every component the kernel runs only.
func (c *compiler) viewJobEvents(n *Node, events []Attr, o *elemOp) ([]Attr, error) {
	if !c.given.view || len(c.given.jobs) == 0 {
		return events, nil
	}
	var rest []Attr
	seen := map[string]bool{}
	for i := range events {
		a := &events[i]
		match := jobCallRe.FindStringSubmatch(a.Value)
		if match == nil || !c.given.jobs[match[1]] {
			rest = append(rest, *a)
			continue
		}
		event, modifiers, _ := strings.Cut(a.Name, ":")
		if modifiers != "" || strings.Contains(event, ",") {
			return nil, errAttr(n, a, "%s gọi việc %s của trang: cầu nối hôm nay nhận một sự kiện không có phần đi kèm (click, change, submit, input)", a.Name, match[1])
		}
		if seen[event] {
			return nil, errAttr(n, a, "%s viết hai lần trên <%s>: giữ một", event, n.Tag)
		}
		seen[event] = true
		in, err := parseInterpolation("{{ ["+match[2]+"] }}", n, a)
		if err != nil {
			return nil, err
		}
		name, err := parseInterpolation(match[1], n, a)
		if err != nil {
			return nil, err
		}
		// both written where the element is drawn: the static attributes are already set by now
		o.dynAttrs = append(o.dynAttrs, dynAttr{name: "data-kitwork-job-" + event, in: name},
			dynAttr{name: "data-kitwork-args-" + event, in: in, json: true})
	}
	return rest, nil
}

// viewVarField marks a field whose value is a var of router.view's function, {{ title }}, with the var's name.
func (c *compiler) viewVarField(htmlName, source string, w *strings.Builder) {
	if !c.given.view || htmlName != "value" && htmlName != "checked" {
		return
	}
	if match := bareNameRe.FindStringSubmatch(source); match != nil && c.given.vars[match[1]] {
		writeAttr(w, "data-kitwork-var", match[1])
	}
}
