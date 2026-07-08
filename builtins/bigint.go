package builtins

import (
	"math"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

func BigInt() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Number, N: 0}
		}
		v := args[0]
		switch v.K {
		case value.Number:
			if v.N != math.Trunc(v.N) {
				return value.Value{K: value.Invalid, V: "BigInt: not an integer"}
			}
			return value.Value{K: value.Number, N: v.N}
		case value.Bool:
			return value.Value{K: value.Number, N: v.N}
		case value.String:
			s := strings.TrimSuffix(strings.TrimSpace(v.Text()), "n")
			if s == "" {
				return value.Value{K: value.Number, N: 0}
			}
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return value.New(i)
			}
			return value.Value{K: value.Invalid, V: "BigInt: cannot convert \"" + v.Text() + "\""}
		}
		return value.Value{K: value.Invalid, V: "BigInt: cannot convert value"}
	})
}
