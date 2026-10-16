package builtins

import (
	"math"

	"github.com/kitwork/engine/utilities/http"
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
	"fail":       Fail,
	"safe":       Safe,
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
	// 15/10/2026 (the owner's L2): a division by zero gives Infinity or NaN, so a program can name and
	// test them, as in JavaScript
	"Infinity": func() value.Value { return value.New(math.Inf(1)) },
	"NaN":      func() value.Value { return value.New(math.NaN()) },
	"isNaN":    IsNaN,
	"isFinite": IsFinite,
}

func InjectJSCompat(globals map[string]value.Value) {
	for name, build := range builtins {
		globals[name] = build()
	}
}
