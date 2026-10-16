package builtins

import (
	"math"

	"github.com/kitwork/engine/value"
)

func Array() value.Value {
	ctor := func(args ...value.Value) value.Value {
		if len(args) == 1 && args[0].K == value.Number {
			if length := args[0].N; length < 0 || length != length || math.IsInf(length, 0) {
				// JavaScript's RangeError (15/10/2026): make() of the int Go made of NaN, an infinity or a
				// negative number panicked past safe()
				return value.InvalidFailure(value.InvalidArgumentCode, "Array(n): độ dài mảng là một số không âm, không có "+value.NumberText(length))
			}
			n := args[0].Int()
			out := make([]value.Value, n)
			for i := 0; i < n; i++ {
				out[i] = value.Value{K: value.Nil}
			}
			return value.New(out)
		}
		return value.New(args)
	}

	props := map[string]value.Value{
		"isArray": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.FALSE
			}
			return value.ToBool(args[0].K == value.Array)
		}),
	}

	return value.NewFuncObject(ctor, props)
}
