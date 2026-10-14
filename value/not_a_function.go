package value

import (
	"reflect"
	"strings"
)

// NotAFunctionCode is the failure code of a call whose callee is not a function: JavaScript's
// "x is not a function", a runtime error since 14/10/2026. Until then the call gave the value itself
// after a dot ({ x: 3 }.x() was 3) and null otherwise, without a word.
const NotAFunctionCode = "NOT_A_FUNCTION"

// NotAFunction is the failure of calling `callee` (the call as the source wrote it: f, o.x, o["x"]),
// whose value `called` is not a function.
func NotAFunction(callee string, called Value) Value {
	return InvalidFailure(NotAFunctionCode, NotAFunctionMessage(callee, called))
}

// NotAFunctionMessage says what was called and what it is: `o.x không phải hàm (là số): không gọi
// được`. Neither a string's text nor a number is ever shown (14/10/2026): what a program calls by
// mistake can be a secret (env.API_KEY(), env.PIN()).
func NotAFunctionMessage(callee string, called Value) string {
	return callee + " không phải hàm (là " + calledKind(called) + "): không gọi được"
}

// NotAFunctionFrom gives the message of a NotAFunction failure, so a caller that knows more of the
// call (the VM: the object, the place; a template: the path) can say it.
func NotAFunctionFrom(result Value) (string, bool) {
	if result.K != Invalid {
		return "", false
	}
	failure, ok := result.ErrorVal.(Failure)
	if !ok || failure.Code != NotAFunctionCode {
		return "", false
	}
	return failure.Message, true
}

// calledKind is what a called value is: its kind, and for a boolean its value.
func calledKind(called Value) string {
	if called.K == Bool {
		return "boolean " + called.Text()
	}
	return KindName(called)
}

// KindName is what kind of value v is, as a message says it ("số", "chuỗi", "mảng"…): never its value.
func KindName(v Value) string {
	switch v.K {
	case Nil:
		return "null"
	case Number:
		return "số"
	case Bool:
		return "boolean"
	case String:
		return "chuỗi"
	case Map:
		return "object"
	case Array:
		return "mảng"
	case Time:
		return "thời điểm"
	case Duration:
		return "khoảng thời gian"
	case Bytes:
		return "dãy byte"
	case Struct, Proxy:
		return "object của engine"
	case Func:
		return "hàm"
	case Invalid:
		return "lỗi"
	default:
		return v.K.String()
	}
}

// hasGoMethod reports whether a host value has a Go method of that name, as Get finds it. Such a method
// without arguments is a getter: Get has already called it, so what Get gave is its result, not data.
func (v Value) hasGoMethod(name string) bool {
	if (v.K != Struct && v.K != Func) || v.V == nil {
		return false
	}
	_, ok := membersFor(reflect.ValueOf(v.V).Type()).methods[strings.ToLower(name)]
	return ok
}
