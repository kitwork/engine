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

	cache map[string]*work.KitWork
}

func New(source string) *Engine {
	return &Engine{
		Source: source,
		cache:  make(map[string]*work.KitWork),
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

func (e *Engine) Load(hostname string) (*work.KitWork, error) {
	if kitwork, ok := e.cache[hostname]; ok {
		return kitwork, nil
	}
	source := path.Join(e.Source, hostname, "app.js")
	// 1. Biên dịch Blueprint (Bytecode)
	bc, err := Source(source).Blueprint()
	if err != nil {
		return nil, err
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
	e.cache[hostname] = kw
	return kw, nil
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("[CRITICAL] Panic Recovered: %v\n", rec)
			// debug.PrintStack()
			http.Error(w, "Internal Server Error (Panic)", http.StatusInternalServerError)
		}
	}()

	domain := strings.Split(r.Host, ":")[0]
	// 4. Kiểm tra và thực hiện Logics dựa trên Routes đã đăng ký
	kitwork, err := e.Load(domain)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := kitwork.Request(r)

	switch response.Type() {
	case "redirect":
		http.Redirect(w, r, response.String(), http.StatusSeeOther)
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(response.Code())
		w.Write(response.Bytes())
		break
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(response.Code())
		w.Write(response.Bytes())
		break
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(response.Code())
		w.Write(response.Bytes())
		break
	case "file":
		http.ServeFile(w, r, response.String())
		break
	case "folder":
		http.FileServer(http.Dir(response.String())).ServeHTTP(w, r)
		break
	case "empty":
		// No action needed for empty response
		break
	case "error":
		w.WriteHeader(http.StatusInternalServerError)
		break
	default:
		http.NotFound(w, r)
	}
	return
}
