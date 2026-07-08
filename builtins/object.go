package builtins

import "github.com/kitwork/engine/value"

func Object() value.Value {
	ctor := func(args ...value.Value) value.Value {
		if len(args) > 0 {
			return args[0]
		}
		return value.New(map[string]value.Value{})
	}

	props := map[string]value.Value{
		"keys": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Map {
				return value.New([]value.Value{})
			}
			m := args[0].Map()
			out := make([]value.Value, 0, len(m))
			for k := range m {
				out = append(out, value.NewString(k))
			}
			return value.New(out)
		}),
		"values": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Map {
				return value.New([]value.Value{})
			}
			m := args[0].Map()
			out := make([]value.Value, 0, len(m))
			for _, v := range m {
				out = append(out, v)
			}
			return value.New(out)
		}),
		"entries": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Map {
				return value.New([]value.Value{})
			}
			m := args[0].Map()
			out := make([]value.Value, 0, len(m))
			for k, v := range m {
				out = append(out, value.New([]value.Value{value.NewString(k), v}))
			}
			return value.New(out)
		}),
		"assign": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.New(map[string]value.Value{})
			}
			target := args[0]
			if target.K != value.Map {
				return target
			}
			tm := target.Map()
			for _, src := range args[1:] {
				if src.K == value.Map {
					for k, v := range src.Map() {
						tm[k] = v
					}
				}
			}
			return target
		}),
		"fromEntries": value.NewFunc(func(args ...value.Value) value.Value {
			out := map[string]value.Value{}
			if len(args) == 0 || args[0].K != value.Array {
				return value.New(out)
			}
			for _, pair := range args[0].Array() {
				if pair.K == value.Array && pair.Len() >= 2 {
					out[pair.Index(0).Text()] = pair.Index(1)
				}
			}
			return value.New(out)
		}),
	}

	return value.NewFuncObject(ctor, props)
}
