package compiler

import (
	"fmt"
	"strconv"

	"github.com/kitwork/engine/runtime"
)

// Block scope (14/10/2026): const and let live in the block that declares them, as in JavaScript.
//
// The VM keeps the variables of a running function in one map, by name. LOAD reads that map, then the
// maps of the functions around it (the closure chain), then the program's; STORE writes the first map
// of the chain that has the name, else the function's own. Before this, two variables of one name met
// in one map: a const in an if overwrote the function's variable of that name, a const in a nested
// function wrote through to its parent's (even to a file's top-level variable), and every closure made
// in a loop saw the loop's last value.
//
// The compiler now resolves every name before it emits code, and gives each variable the name it lives
// under in those maps, its slot (runtime.SlotName reads the variable back). The VM, its instruction set
// and the meaning of every instruction stay as they were:
//
//   - a function's own declarations and its parameters keep their names, since a view's const reaches
//     Kitwork UI by name and a handler's parameters are read by name; a declaration of the function's
//     own scope whose name a function around it may hold becomes name@N, which Kitwork UI reads as name;
//     a parameter that is assigned while a function around may hold its name is copied into name@N as
//     the function starts, so the assignment stays in the function;
//   - a declaration in a block (if, else, a loop's head or body, a switch) becomes name#N;
//   - a name nothing declares stays as written and keeps the VM's lookup: var x = … (an assignment, the
//     language has no var keyword), a global of the host, an import of "kitwork".
//
// A source never spells @ or #, so a slot is never another variable. Declaring a name twice in one
// block is a compile error, as in JavaScript.
//
// A closure made in a loop sees the variables of its own iteration, as in JavaScript. A variable of an
// iteration that some closure reads or writes lives in an object the loop makes for each iteration (its
// environment, slot #N): the loop and the function read and write it there, and the closure is made
// inside a small function whose parameters are those objects, so it keeps the ones of the iteration it
// was made in. A counting loop whose counter is such a variable copies it into the next iteration's
// object before its update, as JavaScript does. Variables no closure reaches stay in the function's map.

type bindingKind uint8

const (
	bindingConst bindingKind = iota
	bindingLet
	bindingParameter
	bindingItem    // for (const x of …)
	bindingCounter // for (let i = 0; …)
)

// binding is one declared variable.
type binding struct {
	name  string
	kind  bindingKind
	token Token
	scope *lexicalScope
	// slot is the name LOAD and STORE use for it.
	slot string
	// loop is the innermost loop of its function that holds its declaration; nil when none.
	loop *loopScope
	// closures lists the functions directly inside its own that read or write it (a deeper one reaches
	// it through them).
	closures []*FunctionLiteral
	// written: assigned after its declaration.
	written bool
	// copied: a parameter the function copies into slot as it starts.
	copied bool
	// environment: it lives in its loop's environment object (a closure reaches it).
	environment bool
	// constant: declared with const (a function declaration aside), so never assigned (constantWritten).
	constant bool
	// kitworkSafe: import { safe } from "kitwork", which binds the global safe itself.
	kitworkSafe bool
}

type lexicalScope struct {
	parent   *lexicalScope
	function *functionScope
	// block is false for the scope of a function's parameters and body, and for the program's top level.
	block bool
	// loop is the innermost loop of its function around it; nil when none.
	loop  *loopScope
	names map[string]*binding
	order []*binding
}

type functionScope struct {
	parent  *functionScope
	literal *FunctionLiteral // nil for the program
	scope   *lexicalScope
	// written lists the names this function assigns without any declaration around them (var x = …).
	written map[string]bool
	// held lists the names this function's map may hold under their own spelling.
	held map[string]bool
}

type loopScope struct {
	parent   *loopScope
	function *functionScope
	// environment is the slot of the object each iteration makes; "" when no closure needs one.
	environment string
}

