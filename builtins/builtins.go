package builtins

import (
	"github.com/kitwork/engine/helpers/http"
	"github.com/kitwork/engine/value"
)

var TestNotifyHook func(string, ...value.Value)

var builtins = map[string]func() value.Value{
	"Math":       Math,
	"Date":       Date,
	"JSON":       JSON,
	"console":    Console,
	"Object":     Object,
	"Number":     Number,
	"BigInt":     BigInt,
	"String":     String,
	"Boolean":    Boolean,
	"Array":      Array,
	"parseInt":   ParseInt,
	"parseFloat": ParseFloat,
	"fail":       Error,
	"Error":      Error,
	"fetch": func() value.Value {
		return value.NewFunc(http.Fetch)
	},
	"testNotify": func() value.Value {
		return value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 && TestNotifyHook != nil {
				TestNotifyHook(args[0].Text(), args[1:]...)
			}
			return value.Value{K: value.Nil}
		})
	},
}

func InjectJSCompat(globals map[string]value.Value) {
	for name, build := range builtins {
		globals[name] = build()
	}
}
