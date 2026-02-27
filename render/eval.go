package render

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// ----------------------------------------------------------------------------
// EVALUATOR
// ----------------------------------------------------------------------------

func eval(n *node, data any, locals ...map[string]value.Value) (out string) {
	// Safety Panic Recovery
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[Render Panic] %v\n", r)
			out = ""
		}
	}()

	var sb strings.Builder

	// Flatten locals for easy lookup
	var scope map[string]value.Value
	if len(locals) > 0 {
		scope = locals[0]
	}

	switch n.typ {
	case nodeRoot:
		return renderChildren(n.children, data, scope)

	case nodeText:
		return n.val

	case nodeVar:
		return resolveVar(n.val, data, scope)

	case nodeIf:
		var truthy bool
		var val value.Value

		// Handle Pattern C: Modulo (5 parts: if var % 2 == 0)
		if len(n.args) >= 4 && n.args[0] == "%" {
			val = resolveValue(n.val, data, scope)
			if val.IsNumeric() {
				var modBy float64
				var target float64
				fmt.Sscanf(n.args[1], "%f", &modBy)
				fmt.Sscanf(n.args[3], "%f", &target)
				op := n.args[2]
				if modBy != 0 {
					current := int(val.Float())
					rem := current % int(modBy)
					switch op {
					case "==":
						truthy = (rem == int(target))
					case "!=":
						truthy = (rem != int(target))
					}
				}
			}
		} else {
			val = resolveValue(n.val, data, scope)
			truthy = val.Truthy()
			if len(n.args) >= 2 {
				op := n.args[0]
				targetRaw := strings.Trim(n.args[1], `"'`)
				if val.IsNumeric() {
					var targetNum float64
					if _, err := fmt.Sscanf(targetRaw, "%f", &targetNum); err == nil {
						currentNum := val.Float()
						switch op {
						case "==":
							truthy = (currentNum == targetNum)
						case "!=":
							truthy = (currentNum != targetNum)
						case ">":
							truthy = (currentNum > targetNum)
						case "<":
							truthy = (currentNum < targetNum)
						case ">=":
							truthy = (currentNum >= targetNum)
						case "<=":
							truthy = (currentNum <= targetNum)
						}
					}
				} else {
					strVal := val.String()
					switch op {
					case "==":
						truthy = (strVal == targetRaw)
					case "!=":
						truthy = (strVal != targetRaw)
					}
				}
			}
		}

		if truthy {
			sb.WriteString(renderChildren(n.children, data, scope))
		} else {
			sb.WriteString(renderChildren(n.alt, data, scope))
		}

	case nodeRange:
		val := resolveValue(n.val, data, scope)
		if val.IsArray() {
			arr := val.Array()
			for i, item := range arr {
				newScope := copyMap(scope)
				if n.keyVar != "" {
					newScope[n.keyVar] = value.New(i)
				}
				if n.valVar != "" {
					newScope[n.valVar] = item
				}
				sb.WriteString(renderChildren(n.children, item, newScope))
			}
		} else if val.IsMap() {
			m := val.Map()
			for k, v := range m {
				newScope := copyMap(scope)
				if n.keyVar != "" {
					newScope[n.keyVar] = value.New(k)
				}
				if n.valVar != "" {
					newScope[n.valVar] = v
				}
				sb.WriteString(renderChildren(n.children, v, newScope))
			}
		}

	case nodeLet:
		val := resolveValue(n.val, data, scope)
		if scope != nil {
			scope[n.keyVar] = val
		}

	case nodePartial:
		viewDir := ""
		if v, ok := scope["__view_dir"]; ok {
			viewDir = v.Text()
		}
		fname := n.val
		if !strings.HasSuffix(fname, ".html") {
			fname += ".html"
		}
		fullPath := filepath.Join(viewDir, fname)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			// Fallback: Check Global View Dir (Shared Layouts)
			if globalVal, ok := scope["__global_view_dir"]; ok {
				fallbackDir := globalVal.Text()
				fullPath = filepath.Join(fallbackDir, fname)
				content, err = os.ReadFile(fullPath)
			}

			if err != nil {
				return fmt.Sprintf("[Error: %v]", err)
			}
		}

		// TRUYỀN SCOPE HIỆN TẠI VÀO PARTIAL
		tokens := specializeTokens(string(content))
		prog := parse(tokens)

		// Kế thừa scope cũ
		newScope := copyMap(scope)
		newScope["__view_dir"] = value.New(viewDir)

		return eval(prog, data, newScope)
	}
	return sb.String()
}

func renderChildren(nodes []*node, data any, scope map[string]value.Value) string {
	var sb strings.Builder
	for _, n := range nodes {
		sb.WriteString(eval(n, data, scope))
	}
	return sb.String()
}