// resolution is what the resolver hands the code generator.
type resolution struct {
	slots map[*Identifier]*binding
	// loops maps a for statement to its loop.
	loops map[Statement]*loopScope
	// environments lists, for a closure made in a loop, the environment slots it is handed as it is made.
	environments map[*FunctionLiteral][]string
	// copies lists, per function, the parameters it copies into their slot as it starts.
	copies map[*FunctionLiteral][]*binding
	// temps gives a write of a variable of an iteration's object from a closure a slot of its own to
	// pass the value through (a slot of the closure's map, never one of the loop's function).
	temps map[*Identifier]string
	// all bindings and functions, in source order, for tests and scans.
	bindings  []*binding
	functions []*functionScope
	// declaresSafe: the program declares or assigns a name safe somewhere (checkSafeCall).
	declaresSafe bool
	// once gives an update or a compound assignment of a member or an item whose object or key does
	// more than read (a[i++]++, get().n += 1) the slots they are kept in, so each runs once
	// (evaluateOnce, 15/10/2026).
	once map[*AssignmentExpression]onceSlots
}

// onceSlots are the slots of an assignment's object and key; "" for one that is compiled where it is.
type onceSlots struct {
	object, key string
}

type resolver struct {
	compiler *Compiler
	function *functionScope
	scope    *lexicalScope
	loop     *loopScope
	result   *resolution
	next     int
	err      error
	// foreignWrites lists the names written from a function other than the one declaring them.
	foreignWrites []*Identifier
	// onces lists, in source order, the assignments that get onceSlots once every other slot is given.
	onces []*AssignmentExpression
	// observe, when set, sees every name read or written, where, and what it resolved to (nil: no
	// declaration around). Scans of the apps use it; compiling does not.
	observe func(name *Identifier, write bool, found *binding, scope *lexicalScope)
}

// resolveProgram resolves every name of a program and gives each variable its slot.
func (c *Compiler) resolveProgram(program *Program) (*resolution, error) {
	return c.resolveObserved(program, nil)
}

func (c *Compiler) resolveObserved(
	program *Program,
	observe func(name *Identifier, write bool, found *binding, scope *lexicalScope),
) (*resolution, error) {
	r := &resolver{
		compiler: c,
		observe:  observe,
		result: &resolution{
			slots:        map[*Identifier]*binding{},
			loops:        map[Statement]*loopScope{},
			environments: map[*FunctionLiteral][]string{},
			copies:       map[*FunctionLiteral][]*binding{},
			temps:        map[*Identifier]string{},
			once:         map[*AssignmentExpression]onceSlots{},
		},
	}
	r.enterFunction(nil)
	r.hoist(program.Statements)
	for _, statement := range program.Statements {
		r.statement(statement)
	}
	r.leaveFunction()
	if r.err != nil {
		return nil, r.err
	}
	r.assignSlots()
	r.assignEnvironments()
	r.assignOnce()
	r.result.declaresSafe = declaresName(r.result, "safe")
	return r.result, nil
}

// declaresName reports whether a program declares name anywhere (a const, a let, a parameter, a
// function, an import, a loop variable) or assigns it without a declaration (var name = …). An import of
// "kitwork" that binds safe to the global safe declares no safe of the program's own (14/10/2026).
func declaresName(result *resolution, name string) bool {
	for _, declared := range result.bindings {
		if declared.name == name && !declared.kitworkSafe {
			return true
		}
	}
	for _, function := range result.functions {
		if function.written[name] {
			return true
		}
	}
	return false
}

func (r *resolver) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

// where is a token's place as file:line:col (line:col when the source has no name).
func (r *resolver) where(token Token) string {
	location := r.compiler.getSourceLocation(token.Source, token.Position)
	if location.Line == 0 {
		return fmt.Sprintf("pos %d", token.Position)
	}
	if location.File == "" {
		return fmt.Sprintf("%d:%d", location.Line, location.Column)
	}
	return fmt.Sprintf("%s:%d:%d", location.File, location.Line, location.Column)
}

func (r *resolver) enterFunction(literal *FunctionLiteral) {
	function := &functionScope{parent: r.function, literal: literal, written: map[string]bool{}}
	function.scope = &lexicalScope{parent: r.scope, function: function, names: map[string]*binding{}}
	r.result.functions = append(r.result.functions, function)
	r.function = function
	r.scope = function.scope
}

