package compiler

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// Bytecode couples an immutable executable Program with its source-file manifest.
type Bytecode struct {
	*runtime.Program
	// Files lists every source file compiled in: the entry plus all natively-bundled imports.
	// Hot reload stats these to catch edits — including edits to an imported ./_core module.
	Files []string

	compilerFingerprint string
	sourceFingerprint   string
}

// Compiler chịu trách nhiệm chuyển đổi AST thành Bytecode
type Compiler struct {
	instructions []byte
	constants    []value.Value
	debugEntries []runtime.DebugEntry
	sources      map[string][]int32
	// texts holds each source's text, so a column counts characters (14/10/2026) and a compile error
	// can quote the source; a source without one counts bytes.
	texts         map[string]string
	columns       columnMemo
	currentSource string
	currentPos    int32

	sourceFingerprint string
	breakFrames       []compilerBreakFrame
	// chain is the optional chain being compiled: the jumps its ?. links take when they find null.
	chain *chainJumps
	// scopes is what resolving the program's names gave (scope.go): each variable's slot.
	scopes *resolution
	// wide writes every jump with a three-byte address: the program is longer than a two-byte jump
	// reaches (65,535 bytes). overflow records a two-byte jump given an address past that, which makes
	// the Program compile again wide (bytecode 3, 14/10/2026).
	wide     bool
	overflow bool
}

type compilerBreakFrame struct {
	cleanup int
	jumps   []int
	// loop: a for, which continue goes on with; a switch is not one. continues are its continue jumps,
	// patched to where the loop goes on (14/10/2026).
	loop      bool
	continues []int
}

// chainJumps collects the exits of one optional chain. A ?. that finds null jumps with that null as
// the one value the chain leaves (short); o.m?.() finds null with o still under it (method), which
// a shared tail drops first.
type chainJumps struct {
	short  []int
	method []int
}

func NewCompiler(source ...string) *Compiler {
	var offsets []int32
	if len(source) > 0 {
		src := source[0]
		offsets = []int32{0} // Dòng 1 bắt đầu ở byte 0
		for i := 0; i < len(src); i++ {
			if src[i] == '\n' {
				offsets = append(offsets, int32(i+1))
			}
		}
	}
	sources := make(map[string][]int32)
	texts := make(map[string]string)
	if offsets != nil {
		sources[""] = offsets
		texts[""] = source[0]
	}
	compiler := &Compiler{
		instructions: []byte{},
		constants:    []value.Value{},
		debugEntries: []runtime.DebugEntry{},
		sources:      sources,
		texts:        texts,
	}
	sourceText := ""
	if len(source) > 0 {
		sourceText = source[0]
	}
	compiler.sourceFingerprint = fingerprintSources(map[string]string{"": sourceText})
	return compiler
}

func newCompilerWithSources(sources map[string]string) *Compiler {
	compiler := &Compiler{
		instructions:      []byte{},
		constants:         []value.Value{},
		debugEntries:      []runtime.DebugEntry{},
		sources:           make(map[string][]int32, len(sources)),
		texts:             make(map[string]string, len(sources)),
		sourceFingerprint: fingerprintSources(sources),
	}
	for name, source := range sources {
		compiler.texts[name] = source
		offsets := []int32{0}
		for index := 0; index < len(source); index++ {
			if source[index] == '\n' {
				offsets = append(offsets, int32(index+1))
			}
		}
		compiler.sources[name] = offsets
	}
	return compiler
}

func CompileSource(src string) (*Bytecode, error) {
	const sourceName = "<source>"
	prog, err := parseProgramSource(src, sourceName)
	if err != nil {
		return nil, err
	}
	comp := newCompilerWithSources(map[string]string{sourceName: src})
	if err := comp.Compile(prog); err != nil {
		return nil, err
	}
	return comp.ByteCodeResult()
}

func getNodePosition(node Node) int32 {
	if node == nil {
		return 0
	}
	switch n := node.(type) {
	case *Program:
		if len(n.Statements) > 0 {
			return getNodePosition(n.Statements[0])
		}
	case *VarStatement:
		return n.Token.Position
	case *ExpressionStatement:
		return n.Token.Position
	case *BlockStatement:
		return n.Token.Position
	case *ReturnStatement:
		return n.Token.Position
	case *BreakStatement:
		return n.Token.Position
	case *ContinueStatement:
		return n.Token.Position
	case *SwitchStatement:
		return n.Token.Position
	case *ForStatement:
		return n.Token.Position
	case *ForRangeStatement:
		return n.Token.Position
	case *DeferStatement:
		return n.Token.Position
	case *Identifier:
		return n.Token.Position
	case *Literal:
		return n.Token.Position
	case *PrefixExpression:
		return n.Token.Position
	case *InfixExpression:
		return n.Token.Position
	case *IfExpression:
		return n.Token.Position
	case *TernaryExpression:
		return n.Token.Position
	case *CallExpression:
		return n.Token.Position
	case *MemberExpression:
		return n.Token.Position
	case *IndexExpression:
		return n.Token.Position
	case *AssignmentExpression:
		return n.Token.Position
	case *ArrayLiteral:
		return n.Token.Position
	case *ObjectLiteral:
		return n.Token.Position
	case *SpreadExpression:
		return n.Token.Position
	case *ParameterList:
		return n.Token.Position
	case *FunctionLiteral:
		return n.Token.Position
	case *SpawnStatement:
		return n.Token.Position
	case *MethodCallExpression:
		return n.Token.Position
	case *TemplateLiteral:
		return n.Token.Position
	case *ChainExpression:
		return getNodePosition(n.Expression)
	case *BindingPattern:
		return n.Token.Position
	}
	return 0
}

// templateText reports a part of a template that is text: written text, or a string written in ${…}.
func templateText(part Expression) bool {
	literal, ok := part.(*Literal)
	return ok && literal.Value.K == value.String
}

// startPosition is where the source of an expression begins: the callee of f(x), o.f(x)(y), g()(z)
// starts at f, o, g. A CALL records it, so an error of the call points at what is called rather
// than at its last argument.
func startPosition(node Expression) int32 {
	for depth := 0; depth < 1024; depth++ {
		switch n := node.(type) {
		case *MemberExpression:
			node = n.Object
		case *IndexExpression:
			node = n.Left
		case *CallExpression:
			node = n.Function
		case *MethodCallExpression:
			node = n.Object
		case *InfixExpression:
			node = n.Left
		case *TernaryExpression:
			node = n.Condition
		case *ChainExpression:
			node = n.Expression
		case *AssignmentExpression:
			node = n.Name
		default:
			return getNodePosition(node)
		}
		if node == nil {
			return 0
		}
	}
	return getNodePosition(node)
}

