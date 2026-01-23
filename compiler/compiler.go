package compiler

import (
	"encoding/binary"

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
		}

	case *PrefixExpression:
		err := c.Compile(n.Right)
		if err != nil {
			return err
		}
		if n.Operator == "-" {
			constIndex := c.addConstant(value.New(-1))
			c.emit(opcode.PUSH, byte(constIndex>>8), byte(constIndex&0xFF))
			c.emit(opcode.MUL)
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
		symbolIndex := c.addConstant(value.NewString(n.Name.Value))
		c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
		c.emit(opcode.POP)

	case *AssignmentExpression:
		err := c.Compile(n.Value)
		if err != nil {
			return err
		}
		if id, ok := n.Name.(*Identifier); ok {
			symbolIndex := c.addConstant(value.NewString(id.Value))
			c.emit(opcode.STORE, byte(symbolIndex>>8), byte(symbolIndex&0xFF))
		} else {
			return nil
		}

	case *IfExpression:
		err := c.Compile(n.Condition)
		if err != nil {
			return err
		}
		unlessPos := c.emit(opcode.UNLESS, 0, 0)
		err = c.Compile(n.Consequence)
		if err != nil {
			return err
		}
		if n.Alternative != nil {
			jumpPos := c.emit(opcode.JUMP, 0, 0)
			c.patchUint16(unlessPos+1, uint16(len(c.instructions)))
			err = c.Compile(n.Alternative)
			if err != nil {
				return err
			}
			c.patchUint16(jumpPos+1, uint16(len(c.instructions)))
		} else {
			c.patchUint16(unlessPos+1, uint16(len(c.instructions)))
		}

	case *BlockStatement:
		for _, s := range n.Statements {
			err := c.Compile(s)
			if err != nil {
				return err
			}
		}

	case *CallExpression:
		for _, arg := range n.Arguments {
			err := c.Compile(arg)
			if err != nil {
				return err
			}
		}
		err := c.Compile(n.Function)
		if err != nil {
			return err
		}
		c.emit(opcode.CALL, byte(len(n.Arguments)))

	case *ReturnStatement:
		if n.ReturnValue != nil {
			err := c.Compile(n.ReturnValue)
			if err != nil {
				return err
			}
		}
		c.emit(opcode.RETURN)

	case *ObjectLiteral:
		c.emit(opcode.MAKE, 0)
		for k, v := range n.Pairs {
			if id, ok := k.(*Identifier); ok {
				methIndex := c.addConstant(value.NewString(id.Value))
				c.emit(opcode.PUSH, byte(methIndex>>8), byte(methIndex&0xFF))
			} else {
				c.Compile(k)
			}
			c.Compile(v)
			c.emit(opcode.SET)
			// Không POP target ở đây để cặp key-value tiếp theo tiếp tục dùng target đó
		}

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
		// 1. Nhảy qua thân hàm (không muốn thực thi ngay)
		jumpOver := c.emit(opcode.JUMP, 0, 0)

		startIP := len(c.instructions)

		// 2. Biên dịch thân hàm
		c.Compile(n.Body)

		// Đảm bảo luôn có Return ở cuối thân hàm
		// Nếu statement cuối là return thì thôi, nhưng ta cứ emit cho chắc
		c.emit(opcode.RETURN)

		endIP := len(c.instructions)
		c.patchUint16(jumpOver+1, uint16(endIP))

		// 3. Đẩy thông tin Lambda lên stack dưới dạng Constant
		params := make([]string, len(n.Parameters))
		for i, p := range n.Parameters {
			params[i] = p.Value
		}

		fnData := &value.ScriptFunction{
			Address:    startIP,
			ParamNames: params,
		}
		idx := c.addConstant(value.New(fnData))
		c.emit(opcode.PUSH, byte(idx>>8), byte(idx&0xFF))
	}

	return nil
}

// Reset xóa sạch trạng thái để tái sử dụng Compiler từ sync.Pool
func (c *Compiler) Reset() {
	if c.instructions != nil {
		c.instructions = c.instructions[:0]
	}
	if c.constants != nil {
		c.constants = c.constants[:0]
	}
}

// ByteCodeResult trả về kết quả biên dịch cuối cùng
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
