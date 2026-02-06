package render

import (
	"fmt"
	"html"
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
		for _, child := range n.children {
			sb.WriteString(eval(child, data, scope))
		}

	case nodeText:
		return n.val

	case nodeVar:
		return resolveVar(n.val, data, scope)

	case nodeIf:
		var truthy bool
		var val value.Value

		// Handle Pattern C: Modulo (5 parts: if var % 2 == 0) -> n.args starts from index 0
		// n.val is "var"
		// n.args is ["%", "2", "==", "0"]
		if len(n.args) >= 4 && n.args[0] == "%" {
			val = resolveValue(n.val, data, scope)
			if val.IsNumeric() {
				var modBy float64
				var target float64
				fmt.Sscanf(n.args[1], "%f", &modBy)  // "2"
				fmt.Sscanf(n.args[3], "%f", &target) // "0"

				op := n.args[2] // "==" or "!="

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
			// Normal Patterns
			val = resolveValue(n.val, data, scope)
			truthy = val.Truthy()

			if len(n.args) >= 2 {
				op := n.args[0]
				targetRaw := strings.Trim(n.args[1], `"'`)

				// Compare Logic
				// 1. Numeric Comparison
				if val.IsNumeric() {
					// Try to parse target as number
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
					// 2. String/Bool Comparison (Fallback)
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
			for _, child := range n.children {
				sb.WriteString(eval(child, data, scope))
			}
		} else {
			for _, child := range n.alt {
				sb.WriteString(eval(child, data, scope))
			}
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
				for _, child := range n.children {
					sb.WriteString(eval(child, item, newScope))
				}
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
				for _, child := range n.children {
					sb.WriteString(eval(child, v, newScope))
				}
			}
		}

	case nodeLet:
		// Evaluate the expression (right-hand side)
		val := resolveValue(n.val, data, scope)
		// Assign to current scope
		if scope != nil {
			scope[n.keyVar] = val
		}
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
	// 1. Handle Raw Output Explicitly: raw(variable)
	if strings.HasPrefix(rawKey, "raw(") && strings.HasSuffix(rawKey, ")") {
		innerKey := rawKey[4 : len(rawKey)-1]
		val := resolveValue(innerKey, data, scope)
		return val.String() // No Escape
	}

	// 2. Global/Root Access: Only strict $.variable or $
	if rawKey == "$" || strings.HasPrefix(rawKey, "$.") {
		return html.EscapeString(resolveValue(rawKey, data, scope).String())
	}

	// 3. Standard Local Access
	val := resolveValue(rawKey, data, scope)

	// Check for SafeHTML flag (set by .html() in JS or raw() wrapper)
	if val.S == value.SafeHTML {
		return val.String()
	}

	return html.EscapeString(val.String())
}

// Helper to find operator index identifying parenthesis balance
func findSplitIndex(s string, checkFn func(int) bool, last bool) int {
	level := 0
	if last {
		for i := len(s) - 1; i >= 0; i-- {
			if s[i] == ')' {
				level++
				continue
			}
			if s[i] == '(' {
				level--
				continue
			}
			if level == 0 && checkFn(i) {
				return i
			}
		}
	} else {
		for i := 0; i < len(s); i++ {
			if s[i] == '(' {
				level++
				continue
			}
			if s[i] == ')' {
				level--
				continue
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

	// 0. Literal Handling (Highest Priority)
	// Check for Quoted String: "value" or 'value'
	if (strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`)) ||
		(strings.HasPrefix(path, `'`) && strings.HasSuffix(path, `'`)) {
		return value.New(path[1 : len(path)-1])
	}
	// Check for Number
	if val, err := strconv.ParseFloat(path, 64); err == nil {
		return value.New(val)
	}

	// 1. Operator Support (Low -> High Precedence)

	// A. Ternary (cond ? true : false)
	// Use findSplitIndex
	qIdx := findSplitIndex(path, func(i int) bool { return path[i] == '?' }, false)
	if qIdx > -1 {
		// Find corresponding ':'
		// We need to find ':' AFTER qIdx, balanced.
		remainder := path[qIdx+1:]
		cIdxRel := findSplitIndex(remainder, func(i int) bool { return remainder[i] == ':' }, false)
		if cIdxRel > -1 {
			cIdx := qIdx + 1 + cIdxRel

			condRaw := path[:qIdx]
			trueRaw := path[qIdx+1 : cIdx]
			falseRaw := path[cIdx+1:]

			cond := resolveValue(condRaw, data, scope)
			if cond.Truthy() {
				return resolveValue(trueRaw, data, scope)
			}
			return resolveValue(falseRaw, data, scope)
		}
	}

	// B. Logical Operators (||, &&)
	// Null Coalescing (??)
	if idx := findSplitIndex(path, func(i int) bool {
		return path[i] == '?' && i+1 < len(path) && path[i+1] == '?'
	}, false); idx > -1 {
		leftRaw := strings.TrimSpace(path[:idx])
		rightRaw := strings.TrimSpace(path[idx+2:])
		left := resolveValue(leftRaw, data, scope)
		if left.IsNil() || left.String() == "null" || left.String() == "" {
			return resolveValue(rightRaw, data, scope)
		}
		return left
	}
	// OR (||)
	if idx := findSplitIndex(path, func(i int) bool {
		return path[i] == '|' && i+1 < len(path) && path[i+1] == '|'
	}, false); idx > -1 {
		leftRaw := strings.TrimSpace(path[:idx])
		rightRaw := strings.TrimSpace(path[idx+2:])
		left := resolveValue(leftRaw, data, scope)
		if !left.Truthy() {
			return resolveValue(rightRaw, data, scope)
		}
		return left
	}
	// AND (&&)
	if idx := findSplitIndex(path, func(i int) bool {
		return path[i] == '&' && i+1 < len(path) && path[i+1] == '&'
	}, false); idx > -1 {
		leftRaw := strings.TrimSpace(path[:idx])
		rightRaw := strings.TrimSpace(path[idx+2:])
		left := resolveValue(leftRaw, data, scope)
		if left.Truthy() {
			return resolveValue(rightRaw, data, scope)
		}
		return left // Return false/falsy value
	}

	// C. Comparison (==, !=, >=, <=, >, <)
	ops := []string{"==", "!=", ">=", "<=", ">", "<"}
	for _, op := range ops {
		target := op
		if idx := findSplitIndex(path, func(i int) bool {
			if i+len(target) > len(path) {
				return false
			}
			return path[i:i+len(target)] == target
		}, false); idx > -1 {
			leftRaw := strings.TrimSpace(path[:idx])
			rightRaw := strings.TrimSpace(path[idx+len(target):])

			left := resolveValue(leftRaw, data, scope)
			right := resolveValue(rightRaw, data, scope)

			switch target {
			case "==":
				return value.ToBool(left.Equal(right))
			case "!=":
				return value.ToBool(left.NotEqual(right))
			case ">=":
				return value.ToBool(left.GreaterEqual(right))
			case "<=":
				return value.ToBool(left.LessEqual(right))
			case ">":
				return value.ToBool(left.Greater(right))
			case "<":
				return value.ToBool(left.Less(right))
			}
		}
	}

	// D. Arithmetic
	// Arithmetic (+, -). Use findSplitIndex with last=true for Left-Associativity
	if idx := findSplitIndex(path, func(i int) bool {
		return (path[i] == '+' || path[i] == '-') && i > 0
	}, true); idx > 0 {
		op := path[idx]
		// Skip if scientific notation 'e-'?
		if idx > 0 && (path[idx-1] == 'e' || path[idx-1] == 'E') {
			// naive skip, but findSplitIndex loop goes backwards.
			// Ideally we should allow helper to return Multiple matches? No.
			// Just assume for now if we hit 'e-', it might be operator if balance 0.
			// But 1e-10 is literal check. Literal check is done before.
			// Wait: literal check failed (because it contains -).
			// So 1e-10 falls here. And we split at -.
			// Left: 1e, Right: 10.
			// resolve(1e) -> variable? fail.
			// resolve(10) -> 10.
			// fail.
			// So scientific notation support requires smarter lexing.
			// For templates, we can ignore complex scientific notation for now.
		}

		leftRaw := strings.TrimSpace(path[:idx])
		rightRaw := strings.TrimSpace(path[idx+1:])

		left := resolveValue(leftRaw, data, scope)
		right := resolveValue(rightRaw, data, scope)

		if op == '+' {
			return left.Add(right)
		}
		if op == '-' {
			return left.Sub(right)
		}
	}

	// Multiplicative (*, /, %)
	// Higher Precedence (Checked AFTER Additive)
	if idx := findSplitIndex(path, func(i int) bool {
		c := path[i]
		return c == '*' || c == '/' || c == '%'
	}, true); idx > 0 {
		op := path[idx]
		leftRaw := strings.TrimSpace(path[:idx])
		rightRaw := strings.TrimSpace(path[idx+1:])

		left := resolveValue(leftRaw, data, scope)
		right := resolveValue(rightRaw, data, scope)

		if op == '*' {
			return left.Mul(right)
		}
		if op == '/' {
			return left.Div(right)
		}
		if op == '%' {
			return left.Mod(right)
		}
	}

	// 3. Unwrap Parentheses (Fallback if no operator matched at top level)
	// This handles (a + b) -> a + b
	if strings.HasPrefix(path, "(") && strings.HasSuffix(path, ")") {
		inner := strings.TrimSpace(path[1 : len(path)-1])
		// Optimization: Check empty ()
		if inner == "" {
			return value.NewNull()
		}
		return resolveValue(inner, data, scope)
	}

	// 4. Variable Lookup (Fallback)
	var current value.Value
	if v, ok := data.(value.Value); ok {
		current = v
	} else {
		current = value.New(data)
	}

	if path == "." {
		return current
	}

	// Handle Explicit Data Access (.prop) -> Go Style
	if strings.HasPrefix(path, ".") {
		cleanPath := strings.TrimPrefix(path, ".")
		// Bypass Scope, Traverse Direct Data
		return traverse(current, strings.Split(cleanPath, "."))
	}

	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return value.NewNull()
	}

	// Priority 1: Check Scope (locals)
	// Try Exact Match
	if val, ok := scope[parts[0]]; ok {
		current = val
		if len(parts) > 1 {
			return traverse(current, parts[1:])
		}
		return current
	}
	// Try Alias Match ($var -> var)
	if strings.HasPrefix(parts[0], "$") {
		noDollar := strings.TrimPrefix(parts[0], "$")
		if val, ok := scope[noDollar]; ok {
			current = val
			if len(parts) > 1 {
				return traverse(current, parts[1:])
			}
			return current
		}
	}

	// Priority 2: Check Data (context)
	// Try Exact Match
	res := traverse(current, parts)
	if !res.IsNil() {
		return res
	}

	// Try Alias Match ($var -> var)
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

		// Unbox if necessary (Engine often wraps Map in Value)
		// Try direct Get first
		res := current.Get(part)

		// If direct Get fails (Nil) but it's a Value wrapping another Value/Map
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