func getNodeSource(node Node) string {
	if node == nil {
		return ""
	}
	switch n := node.(type) {
	case *Program:
		if len(n.Statements) > 0 {
			return getNodeSource(n.Statements[0])
		}
	case *GroupStatement:
		if len(n.Statements) > 0 {
			return getNodeSource(n.Statements[0])
		}
	case *VarStatement:
		return n.Token.Source
	case *ExpressionStatement:
		return n.Token.Source
	case *BlockStatement:
		return n.Token.Source
	case *ReturnStatement:
		return n.Token.Source
	case *BreakStatement:
		return n.Token.Source
	case *ContinueStatement:
		return n.Token.Source
	case *SwitchStatement:
		return n.Token.Source
	case *ForStatement:
		return n.Token.Source
	case *ForRangeStatement:
		return n.Token.Source
	case *DeferStatement:
		return n.Token.Source
	case *Identifier:
		return n.Token.Source
	case *Literal:
		return n.Token.Source
	case *PrefixExpression:
		return n.Token.Source
	case *InfixExpression:
		return n.Token.Source
	case *IfExpression:
		return n.Token.Source
	case *TernaryExpression:
		return n.Token.Source
	case *CallExpression:
		return n.Token.Source
	case *MemberExpression:
		return n.Token.Source
	case *IndexExpression:
		return n.Token.Source
	case *AssignmentExpression:
		return n.Token.Source
	case *ArrayLiteral:
		return n.Token.Source
	case *ObjectLiteral:
		return n.Token.Source
	case *SpreadExpression:
		return n.Token.Source
	case *ParameterList:
		return n.Token.Source
	case *FunctionLiteral:
		return n.Token.Source
	case *SpawnStatement:
		return n.Token.Source
	case *MethodCallExpression:
		return n.Token.Source
	case *TemplateLiteral:
		return n.Token.Source
	case *ChainExpression:
		return getNodeSource(n.Expression)
	case *BindingPattern:
		return n.Token.Source
	}
	return ""
}

func nameFunction(expression Expression, name string) {
	if function, ok := expression.(*FunctionLiteral); ok && function.DebugName == "" {
		function.DebugName = name
	}
}

func (c *Compiler) getLineNumber(pos int32) int32 {
	return c.getSourceLocation(c.currentSource, pos).Line
}

func (c *Compiler) getSourceLocation(source string, pos int32) runtime.SourceLocation {
	offsets := c.sources[source]
	if len(offsets) == 0 {
		return runtime.SourceLocation{}
	}
	if pos < 0 {
		pos = 0
	}
	lineIndex := sort.Search(len(offsets), func(index int) bool {
		return offsets[index] > pos
	}) - 1
	if lineIndex < 0 {
		lineIndex = 0
	}
	return runtime.SourceLocation{
		File:   source,
		Line:   int32(lineIndex + 1),
		Column: c.column(source, offsets[lineIndex], pos),
	}
}

// columnMemo is the column found last: positions of one line come one after another while a line
// compiles, so the next one counts on from it instead of from the line's start.
type columnMemo struct {
	source    string
	lineStart int32
	pos       int32
	column    int32
}

// column is the column of pos, on the line that starts at lineStart, counted in characters as an editor
// shows it (14/10/2026; until then in bytes, so a line with Vietnamese text before the place was off).
func (c *Compiler) column(source string, lineStart, pos int32) int32 {
	text, ok := c.texts[source]
	if !ok || pos > int32(len(text)) || lineStart > pos {
		return pos - lineStart + 1
	}
	from, column := lineStart, int32(1)
	if memo := c.columns; memo.source == source && memo.lineStart == lineStart && memo.pos <= pos {
		from, column = memo.pos, memo.column
	}
	column += int32(utf8.RuneCountInString(text[from:pos]))
	c.columns = columnMemo{source: source, lineStart: lineStart, pos: pos, column: column}
	return column
}

// sourceText is the source from start up to end (positions in source), as one line; "" when the compiler
// does not have it.
func (c *Compiler) sourceText(source string, start, end int32) string {
	text, ok := c.texts[source]
	if !ok || start < 0 || end > int32(len(text)) || start >= end {
		return ""
	}
	return oneLine(text[start:end])
}

// locationText writes a place as an error leads with it: file:line:column, line:column for a source
// with no name.
func locationText(location runtime.SourceLocation) string {
	if location.File == "" {
		return fmt.Sprintf("%d:%d", location.Line, location.Column)
	}
	return fmt.Sprintf("%s:%d:%d", location.File, location.Line, location.Column)
}

// checkSafeCall makes the mistake a move from x.safe() makes most a compile error: safe(findUser(id)),
// where the arrow is forgotten. At run time findUser(id) runs before safe is called, so if it fails the
// request ends with findUser's error and nothing says the arrow was missing. An argument that is
// certainly not a function (a call, a literal, an object, an array, arithmetic) is the error, quoting
// what to write; so is a call of safe without exactly one argument. A name, a member or a ternary may
// hold a function and is checked when it runs. The check is off when the bundle (the entry file and
// every module it imports) declares or assigns a name safe anywhere: that safe is the author's own.
func (c *Compiler) checkSafeCall(n *CallExpression) error {
	callee, ok := n.Function.(*Identifier)
	if !ok || callee.Value != "safe" || (c.scopes != nil && c.scopes.declaresSafe) {
		return nil
	}
	var message string
	switch {
	case len(n.Arguments) == 0:
		message = "safe nhận một hàm: viết safe(() => …)"
	case len(n.Arguments) > 1:
		message = fmt.Sprintf("safe nhận đúng một hàm, không nhận %d đối số: viết safe(() => …)", len(n.Arguments))
	case certainlyNotAFunction(n.Arguments[0]):
		written := ""
		if n.Close > n.Token.Position {
			written = c.sourceText(n.Token.Source, n.Token.Position+1, n.Close)
		}
		if written == "" {
			written = "…"
		}
		if strings.HasPrefix(written, "{") {
			written = "(" + written + ")"
		}
		message = "safe nhận một hàm: viết safe(() => " + written + ")"
	default:
		return nil
	}
	return fmt.Errorf("%s: %s", locationText(c.getSourceLocation(callee.Token.Source, callee.Token.Position)), message)
}

// certainlyNotAFunction reports an expression that can never give a function: a call (its result is
// computed before safe runs), a literal, an object, an array, arithmetic, a comparison, a negation.
func certainlyNotAFunction(e Expression) bool {
	switch n := e.(type) {
	case *CallExpression, *MethodCallExpression, *Literal, *TemplateLiteral, *ObjectLiteral, *ArrayLiteral,
		*PrefixExpression:
		return true
	case *ChainExpression:
		return certainlyNotAFunction(n.Expression)
	case *InfixExpression:
		switch n.Operator {
		case "&&", "||", "??":
			return false
		}
		return true
	}
	return false
}

