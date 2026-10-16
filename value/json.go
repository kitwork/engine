package value

import (
	"encoding/json"
	"sort"
	"time"
)

// MarshalJSON implements the json.Marshaler interface for Value.
// This ensures that when a Value is marshaled, it outputs its actual content
// (e.g. string, number) rather than the internal struct fields (N, V, K, S).
// This enables critical performance optimizations by avoiding the need to
// convert the entire structure to interface{} (map[string]any) before marshaling.
func (v Value) MarshalJSON() ([]byte, error) {
	switch v.K {
	case Invalid:
		return []byte("null"), nil
	case Nil:
		return []byte("null"), nil
	case Bool:
		if v.N > 0 {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case Number:
		if _, nonFinite := NonFiniteText(v.N); nonFinite {
			// NaN, Infinity and -Infinity are null, as JSON.stringify writes them (15/10/2026): JSON has
			// no spelling for them, and encoding/json refused them, so ctx.json of one answered 500
			return []byte("null"), nil
		}
		// as JSON.stringify writes a number, which is String(n) (AppendNumber): a whole number past 2^53
		// has JavaScript's digits (2 ** 60 is 1152921504606847000), not every digit of the int64
		return AppendNumber(nil, v.N), nil
	case String:
		return json.Marshal(v.V.(string))
	case Time:
		return json.Marshal(time.Unix(0, int64(v.N)))
	case Duration:
		return json.Marshal(time.Duration(v.N).String())
	case Bytes:
		return json.Marshal(v.Bytes())
	case Array, Map:
		// The engine walks an object or array itself rather than handing it to json.Marshal, which
		// would call this method again for every value inside, with no memory of the path: an object
		// that holds itself recursed until Go's stack overflowed, killing the host (14/10/2026).
		var w walk
		w.enterValue(v, "", rootStep)
		return w.appendJSON(nil, v)
	case Func:
		// Functions cannot be marshaled to JSON, return null or string representation
		return []byte(`"<function>"`), nil
	case Proxy:
		return []byte(`"<proxy>"`), nil
	default:
		return []byte("null"), nil
	}
}

// UnmarshalJSON implements json.Unmarshaler
func (v *Value) UnmarshalJSON(data []byte) error {
	var i any
	if err := json.Unmarshal(data, &i); err != nil {
		return err
	}
	*v = New(i)
	return nil
}

// appendJSON writes the object or array v after dst exactly as encoding/json wrote it through
// MarshalJSON — an object's keys sorted, strings HTML-escaped, a nil slice or map null — walking into
// what it holds on the path w, so a value that holds itself or nests past MaxDepth is a *WalkError.
func (w *walk) appendJSON(dst []byte, v Value) ([]byte, error) {
	switch data := v.V.(type) {
	case *[]Value:
		if data == nil {
			return append(dst, "null"...), nil
		}
		return w.appendJSONArray(dst, *data)
	case []Value:
		return w.appendJSONArray(dst, data)
	case map[string]Value:
		if data == nil {
			return append(dst, "null"...), nil
		}
		keys := make([]string, 0, len(data))
		for key := range data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		dst = append(dst, '{')
		for at, key := range keys {
			if at > 0 {
				dst = append(dst, ',')
			}
			dst = appendJSONString(dst, key)
			dst = append(dst, ':')
			var err error
			if dst, err = w.appendJSONItem(dst, data[key], key, keyStep); err != nil {
				return dst, err
			}
		}
		return append(dst, '}'), nil
	}
	// another payload under an object or array kind: what encoding/json makes of it, as before
	encoded, err := json.Marshal(v.V)
	return append(dst, encoded...), err
}

func (w *walk) appendJSONArray(dst []byte, items []Value) ([]byte, error) {
	if items == nil {
		return append(dst, "null"...), nil
	}
	dst = append(dst, '[')
	for at, item := range items {
		if at > 0 {
			dst = append(dst, ',')
		}
		var err error
		if dst, err = w.appendJSONItem(dst, item, "", at); err != nil {
			return dst, err
		}
	}
	return append(dst, ']'), nil
}

// appendJSONItem writes one value an object holds under key, or an array at index.
func (w *walk) appendJSONItem(dst []byte, item Value, key string, index int) ([]byte, error) {
	switch item.K {
	case Array, Map:
		if err := w.enterValue(item, key, index); err != nil {
			return dst, err
		}
		dst, err := w.appendJSON(dst, item)
		w.leave()
		return dst, err
	case Nil, Invalid:
		return append(dst, "null"...), nil
	case Bool:
		if item.N > 0 {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case Number:
		if _, nonFinite := NonFiniteText(item.N); !nonFinite {
			return AppendNumber(dst, item.N), nil
		}
	case String:
		if text, ok := item.V.(string); ok && plainJSONString(text) {
			dst = append(dst, '"')
			dst = append(dst, text...)
			return append(dst, '"'), nil
		}
	}
	// everything else exactly as encoding/json wrote a Value it held: MarshalJSON, compacted and
	// HTML-escaped ("<function>" is "\u003cfunction\u003e" inside an object)
	encoded, err := json.Marshal(item)
	return append(dst, encoded...), err
}

// appendJSONString writes text as encoding/json writes a string or a key: quoted, HTML-escaped.
func appendJSONString(dst []byte, text string) []byte {
	if plainJSONString(text) {
		dst = append(dst, '"')
		dst = append(dst, text...)
		return append(dst, '"')
	}
	encoded, _ := json.Marshal(text)
	return append(dst, encoded...)
}

// plainJSONString reports whether encoding/json writes text as it is between quotes: printable ASCII
// with nothing to escape (no quote, backslash, control character, or <, >, & — it escapes HTML).
func plainJSONString(text string) bool {
	for at := 0; at < len(text); at++ {
		switch char := text[at]; {
		case char < 0x20 || char >= 0x80:
			return false
		case char == '"' || char == '\\' || char == '<' || char == '>' || char == '&':
			return false
		}
	}
	return true
}
