package compiler

import "strings"

// ViewReadOnlyName is the key of what the compiler gives router.view((context) => { … }) as a second argument,
// { __readOnly: ["search"] }: the jobs of the page that change nothing but the page's vars (29/10/2026). A call
// of one waits for no other call of the page (work/view_call.go): a search typed while another person's tick
// is out goes at once. The author never writes it.
const ViewReadOnlyName = "__readOnly"

// readOnlyJobs are the jobs of the view's function, by the names it binds, whose source writes nothing but the
// page's vars and its own names: each assignment's target is a var of the page or a name the job declares, a
// member of one of them, and the job calls nothing but a method of a value its caller sent (text.trim()), or of
// a name made of those. A job that calls a function, or a method of anything else (list.set(…), db.insert(…),
// context.redirect(…)), or does what this does not know, is not one: it waits for the page's lock, as every
// job did.
//
// The function is read as a whole: a job is one only when every bind of its name is, each written in the bind
// or named there by a const at the top of the function that no other name of it shadows. A function one of
// whose returns is not .bind({ … }) written out has none.
func readOnlyJobs(fn *FunctionLiteral) []string {
	if fn == nil || fn.Body == nil {
		return nil
	}
	top := fn.Body.Statements
	vars := map[string]bool{}
	functions := map[string]*FunctionLiteral{}
	for i, statement := range top {
		if bareVar(statement) && i+1 < len(top) {
			if line, ok := top[i+1].(*ExpressionStatement); ok {
				if assign, ok := line.Expression.(*AssignmentExpression); ok {
					if name, ok := assign.Name.(*Identifier); ok {
						vars[name.Value] = true
					}
				}
			}
		}
		if declared, ok := statement.(*VarStatement); ok && declared.Token.Kind == Const && !declared.Function &&
			declared.Pattern == nil && declared.DestructMode == DestructNone && len(declared.Names) == 1 {
			if job, ok := declared.Value.(*FunctionLiteral); ok {
				functions[declared.Names[0].Value] = job
			}
		}
	}
	walk := viewWalk{declared: map[string]int{}}
	walk.statements(top)
	if walk.unknown {
		return nil
	}
	verdict := map[string]bool{}
	var order []string
	for _, object := range walk.binds {
		for _, entry := range object.Entries {
			name := entryName(entry)
			if name == "" || strings.HasPrefix(name, "__") {
				continue
			}
			var job *FunctionLiteral
			switch v := entry.Value.(type) {
			case *FunctionLiteral:
				job = v
			case *Identifier:
				if walk.declared[v.Value] == 1 {
					job = functions[v.Value]
				}
			}
			was, seen := verdict[name]
			if !seen {
				order = append(order, name)
				was = true
			}
			verdict[name] = was && job != nil && writesOnlyVars(job, vars)
		}
	}
	var names []string
	for _, name := range order {
		if verdict[name] {
			names = append(names, name)
		}
	}
	return names
}

// viewWalk goes through the view's function, not into the functions written in it: the names it declares,
// how many times each, and the object of each of its returns; unknown when one returns anything else.
type viewWalk struct {
	declared map[string]int
	binds    []*ObjectLiteral
	unknown  bool
}

func (w *viewWalk) statements(statements []Statement) {
	for _, s := range statements {
		w.statement(s)
	}
}

func (w *viewWalk) statement(s Statement) {
	switch s := s.(type) {
	case *VarStatement:
		for _, name := range s.Names {
			w.declared[name.Value]++
		}
	case *ReturnStatement:
		if object := boundObject(s.ReturnValue); object != nil {
			w.binds = append(w.binds, object)
		} else {
			w.unknown = true
		}
	case *BlockStatement:
		w.statements(s.Statements)
	case *GroupStatement:
		w.statements(s.Statements)
	case *ExpressionStatement:
		if branch, ok := s.Expression.(*IfExpression); ok && branch.Consequence != nil {
			w.statement(branch.Consequence)
			if branch.Alternative != nil {
				w.statement(branch.Alternative)
			}
		}
	case *ForStatement:
		if s.Item != nil {
			w.declared[s.Item.Value]++
		}
		if s.Body != nil {
			w.statement(s.Body)
		}
	case *ForRangeStatement:
		w.declared[s.Counter]++
		if s.Body != nil {
			w.statement(s.Body)
		}
	case *SwitchStatement:
		for _, clause := range s.Cases {
			w.statements(clause.Statements)
		}
	}
}