// Compile bắt đầu quá trình duyệt cây và phát sinh mã
func (c *Compiler) Compile(node Node) error {
	if node == nil {
		return nil
	}

	source := getNodeSource(node)
	if _, knownSource := c.sources[source]; knownSource {
		c.currentSource = source
		c.currentPos = getNodePosition(node)
	}

	switch n := node.(type) {
	case *Program:
		start, constants, entries := len(c.instructions), len(c.constants), len(c.debugEntries)
		if c.scopes == nil {
			// every name is resolved before code is emitted: a variable's slot depends on the whole
			// program (14/10/2026, scope.go)
			scopes, err := c.resolveProgram(n)
			if err != nil {
				return err
			}
			c.scopes = scopes
		}
		for _, s := range n.Statements {
			err := c.Compile(s)
			if err != nil {
				return err
			}
		}
		// Luôn kết thúc Program bằng RETURN để đảm bảo thực thi defer
		c.emit(runtime.RETURN)
		if c.overflow && !c.wide {
			// a jump's address did not fit two bytes: the program is written again with every jump
			// wide. Names were resolved once (c.scopes), so the second pass emits the same program.
			c.instructions = c.instructions[:start]
			c.constants = c.constants[:constants]
			c.debugEntries = c.debugEntries[:entries]
			c.breakFrames, c.chain = nil, nil
			c.wide, c.overflow = true, false
			return c.Compile(n)
		}

	case *ImportStatement:
		// Bundler ở package script phải giải quyết hết ImportStatement (IIFE-wrap)
		// TRƯỚC khi compile. Còn sót tới đây = import chưa được resolve.
		return fmt.Errorf("compiler: unresolved relative import %q (native bundler did not run)", n.Source)

	case *GroupStatement:
		for _, s := range n.Statements {
			if err := c.Compile(s); err != nil {
				return err
			}
		}

	case *ExpressionStatement:
		if err := c.compileEffect(n.Expression); err != nil {
			return err
		}
		switch n.Expression.(type) {
		case *IfExpression:
			// Skip POP for control flow expressions (if statements)
		default:
			// Finish configuring a deferred effect before discarding the expression result.
			// COMMIT is a no-op for ordinary values and deliberately does not pop the stack.
			c.emit(runtime.COMMIT)
			c.emit(runtime.POP)
		}
		return nil

	case *InfixExpression:
		switch n.Operator {
		case "&&", "||", "??":
			return c.compileLogical(n)
		}
		if n.Step {
			// x ± 1 of ++ and --: x; STEP 1 (or 2), the number JavaScript's ToNumber makes of x, stepped
			// (bytecode 4, 15/10/2026). It replaces x; PUSH 1; ADD, so "5"++ is 6, not "51", and costs
			// the same energy.
			if err := c.Compile(n.Left); err != nil {
				return err
			}
			c.at(n.Token.Source, n.Token.Position)
			step := runtime.StepIncrement
			if n.Operator == "-" {
				step = runtime.StepDecrement
			}
			c.emit(runtime.STEP, byte(step))
			return nil
		}
		err := c.Compile(n.Left)
		if err != nil {
			return err
		}
		err = c.Compile(n.Right)
		if err != nil {
			return err
		}
		// an operation that fails (o - 1 on an object: TYPE) fails at its operator, not at its right
		// operand (14/10/2026)
		c.at(n.Token.Source, n.Token.Position)

		switch n.Operator {
		case "+":
			c.emit(runtime.ADD)
		case "-":
			c.emit(runtime.SUB)
		case "*":
			c.emit(runtime.MUL)
		case "/":
			c.emit(runtime.DIV)
		case "%":
			c.emit(runtime.MOD)
		case "==", "===":
			c.emit(runtime.COMPARE, 0)
		case "!=", "!==":
			c.emit(runtime.COMPARE, 1)
		case ">":
			c.emit(runtime.COMPARE, 2)
		case "<":
			c.emit(runtime.COMPARE, 3)
		case ">=":
			c.emit(runtime.COMPARE, 4)
		case "<=":
			c.emit(runtime.COMPARE, 5)
		}

	case *PrefixExpression:
		err := c.Compile(n.Right)
		if err != nil {
			return err
		}
		c.at(n.Token.Source, n.Token.Position)
		switch n.Operator {
		case "-":
			constIndex := c.addConstant(value.New(-1))
			c.emitIndex(runtime.PUSH, constIndex)
			c.emit(runtime.MUL)
		case "!":
			c.emit(runtime.NOT)
		case "+":
			// +x: x; STEP 0, the number JavaScript's ToNumber makes of x, as Number(x) (16/10/2026, the
			// owner's E2): +"5" is 5, +"" 0, +"abc" NaN, +[5] 5. STEP 0 is what x++ used as a value already
			// makes of the old x, so no opcode is new
			c.emit(runtime.STEP, runtime.StepNumber)
		case "void":
			// void expr — bỏ kết quả biểu thức, trả về null (esbuild sinh `void 0`)
			c.emit(runtime.POP)
			nullIndex := c.addConstant(value.NewNull())
			c.emitIndex(runtime.PUSH, nullIndex)
		case "typeof":
			// typeof x: x; PUSH "#typeof"; INVOKE 0, which the VM answers before any key or method of x
			// (runtime.TypeofMethod, 14/10/2026). A name nothing declares reads null: "undefined", no error.
			typeofIndex := c.addConstant(value.NewString(runtime.TypeofMethod))
			c.emitIndex(runtime.PUSH, typeofIndex)
			c.emit(runtime.INVOKE, 0)
		}

	case *Literal:
		constIndex := c.addConstant(n.Value)
		c.emitIndex(runtime.PUSH, constIndex)

	case *Identifier:
		if n.Value == "kitwork" {
			c.emit(runtime.BUILTIN, 0)
			return nil
		}
		c.loadName(n)

	case *VarStatement:
		if n.DestructMode == DestructNone && len(n.Names) > 0 {
			nameFunction(n.Value, n.Names[0].Value)
		}
		err := c.Compile(n.Value)
		if err != nil {
			return err
		}

		if n.DestructMode == DestructObject || n.DestructMode == DestructArray {
			pattern := n.Pattern
			if pattern == nil {
				// const { a, b } = kitwork(), lowered from an import: the names are the keys.
				pattern = &BindingPattern{Array: n.DestructMode == DestructArray}
				for _, id := range n.Names {
					pattern.Elements = append(pattern.Elements, BindingElement{Key: id.Value, KeyToken: id.Token, Name: id})
				}
			}
			if err := c.compilePattern(pattern); err != nil {
				return err
			}
		} else {
			c.storeName(n.Names[0])
		}
		// STORE keeps the value on the stack. Commit the configured expression, then clean up.
		c.emit(runtime.COMMIT)
		c.emit(runtime.POP)

	case *AssignmentExpression:
		// a[i++]++, get().n += 1: the object and key run once, into their slots (15/10/2026)
		once, err := c.evaluateOnce(n)
		if err != nil {
			return err
		}
		n = once
		_, name := n.Name.(*Identifier)
		switch {
		case n.Update == PostfixUpdate:
			// x++ as a value is the old x (14/10/2026): read it, update, drop what the update left. The
			// old x is made a number, as JavaScript's x++ gives it (STEP 0, 15/10/2026): "5"++ gives 5.
			if err := c.Compile(n.Name); err != nil {
				return err
			}
			c.at(n.Token.Source, n.Token.Position)
			c.emit(runtime.STEP, runtime.StepNumber)
			if err := c.compileAssignment(n); err != nil {
				return err
			}
			c.emit(runtime.POP)
		case n.Update == PrefixUpdate && !name:
			// ++o.x as a value is the new o.x, not the object SET leaves: update, drop it, read again
			if err := c.compileAssignment(n); err != nil {
				return err
			}
			c.emit(runtime.POP)
			return c.Compile(n.Name)
		default:
			return c.compileAssignment(n)
		}

	case *TernaryExpression:
		ternFalseJumps, err := c.compileCondition(n.Condition)
		if err != nil {
			return err
		}
		err = c.Compile(n.Consequence)
		if err != nil {
			return err
		}
		ternJumpPos := c.emitJump(runtime.JUMP)
		c.patchJumps(ternFalseJumps, len(c.instructions))
		err = c.Compile(n.Alternative)
		if err != nil {
			return err
		}
		c.patchJump(ternJumpPos, len(c.instructions))

	case *IfExpression:
		falseJumps, err := c.compileCondition(n.Condition)
		if err != nil {
			return err
		}
		err = c.Compile(n.Consequence)
		if err != nil {
			return err
		}

		if n.Alternative != nil {
			jumpPos := c.emitJump(runtime.JUMP)
			c.patchJumps(falseJumps, len(c.instructions))
			err = c.Compile(n.Alternative)
			if err != nil {
				return err
			}
			c.patchJump(jumpPos, len(c.instructions))
		} else {
			c.patchJumps(falseJumps, len(c.instructions))
		}

	case *SwitchStatement:
		return c.compileSwitchStatement(n)

	case *BreakStatement:
		return c.compileBreakStatement()

	case *ContinueStatement:
		return c.compileContinueStatement()

	case *ForStatement:
		if err := c.Compile(n.Iterable); err != nil {
			return err
		}
		constZero := c.addConstant(value.New(0))
		c.emitIndex(runtime.PUSH, constZero)
		loopStart := len(c.instructions)
		exitJump := c.emitJump(runtime.ITER)
		// each iteration makes its own object when a closure reaches a variable of it (scope.go)
		if environment := c.loopEnvironment(n); environment != "" {
			c.newIteration(environment)
		}
		c.storeName(n.Item)
		c.emit(runtime.POP)
		c.pushLoopFrame(2)
		bodyErr := c.Compile(n.Body)
		frame := c.popFrame()
		if bodyErr != nil {
			return bodyErr
		}
		// continue goes on with the next item, as the end of the body does
		c.patchJumps(frame.continues, loopStart)
		c.emitJumpTo(runtime.JUMP, loopStart)
		loopEnd := len(c.instructions)
		c.patchJump(exitJump, loopEnd)
		c.patchBreakJumps(frame.jumps, loopEnd)

	case *ForRangeStatement:
		// Counting loop, compiled to a plain condition-jump — bounded by construction because the
		// parser guarantees the shape (declared counter, compares the counter, mutates the counter).
		// When a closure reaches a variable of an iteration, each iteration makes its own object
		// (scope.go): before the counter when the counter is one, else as the body starts.
		environment := c.loopEnvironment(n)
		var counter *binding
		if init, ok := n.Init.(*VarStatement); ok && len(init.Names) == 1 {
			counter = c.variable(init.Names[0])
		}
		counterInObject := environment != "" && counter != nil && counter.environment
		if counterInObject {
			c.newIteration(environment)
		}
		//   init: let i = 0        (VarStatement leaves a clean stack — it POPs its own value)
		if err := c.Compile(n.Init); err != nil {
			return err
		}
		loopStart := len(c.instructions)
		//   cond: i < n            (pushes a bool that FALSE consumes; jump past the loop when false)
		if err := c.Compile(n.Cond); err != nil {
			return err
		}
		exitJump := c.emitJump(runtime.FALSE)
		if environment != "" && !counterInObject {
			c.newIteration(environment)
		}
		c.pushLoopFrame(0)
		bodyErr := c.Compile(n.Body)
		frame := c.popFrame()
		if bodyErr != nil {
			return bodyErr
		}
		// continue goes on where the end of the body does: the counter's copy, then the update
		c.patchJumps(frame.continues, len(c.instructions))
		if counterInObject {
			// the next iteration gets its own counter before the update changes it
			c.nextIteration(environment, counter.slot)
		}
		//   update: i = i + 1      (AssignmentExpression leaves its value on the stack → POP it; i++ is the
		//   assignment alone, its old value is not read)
		if err := c.compileEffect(n.Update); err != nil {
			return err
		}
		c.emit(runtime.POP)
		c.emitJumpTo(runtime.JUMP, loopStart)
		loopEnd := len(c.instructions)
		c.patchJump(exitJump, loopEnd)
		c.patchBreakJumps(frame.jumps, loopEnd)

	case *BlockStatement:
		for _, s := range n.Statements {
			if err := c.Compile(s); err != nil {
				return err
			}
		}

	case *ReturnStatement:
		// a return inside for…of leaves the loop's iterable and index on the stack: they go first, as a
		// break drops them (14/10/2026; the verifier refused the program, RETURN retains 3 stack values)
		for _, frame := range c.breakFrames {
			for cleanup := 0; cleanup < frame.cleanup; cleanup++ {
				c.emit(runtime.POP)
			}
		}
		if n.ReturnValue != nil {
			if err := c.Compile(n.ReturnValue); err != nil {
				return err
			}
		} else {
			constNull := c.addConstant(value.Value{K: value.Nil})
			c.emitIndex(runtime.PUSH, constNull)
		}
		c.emit(runtime.COMMIT)
		c.emit(runtime.RETURN)

	case *CallExpression:
		if err := c.checkSafeCall(n); err != nil {
			return err
		}
		if err := c.Compile(n.Function); err != nil {
			return err
		}
		if n.Optional {
			if err := c.optionalLink(false); err != nil {
				return err
			}
		}
		for _, arg := range n.Arguments {
			if err := c.Compile(arg); err != nil {
				return err
			}
		}
		// The call is where what it calls starts, not its last argument (14/10/2026).
		c.at(n.Token.Source, startPosition(n.Function))
		c.emit(runtime.CALL, byte(len(n.Arguments)))

	case *ChainExpression:
		return c.compileChain(n.Expression)

	case *ObjectLiteral:
		c.emit(runtime.MAKE, 0)
		for index, entry := range n.Entries {
			if entry.IsSpread {
				if entry.Value == nil {
					return fmt.Errorf("compiler: object spread %d has no value", index+1)
				}
				if err := c.Compile(entry.Value); err != nil {
					return err
				}
				c.emit(runtime.MERGE)
			} else {
				if entry.Key == nil {
					return fmt.Errorf("compiler: object entry %d has no key", index+1)
				}
				if entry.Value == nil {
					return fmt.Errorf("compiler: object entry %d has no value", index+1)
				}
				c.emit(runtime.DUP)
				// Nếu key là Identifier, ta coi như chuỗi (JS style: { name: "..." })
				if id, ok := entry.Key.(*Identifier); ok {
					nameFunction(entry.Value, id.Value)
					idx := c.addConstant(value.NewString(id.Value))
					c.emitIndex(runtime.PUSH, idx)
				} else {
					nameFunction(entry.Value, entry.Key.String())
					if err := c.Compile(entry.Key); err != nil {
						return err
					}
				}
				if err := c.Compile(entry.Value); err != nil {
					return err
				}
				c.emit(runtime.SET)
				c.emit(runtime.POP) // Loại bỏ giá trị dư từ SET (SET đẩy lại target lên stack)
			}
		}

	case *ArrayLiteral:
		c.emit(runtime.MAKE, 1) // 1 for Array
		for i, el := range n.Elements {
			c.emit(runtime.DUP)
			// Push the index as the key for SET
			idx := c.addConstant(value.New(float64(i)))
			c.emitIndex(runtime.PUSH, idx)
			if err := c.Compile(el); err != nil {
				return err
			}
			c.emit(runtime.SET)
			c.emit(runtime.POP) // SET pushes target back, but we duped it already
		}

	case *IndexExpression:
		if err := c.Compile(n.Left); err != nil {
			return err
		}
		if n.Optional {
			if err := c.optionalLink(false); err != nil {
				return err
			}
		}
		if err := c.Compile(n.Index); err != nil {
			return err
		}
		// a read that fails (of a request with no answer) fails at its [ (14/10/2026)
		c.at(n.Token.Source, n.Token.Position)
		c.emit(runtime.GET)

	case *MethodCallExpression:
		// x.safe() is gone (14/10/2026): the parser reports it with the new spelling, so it never gets here
		// as a protected evaluation; with arguments it is an ordinary call of a method named safe.
		if err := c.Compile(n.Object); err != nil {
			return err
		}
		if n.Optional {
			if err := c.optionalLink(false); err != nil {
				return err
			}
		}
		if n.OptionalCall {
			// o.m?.(…): read o.m first; null skips the call with o still under it.
			c.emit(runtime.DUP)
			readIndex := c.addConstant(value.NewString(n.Method.Value))
			c.emitIndex(runtime.PUSH, readIndex)
			c.emit(runtime.GET)
			if err := c.optionalLink(true); err != nil {
				return err
			}
			c.emit(runtime.POP)
		}
		for _, arg := range n.Arguments {
			if err := c.Compile(arg); err != nil {
				return err
			}
		}
		// The call is at its dot, not at its last argument (14/10/2026).
		c.at(n.Token.Source, n.Token.Position)
		methIndex := c.addConstant(value.NewString(n.Method.Value))
		c.emitIndex(runtime.PUSH, methIndex)
		c.emit(runtime.INVOKE, byte(len(n.Arguments)))

	case *MemberExpression:
		if err := c.Compile(n.Object); err != nil {
			return err
		}
		if n.Optional {
			if err := c.optionalLink(false); err != nil {
				return err
			}
		}
		// a read that fails (.status of a request with no answer) fails at its dot, as a method call
		// does, not where the chain started (14/10/2026)
		c.at(n.Token.Source, n.Token.Position)
		propIndex := c.addConstant(value.NewString(n.Property.Value))
		c.emitIndex(runtime.PUSH, propIndex)
		c.emit(runtime.GET)

	case *FunctionLiteral:
		if c.scopes != nil {
			if environments := c.scopes.environments[n]; len(environments) > 0 {
				return c.compileHandedClosure(n, environments)
			}
		}
		return c.compileFunctionLiteral(n)

	case *TemplateLiteral:
		if len(n.Parts) == 0 {
			idx := c.addConstant(value.NewString(""))
			c.emitIndex(runtime.PUSH, idx)
			return nil
		}
		// A template is text: one that starts with two values, or is one value, starts from "", so
		// `${a}${b}` of 1 and 2 is "12" and `${5}` is "5" (14/10/2026: they added, and stayed a number).
		// One that starts with text needs nothing: its first + joins text.
		if !templateText(n.Parts[0]) && (len(n.Parts) == 1 || !templateText(n.Parts[1])) {
			empty := c.addConstant(value.NewString(""))
			c.emitIndex(runtime.PUSH, empty)
			if err := c.Compile(n.Parts[0]); err != nil {
				return err
			}
			c.emit(runtime.ADD)
		} else if err := c.Compile(n.Parts[0]); err != nil {
			return err
		}
		// Compile and ADD subsequent parts
		for i := 1; i < len(n.Parts); i++ {
			err := c.Compile(n.Parts[i])
			if err != nil {
				return err
			}
			c.emit(runtime.ADD)
		}
	}

	return nil
}

