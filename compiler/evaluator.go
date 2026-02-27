package compiler

import (
	"github.com/kitwork/engine/value"
)

// Hằng số nội bộ để tối ưu hiệu năng
var (
	NULL_VAL = value.NULL
)

// Evaluator là hàm thực thi chính, duyệt qua các nút của cây AST
func Evaluator(node Node, env *Environment) value.Value {
	if node == nil {
		return NULL_VAL
	}

	switch n := node.(type) {

	/* -------------------------------------------------------------------------
	   1. CÁC CÂU LỆNH (STATEMENTS)
	   ------------------------------------------------------------------------- */

	case *Program:
		return evalProgram(n, env)

	case *AssignmentExpression:
		val := Evaluator(n.Value, env)
		if val.IsInvalid() {
			return val
		}

		if id, ok := n.Name.(*Identifier); ok {
			env.Set(id.Value, val)
			return val
		}
		return value.Value{K: value.Invalid}

	case *BlockStatement:
		// Tạo môi trường mới (Lexical Scope) cho mỗi khối lệnh { }
		return evalBlockStatement(n, env)

	case *ExpressionStatement:
		return Evaluator(n.Expression, env)

	case *VarStatement:
		val := Evaluator(n.Value, env)
		if val.IsInvalid() {
			return val
		}
		if n.DestructMode == DestructObject {
			for _, id := range n.Names {
				env.Set(id.Value, val.Get(id.Value))
			}
		} else if n.DestructMode == DestructArray {
			for i, id := range n.Names {
				env.Set(id.Value, val.Index(i))
			}
		} else if len(n.Names) > 0 {
			env.Set(n.Names[0].Value, val)
		}
		return NULL_VAL

	case *ReturnStatement:
		// 1. Tính toán giá trị cần trả về
		val := Evaluator(n.ReturnValue, env)

		// 2. Nếu biểu thức lỗi, trả về Invalid ngay
		if val.IsInvalid() {
			return val
		}

		// 3. Trả về giá trị đã tính được
		return val

	/* -------------------------------------------------------------------------
	   2. CÁC BIỂU THỨC (EXPRESSIONS)
	   ------------------------------------------------------------------------- */

	case *Literal:
		// Trả về giá trị 24-byte đã được Parser đóng gói sẵn
		return n.Value

	case *Identifier:
		// Truy xuất biến từ Environment
		val, ok := env.Get(n.Value)
		if !ok {
			return value.Value{K: value.Invalid}
		}
		return val

	case *PrefixExpression:
		// Xử lý các tiền tố như ! hoặc -
		right := Evaluator(n.Right, env)
		if right.IsInvalid() {
			return right
		}
		return evalPrefixExpression(n.Operator, right)

	case *InfixExpression:
		// Xử lý toán tử 2 ngôi (a + b, x == y, ...)
		left := Evaluator(n.Left, env)
		if left.IsInvalid() {
			return left
		}

		right := Evaluator(n.Right, env)
		if right.IsInvalid() {
			return right
		}

		return evalInfixExpression(n.Operator, left, right)

	case *IfExpression:
		condition := Evaluator(n.Condition, env)
		if condition.IsInvalid() {
			return condition
		}

		if condition.Truthy() {
			// Thực thi khối lệnh true
			result := Evaluator(n.Consequence, env)
			// QUAN TRỌNG: Nếu kết quả bên trong block là Return, phải truyền nó đi tiếp (Bubble up)
			return result
		} else if n.Alternative != nil {
			return Evaluator(n.Alternative, env)
		}
		return NULL_VAL

	case *ArrayLiteral:
		// Duyệt và thực thi từng phần tử trong mảng
		elements := make([]value.Value, len(n.Elements))
		for i, el := range n.Elements {
			elements[i] = Evaluator(el, env)
		}
		// Tận dụng value.New() để đóng gói []value.Value vào struct 24-byte
		return value.New(elements)

	case *ObjectLiteral:
		// Duyệt và thực thi các cặp key-value
		obj := make(map[string]value.Value)
		for keyNode, valNode := range n.Pairs {
			var k string
			// Nếu key là Identifier (ví dụ: { name: "..." }), lấy tên trực tiếp thay vì lookup biến
			if id, ok := keyNode.(*Identifier); ok {
				k = id.Value
			} else {
				k = Evaluator(keyNode, env).Text()
			}
			obj[k] = Evaluator(valNode, env)
		}
		return value.New(obj)

	case *MemberExpression:
		left := Evaluator(n.Object, env)
		if left.IsInvalid() || left.IsNil() {
			return left
		}
		return left.Get(n.Property.Value)

	case *IndexExpression:
		// Truy cập theo chỉ mục: array[index] hoặc map["key"]
		left := Evaluator(n.Left, env)
		idx := Evaluator(n.Index, env)

		if left.IsInvalid() || idx.IsInvalid() {
			return value.Value{K: value.Invalid}
		}

		// Nếu index là số, dùng Index(int). Nếu là chuỗi, dùng Get(string)
		if idx.K == value.Number {
			return left.Index(int(idx.N))
		}
		return left.Get(idx.Text())

	case *CallExpression:
		return evalCallExpression(n, env)

	case *MethodCallExpression:
		target := Evaluator(n.Object, env)
		// Nil-safety check: Cho phép gọi method trên Nil mà không crash, trả về Nil
		if target.IsInvalid() || target.IsNil() {
			return target
		}

		args := evalExpressions(n.Arguments, env)
		// Kiểm tra xem có argument nào bị Invalid không
		if len(args) > 0 && args[0].IsInvalid() {
			return args[0]
		}

		// Invoke sẽ tự động ưu tiên Prototype Table trước, sau đó mới tới Reflection
		return target.Invoke(n.Method.Value, args...)

	case *FunctionLiteral:
		// Create a placeholder ScriptFunction with Address=0
		// Trigger() will update this with the correct bytecode address
		params := make([]string, len(n.Parameters))
		for i, p := range n.Parameters {
			params[i] = p.Value
		}
		sFn := &value.Lambda{
			Address: n.Address, // Will be correct if Compiler ran first
			Params:  params,
		}
		return value.New(sFn)

	case *ParameterList:
		return NULL_VAL
	}

	return NULL_VAL
}

