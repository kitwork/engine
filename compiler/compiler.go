package compiler

import (
	"encoding/binary"
	"fmt"

	"github.com/kitwork/engine/opcode"
	"github.com/kitwork/engine/value"
)

// Bytecode đại diện cho kết quả sau khi biên dịch
type Bytecode struct {
	Instructions []byte
	Constants    []value.Value
}

// Compiler chịu trách nhiệm chuyển đổi AST thành Bytecode
type Compiler struct {
	instructions []byte
	constants    []value.Value
}

func NewCompiler() *Compiler {
	return &Compiler{
		instructions: []byte{},
		constants:    []value.Value{},
	}
}

// Compile bắt đầu quá trình duyệt cây và phát sinh mã
func (c *Compiler) Compile(node Node) error {
	if node == nil {
		return nil
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
					c.emit(opcode.POP)
				}
			}
		}
		// Luôn kết thúc Program bằng RETURN để đảm bảo thực thi defer
		c.emit(opcode.RETURN)

	case *ExpressionStatement:
		return c.Compile(n.Expression)

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
			c.emit(opcode.ADD)
		case "-":
			c.emit(opcode.SUB)
		case "*":
			c.emit(opcode.MUL)
		case "/":
			c.emit(opcode.DIV)
		case "==":
			c.emit(opcode.COMPARE, 0)
		case "!=":
			c.emit(opcode.COMPARE, 1)
		case ">":
			c.emit(opcode.COMPARE, 2)
		case "<":
			c.emit(opcode.COMPARE, 3)
		case ">=":
			c.emit(opcode.COMPARE, 4)
		case "<=":
			c.emit(opcode.COMPARE, 5)
		case "&&":
			c.emit(opcode.AND)
		case "||":
			c.emit(opcode.OR)
		}

	case *PrefixExpression:
		err := c.Compile(n.Right)
		if err != nil {
			return err
		}
		switch n.Operator {
		case "-":
			constIndex := c.addConstant(value.New(-1))
			c.emit(opcode.PUSH, byte(constIndex>>8), byte(constIndex&0xFF))
			c.emit(opcode.MUL)
		case "!":
			c.emit(opcode.NOT)
		}

	case *Literal:
		constIndex := c.addConstant(n.Value)
		c.emit(opcode.PUSH, byte(constIndex>>8), byte(constIndex&0xFF))

	case *Identifier:
		symbolIndex := c.addConstant(value.NewString(n.Value))
		c.emit(opcode.LOAD, byte(symbolIndex>>8), byte(symbolIndex&0xFF))

	case *VarStatement:
		err := c.Compile(n.Value)
		if err != nil {
			return err
		}

		if n.DestructMode == DestructObject {
			for _, id := range n.Names {
				c.emit(opcode.DUP)
				symbolIndex := c.addConstant(value.NewString(id.Value))
				c.emit(opcode.PUSH, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
				c.emit(opcode.GET)
				c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
				c.emit(opcode.POP)
			}
		} else if n.DestructMode == DestructArray {
			for i, id := range n.Names {
				c.emit(opcode.DUP)
				idxIndex := c.addConstant(value.New(i))
				c.emit(opcode.PUSH, byte(idxIndex>>8), byte(idxIndex&0xFF))
				c.emit(opcode.GET)
				symbolIndex := c.addConstant(value.NewString(id.Value))
				c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
				c.emit(opcode.POP)
			}
		} else {
			symbolIndex := c.addConstant(value.NewString(n.Names[0].Value))
			c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
		}
		c.emit(opcode.POP)

	case *AssignmentExpression:
		if id, ok := n.Name.(*Identifier); ok {
			err := c.Compile(n.Value)
			if err != nil {
				return err
			}
			symbolIndex := c.addConstant(value.NewString(id.Value))
			c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
		} else if mem, ok := n.Name.(*MemberExpression); ok {
			c.Compile(mem.Object)
			propIndex := c.addConstant(value.NewString(mem.Property.Value))
			c.emit(opcode.PUSH, byte(propIndex>>8), byte(propIndex&0xFF))
			c.Compile(n.Value)
			c.emit(opcode.SET)
		} else if obj, ok := n.Name.(*ObjectLiteral); ok {
			err := c.Compile(n.Value)
			if err != nil {
				return err
			}
			for k := range obj.Pairs {
				if id, ok := k.(*Identifier); ok {
					c.emit(opcode.DUP)
					symbolIndex := c.addConstant(value.NewString(id.Value))
					c.emit(opcode.PUSH, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
					c.emit(opcode.GET)
					c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
					c.emit(opcode.POP)
				}
			}
		} else if arr, ok := n.Name.(*ArrayLiteral); ok {
			err := c.Compile(n.Value)
			if err != nil {
				return err
			}
			for i, el := range arr.Elements {
				if id, ok := el.(*Identifier); ok {
					c.emit(opcode.DUP)
					idxIndex := c.addConstant(value.New(i))
					c.emit(opcode.PUSH, byte(idxIndex>>8), byte(idxIndex&0xFF))
					c.emit(opcode.GET)
					symbolIndex := c.addConstant(value.NewString(id.Value))
					c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
					c.emit(opcode.POP)
				}
			}
		}

	case *IfExpression:
		err := c.Compile(n.Condition)
		if err != nil {
			return err
		}
		falsePos := c.emit(opcode.FALSE, 0, 0)
		err = c.Compile(n.Consequence)
		if err != nil {
			return err
		}

		if n.Alternative != nil {
			jumpPos := c.emit(opcode.JUMP, 0, 0)
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
		c.emit(opcode.PUSH, byte(constZero>>8), byte(constZero&0xFF))
		loopStart := len(c.instructions)
		exitJump := c.emit(opcode.ITER, 0, 0)
		symbolIndex := c.addConstant(value.NewString(n.Item.Value))
		c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
		c.emit(opcode.POP)
		c.Compile(n.Body)
		c.emit(opcode.JUMP, byte(loopStart>>8), byte(loopStart&0xFF))
		c.patchUint16(exitJump+1, uint16(len(c.instructions)))
		c.emit(opcode.POP)
		c.emit(opcode.POP)

	case *BlockStatement:
		for _, s := range n.Statements {
			c.Compile(s)
		}

	case *ReturnStatement:
		if n.ReturnValue != nil {
			c.Compile(n.ReturnValue)
		} else {
			constNull := c.addConstant(value.Value{K: value.Nil})
			c.emit(opcode.PUSH, byte(constNull>>8), byte(constNull&0xFF))
		}
		c.emit(opcode.RETURN)

	case *CallExpression:
		c.Compile(n.Function)
		for _, arg := range n.Arguments {
			c.Compile(arg)
		}
		c.emit(opcode.CALL, byte(len(n.Arguments)))

	case *ObjectLiteral:
		c.emit(opcode.MAKE, 0)
		for key, val := range n.Pairs {
			c.emit(opcode.DUP)
			// Nếu key là Identifier, ta coi như chuỗi (JS style: { name: "..." })
			if id, ok := key.(*Identifier); ok {
				idx := c.addConstant(value.NewString(id.Value))
				c.emit(opcode.PUSH, byte(idx>>8), byte(idx&0xFF))
			} else {
				c.Compile(key)
			}
			c.Compile(val)
			c.emit(opcode.SET)
			c.emit(opcode.POP) // Loại bỏ giá trị dư từ SET (SET đẩy lại target lên stack)
		}

	case *ArrayLiteral:
		c.emit(opcode.MAKE, 1) // 1 for Array
		for i, el := range n.Elements {
			c.emit(opcode.DUP)
			// Push the index as the key for SET
			idx := c.addConstant(value.New(float64(i)))
			c.emit(opcode.PUSH, byte(idx>>8), byte(idx&0xFF))
			c.Compile(el)
			c.emit(opcode.SET)
			c.emit(opcode.POP) // SET pushes target back, but we duped it already
		}

	case *IndexExpression:
		c.Compile(n.Left)
		c.Compile(n.Index)
		c.emit(opcode.GET)

	case *MethodCallExpression:
		c.Compile(n.Object)
		for _, arg := range n.Arguments {
			c.Compile(arg)
		}
		methIndex := c.addConstant(value.NewString(n.Method.Value))
		c.emit(opcode.PUSH, byte(methIndex>>8), byte(methIndex&0xFF))
		c.emit(opcode.INVOKE, byte(len(n.Arguments)))

	case *MemberExpression:
		c.Compile(n.Object)
		propIndex := c.addConstant(value.NewString(n.Property.Value))
		c.emit(opcode.PUSH, byte(propIndex>>8), byte(propIndex&0xFF))
		c.emit(opcode.GET)

	case *FunctionLiteral:
		jumpOver := c.emit(opcode.JUMP, 0, 0)
		startIP := len(c.instructions)
		c.Compile(n.Body)
		c.emit(opcode.RETURN)
		endIP := len(c.instructions)
		c.patchUint16(jumpOver+1, uint16(endIP))

		params := make([]string, len(n.Parameters))
		for i, p := range n.Parameters {
			params[i] = p.Value
		}
		fnData := &value.ScriptFunction{
			Address:    startIP,
			ParamNames: params,
		}
		fmt.Printf("[Compiler] Created ScriptFunction with Address: %d\n", startIP)
		idx := c.addConstant(value.New(fnData))
		c.emit(opcode.PUSH, byte(idx>>8), byte(idx&0xFF))
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
}

func (c *Compiler) ByteCodeResult() *Bytecode {
	bc := &Bytecode{
		Instructions: make([]byte, len(c.instructions)),
		Constants:    make([]value.Value, len(c.constants)),
	}
	copy(bc.Instructions, c.instructions)
	copy(bc.Constants, c.constants)
	return bc
}

func (c *Compiler) emit(op opcode.Opcode, operands ...byte) int {
	pos := len(c.instructions)
	c.instructions = append(c.instructions, byte(op))
	c.instructions = append(c.instructions, operands...)
	return pos
}

func (c *Compiler) patchUint16(pos int, val uint16) {
	binary.BigEndian.PutUint16(c.instructions[pos:], val)
}

func (c *Compiler) addConstant(v value.Value) int {
	c.constants = append(c.constants, v)
	return len(c.constants) - 1
}