func (r *resolver) leaveFunction() {
	r.scope = r.function.scope.parent
	r.function = r.function.parent
}

func (r *resolver) enterBlock() {
	r.scope = &lexicalScope{parent: r.scope, function: r.function, block: true, loop: r.loop, names: map[string]*binding{}}
}

func (r *resolver) leaveBlock() {
	r.scope = r.scope.parent
}

// hoist declares the variables a list of statements declares, before any of them is read: a function
// may call one declared after it, as in JavaScript.
func (r *resolver) hoist(statements []Statement) {
	for _, statement := range statements {
		switch node := statement.(type) {
		case *VarStatement:
			kind := bindingConst
			if node.Token.Kind == Let {
				kind = bindingLet
			}
			for _, name := range node.Names {
				declared := r.declare(name, kind)
				if declared != nil && node.Function {
					declared.constant = false
				}
				if declared != nil && node.KitworkSafe && name.Value == "safe" {
					declared.kitworkSafe = true
				}
			}
		case *GroupStatement:
			r.hoist(node.Statements)
		}
	}
}

// declare adds a variable to the current scope. A name declared twice in one scope is the compile
// error JavaScript gives.
func (r *resolver) declare(name *Identifier, kind bindingKind) *binding {
	if name == nil {
		return nil
	}
	if first, ok := r.scope.names[name.Value]; ok {
		r.fail(r.redeclared(name, kind, first))
		return nil
	}
	declared := &binding{name: name.Value, kind: kind, token: name.Token, scope: r.scope, loop: r.loop, constant: kind == bindingConst}
	r.scope.names[name.Value] = declared
	r.scope.order = append(r.scope.order, declared)
	r.result.bindings = append(r.result.bindings, declared)
	r.result.slots[name] = declared
	return declared
}

func (r *resolver) redeclared(name *Identifier, kind bindingKind, first *binding) error {
	if first.kind == bindingParameter && kind == bindingParameter {
		return fmt.Errorf("%s: hàm có hai tham số cùng tên %s (tham số kia ở %s): đặt tên khác cho một tham số",
			r.where(name.Token), name.Value, r.where(first.token))
	}
	if first.kind == bindingParameter {
		return fmt.Errorf("%s: %s đã là tham số của hàm (%s): đặt tên khác cho biến, hoặc gán %s = … nếu muốn đổi giá trị của tham số",
			r.where(name.Token), name.Value, r.where(first.token), name.Value)
	}
	return fmt.Errorf("%s: %s đã được khai báo trong cùng khối ở %s; một tên chỉ khai báo một lần trong một khối, như JavaScript: đặt tên khác (const { error: saveError } = …), hoặc khai báo một lần bằng let rồi gán %s = …",
		r.where(name.Token), name.Value, r.where(first.token), name.Value)
}

// constantWritten is the error of a write to a const: JavaScript throws it when the write runs
// (TypeError: Assignment to constant variable), Kitwork refuses the program when it compiles
// (14/10/2026; the write happened and the program went on).
func (r *resolver) constantWritten(name *Identifier, declared *binding) error {
	what := "hằng (const)"
	switch declared.kind {
	case bindingItem:
		what = "biến của for (const … of …)"
	case bindingCounter:
		what = "biến đếm khai báo bằng const"
	}
	return fmt.Errorf("%s: %s là %s, khai báo ở %s, không gán lại được, như JavaScript: khai báo bằng let nếu cần đổi giá trị",
		r.where(name.Token), name.Value, what, r.where(declared.token))
}

func (r *resolver) lookup(name string) *binding {
	for scope := r.scope; scope != nil; scope = scope.parent {
		if found, ok := scope.names[name]; ok {
			return found
		}
	}
	return nil
}

// closureUnder is the function literal directly inside function that holds the code being read.
func (r *resolver) closureUnder(function *functionScope) *FunctionLiteral {
	for inner := r.function; inner != nil; inner = inner.parent {
		if inner.parent == function {
			return inner.literal
		}
	}
	return nil
}