/* -------------------------------------------------------------------------
   3. CÁC HÀM TRỢ GIÚP (INTERNAL HELPERS)
   ------------------------------------------------------------------------- */

func evalProgram(p *Program, env *Environment) value.Value {
	var last value.Value = NULL_VAL

	for _, stmt := range p.Statements {
		last = Evaluator(stmt, env)

		// Nếu gặp Invalid (Lỗi Runtime)
		if last.IsInvalid() {
			return last
		}

		// Nếu gặp tín hiệu Return
		if last.K == value.Return {
			if val, ok := last.V.(value.Value); ok {
				return val
			}
			return last
		}
	}
	return last
}

func evalBlockStatement(block *BlockStatement, env *Environment) value.Value {
	// Tạo scope mới trỏ về scope hiện tại
	innerEnv := &Environment{
		store: make(map[string]value.Value),
		outer: env,
	}
	var last value.Value
	for _, stmt := range block.Statements {
		last = Evaluator(stmt, innerEnv)

		if last.K == value.Return || last.K == value.Invalid {
			return last
		}
	}
	return last
}

func evalPrefixExpression(op string, right value.Value) value.Value {
	switch op {
	case "!":
		return value.ToBool(!right.Truthy())
	case "-":
		if right.K != value.Number {
			return value.Value{K: value.Invalid}
		}
		return value.Value{K: value.Number, N: -right.N}
	default:
		return value.Value{K: value.Invalid}
	}
}

func evalInfixExpression(op string, left, right value.Value) value.Value {
	switch op {
	case "+":
		return left.Add(right)
	case "-":
		return left.Sub(right)
	case "*":
		return left.Mul(right)
	case "/":
		return left.Div(right)

	// So sánh
	case "==":
		return value.ToBool(left.Equal(right))
	case "!=":
		return value.ToBool(left.NotEqual(right))
	case ">":
		return value.ToBool(left.Greater(right))
	case "<":
		return value.ToBool(left.Less(right))
	case ">=":
		return value.ToBool(left.GreaterEqual(right))
	case "<=":
		return value.ToBool(left.LessEqual(right))

	// Logic chaining
	case "&&":
		if left.Truthy() {
			return right
		}
		return left
	case "||":
		if left.Truthy() {
			return left
		}
		return right
	case "??":
		// Handle standard Nil and string "null" (edge case)
		if left.IsNil() || left.String() == "null" {
			return right
		}
		return left

	default:
		return value.Value{K: value.Invalid}
	}
}

func evalCallExpression(ce *CallExpression, env *Environment) value.Value {
	fnValue := Evaluator(ce.Function, env)
	if fnValue.IsInvalid() {
		return fnValue
	}

	args := make([]value.Value, len(ce.Arguments))
	for i, argNode := range ce.Arguments {
		val := Evaluator(argNode, env)
		if val.IsInvalid() {
			return val
		}
		args[i] = val
	}

	switch fnValue.K {
	case value.Func:
		if goFunc, ok := fnValue.V.(func(...value.Value) value.Value); ok {
			return goFunc(args...)
		}
	}

	return value.Value{K: value.Invalid}
}

func evalExpressions(exps []Expression, env *Environment) []value.Value {
	var result []value.Value
	for _, e := range exps {
		evaluated := Evaluator(e, env)
		if evaluated.IsInvalid() {
			return []value.Value{evaluated}
		}
		result = append(result, evaluated)
	}
	return result
}
