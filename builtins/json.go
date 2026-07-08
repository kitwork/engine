package builtins

import (
	"encoding/json"

	"github.com/kitwork/engine/value"
)

func JSON() value.Value {
	stringify := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		bytes, err := json.Marshal(args[0])
		if err != nil {
			return value.Value{K: value.Invalid, V: err.Error()}
		}
		return value.NewString(string(bytes))
	})
	parse := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		var val value.Value
		if err := json.Unmarshal([]byte(args[0].Text()), &val); err != nil {
			return value.Value{K: value.Invalid, V: err.Error()}
		}
		return val
	})
	return value.New(map[string]value.Value{
		"stringify": stringify,
		"parse":     parse,
	})
}