// compileAssignment compiles target = value, leaving what the assignment gives on the stack: the value for
// a name and a pattern, the object for o.x = v and o[k] = v (SET leaves its target). A SET that fails (an
// array's key that is no index) fails at the target's dot or bracket (14/10/2026).
func (c *Compiler) compileAssignment(n *AssignmentExpression) error {
	if id, ok := n.Name.(*Identifier); ok {
		nameFunction(n.Value, id.Value)
		return c.assignName(id, n.Value)
	} else if mem, ok := n.Name.(*MemberExpression); ok {
		nameFunction(n.Value, mem.String())
		if err := c.Compile(mem.Object); err != nil {
			return err
		}
		propIndex := c.addConstant(value.NewString(mem.Property.Value))
		c.emitIndex(runtime.PUSH, propIndex)
		if err := c.Compile(n.Value); err != nil {
			return err
		}
		c.at(mem.Token.Source, mem.Token.Position)
		c.emit(runtime.SET)
	} else if index, ok := n.Name.(*IndexExpression); ok {
		nameFunction(n.Value, index.String())
		if err := c.Compile(index.Left); err != nil {
			return err
		}
		if err := c.Compile(index.Index); err != nil {
			return err
		}
		if err := c.Compile(n.Value); err != nil {
			return err
		}
		c.at(index.Token.Source, index.Token.Position)
		c.emit(runtime.SET)
	} else if pattern, ok := n.Name.(*BindingPattern); ok {
		// ({ a, b: c } = o), [a, b] = arr: the value stays on the stack as the expression's result.
		if err := c.Compile(n.Value); err != nil {
			return err
		}
		if err := c.compilePattern(pattern); err != nil {
			return err
		}
	} else {
		location := c.getSourceLocation(n.Token.Source, n.Token.Position)
		return fmt.Errorf("%s:%d:%d: không gán được vào %s: vế trái của = là một tên, o.x, o[k] hay một mẫu tách { … }, [ … ]",
			location.File, location.Line, location.Column, n.Name.String())
	}
	return nil
}