func (r *resolver) reference(name *Identifier, write bool) {
	if name == nil {
		return
	}
	if name.Value == "kitwork" && !write {
		return // BUILTIN 0, not a variable
	}
	found := r.lookup(name.Value)
	if r.observe != nil {
		r.observe(name, write, found, r.scope)
	}
	if found == nil {
		if write {
			r.function.written[name.Value] = true
		}
		return
	}
	if previous, ok := r.result.slots[name]; ok && previous != found {
		r.fail(fmt.Errorf("%s: compiler: tên %s trỏ tới hai biến", r.where(name.Token), name.Value))
		return
	}
	r.result.slots[name] = found
	if write && found.scope.function != r.function {
		r.foreignWrites = append(r.foreignWrites, name)
	}
	if found.scope.function != r.function {
		if closure := r.closureUnder(found.scope.function); closure != nil {
			known := false
			for _, seen := range found.closures {
				if seen == closure {
					known = true
					break
				}
			}
			if !known {
				found.closures = append(found.closures, closure)
			}
		}
	}
	if write {
		if found.constant {
			r.fail(r.constantWritten(name, found))
			return
		}
		found.written = true
	}
}

func (r *resolver) statement(statement Statement) {
	if r.err != nil || statement == nil {
		return
	}
	switch node := statement.(type) {
	case *VarStatement:
		r.expression(node.Value)
		if node.Pattern != nil {
			r.patternDefaults(node.Pattern)
		}
	case *ExpressionStatement:
		r.expression(node.Expression)
	case *GroupStatement:
		for _, inner := range node.Statements {
			r.statement(inner)
		}
	case *BlockStatement:
		r.block(node)
	case *ReturnStatement:
		r.expression(node.ReturnValue)
	case *BreakStatement, *ContinueStatement, *ImportStatement:
	case *SwitchStatement:
		r.switchStatement(node)
	case *ForStatement:
		r.forOf(node)
	case *ForRangeStatement:
		r.forRange(node)
	case *DeferStatement:
		r.expression(node.Fn)
	case *SpawnStatement:
		r.expression(node.Fn)
	default:
		if expression, ok := statement.(Expression); ok {
			r.expression(expression)
		}
	}
}

func (r *resolver) patternDefaults(pattern *BindingPattern) {
	for _, element := range pattern.Elements {
		r.expression(element.Default)
		if element.Pattern != nil {
			r.patternDefaults(element.Pattern)
		}
	}
}

func (r *resolver) block(block *BlockStatement) {
	if block == nil {
		return
	}
	r.enterBlock()
	r.hoist(block.Statements)
	for _, statement := range block.Statements {
		r.statement(statement)
	}
	r.leaveBlock()
}

func (r *resolver) switchStatement(node *SwitchStatement) {
	r.expression(node.Discriminant)
	// the cases of a switch are one block, as in JavaScript
	r.enterBlock()
	for _, clause := range node.Cases {
		r.hoist(clause.Statements)
	}
	for _, clause := range node.Cases {
		r.expression(clause.Test)
		for _, statement := range clause.Statements {
			r.statement(statement)
		}
	}
	r.leaveBlock()
}

func (r *resolver) enterLoop(node Statement) *loopScope {
	loop := &loopScope{parent: r.loop, function: r.function}
	r.result.loops[node] = loop
	r.loop = loop
	return loop
}

func (r *resolver) forOf(node *ForStatement) {
	r.expression(node.Iterable)
	loop := r.enterLoop(node)
	r.enterBlock()
	if item := r.declare(node.Item, bindingItem); item != nil {
		item.constant = node.Const
	}
	r.block(node.Body)
	r.leaveBlock()
	r.loop = loop.parent
}

func (r *resolver) forRange(node *ForRangeStatement) {
	loop := r.enterLoop(node)
	r.enterBlock()
	if init, ok := node.Init.(*VarStatement); ok && len(init.Names) == 1 {
		if counter := r.declare(init.Names[0], bindingCounter); counter != nil {
			counter.constant = init.Token.Kind == Const
		}
		r.expression(init.Value)
	} else {
		r.statement(node.Init)
	}
	r.expression(node.Cond)
	r.expression(node.Update)
	r.block(node.Body)
	r.leaveBlock()
	r.loop = loop.parent
}

