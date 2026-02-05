package runtime

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

func (vm *Runtime) compare(a, b value.Value, mode uint8) {
	if a.K == value.Proxy || b.K == value.Proxy {
		var op string
		switch mode {
		case 0:
			op = "="
		case 1:
			op = "!="
		case 2:
			op = ">"
		case 3:
			op = "<"
		case 4:
			op = ">="
		case 5:
			op = "<="
		}
		if a.K == value.Proxy {
			if handler, ok := a.V.(value.ProxyHandler); ok {
				vm.push(handler.OnCompare(op, b))
				return
			}
		} else {
			if handler, ok := b.V.(value.ProxyHandler); ok {
				vm.push(handler.OnCompare(op, a))
				return
			}
		}
	}

	var res bool
	switch mode {
	case 0:
		res = a.Equal(b)
	case 1:
		res = a.NotEqual(b)
	case 2:
		res = a.Greater(b)
	case 3:
		res = a.Less(b)
	case 4:
		res = a.GreaterEqual(b)
	case 5:
		res = a.LessEqual(b)
	}
	vm.push(value.ToBool(res))
}

func (vm *Runtime) call(name string, args ...value.Value) {
	if name == "log" || name == "PRINT" {
		for _, arg := range args {
			fmt.Print(arg.Text(), " ")
		}
		fmt.Println()
		vm.push(value.Value{K: value.Nil})
	} else {
		vm.push(value.Value{K: value.Nil})
	}
}