// compileEffect compiles an expression whose value is dropped (a statement, a for's update): x++ and ++x
// are then the assignment alone, the code they always compiled to, as the old or new value is not read.
func (c *Compiler) compileEffect(e Expression) error {
	if update, ok := e.(*AssignmentExpression); ok && update.Update != NotAnUpdate {
		// where Compile would put it, so the debug table is the one it always was
		c.at(update.Token.Source, update.Token.Position)
		once, err := c.evaluateOnce(update)
		if err != nil {
			return err
		}
		return c.compileAssignment(once)
	}
	return c.Compile(e)
}

// evaluateOnce runs the object and the key of an update or a compound assignment of a member or an item
// once, into the slots scope.go gave them (onceParts), and gives back the assignment on those slots, as
// JavaScript evaluates them once (15/10/2026). Until then the target was compiled where it is read and
// where it is written, and once more for x++ or ++x used as a value, so a[i++]++ read one item and wrote
// another. An assignment whose object and key only read (a[i]++, o.n += 1) is given back as it is: its
// code did not change.
//
//	object; STORE #o; POP; [key; STORE #k; POP]; <the assignment, reading LOAD #o and LOAD #k>
func (c *Compiler) evaluateOnce(n *AssignmentExpression) (*AssignmentExpression, error) {
	if c.scopes == nil {
		return n, nil
	}
	slots, ok := c.scopes.once[n]
	if !ok {
		return n, nil
	}
	keep := func(e Expression, slot string, at Token) (Expression, error) {
		if slot == "" {
			return e, nil
		}
		if err := c.Compile(e); err != nil {
			return nil, err
		}
		c.emitName(runtime.STORE, slot)
		c.emit(runtime.POP)
		return &Identifier{Token: at, Value: slot}, nil
	}
	var target Expression
	switch name := n.Name.(type) {
	case *MemberExpression:
		object, err := keep(name.Object, slots.object, name.Token)
		if err != nil {
			return nil, err
		}
		target = &MemberExpression{Token: name.Token, Object: object, Property: name.Property}
	case *IndexExpression:
		object, err := keep(name.Left, slots.object, name.Token)
		if err != nil {
			return nil, err
		}
		key, err := keep(name.Index, slots.key, name.Token)
		if err != nil {
			return nil, err
		}
		target = &IndexExpression{Token: name.Token, Left: object, Index: key}
	default:
		return n, nil
	}
	infix := n.Value.(*InfixExpression)
	return &AssignmentExpression{
		Token:  n.Token,
		Name:   target,
		Value:  &InfixExpression{Token: infix.Token, Left: target, Operator: infix.Operator, Right: infix.Right, Step: infix.Step},
		Update: n.Update,
	}, nil
}

