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

func resolveValue(path string, data any, scope map[string]value.Value) value.Value {
	// 0. (Removed) Auto-Normalize $var -> $.var
	// We allow users to use $ for local vars if they want (e.g. let $a = 1)
	// So explicit $.var is required for Root Access, or just use 'this' or standard lookup.

	// 1. Literal Handling (Number & String)
	// Check for Quoted String: "value" or 'value'
	if (strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`)) ||
		(strings.HasPrefix(path, `'`) && strings.HasSuffix(path, `'`)) {
		return value.New(path[1 : len(path)-1])
	}
	// Check for Number
	if val, err := strconv.ParseFloat(path, 64); err == nil {
		return value.New(val)
	}

	// Root Context Wrapper
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
