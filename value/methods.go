package value

import (
	"encoding/json"
	"math/rand"
	"strings"
)

/* =============================================================================
   STANDARD VALUE METHODS (JS-Like Standard Library)
   ============================================================================= */

// --- Casting & Conversion ---

func (v Value) ToString(_ ...Value) Value { v.V = v.Text(); v.K = String; return v }
func (v Value) Integer(_ ...Value) Value  { v.N = float64(int64(v.N)); v.K = Number; return v }
func (v Value) ToFloat(_ ...Value) Value  { v.K = Number; return v }
func (v Value) Length(_ ...Value) Value   { v.N = float64(v.Len()); v.K = Number; return v }

func (v Value) ToJson(args ...Value) Value {
	if len(args) > 0 {
		if args[0].V == "string" {
			v.V = string(v.ToJSON())
			v.K = String
			return v
		}
	} else if v.K == String {
		// If no args and it's a string, try to PARSE it
		var data any
		if err := json.Unmarshal([]byte(v.String()), &data); err == nil {
			return New(data)
		}
	}
	return v
}

// --- String Methods ---

func (v Value) Upper(_ ...Value) Value {
	return New(strings.ToUpper(v.Text()))
}

func (v Value) Lower(_ ...Value) Value {
	return New(strings.ToLower(v.Text()))
}

func (v Value) Capitalize(_ ...Value) Value {
	s := v.String()
	if len(s) == 0 {
		return v
	}
	// Note: Strings in Go are UTF-8, but for simple capitalize, this works.
	return New(strings.ToUpper(s[:1]) + s[1:])
}

func (v Value) Safe(_ ...Value) Value {
	return Value{K: String, V: SafeHTML(v.Text())}
}

func (v Value) Trim(_ ...Value) Value {
	return New(strings.TrimSpace(v.Text()))
}

func (v Value) Includes(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	return ToBool(strings.Contains(v.String(), args[0].String()))
}

func (v Value) StartsWith(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	return ToBool(strings.HasPrefix(v.String(), args[0].String()))
}

func (v Value) EndsWith(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	return ToBool(strings.HasSuffix(v.String(), args[0].String()))
}

func (v Value) Split(args ...Value) Value {
	sep := ""
	if len(args) > 0 {
		sep = args[0].String()
	}
	parts := strings.Split(v.String(), sep)
	res := make([]Value, len(parts))
	for i, p := range parts {
		res[i] = Value{K: String, V: p}
	}
	return New(res)
}

func (v Value) Replace(args ...Value) Value {
	if len(args) < 2 {
		return v
	}
	v.V = strings.ReplaceAll(v.String(), args[0].String(), args[1].String())
	return v
}

// --- Array Methods (Mutation with *[]Value) ---

func (v Value) Push(args ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		*ptr = append(*ptr, args...)
	}
	return v
}

func (v Value) ItemAt(args ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok && len(args) > 0 {
		a := *ptr
		idx := int(args[0].N)
		if idx < 0 {
			idx = len(a) + idx
		}
		if idx >= 0 && idx < len(a) {
			return a[idx]
		}
	}
	return Value{K: Nil}
}

func (v Value) Pop(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok && len(*ptr) > 0 {
		idx := len(*ptr) - 1
		res := (*ptr)[idx]
		*ptr = (*ptr)[:idx]
		return res
	}
	return Value{K: Nil}
}

func (v Value) Shift(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok && len(*ptr) > 0 {
		res := (*ptr)[0]
		*ptr = (*ptr)[1:]
		return res
	}
	return Value{K: Nil}
}

func (v Value) Unshift(args ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		*ptr = append(args, *ptr...)
	}
	return v
}

func (v Value) Compact(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		res := make([]Value, 0)
		for _, item := range a {
			if item.K != Nil && item.Truthy() {
				res = append(res, item)
			}
		}
		*ptr = res
	}
	return v
}

func (v Value) Unique(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		seen := make(map[any]bool)
		res := make([]Value, 0)
		for _, item := range a {
			// Basic uniqueness by interface value
			key := item.Interface()
			if !seen[key] {
				seen[key] = true
				res = append(res, item)
			}
		}
		*ptr = res
	}
	return v
}

func (v Value) Reverse(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
			a[i], a[j] = a[j], a[i]
		}
	}
	return v
}

func (v Value) Shuffle(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		rand.Shuffle(len(a), func(i, j int) {
			a[i], a[j] = a[j], a[i]
		})
	}
	return v
}

func (v Value) Random(args ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok && len(*ptr) > 0 {
		a := *ptr
		count := 1
		if len(args) > 0 {
			count = int(args[0].N)
		}

		if count <= 1 {
			return a[rand.Intn(len(a))]
		}

		// Multi-sample: return a new array
		if count > len(a) {
			count = len(a)
		}
		// Copy and shuffle to get 'count' unique items
		tmp := make([]Value, len(a))
		copy(tmp, a)
		rand.Shuffle(len(tmp), func(i, j int) {
			tmp[i], tmp[j] = tmp[j], tmp[i]
		})
		res := tmp[:count]
		return Value{K: Array, V: &res}
	}
	return Value{K: Nil}
}

func (v Value) Join(args ...Value) Value {
	sep := ","
	if len(args) > 0 {
		sep = args[0].Text()
	}
	if ptr, ok := v.V.(*[]Value); ok {
		var b strings.Builder
		for i, item := range *ptr {
			if i > 0 {
				b.WriteString(sep)
			}
			b.WriteString(item.Text())
		}
		return Value{K: String, V: b.String()}
	}
	return Value{K: String, V: ""}
}

// --- Map Methods ---

func (v Value) Has(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	if m, ok := v.V.(map[string]Value); ok {
		_, exists := m[args[0].String()]
		return ToBool(exists)
	}
	return FALSE
}

func (v Value) Keys(_ ...Value) Value {
	if m, ok := v.V.(map[string]Value); ok {
		keys := make([]Value, 0, len(m))
		for k := range m {
			keys = append(keys, Value{K: String, V: k})
		}
		return New(keys)
	}
	return Value{K: Array, V: &[]Value{}}
}

func (v Value) Delete(args ...Value) Value {
	if len(args) > 0 {
		if m, ok := v.V.(map[string]Value); ok {
			delete(m, args[0].Text())
		}
	}
	return v
}

func (v Value) Merge(args ...Value) Value {
	if len(args) > 0 && args[0].IsMap() {
		if m, ok := v.V.(map[string]Value); ok {
			other := args[0].Map()
			for k, val := range other {
				m[k] = val
			}
		}
	}
	return v
}

// --- Common ---

func (v Value) HTML(_ ...Value) Value {
	return Value{
		K: Map,
		V: map[string]Value{
			"__is_safe_html": TRUE,
			"content":        {K: String, V: v.Text()},
		},
	}
}

func (v Value) Render(args ...Value) Value {
	res := map[string]Value{"template": v, "__is_html": TRUE}
	if len(args) > 0 {
		res["data"] = args[0]
	}
	return Value{K: Map, V: res}
}