// compileFunctionLiteral jumps over the function's code and pushes it as a value, which the VM makes a
// closure of the frame running.
func (c *Compiler) compileFunctionLiteral(n *FunctionLiteral) error {
	jumpOver := c.emitJump(runtime.JUMP)
	startIP := len(c.instructions)
	if c.scopes != nil {
		// a parameter assigned while a function around may hold its name lives in a slot of its own
		// (scope.go): the function copies it there as it starts
		for _, parameter := range c.scopes.copies[n] {
			c.emitName(runtime.LOAD, parameter.name)
			c.emitName(runtime.STORE, parameter.slot)
			c.emit(runtime.POP)
		}
	}
	outerBreakFrames := c.breakFrames
	c.breakFrames = nil
	bodyErr := c.Compile(n.Body)
	c.breakFrames = outerBreakFrames
	if bodyErr != nil {
		return bodyErr
	}
	c.emit(runtime.RETURN)
	endIP := len(c.instructions)
	c.patchJump(jumpOver, endIP)

	params := make([]string, len(n.Parameters))
	for i, p := range n.Parameters {
		if p == nil {
			return fmt.Errorf("compiler: arrow function parameter %d is invalid", i+1)
		}
		params[i] = p.Value
	}
	n.Address = startIP // Propagate address back to AST node
	name := n.DebugName
	if name == "" {
		name = "<anonymous>"
	}
	location := c.getSourceLocation(n.Token.Source, n.Token.Position)
	fnData := &value.Lambda{
		Address:      startIP,
		Name:         name,
		SourceFile:   location.File,
		SourceLine:   location.Line,
		SourceColumn: location.Column,
		Params:       params,
	}
	idx := c.addConstant(value.New(fnData))
	c.emitIndex(runtime.PUSH, idx)
	return nil
}

func (c *Compiler) compileSwitchStatement(statement *SwitchStatement) error {
	if statement == nil || statement.Discriminant == nil {
		return fmt.Errorf("compiler: switch discriminant is required")
	}
	if err := c.Compile(statement.Discriminant); err != nil {
		return err
	}

	caseDispatchJumps := make([]int, len(statement.Cases))
	for index := range caseDispatchJumps {
		caseDispatchJumps[index] = -1
	}
	defaultIndex := -1
	for index, clause := range statement.Cases {
		if clause.Test == nil {
			if defaultIndex >= 0 {
				return fmt.Errorf("compiler: switch has multiple default clauses")
			}
			defaultIndex = index
			continue
		}
		c.emit(runtime.DUP)
		if err := c.Compile(clause.Test); err != nil {
			return err
		}
		c.emit(runtime.COMPARE, 0)
		caseDispatchJumps[index] = c.emitJump(runtime.TRUE)
	}

	// Both matched and unmatched dispatch paths discard the single discriminant
	// before entering a body, so fallthrough bodies all share one stack shape.
	c.emit(runtime.POP)
	noMatchJump := c.emitJump(runtime.JUMP)
	bodyDispatchJumps := make([]int, len(statement.Cases))
	for index := range bodyDispatchJumps {
		bodyDispatchJumps[index] = -1
	}
	for index, dispatchJump := range caseDispatchJumps {
		if dispatchJump < 0 {
			continue
		}
		c.patchJump(dispatchJump, len(c.instructions))
		c.emit(runtime.POP)
		bodyDispatchJumps[index] = c.emitJump(runtime.JUMP)
	}

	bodyPositions := make([]int, len(statement.Cases))
	c.pushBreakFrame(compilerBreakFrame{})
	var bodyErr error
	for index, clause := range statement.Cases {
		bodyPositions[index] = len(c.instructions)
		for _, caseStatement := range clause.Statements {
			if err := c.Compile(caseStatement); err != nil {
				bodyErr = err
				break
			}
		}
		if bodyErr != nil {
			break
		}
	}
	breakJumps := c.popFrame().jumps
	if bodyErr != nil {
		return bodyErr
	}

	switchEnd := len(c.instructions)
	for index, bodyJump := range bodyDispatchJumps {
		if bodyJump >= 0 {
			c.patchJump(bodyJump, bodyPositions[index])
		}
	}
	if defaultIndex >= 0 {
		c.patchJump(noMatchJump, bodyPositions[defaultIndex])
	} else {
		c.patchJump(noMatchJump, switchEnd)
	}
	c.patchBreakJumps(breakJumps, switchEnd)
	return nil
}

// at moves the position the next instructions record, when the source is one this compiler knows.
func (c *Compiler) at(source string, position int32) {
	if _, known := c.sources[source]; known {
		c.currentSource = source
		c.currentPos = position
	}
}

func (c *Compiler) pushNull() {
	index := c.addConstant(value.NewNull())
	c.emitIndex(runtime.PUSH, index)
}

// emitNullishTest leaves the value on top of the stack where it is and jumps when it is null or
// undefined (one Nil in the VM); it returns the jump to patch. 0, "", false and NaN are not nullish.
// A truthy value is let through first: a proxy (the query builder's row) is truthy, and comparing it
// with null would ask the proxy to build a condition.
//
//	DUP; TRUE go; DUP; PUSH null; COMPARE ==; TRUE <nullish>; go:
func (c *Compiler) emitNullishTest() int {
	c.emit(runtime.DUP)
	present := c.emitJump(runtime.TRUE)
	c.emit(runtime.DUP)
	c.pushNull()
	c.emit(runtime.COMPARE, 0)
	nullish := c.emitJump(runtime.TRUE)
	c.patchJump(present, len(c.instructions))
	return nullish
}

// emitPresentExits is the other side of the same test, for a ?? b and a default: two jumps, to patch
// to where the value is kept, taken when the value on top is not null; falling through, it is null.
//
//	DUP; TRUE keep; DUP; PUSH null; COMPARE ==; FALSE keep; (null here)
func (c *Compiler) emitPresentExits() []int {
	c.emit(runtime.DUP)
	truthy := c.emitJump(runtime.TRUE)
	c.emit(runtime.DUP)
	c.pushNull()
	c.emit(runtime.COMPARE, 0)
	notNull := c.emitJump(runtime.FALSE)
	return []int{truthy, notNull}
}

// optionalLink is a ?. of the chain being compiled: when the value on top is null the chain ends there
// and is null. method is o.m?.(…), whose test runs on o.m with o under it.
func (c *Compiler) optionalLink(method bool) error {
	if c.chain == nil {
		return fmt.Errorf("compiler: ?. ngoài một chuỗi tuỳ chọn")
	}
	jump := c.emitNullishTest()
	if method {
		c.chain.method = append(c.chain.method, jump)
	} else {
		c.chain.short = append(c.chain.short, jump)
	}
	return nil
}

// compileChain compiles an optional chain: every ?. in it that finds null jumps to its end, where that
// null is the chain's one value. A chain without ?. compiles exactly as the expression alone.
func (c *Compiler) compileChain(expression Expression) error {
	outer := c.chain
	jumps := &chainJumps{}
	c.chain = jumps
	err := c.Compile(expression)
	c.chain = outer
	if err != nil {
		return err
	}
	if len(jumps.method) > 0 {
		over := c.emitJump(runtime.JUMP)
		for _, jump := range jumps.method {
			c.patchJump(jump, len(c.instructions))
		}
		// [o, null] → [null]
		c.emit(runtime.POP)
		c.emit(runtime.POP)
		c.pushNull()
		c.patchJump(over, len(c.instructions))
	}
	for _, jump := range jumps.short {
		c.patchJump(jump, len(c.instructions))
	}
	return nil
}

