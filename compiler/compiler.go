package compiler

import (
	"encoding/binary"
	"fmt"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// Bytecode đại diện cho kết quả sau khi biên dịch
type Bytecode struct {
	Instructions []byte
	Constants    []value.Value
	SourceMap    []int32
	// Files lists every source file compiled in: the entry plus all natively-bundled imports.
	// Hot reload stats these to catch edits — including edits to an imported ./_core module.
	Files []string
}

// Compiler chịu trách nhiệm chuyển đổi AST thành Bytecode
type Compiler struct {
	instructions []byte
	constants    []value.Value
	sourceMap    []int32
	lineOffsets  []int32
	currentPos   int32
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
	return &Compiler{
		instructions: []byte{},
		constants:    []value.Value{},
		sourceMap:    []int32{},
		lineOffsets:  offsets,
	}
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
	}
	return 0
}

func (c *Compiler) getLineNumber(pos int32) int32 {
	if len(c.lineOffsets) == 0 {
		return 0
	}
	l, r := 0, len(c.lineOffsets)-1
	ans := 0
	for l <= r {
		mid := (l + r) / 2
		if c.lineOffsets[mid] <= pos {
			ans = mid
			l = mid + 1
		} else {
			r = mid - 1
		}
	}
	return int32(ans + 1)
}

