package builtins

import (
	"encoding/json"
	"errors"

	"github.com/kitwork/engine/value"
)

func JSON() value.Value {
	stringify := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		bytes, err := json.Marshal(args[0])
		if err != nil {
			// An object that holds itself is JavaScript's TypeError (TYPE), one nested past
			// value.MaxDepth its RangeError (INVALID_ARGUMENT): a failure the program can catch with
			// safe(() => …) (14/10/2026). Until then the first stopped the whole host with a Go stack
			// overflow.
			var walk *value.WalkError
			if errors.As(err, &walk) {
				return walk.Failure("JSON.stringify")
			}
			return value.InvalidFailure(value.InvalidArgumentCode, "JSON.stringify: "+err.Error())
		}
		return value.NewString(string(bytes))
	})
	parse := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		var val value.Value
		if err := json.Unmarshal([]byte(args[0].Text()), &val); err != nil {
			// text that is not JSON is data from outside that cannot be read
			return value.InvalidFailure(value.InvalidInputCode, "JSON.parse: "+err.Error())
		}
		return val
	})
	return value.New(map[string]value.Value{
		"stringify": stringify,
		"parse":     parse,
	})
}
