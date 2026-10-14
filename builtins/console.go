package builtins

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/value"
)

// Console is the global console: log, error, warn, info and debug each print their arguments' text,
// separated by spaces, on one line of standard output, led by their own name ("[console.warn] …").
// error, warn, info and debug came on 14/10/2026: code written for JavaScript calls them, and calling
// what is not a function is a runtime error since that day.
func Console() value.Value {
	methods := make(map[string]value.Value, 5)
	for _, name := range []string{"log", "error", "warn", "info", "debug"} {
		methods[name] = consoleMethod("[console." + name + "]")
	}
	return value.New(methods)
}

func consoleMethod(prefix string) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		var sb strings.Builder
		for i, arg := range args {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(arg.Text())
		}
		fmt.Println(prefix, sb.String())
		return value.Value{K: value.Nil}
	})
}