func (r *resolver) expression(expression Expression) {
	if r.err != nil || expression == nil {
		return
	}
	switch node := expression.(type) {
	case *Identifier:
		r.reference(node, false)
	case *Literal, *ParameterList:
	case *PrefixExpression:
		r.expression(node.Right)
	case *InfixExpression:
		r.expression(node.Left)
		r.expression(node.Right)
	case *IfExpression:
		r.expression(node.Condition)
		r.block(node.Consequence)
		r.block(node.Alternative)
	case *TernaryExpression:
		r.expression(node.Condition)
		r.expression(node.Consequence)
		r.expression(node.Alternative)
	case *CallExpression:
		r.expression(node.Function)
		for _, argument := range node.Arguments {
			r.expression(argument)
		}
	case *MemberExpression:
		r.expression(node.Object)
	case *IndexExpression:
		r.expression(node.Left)
		r.expression(node.Index)
	case *ChainExpression:
		r.expression(node.Expression)
	case *AssignmentExpression:
		r.target(node.Name)
		r.expression(node.Value)
		if object, key := onceParts(node); object || key {
			r.onces = append(r.onces, node)
		}
	case *BindingPattern:
		r.target(node)
	case *ArrayLiteral:
		for _, element := range node.Elements {
			r.expression(element)
		}
	case *ObjectLiteral:
		for _, entry := range node.Entries {
			if assignment, ok := entry.Key.(*AssignmentExpression); ok && !entry.IsSpread && entry.Key == entry.Value {
				// { a = 1 } is a default of a pattern (const { a = 1 } = o); in an object it is the
				// SyntaxError of JavaScript, not an assignment to a and a key "1" (14/10/2026)
				at, name := assignment.Token, assignment.Name.String()
				if id, ok := assignment.Name.(*Identifier); ok {
					at = id.Token
				}
				r.fail(fmt.Errorf("%s: { %s = … } chỉ là giá trị mặc định khi tách (const { %s = … } = o); trong một object viết { %s: … }",
					r.where(at), name, name, name))
				return
			}
			if !entry.IsSpread {
				// a name before ':' is the key's spelling; { a } reads a through its value
				if _, isName := entry.Key.(*Identifier); !isName {
					r.expression(entry.Key)
				}
			}
			r.expression(entry.Value)
		}
	case *SpreadExpression:
		r.expression(node.Value)
	case *FunctionLiteral:
		r.functionLiteral(node)
	case *MethodCallExpression:
		r.expression(node.Object)
		for _, argument := range node.Arguments {
			r.expression(argument)
		}
	case *TemplateLiteral:
		for _, part := range node.Parts {
			r.expression(part)
		}
	case *DeferStatement:
		r.expression(node.Fn)
	case *SpawnStatement:
		r.expression(node.Fn)
	case *ForStatement:
		r.forOf(node)
	case *ForRangeStatement:
		r.forRange(node)
	}
}

// target reads the left side of an assignment.
func (r *resolver) target(target Expression) {
	switch node := target.(type) {
	case *Identifier:
		r.reference(node, true)
	case *BindingPattern:
		for _, element := range node.Elements {
			if element.Pattern != nil {
				r.target(element.Pattern)
			} else {
				r.reference(element.Name, true)
			}
			r.expression(element.Default)
		}
	default:
		r.expression(target)
	}
}

func (r *resolver) functionLiteral(literal *FunctionLiteral) {
	outerLoop := r.loop
	r.loop = nil
	r.enterFunction(literal)
	for _, parameter := range literal.Parameters {
		r.declare(parameter, bindingParameter)
	}
	if literal.Body != nil {
		r.hoist(literal.Body.Statements)
		for _, statement := range literal.Body.Statements {
			r.statement(statement)
		}
	}
	r.leaveFunction()
	r.loop = outerLoop
}

