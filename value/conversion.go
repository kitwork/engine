package value

import (
	"encoding/json"
	"strconv"
	"time"
	"unsafe"
)

func (v Value) Text() string {
	buf := make([]byte, 0, 64)
	return string(v.Append(buf))
}

func (v Value) String() string {
	if s, ok := v.V.(string); ok {
		return s
	}
	if safe, ok := v.V.(SafeHTML); ok {
		return string(safe)
	}
	if v.K == Number || v.K == Bool || v.K == Nil {
		return v.Text()
	}
	return ""
}

func (v Value) Append(b []byte) []byte {
	switch v.K {
	case String:
		return append(b, v.String()...)
	case Number:
		i := int64(v.N)
		if v.N == float64(i) {
			return strconv.AppendInt(b, i, 10)
		}
		return strconv.AppendFloat(b, v.N, 'g', -1, 64)
	case Bool:
		if v.N > 0 {
			return append(b, "true"...)
		}
		return append(b, "false"...)
	case Nil:
		return append(b, "null"...)
	case Time:
		return time.Unix(0, int64(v.N)).AppendFormat(b, time.RFC3339)
	case Duration:
		return append(b, time.Duration(int64(v.N)).String()...)
	case Bytes:
		return append(b, v.Bytes()...)
	case Array:
		b = append(b, '[')
		ptr := v.V.(*[]Value)
		arr := *ptr

		for i, item := range arr {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = item.Append(b)
		}
		return append(b, ']')
	case Map:
		b = append(b, '{')
		m := v.V.(map[string]Value)
		first := true
		for k, val := range m {
			if !first {
				b = append(b, ", "...)
			}
			b = append(b, k...)
			b = append(b, ": "...)
			b = val.Append(b)
			first = false
		}
		return append(b, '}')
	default:
		return b
	}
}

func (v Value) Interface() any {
	switch v.K {
	case Nil:
		return nil
	case Bool:
		return v.N > 0
	case Number:
		return v.N
	case String:
		return v.V.(string)
	case Time:
		return time.Unix(0, int64(v.N))
	case Duration:
		return time.Duration(int64(v.N))
	case Bytes:
		return v.V.([]byte)
	case Array:
		ptr := v.V.(*[]Value)
		arr := *ptr
		res := make([]any, len(arr))
		for i, val := range arr {
			res[i] = val.Interface()
		}
		return res

	case Map:
		m := v.V.(map[string]Value)
		res := make(map[string]any)
		for k, val := range m {
			res[k] = val.Interface()
		}
		return res
	default:
		return v.V
	}
}

func (v Value) Int() int64     { return int64(v.N) }
func (v Value) Float() float64 { return v.N }

func (v Value) Bytes() []byte {
	if b, ok := v.V.([]byte); ok {
		return b
	}
	return nil
}

// AsBytes provides a zero-copy read-only view into string data.
func (v Value) AsBytes() []byte {
	if v.K == Bytes {
		return v.Bytes()
	}
	s := v.String()
	if s == "" {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func (v Value) ByteSlice() []byte {
	switch v.K {
	case Bytes:
		return v.Bytes()
	case String:
		return v.AsBytes()
	default:
		return nil
	}
}

func (v Value) ToJSON() []byte {
	data, _ := json.Marshal(v.Interface())
	return data
}