func copyMap(src map[string]value.Value) map[string]value.Value {
	dst := make(map[string]value.Value)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func resolveVar(rawKey string, data any, scope map[string]value.Value) string {
	if strings.HasPrefix(rawKey, "raw(") && strings.HasSuffix(rawKey, ")") {
		innerKey := rawKey[4 : len(rawKey)-1]
		val := resolveValue(innerKey, data, scope)
		return val.String()
	}

	if rawKey == "$" || strings.HasPrefix(rawKey, "$.") {
		return html.EscapeString(resolveValue(rawKey, data, scope).String())
	}

	val := resolveValue(rawKey, data, scope)
	// if val.S == value.SafeHTML {
	// 	return val.String()
	// }

	return html.EscapeString(val.String())
}

func findSplitIndex(s string, checkFn func(int) bool, last bool) int {
	level := 0
	if last {
		for i := len(s) - 1; i >= 0; i-- {
			if s[i] == ')' {
				level++
			}
			if s[i] == '(' {
				level--
			}
			if level == 0 && checkFn(i) {
				return i
			}
		}
	} else {
		for i := 0; i < len(s); i++ {
			if s[i] == '(' {
				level++
			}
			if s[i] == ')' {
				level--
			}
			if level == 0 && checkFn(i) {
				return i
			}
		}
	}
	return -1
}

func resolveValue(path string, data any, scope map[string]value.Value) value.Value {
	path = strings.TrimSpace(path)
	if (strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`)) ||
		(strings.HasPrefix(path, `'`) && strings.HasSuffix(path, `'`)) {
		return value.New(path[1 : len(path)-1])
	}
	if val, err := strconv.ParseFloat(path, 64); err == nil {
		return value.New(val)
	}

	// 1. Ternary (cond ? true : false)
	qIdx := findSplitIndex(path, func(i int) bool { return path[i] == '?' }, false)
	if qIdx > -1 && (qIdx+1 >= len(path) || path[qIdx+1] != '?') {
		remainder := path[qIdx+1:]
		cIdxRel := findSplitIndex(remainder, func(i int) bool { return remainder[i] == ':' }, false)
		if cIdxRel > -1 {
			cIdx := qIdx + 1 + cIdxRel
			cond := resolveValue(path[:qIdx], data, scope)
			if cond.Truthy() {
				return resolveValue(path[qIdx+1:cIdx], data, scope)
			}
			return resolveValue(path[cIdx+1:], data, scope)
		}
	}

	// 2. Logic & Null Coalescing
	// ??
	if idx := findSplitIndex(path, func(i int) bool {
		return path[i] == '?' && i+1 < len(path) && path[i+1] == '?'
	}, false); idx > -1 {
		left := resolveValue(path[:idx], data, scope)
		if left.IsNil() || left.String() == "null" || (left.K == value.String && left.Text() == "") {
			return resolveValue(path[idx+2:], data, scope)
		}
		return left
	}
	// ||
	if idx := findSplitIndex(path, func(i int) bool {
		return path[i] == '|' && i+1 < len(path) && path[i+1] == '|'
	}, false); idx > -1 {
		left := resolveValue(path[:idx], data, scope)
		if !left.Truthy() {
			return resolveValue(path[idx+2:], data, scope)
		}
		return left
	}

	// 3. Comparisons & Basic Arithmetic
	ops := []string{"==", "!=", ">=", "<=", ">", "<", "+", "-", "*", "/", "%"}
	for _, op := range ops {
		if idx := findSplitIndex(path, func(i int) bool {
			return strings.HasPrefix(path[i:], op)
		}, true); idx > 0 {
			left := resolveValue(path[:idx], data, scope)
			right := resolveValue(path[idx+len(op):], data, scope)
			switch op {
			case "==":
				return value.ToBool(left.Equal(right))
			case "!=":
				return value.ToBool(left.NotEqual(right))
			case "+":
				return left.Add(right)
			case "-":
				return left.Sub(right)
			case "*":
				return left.Mul(right)
			case "/":
				return left.Div(right)
			}
		}
	}

	// 4. variable lookup
	var current value.Value
	if v, ok := data.(value.Value); ok {
		current = v
	} else {
		current = value.New(data)
	}

	if path == "." {
		return current
	}
	if strings.HasPrefix(path, ".") {
		return traverse(current, strings.Split(strings.TrimPrefix(path, "."), "."))
	}

	parts := strings.Split(path, ".")
	if val, ok := scope[parts[0]]; ok {
		if len(parts) > 1 {
			return traverse(val, parts[1:])
		}
		return val
	}

	res := traverse(current, parts)
	if !res.IsNil() {
		return res
	}
	if strings.HasPrefix(parts[0], "$") {
		parts[0] = strings.TrimPrefix(parts[0], "$")
		return traverse(current, parts)
	}
	return res
}

func traverse(current value.Value, parts []string) value.Value {
	for _, part := range parts {
		if current.IsNil() {
			return current
		}
		res := current.Get(part)
		if res.IsNil() {
			if nested, ok := current.V.(value.Value); ok {
				current = nested
				res = current.Get(part)
			}
		}
		current = res
		if current.IsNil() {
			return current
		}
	}
	return current
}