// assignSlots gives every variable the name it lives under. Functions come outer first, so what a
// function around holds is known when a function inside decides.
func (r *resolver) assignSlots() {
	for _, function := range r.result.functions {
		function.held = map[string]bool{}
		for _, declared := range function.scope.order {
			switch {
			case function.literal == nil, declared.kind == bindingParameter:
				declared.slot = declared.name
			case r.heldAround(function, declared.name):
				declared.slot = r.slot(declared.name, runtime.SlotFunctionMark)
			default:
				declared.slot = declared.name
			}
			if declared.slot == declared.name {
				function.held[declared.name] = true
			}
		}
		for name := range function.written {
			function.held[name] = true
		}
		if function.literal == nil {
			continue
		}
		for _, declared := range function.scope.order {
			if declared.kind == bindingParameter && declared.written && r.heldAround(function, declared.name) {
				declared.slot = r.slot(declared.name, runtime.SlotFunctionMark)
				declared.copied = true
				r.result.copies[function.literal] = append(r.result.copies[function.literal], declared)
			}
		}
	}
	for _, declared := range r.result.bindings {
		if declared.scope.block {
			declared.slot = r.slot(declared.name, runtime.SlotBlockMark)
		}
	}
}

// heldAround reports whether a function around this one may hold name in its map under that spelling:
// a parameter, a declaration of its own scope that kept its name, or a name it assigns undeclared.
func (r *resolver) heldAround(function *functionScope, name string) bool {
	for around := function.parent; around != nil; around = around.parent {
		if around.held[name] {
			return true
		}
	}
	return false
}

func (r *resolver) slot(name string, mark byte) string {
	r.next++
	return name + string(mark) + strconv.Itoa(r.next)
}

// assignOnce gives each assignment of r.onces its slots, after every other slot, so the slots of a program
// without such an assignment keep their names. A node resolved twice (the target of an update is also
// the left side of its value) gets its slots once.
func (r *resolver) assignOnce() {
	for _, node := range r.onces {
		if _, given := r.result.once[node]; given {
			continue
		}
		object, key := onceParts(node)
		var slots onceSlots
		if object {
			slots.object = r.slot("", runtime.SlotBlockMark)
		}
		if key {
			slots.key = r.slot("", runtime.SlotBlockMark)
		}
		r.result.once[node] = slots
	}
}

// onceParts tells which parts of an update or a compound assignment of a member or an item are kept in a
// slot: the key when it does more than read, and then the object too, which JavaScript evaluates before
// the key; the object alone when only it does. A plain assignment (a[f()] = v) evaluates its target once
// already.
func onceParts(node *AssignmentExpression) (object, key bool) {
	infix, ok := node.Value.(*InfixExpression)
	if !ok || infix.Left != node.Name {
		return false, false
	}
	switch target := node.Name.(type) {
	case *MemberExpression:
		return !readsOnly(target.Object), false
	case *IndexExpression:
		if !readsOnly(target.Index) {
			return true, true
		}
		return !readsOnly(target.Left), false
	}
	return false, false
}

// assignEnvironments puts every variable of an iteration that a closure reaches in its loop's
// environment, and lists for each such closure the environments it is handed as it is made.
func (r *resolver) assignEnvironments() {
	for _, declared := range r.result.bindings {
		if declared.loop == nil || len(declared.closures) == 0 {
			continue
		}
		declared.environment = true
		if declared.loop.environment == "" {
			declared.loop.environment = r.slot("", runtime.SlotBlockMark)
		}
		for _, closure := range declared.closures {
			handed := r.result.environments[closure]
			known := false
			for _, slot := range handed {
				if slot == declared.loop.environment {
					known = true
					break
				}
			}
			if !known {
				r.result.environments[closure] = append(handed, declared.loop.environment)
			}
		}
	}
	for _, name := range r.foreignWrites {
		if found := r.result.slots[name]; found != nil && found.environment {
			if _, ok := r.result.temps[name]; !ok {
				r.result.temps[name] = r.slot("", runtime.SlotBlockMark)
			}
		}
	}
}
