package value

import (
	"encoding/json"
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
		// Use standard number formatting
		if v.N == float64(int64(v.N)) {
			// Integer case
			return json.Marshal(int64(v.N))
		}
		return json.Marshal(v.N)
	case String:
		return json.Marshal(v.V.(string))
	case Time:
		return json.Marshal(time.Unix(0, int64(v.N)))
	case Duration:
		return json.Marshal(time.Duration(v.N).String())
	case Bytes:
		return json.Marshal(v.Bytes())
	case Array:
		// v.V is *[]Value. json.Marshal will iterate and call MarshalJSON on elements.
		return json.Marshal(v.V)
	case Map:
		// v.V is map[string]Value. json.Marshal will call MarshalJSON on values.
		return json.Marshal(v.V)
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
