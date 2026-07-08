package builtins

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/value"
)

func Console() value.Value {
	log := value.NewFunc(func(args ...value.Value) value.Value {
		var sb strings.Builder
		for i, arg := range args {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(arg.Text())
		}
		fmt.Println("[console.log]", sb.String())
		return value.Value{K: value.Nil}
	})
	return value.New(map[string]value.Value{"log": log})
}