// entryName is the name an entry of an object literal is written under, "" for a spread or a computed key.
func entryName(entry ObjectEntry) string {
	if entry.IsSpread {
		return ""
	}
	switch key := entry.Key.(type) {
	case *Identifier:
		return key.Value
	case *Literal:
		if key.Value.IsString() {
			return key.Value.Text()
		}
	}
	return ""
}

// readOnlyCheck walks one job. Its scopes hold the names the job declares, inner last, each true when its value
// is plain: a value its caller sent (a parameter: JSON, never a handle on data) or made only of those.
type readOnlyCheck struct {
	vars   map[string]bool
	scopes []map[string]bool
}

func writesOnlyVars(job *FunctionLiteral, vars map[string]bool) bool {
	c := &readOnlyCheck{vars: vars}
	params := map[string]bool{}
	for _, p := range job.Parameters {
		params[p.Value] = true
	}
	c.scopes = append(c.scopes, params)
	return job.Body != nil && c.block(job.Body.Statements)
}

// declared reports whether name is one the job declares, and whether its value is plain.
func (c *readOnlyCheck) declared(name string) (found, plain bool) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if p, ok := c.scopes[i][name]; ok {
			return true, p
		}
	}
	return false, false
}

func (c *readOnlyCheck) declare(name string, plain bool) { c.scopes[len(c.scopes)-1][name] = plain }

func (c *readOnlyCheck) block(statements []Statement) bool {
	c.scopes = append(c.scopes, map[string]bool{})
	defer func() { c.scopes = c.scopes[:len(c.scopes)-1] }()
	for _, s := range statements {
		if !c.statement(s) {
			return false
		}
	}
	return true
}

func (c *readOnlyCheck) statement(s Statement) bool {
	switch s := s.(type) {
	case *ExpressionStatement:
		return s.Expression == nil || c.expression(s.Expression)
	case *VarStatement:
		if s.Function || s.Value == nil || !c.expression(s.Value) {
			return false
		}
		plain := c.plain(s.Value)
		for _, name := range s.Names {
			c.declare(name.Value, plain)
		}
		return true
	case *ReturnStatement:
		return s.ReturnValue == nil || c.expression(s.ReturnValue)
	case *BlockStatement:
		return c.block(s.Statements)
	case *BreakStatement, *ContinueStatement:
		return true
	case *ForStatement:
		if s.Item == nil || s.Body == nil || !c.expression(s.Iterable) {
			return false
		}
		c.scopes = append(c.scopes, map[string]bool{s.Item.Value: c.plain(s.Iterable)})
		defer func() { c.scopes = c.scopes[:len(c.scopes)-1] }()
		return c.block(s.Body.Statements)
	case *ForRangeStatement:
		if s.Body == nil {
			return false
		}
		c.scopes = append(c.scopes, map[string]bool{s.Counter: true})
		defer func() { c.scopes = c.scopes[:len(c.scopes)-1] }()
		if s.Init != nil && !c.statement(s.Init) || !c.expression(s.Cond) || !c.expression(s.Update) {
			return false
		}
		return c.block(s.Body.Statements)
	case *SwitchStatement:
		if !c.expression(s.Discriminant) {
			return false
		}
		for _, clause := range s.Cases {
			if clause.Test != nil && !c.expression(clause.Test) || !c.block(clause.Statements) {
				return false
			}
		}
		return true
	}
	return false // defer, go, an import, what this does not know
}

