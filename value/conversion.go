package value

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"
	"unsafe"
)

// Strftime to Go Layout Converter moved to format.go

func (v Value) Text() string {
	// String values already own their canonical text. Returning the payload
	// directly keeps property names and method names allocation-free in the VM.
	if v.K == String {
		s, _ := v.V.(string)
		return s
	}
	if v.K == Invalid {
		if s, ok := v.V.(string); ok {
			return s
		}
		if failure, ok := v.V.(error); ok {
			// a run's diagnostic writes its text only when it is read (runtime.diagnosticResult)
			return failure.Error()
		}
	}
	if v.K == Array || v.K == Map {
		return string(v.nestedText())
	}
	buf := make([]byte, 0, 64)
	return string(v.Append(buf))
}

func (v Value) String() string {
	if s, ok := v.V.(string); ok {
		return s
	}
	// Fallback to Text() for Time, Duration, Map, Array, Nil, etc.
	return v.Text()
}

// Text marks, in the text of an object or array, a value it holds that holds it back (o.self = o:
// {self: [tự tham chiếu]}) and one nested past MaxDepth: a text cannot fail, and walking either
// would never end (14/10/2026; until then it ended in a Go stack overflow that stopped the host).
const (
	cycleText = "[tự tham chiếu]"
	deepText  = "[quá sâu]"
)

func (v Value) Append(b []byte) []byte {
	if v.K == Array || v.K == Map {
		return append(b, v.nestedText()...)
	}
	return v.appendScalar(b)
}

// nestedText is the text of the object or array v, in a buffer of its own: Append's buffer is often
// its caller's, on the caller's stack (a template writing a number), and handing it to the recursive
// walk would send it to the heap on every call.
func (v Value) nestedText() []byte {
	var w walk
	w.enterValue(v, "", rootStep)
	return w.appendText(make([]byte, 0, 64), v)
}

// appendText writes the text of the object or array v, walking into what it holds on the path w.
func (w *walk) appendText(b []byte, v Value) []byte {
	if v.K == Array {
		b = append(b, '[')
		for i, item := range v.Array() {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = w.appendTextItem(b, item, "", i)
		}
		return append(b, ']')
	}
	// An object's keys are written sorted (14/10/2026), as JSON.stringify writes them: the language
	// promises no order of keys (an object is a Go map, ENGINE_CAPABILITIES), so the text followed Go's
	// map order, which changes from one run to the next. console.log of the same object printed
	// {at, message, code} once and {code, at, message} the next time, and a template printing an object
	// changed its page between two requests. Sorted, the same object always has the same text.
	m := v.Map()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b = append(b, '{')
	for i, k := range keys {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = append(b, k...)
		b = append(b, ": "...)
		b = w.appendTextItem(b, m[k], k, keyStep)
	}
	return append(b, '}')
}

func (w *walk) appendTextItem(b []byte, item Value, key string, index int) []byte {
	if item.K != Array && item.K != Map {
		return item.appendScalar(b)
	}
	if err := w.enterValue(item, key, index); err != nil {
		if err.Cycle {
			return append(b, cycleText...)
		}
		return append(b, deepText...)
	}
	b = w.appendText(b, item)
	w.leave()
	return b
}

func (v Value) appendScalar(b []byte) []byte {
	switch v.K {
	case Invalid:
		if s, ok := v.V.(string); ok {
			return append(b, s...)
		}
		if failure, ok := v.V.(error); ok {
			return append(b, failure.Error()...)
		}
		return append(b, "invalid"...)
	case String:
		// the payload itself, as String() gives it: calling String() here would make Append part of
		// its own recursion (String → Text → Append) and send every caller's buffer to the heap
		s, _ := v.V.(string)
		return append(b, s...)
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
	default:
		return b
	}
}

// Interface is v as plain Go data. A value an object or array holds that holds it back, or one
// nested past MaxDepth, is nil there: Interface cannot fail, and walking either would never end.
func (v Value) Interface() any {
	if v.K == Array || v.K == Map {
		var w walk
		w.enterValue(v, "", rootStep)
		return w.plain(v)
	}
	return v.interfaceScalar()
}

// plain is the object or array v as plain Go data, walking into what it holds on the path w.
func (w *walk) plain(v Value) any {
	switch m := v.V.(type) {
	case *[]Value, []Value:
		arr := v.Array()
		res := make([]any, len(arr))
		for i, val := range arr {
			res[i] = w.plainItem(val, "", i)
		}
		return res
	case map[string]Value:
		res := make(map[string]any)
		for k, val := range m {
			res[k] = w.plainItem(val, k, keyStep)
		}
		return res
	}
	return v.V
}

func (w *walk) plainItem(item Value, key string, index int) any {
	if item.K != Array && item.K != Map {
		return item.interfaceScalar()
	}
	if err := w.enterValue(item, key, index); err != nil {
		return nil
	}
	plain := w.plain(item)
	w.leave()
	return plain
}

func (v Value) interfaceScalar() any {
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
	case Invalid:
		if failure, ok := v.V.(error); ok {
			return failure.Error()
		}
		return v.V
	default:
		return v.V
	}
}

func (v Value) Int() int       { return int(v.N) }
func (v Value) Int64() int64   { return int64(v.N) }
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
	data, _ := json.Marshal(v)
	return data
}