// compileLogical compiles a && b, a || b, a ?? b as JavaScript runs them (14/10/2026): b runs only when
// a does not decide, and the result is the operand that decided. Until then the compiler ran both
// sides and chose with AND/OR, so b's calls, writes and errors happened either way.
//
//	a && b: a; DUP; FALSE end; POP; b; end:
//	a || b: a; DUP; TRUE end; POP; b; end:
//	a ?? b: a; <a not null: end>; POP; b; end:
//
// A right side that is a constant or a name can neither fail nor change anything, so running it
// anyway is the same program: there the compiler keeps AND/OR, one byte instead of five. With the
// condition form (compileCondition) that kept the largest bundles under bytecode 2's 65,535-byte limit
// of one program (bytecode 3 holds 2 MiB): apptop.vn's app/{host} route was 63,630 bytes with 654 of
// these operators, 310 of them with such a right side, and was 64,550 bytes after (every operator in
// the jump form would have made it 66,246).
func (c *Compiler) compileLogical(n *InfixExpression) error {
	if err := c.Compile(n.Left); err != nil {
		return err
	}
	if n.Operator != "??" && plainOperand(n.Right) {
		if err := c.Compile(n.Right); err != nil {
			return err
		}
		if n.Operator == "&&" {
			c.emit(runtime.AND)
		} else {
			c.emit(runtime.OR)
		}
		return nil
	}
	var exits []int
	switch n.Operator {
	case "&&":
		c.emit(runtime.DUP)
		exits = append(exits, c.emitJump(runtime.FALSE))
	case "||":
		c.emit(runtime.DUP)
		exits = append(exits, c.emitJump(runtime.TRUE))
	case "??":
		exits = c.emitPresentExits()
	}
	c.emit(runtime.POP)
	if err := c.Compile(n.Right); err != nil {
		return err
	}
	for _, exit := range exits {
		c.patchJump(exit, len(c.instructions))
	}
	return nil
}

// compileCondition compiles the condition of an if or a ternary for its truth alone, and returns the
// jumps taken when it is false. A && or || there needs no value, only where to go: if (a && f()) is
// a; FALSE else; f(); FALSE else, two bytes shorter than its value form, with the same order and
// short-circuit. Any other condition compiles as before: the expression, then FALSE.
func (c *Compiler) compileCondition(condition Expression) ([]int, error) {
	return c.compileJumps(condition, false)
}

// compileJumps compiles e for its truth and returns the jumps taken when that truth is `when`; control
// falls through otherwise, with the stack as it was.
func (c *Compiler) compileJumps(e Expression, when bool) ([]int, error) {
	if branching(e) {
		infix := e.(*InfixExpression)
		if (infix.Operator == "&&") != when {
			// a && b is false as soon as a is, a || b true as soon as a is: both jump to one place.
			left, err := c.compileJumps(infix.Left, when)
			if err != nil {
				return nil, err
			}
			right, err := c.compileJumps(infix.Right, when)
			if err != nil {
				return nil, err
			}
			return append(left, right...), nil
		}
		// a && b is not true when a is false, a || b not false when a is true: then b is skipped.
		skip, err := c.compileJumps(infix.Left, !when)
		if err != nil {
			return nil, err
		}
		jumps, err := c.compileJumps(infix.Right, when)
		if err != nil {
			return nil, err
		}
		c.patchJumps(skip, len(c.instructions))
		return jumps, nil
	}
	if prefix, ok := e.(*PrefixExpression); ok && prefix.Operator == "!" && branching(prefix.Right) {
		return c.compileJumps(prefix.Right, !when)
	}
	if err := c.Compile(e); err != nil {
		return nil, err
	}
	if when {
		return []int{c.emitJump(runtime.TRUE)}, nil
	}
	return []int{c.emitJump(runtime.FALSE)}, nil
}

// branching is a && or || that compileLogical would give jumps (its right side is not plain).
func branching(e Expression) bool {
	infix, ok := e.(*InfixExpression)
	return ok && (infix.Operator == "&&" || infix.Operator == "||") && !plainOperand(infix.Right)
}

func (c *Compiler) patchJumps(jumps []int, target int) {
	for _, jump := range jumps {
		c.patchJump(jump, target)
	}
}

// plainOperand is an operand whose evaluation reads a constant or a variable and nothing else: it
// cannot fail, call, or write (LOAD of a missing name is null).
func plainOperand(e Expression) bool {
	switch e.(type) {
	case *Literal, *Identifier:
		return true
	}
	return false
}

// readsOnly is an expression that reads and does nothing else: a name, a literal, a member or an item of
// such, an operator, a ternary or a template of such. Evaluated twice it gives the same value, so the
// object and key of an update or a compound assignment of such need no slot (evaluateOnce). A call, an
// assignment, an update or a function made is more than a read.
func readsOnly(e Expression) bool {
	switch n := e.(type) {
	case *Identifier, *Literal:
		return true
	case *MemberExpression:
		return !n.Optional && readsOnly(n.Object)
	case *IndexExpression:
		return !n.Optional && readsOnly(n.Left) && readsOnly(n.Index)
	case *InfixExpression:
		return readsOnly(n.Left) && readsOnly(n.Right)
	case *PrefixExpression:
		return readsOnly(n.Right)
	case *TernaryExpression:
		return readsOnly(n.Condition) && readsOnly(n.Consequence) && readsOnly(n.Alternative)
	case *TemplateLiteral:
		for _, part := range n.Parts {
			if !readsOnly(part) {
				return false
			}
		}
		return true
	}
	return false
}

// compilePattern destructures the value on top of the stack, which stays there: each element reads its
// key (an index in an array pattern) as a member read does, takes its default when what it read is null
// or missing, then stores a variable or destructures further.
//
//	DUP; PUSH key; GET; [default]; STORE name; POP
//
// { a } uses one constant for the key and the name, as before 14/10/2026, so the destructuring code
// already written compiles to the same bytes.
func (c *Compiler) compilePattern(pattern *BindingPattern) error {
	for index, element := range pattern.Elements {
		c.emit(runtime.DUP)
		var keyIndex int
		if pattern.Array {
			keyIndex = c.addConstant(value.New(index))
		} else {
			keyIndex = c.addConstant(value.NewString(element.Key))
		}
		c.emitIndex(runtime.PUSH, keyIndex)
		c.emit(runtime.GET)
		if element.Default != nil {
			// what was read stays unless it is null or missing; then the default replaces it, as v ?? d
			exits := c.emitPresentExits()
			c.emit(runtime.POP)
			if err := c.Compile(element.Default); err != nil {
				return err
			}
			for _, exit := range exits {
				c.patchJump(exit, len(c.instructions))
			}
		}
		switch {
		case element.Pattern != nil:
			if err := c.compilePattern(element.Pattern); err != nil {
				return err
			}
			c.emit(runtime.POP)
		case element.Name != nil:
			if found := c.variable(element.Name); !pattern.Array && c.slotOf(element.Name) == element.Key &&
				(found == nil || !found.environment) {
				c.emitIndex(runtime.STORE, keyIndex)
			} else {
				c.storeName(element.Name)
			}
			c.emit(runtime.POP)
		default:
			return fmt.Errorf("compiler: phần %d của mẫu tách không có tên", index+1)
		}
	}
	return nil
}