func (c *readOnlyCheck) expression(e Expression) bool {
	switch e := e.(type) {
	case nil:
		return true
	case *Literal, *Identifier:
		return true
	case *PrefixExpression:
		return e.Operator != "delete" && c.expression(e.Right)
	case *InfixExpression:
		return c.expression(e.Left) && c.expression(e.Right)
	case *TernaryExpression:
		return c.expression(e.Condition) && c.expression(e.Consequence) && c.expression(e.Alternative)
	case *IfExpression:
		if e.Consequence == nil || !c.expression(e.Condition) || !c.block(e.Consequence.Statements) {
			return false
		}
		return e.Alternative == nil || c.block(e.Alternative.Statements)
	case *MemberExpression:
		return c.expression(e.Object)
	case *IndexExpression:
		return c.expression(e.Left) && c.expression(e.Index)
	case *ChainExpression:
		return c.expression(e.Expression)
	case *SpreadExpression:
		return c.expression(e.Value)
	case *TemplateLiteral:
		for _, part := range e.Parts {
			if !c.expression(part) {
				return false
			}
		}
		return true
	case *ArrayLiteral:
		for _, element := range e.Elements {
			if !c.expression(element) {
				return false
			}
		}
		return true
	case *ObjectLiteral:
		for _, entry := range e.Entries {
			if !entry.IsSpread && !c.expression(entry.Key) || !c.expression(entry.Value) {
				return false
			}
		}
		return true
	case *AssignmentExpression:
		return c.target(e.Name) && c.expression(e.Value)
	case *MethodCallExpression:
		// a method of a plain value: a string's, an array's, an object's of JSON, which keep nothing
		if !c.plain(e.Object) || !c.expression(e.Object) {
			return false
		}
		for _, argument := range e.Arguments {
			if _, isFunction := argument.(*FunctionLiteral); isFunction || !c.expression(argument) {
				return false
			}
		}
		return true
	}
	return false // a call of a function, a function written in the job, what this does not know
}

// target reports whether an assignment writes a var of the page, a name the job declares, or a member of one.
func (c *readOnlyCheck) target(e Expression) bool {
	switch e := e.(type) {
	case *Identifier:
		found, _ := c.declared(e.Value)
		return found || c.vars[e.Value]
	case *MemberExpression:
		return c.target(e.Object)
	case *IndexExpression:
		return c.target(e.Left) && c.expression(e.Index)
	case *BindingPattern:
		for _, name := range e.Names() {
			if !c.target(name) {
				return false
			}
		}
		for _, element := range e.Elements {
			if element.Default != nil && !c.expression(element.Default) {
				return false
			}
		}
		return true
	}
	return false
}

// plain reports whether an expression's value is plain: a literal, a name the job declares whose value is,
// what an operator makes, a member, an element or a method's result of a plain value. A var of the page is
// not: its first value is the view's to choose.
func (c *readOnlyCheck) plain(e Expression) bool {
	switch e := e.(type) {
	case *Literal:
		return true
	case *Identifier:
		_, plain := c.declared(e.Value)
		return plain
	case *PrefixExpression:
		return true // !, -, +, typeof: a boolean, a number, a string
	case *InfixExpression:
		switch e.Operator {
		case "&&", "||", "??":
			return c.plain(e.Left) && c.plain(e.Right)
		}
		return true // arithmetic and comparison: a number, a string, a boolean
	case *TernaryExpression:
		return c.plain(e.Consequence) && c.plain(e.Alternative)
	case *MemberExpression:
		return c.plain(e.Object)
	case *IndexExpression:
		return c.plain(e.Left)
	case *ChainExpression:
		return c.plain(e.Expression)
	case *MethodCallExpression:
		return c.plain(e.Object)
	case *TemplateLiteral:
		return true
	case *ArrayLiteral:
		for _, element := range e.Elements {
			if !c.plain(element) {
				return false
			}
		}
		return true
	case *ObjectLiteral:
		for _, entry := range e.Entries {
			if !c.plain(entry.Value) {
				return false
			}
		}
		return true
	case *SpreadExpression:
		return c.plain(e.Value)
	}
	return false
}

// readOnlyArgument is the second argument router.view gets: { __readOnly: [ …names ] }.
func readOnlyArgument(token Token, names []string) *ObjectLiteral {
	list := &ArrayLiteral{Token: token}
	for _, name := range names {
		list.Elements = append(list.Elements, stringLiteral(token, name))
	}
	return &ObjectLiteral{Token: token, Entries: []ObjectEntry{{Key: stringLiteral(token, ViewReadOnlyName), Value: list}}}
}
