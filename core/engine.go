package core

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

type Engine struct {
	stdlib *compiler.Environment
	Source string

	// CacheRouter map[string]http.HandlerFunc // GET:kitwork.vn/path ....
	// CacheDomain map[string]string           // kitwork.vn -> identity
}

func New(source string) *Engine {
	return &Engine{
		Source: source,
	}
}

// func (e *Engine) Builtins() {
// 	e.stdlib.Set("kitwork", value.NewFunc(func(args ...value.Value) value.Value {
// 		kw := work.New(e.Source, )
// 		return value.New(kw)
// 	}))
// }

func (e *Engine) identity(hostname string) string {
	return "test"
}

func (e *Engine) path(hostname string) string {
	identity := e.identity(hostname)
	return path.Join(e.Source, identity, hostname, "work.js")
}

func (e *Engine) HandleRouter(w http.ResponseWriter, r *http.Request) error {
	domain := strings.Split(r.Host, ":")[0]
	source := path.Join(e.Source, domain, "app.js")
	fmt.Printf("[HandleRouter] Domain: %s, Source: %s\n", domain, source)

	// 1. Biên dịch Blueprint (Bytecode)
	bc, err := Source(source).Blueprint()
	if err != nil {
		return err
	}

	// 2. Khởi tạo VM, Môi trường và KitWork Provider
	vm := runtime.New(bc.Instructions, bc.Constants)
	kw := work.New()
	stdlib := compiler.NewEnvironment()

	// Đăng ký kitwork là Object (cho phép kitwork.router nhờ Getter)
	stdlib.Set("kitwork", value.New(kw))

	vm.Globals = stdlib.Store()

	// 3. Thực thi Script để nạp các Routes
	vm.Run()

	// 4. Kiểm tra và thực hiện Logics dựa trên Routes đã đăng ký

	return kw.Server(w, r)
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("[CRITICAL] Panic Recovered: %v\n", rec)
			// debug.PrintStack()
			http.Error(w, "Internal Server Error (Panic)", http.StatusInternalServerError)
		}
	}()

	if err := e.HandleRouter(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

}