func (c *Compiler) compileBreakStatement() error {
	if len(c.breakFrames) == 0 {
		return fmt.Errorf("compiler: break outside switch or bounded loop")
	}
	frameIndex := len(c.breakFrames) - 1
	for cleanup := 0; cleanup < c.breakFrames[frameIndex].cleanup; cleanup++ {
		c.emit(runtime.POP)
	}
	jump := c.emitJump(runtime.JUMP)
	c.breakFrames[frameIndex].jumps = append(c.breakFrames[frameIndex].jumps, jump)
	return nil
}

// compileContinueStatement jumps to where the nearest loop goes on (14/10/2026): its frame patches the
// jump once that place is known. The frames left on the way are switches, whose values (none) go first,
// as a break drops them; the loop's own (for…of's iterable and index) stay for its next iteration.
func (c *Compiler) compileContinueStatement() error {
	for index := len(c.breakFrames) - 1; index >= 0; index-- {
		if !c.breakFrames[index].loop {
			continue
		}
		for _, frame := range c.breakFrames[index+1:] {
			for cleanup := 0; cleanup < frame.cleanup; cleanup++ {
				c.emit(runtime.POP)
			}
		}
		jump := c.emitJump(runtime.JUMP)
		c.breakFrames[index].continues = append(c.breakFrames[index].continues, jump)
		return nil
	}
	return fmt.Errorf("compiler: continue outside a bounded loop")
}

func (c *Compiler) pushBreakFrame(frame compilerBreakFrame) {
	c.breakFrames = append(c.breakFrames, frame)
}

// pushLoopFrame opens a for's frame: break leaves it, dropping cleanup values; continue goes on with it.
func (c *Compiler) pushLoopFrame(cleanup int) {
	c.pushBreakFrame(compilerBreakFrame{cleanup: cleanup, loop: true})
}

func (c *Compiler) popFrame() compilerBreakFrame {
	last := len(c.breakFrames) - 1
	frame := c.breakFrames[last]
	c.breakFrames = c.breakFrames[:last]
	return frame
}

func (c *Compiler) patchBreakJumps(jumps []int, target int) {
	for _, jump := range jumps {
		c.patchJump(jump, target)
	}
}

func (c *Compiler) Reset() {
	if c.instructions != nil {
		c.instructions = c.instructions[:0]
	}
	if c.constants != nil {
		c.constants = c.constants[:0]
	}
	if c.debugEntries != nil {
		c.debugEntries = c.debugEntries[:0]
	}
	if c.breakFrames != nil {
		c.breakFrames = c.breakFrames[:0]
	}
	c.scopes = nil
	c.wide, c.overflow = false, false
}

func (c *Compiler) ByteCodeResult() (*Bytecode, error) {
	if c.overflow {
		// only a compile that did not start from a Program gets here: its jumps were never rewritten
		return nil, fmt.Errorf("compiler: a jump address past %d bytes was written in two bytes", runtime.MaxNarrowOperand)
	}
	if len(c.instructions) > runtime.MaxBytecodeSize {
		location := c.debugLocationAt(runtime.MaxBytecodeSize)
		return nil, fmt.Errorf(
			"%s: chương trình dịch ra %d byte bytecode, quá giới hạn %d byte của một chương trình (đến đây thì vượt): chia bớt mã ra module, để mỗi route chỉ import phần nó dùng",
			locationText(location), len(c.instructions), runtime.MaxBytecodeSize,
		)
	}
	program, err := runtime.NewProgramWithDebug(c.instructions, c.constants, c.debugEntries)
	if err != nil {
		var verifyErr *runtime.VerifyError
		if errors.As(err, &verifyErr) &&
			verifyErr.IP >= 0 &&
			verifyErr.IP < len(c.instructions) {
			location := c.debugLocationAt(verifyErr.IP)
			return nil, fmt.Errorf(
				"compiler produced invalid bytecode at %s:%d:%d: %w",
				location.File,
				location.Line,
				location.Column,
				err,
			)
		}
		return nil, fmt.Errorf("compiler produced invalid bytecode: %w", err)
	}
	// What a cache or a check stores is the envelope, whose debug table writes each entry's file name: a
	// dense module on a long path passes MaxProgramBinarySize with its code under the limit. Such a
	// program ran, but was never cached and failed every check (15/10/2026).
	if size, crossing := program.EncodedSize(); size > runtime.MaxProgramBinarySize {
		location, reached := c.debugLocationAt(0), "mã và hằng của nó đã vượt"
		if entries := program.DebugEntries(); crossing >= 0 && crossing < len(entries) {
			location, reached = entries[crossing].SourceLocation, "đến đây thì vượt"
		}
		return nil, fmt.Errorf(
			"%s: chương trình lưu ra %d byte (mã, hằng và bảng vị trí, mỗi mục ghi tên tệp của nó), quá giới hạn %d byte của một chương trình (%s): chia bớt mã ra module, để mỗi route chỉ import phần nó dùng, hay đặt module ở đường dẫn ngắn hơn",
			locationText(location), size, runtime.MaxProgramBinarySize, reached,
		)
	}
	return &Bytecode{
		Program:             program,
		compilerFingerprint: Fingerprint(),
		sourceFingerprint:   c.sourceFingerprint,
	}, nil
}

func (c *Compiler) emit(op runtime.Opcode, operands ...byte) int {
	pos := len(c.instructions)
	c.instructions = append(c.instructions, byte(op))
	c.instructions = append(c.instructions, operands...)

	location := c.getSourceLocation(c.currentSource, c.currentPos)
	if location.Line > 0 &&
		(len(c.debugEntries) == 0 ||
			c.debugEntries[len(c.debugEntries)-1].SourceLocation != location) {
		c.debugEntries = append(c.debugEntries, runtime.DebugEntry{
			IP:             pos,
			SourceLocation: location,
		})
	}
	return pos
}

func (c *Compiler) debugLocationAt(ip int) runtime.SourceLocation {
	for index := len(c.debugEntries) - 1; index >= 0; index-- {
		if c.debugEntries[index].IP <= ip {
			return c.debugEntries[index].SourceLocation
		}
	}
	return runtime.SourceLocation{}
}

// emitIndex writes PUSH, LOAD or STORE of a constant: its wide form when the index needs three bytes.
func (c *Compiler) emitIndex(op runtime.Opcode, index int) int {
	if index > runtime.MaxNarrowOperand {
		return c.emit(op.Wide(), byte(index>>16), byte(index>>8), byte(index))
	}
	return c.emit(op, byte(index>>8), byte(index))
}

// emitJump writes JUMP, TRUE, FALSE or ITER whose address patchJump writes once it is known: wide when
// the program is.
func (c *Compiler) emitJump(op runtime.Opcode) int {
	if c.wide {
		return c.emit(op.Wide(), 0, 0, 0)
	}
	return c.emit(op, 0, 0)
}

// emitJumpTo writes a jump back to an address already known.
func (c *Compiler) emitJumpTo(op runtime.Opcode, target int) {
	c.patchJump(c.emitJump(op), target)
}

// patchJump writes the address of the jump at position jump: three bytes in a wide jump, two otherwise.
func (c *Compiler) patchJump(jump, target int) {
	if runtime.Opcode(c.instructions[jump]).IsWide() {
		c.instructions[jump+1] = byte(target >> 16)
		c.instructions[jump+2] = byte(target >> 8)
		c.instructions[jump+3] = byte(target)
		return
	}
	if target > runtime.MaxNarrowOperand {
		c.overflow = true
	}
	binary.BigEndian.PutUint16(c.instructions[jump+1:], uint16(target))
}

func (c *Compiler) addConstant(v value.Value) int {
	c.constants = append(c.constants, v)
	return len(c.constants) - 1
}