// Compile bắt đầu quá trình duyệt cây và phát sinh mã
func (c *Compiler) Compile(node Node) error {
	if node == nil {
		return nil
	}

	if pos := getNodePosition(node); pos > 0 {
		c.currentPos = pos
	}

	switch n := node.(type) {
	case *Program:
		for i, s := range n.Statements {
			err := c.Compile(s)
			if err != nil {
				return err
			}
			// Nếu là ExpressionStatement và KHÔNG PHẢI cuối cùng, ta POP
			if _, ok := s.(*ExpressionStatement); ok {
				if i < len(n.Statements)-1 {
					c.emit(runtime.POP)
				}
			}
		}
		// Luôn kết thúc Program bằng RETURN để đảm bảo thực thi defer
		c.emit(runtime.RETURN)

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
		if err := c.Compile(n.Expression); err != nil {
			return err
		}
		switch n.Expression.(type) {
		case *IfExpression:
			// Skip POP for control flow expressions (if statements)
		default:
			// POPFIN (a safe superset of POP): pops the discarded value AND, if it is a
			// value.StatementFinalizer (a lazy http request), fires it once + runs .then()/.catch().
			// For every other value it is identical to POP.
			c.emit(runtime.POPFIN)
		}
		return nil

	case *InfixExpression:
		err := c.Compile(n.Left)
		if err != nil {
			return err
		}
		err = c.Compile(n.Right)
		if err != nil {
			return err
		}

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
		case "&&":
			c.emit(runtime.AND)
		case "||":
			c.emit(runtime.OR)
		}

	case *PrefixExpression:
		err := c.Compile(n.Right)
		if err != nil {
			return err
		}
		switch n.Operator {
		case "-":
			constIndex := c.addConstant(value.New(-1))
			c.emit(runtime.PUSH, byte(constIndex>>8), byte(constIndex&0xFF))
			c.emit(runtime.MUL)
		case "!":
			c.emit(runtime.NOT)
		case "void":
			// void expr — bỏ kết quả biểu thức, trả về null (esbuild sinh `void 0`)
			c.emit(runtime.POP)
			nullIndex := c.addConstant(value.NewNull())
			c.emit(runtime.PUSH, byte(nullIndex>>8), byte(nullIndex&0xFF))
		}

	case *Literal:
		constIndex := c.addConstant(n.Value)
		c.emit(runtime.PUSH, byte(constIndex>>8), byte(constIndex&0xFF))

	case *Identifier:
		if n.Value == "kitwork" {
			c.emit(runtime.BUILTIN, 0)
			return nil
		}
		symbolIndex := c.addConstant(value.NewString(n.Value))
		c.emit(runtime.LOAD, byte(symbolIndex>>8), byte(symbolIndex&0xFF))

	case *VarStatement:
		err := c.Compile(n.Value)
		if err != nil {
			return err
		}

		if n.DestructMode == DestructObject {
			for _, id := range n.Names {
				c.emit(runtime.DUP)
				symbolIndex := c.addConstant(value.NewString(id.Value))
				c.emit(runtime.PUSH, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
				c.emit(runtime.GET)
				c.emit(runtime.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
				c.emit(runtime.POP)
			}
		} else if n.DestructMode == DestructArray {
			for i, id := range n.Names {
				c.emit(runtime.DUP)
				idxIndex := c.addConstant(value.New(i))
				c.emit(runtime.PUSH, byte(idxIndex>>8), byte(idxIndex&0xFF))
				c.emit(runtime.GET)
				symbolIndex := c.addConstant(value.NewString(id.Value))
				c.emit(runtime.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
				c.emit(runtime.POP)
			}
		} else {
			symbolIndex := c.addConstant(value.NewString(n.Names[0].Value))
			c.emit(runtime.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
		}
		// POPFINSOFT (soft finalize): if the assigned value is an http request WITH a .then()/.catch()
		// handler, fire it + run the handler here; a plain lazy request stays lazy. Otherwise = POP.
		c.emit(runtime.POPFINSOFT)

	case *AssignmentExpression:
		if id, ok := n.Name.(*Identifier); ok {
			err := c.Compile(n.Value)
			if err != nil {
				return err
			}
			symbolIndex := c.addConstant(value.NewString(id.Value))
			c.emit(runtime.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
		} else if mem, ok := n.Name.(*MemberExpression); ok {
			c.Compile(mem.Object)
			propIndex := c.addConstant(value.NewString(mem.Property.Value))
			c.emit(runtime.PUSH, byte(propIndex>>8), byte(propIndex&0xFF))
			c.Compile(n.Value)
			c.emit(runtime.SET)
		} else if obj, ok := n.Name.(*ObjectLiteral); ok {
			err := c.Compile(n.Value)
			if err != nil {
				return err
			}
			for _, entry := range obj.Entries {
				if id, ok := entry.Key.(*Identifier); ok {
					c.emit(runtime.DUP)
					symbolIndex := c.addConstant(value.NewString(id.Value))
					c.emit(runtime.PUSH, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
					c.emit(runtime.GET)
					c.emit(runtime.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
					c.emit(runtime.POP)
				}
			}
		} else if arr, ok := n.Name.(*ArrayLiteral); ok {
			err := c.Compile(n.Value)
			if err != nil {
				return err
			}
			for i, el := range arr.Elements {
				if id, ok := el.(*Identifier); ok {
					c.emit(runtime.DUP)
					idxIndex := c.addConstant(value.New(i))
					c.emit(runtime.PUSH, byte(idxIndex>>8), byte(idxIndex&0xFF))
					c.emit(runtime.GET)
					symbolIndex := c.addConstant(value.NewString(id.Value))
					c.emit(runtime.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
					c.emit(runtime.POP)
				}
			}
		}

	case *TernaryExpression:
		err := c.Compile(n.Condition)
		if err != nil {
			return err
		}
		ternFalsePos := c.emit(runtime.FALSE, 0, 0)
		err = c.Compile(n.Consequence)
		if err != nil {
			return err
		}
		ternJumpPos := c.emit(runtime.JUMP, 0, 0)
		c.patchUint16(ternFalsePos+1, uint16(len(c.instructions)))
		err = c.Compile(n.Alternative)
		if err != nil {
			return err
		}
		c.patchUint16(ternJumpPos+1, uint16(len(c.instructions)))

	case *IfExpression:
		err := c.Compile(n.Condition)
		if err != nil {
			return err
		}
		falsePos := c.emit(runtime.FALSE, 0, 0)
		err = c.Compile(n.Consequence)
		if err != nil {
			return err
		}

		if n.Alternative != nil {
			jumpPos := c.emit(runtime.JUMP, 0, 0)
			c.patchUint16(falsePos+1, uint16(len(c.instructions)))
			err = c.Compile(n.Alternative)
			if err != nil {
				return err
			}
			c.patchUint16(jumpPos+1, uint16(len(c.instructions)))
		} else {
			c.patchUint16(falsePos+1, uint16(len(c.instructions)))
		}

	case *ForStatement:
		c.Compile(n.Iterable)
		constZero := c.addConstant(value.New(0))
		c.emit(runtime.PUSH, byte(constZero>>8), byte(constZero&0xFF))
		loopStart := len(c.instructions)
		exitJump := c.emit(runtime.ITER, 0, 0)
		symbolIndex := c.addConstant(value.NewString(n.Item.Value))
		c.emit(runtime.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
		c.emit(runtime.POP)
		c.Compile(n.Body)
		c.emit(runtime.JUMP, byte(loopStart>>8), byte(loopStart&0xFF))
		c.patchUint16(exitJump+1, uint16(len(c.instructions)))
		c.emit(runtime.POP)
		c.emit(runtime.POP)

	case *ForRangeStatement:
		// Counting loop, compiled to a plain condition-jump — bounded by construction because the
		// parser guarantees the shape (declared counter, compares the counter, mutates the counter).
		//   init: let i = 0        (VarStatement leaves a clean stack — it POPs its own value)
		if err := c.Compile(n.Init); err != nil {
			return err
		}
		loopStart := len(c.instructions)
		//   cond: i < n            (pushes a bool that FALSE consumes; jump past the loop when false)
		if err := c.Compile(n.Cond); err != nil {
			return err
		}
		exitJump := c.emit(runtime.FALSE, 0, 0)
		if err := c.Compile(n.Body); err != nil {
			return err
		}
		//   update: i = i + 1      (AssignmentExpression leaves its value on the stack → POP it)
		if err := c.Compile(n.Update); err != nil {
			return err
		}
		c.emit(runtime.POP)
		c.emit(runtime.JUMP, byte(loopStart>>8), byte(loopStart&0xFF))
		c.patchUint16(exitJump+1, uint16(len(c.instructions)))

	case *BlockStatement:
		for _, s := range n.Statements {
			c.Compile(s)
		}

	case *ReturnStatement:
		if n.ReturnValue != nil {
			c.Compile(n.ReturnValue)
		} else {
			constNull := c.addConstant(value.Value{K: value.Nil})
			c.emit(runtime.PUSH, byte(constNull>>8), byte(constNull&0xFF))
		}
		c.emit(runtime.RETURN)

	case *CallExpression:
		c.Compile(n.Function)
		for _, arg := range n.Arguments {
			c.Compile(arg)
		}
		c.emit(runtime.CALL, byte(len(n.Arguments)))

	case *ObjectLiteral:
		c.emit(runtime.MAKE, 0)
		for _, entry := range n.Entries {
			if entry.IsSpread {
				c.Compile(entry.Value)
				c.emit(runtime.MERGE)
			} else {
				c.emit(runtime.DUP)
				// Nếu key là Identifier, ta coi như chuỗi (JS style: { name: "..." })
				if id, ok := entry.Key.(*Identifier); ok {
					idx := c.addConstant(value.NewString(id.Value))
					c.emit(runtime.PUSH, byte(idx>>8), byte(idx&0xFF))
				} else {
					c.Compile(entry.Key)
				}
				c.Compile(entry.Value)
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
			c.emit(runtime.PUSH, byte(idx>>8), byte(idx&0xFF))
			c.Compile(el)
			c.emit(runtime.SET)
			c.emit(runtime.POP) // SET pushes target back, but we duped it already
		}

	case *IndexExpression:
		c.Compile(n.Left)
		c.Compile(n.Index)
		c.emit(runtime.GET)

	case *MethodCallExpression:
		c.Compile(n.Object)
		for _, arg := range n.Arguments {
			c.Compile(arg)
		}
		methIndex := c.addConstant(value.NewString(n.Method.Value))
		c.emit(runtime.PUSH, byte(methIndex>>8), byte(methIndex&0xFF))
		c.emit(runtime.INVOKE, byte(len(n.Arguments)))

	case *MemberExpression:
		c.Compile(n.Object)
		propIndex := c.addConstant(value.NewString(n.Property.Value))
		c.emit(runtime.PUSH, byte(propIndex>>8), byte(propIndex&0xFF))
		c.emit(runtime.GET)

	case *FunctionLiteral:
		jumpOver := c.emit(runtime.JUMP, 0, 0)
		startIP := len(c.instructions)
		c.Compile(n.Body)
		c.emit(runtime.RETURN)
		endIP := len(c.instructions)
		c.patchUint16(jumpOver+1, uint16(endIP))

		params := make([]string, len(n.Parameters))
		for i, p := range n.Parameters {
			params[i] = p.Value
		}
		n.Address = startIP // Propagate address back to AST node
		fnData := &value.Lambda{
			Address: startIP,
			Params:  params,
		}
		idx := c.addConstant(value.New(fnData))
		c.emit(runtime.PUSH, byte(idx>>8), byte(idx&0xFF))

	case *TemplateLiteral:
		if len(n.Parts) == 0 {
			idx := c.addConstant(value.NewString(""))
			c.emit(runtime.PUSH, byte(idx>>8), byte(idx&0xFF))
			return nil
		}
		// Compile first part
		err := c.Compile(n.Parts[0])
		if err != nil {
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

func (c *Compiler) Reset() {
	if c.instructions != nil {
		c.instructions = c.instructions[:0]
	}
	if c.constants != nil {
		c.constants = c.constants[:0]
	}
	if c.sourceMap != nil {
		c.sourceMap = c.sourceMap[:0]
	}
}

func (c *Compiler) ByteCodeResult() *Bytecode {
	bc := &Bytecode{
		Instructions: make([]byte, len(c.instructions)),
		Constants:    make([]value.Value, len(c.constants)),
		SourceMap:    make([]int32, len(c.sourceMap)),
	}
	copy(bc.Instructions, c.instructions)
	copy(bc.Constants, c.constants)
	copy(bc.SourceMap, c.sourceMap)
	return bc
}

func (c *Compiler) emit(op runtime.Opcode, operands ...byte) int {
	pos := len(c.instructions)
	c.instructions = append(c.instructions, byte(op))
	c.instructions = append(c.instructions, operands...)

	line := c.getLineNumber(c.currentPos)
	for len(c.sourceMap) < len(c.instructions) {
		c.sourceMap = append(c.sourceMap, line)
	}
	return pos
}

func (c *Compiler) patchUint16(pos int, val uint16) {
	binary.BigEndian.PutUint16(c.instructions[pos:], val)
}

func (c *Compiler) addConstant(v value.Value) int {
	c.constants = append(c.constants, v)
	return len(c.constants) - 1
}
