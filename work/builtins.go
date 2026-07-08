package work

import (
	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/helpers/http"
	"github.com/kitwork/engine/value"
)

var TestNotifyHook func(string, ...value.Value)

func injectJSCompat(globals map[string]value.Value) {
	http.IsLocalAllowed = func() bool { return AllowLocal }
	http.GetServerPort = func() int { return ServerPort }
	builtins.TestNotifyHook = TestNotifyHook
	builtins.InjectJSCompat(globals)
}
