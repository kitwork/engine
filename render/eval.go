package render

import (
	"fmt"
	"html"
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
					sb.WriteString(eval(child, item, newScope)) // item is value.Value
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
	isRaw := strings.HasPrefix(rawKey, "$")
	key := rawKey
	if isRaw {
		key = strings.TrimPrefix(rawKey, "$")
	}

	val := resolveValue(key, data, scope)
	if val.IsNil() && isRaw {
		val = resolveValue(rawKey, data, scope)
	}

	if val.IsNil() {
		return ""
	}

	// Double unwrap pattern for complex Values
	// 1. Check for Enum Flag (Fastest & Preferred)
	if val.S == value.SafeHTML {
		return val.String()
	}

	// 3. Check for Safe Map Wrapper (Legacy/Fallback)
	if val.IsMap() {
		m := val.Map()
		if _, isSafe := m["__is_safe_html"]; isSafe {
			if content, hasContent := m["content"]; hasContent {
				return content.String()
			}
		}
	}

	strVal := val.String()
	if isRaw {
		return strVal
	}
	return html.EscapeString(strVal)
}

func resolveValue(path string, data any, scope map[string]value.Value) value.Value {
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

	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return value.NewNull()
	}

	// Priority 1: Check Scope (locals)
	if val, ok := scope[parts[0]]; ok {
		current = val
		if len(parts) > 1 {
			return traverse(current, parts[1:])
		}
		return current
	}

	// Priority 2: Check Data (context)
	return traverse(current, parts)
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
