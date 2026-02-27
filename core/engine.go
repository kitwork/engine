package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/kitwork/engine/compiler"
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

// func (e *Engine) load(hostname string) error {
// 	path := e.path(hostname)
// 	content, err := os.ReadFile(path)
// 	if err != nil {
// 		return err
// 	}

// 	l := compiler.NewLexer(string(content))
// 	p := compiler.NewParser(l)
// 	prog := p.ParseProgram()
// 	if len(p.Errors()) > 0 {
// 		return fmt.Errorf("compile error: %s", p.Errors()[0])
// 	}

// 	compiler.Evaluator(prog, e.stdlib)

// 	return nil
// }

func (e *Engine) Work(hostname string, r *http.Request) (*work.Response, error) {
	resp := new(work.Response)
	resp.Status(http.StatusOK)
	return resp, nil
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

	response, err := e.Work(domain, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(response.Code())
	switch response.Type() {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		if response.Data().Interface() != nil {
			b, _ := json.Marshal(response.Data().Interface())
			w.Write(b)
		}
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if response.Data().String() != "" {
			w.Write([]byte(response.Data().String()))
		}
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if response.Data().String() != "" {
			w.Write([]byte(response.Data().String()))
		}
	case "redirect":
		http.Redirect(w, r, e.path(domain), http.StatusSeeOther)
	case "file":
		http.ServeFile(w, r, e.path(domain))
	case "folder":
		http.FileServer(http.Dir(e.path(domain))).ServeHTTP(w, r)
	case "empty":
		// Do nothing, Header OK is enough
	case "error":
		w.WriteHeader(http.StatusInternalServerError)
	default:
		http.NotFound(w, r)
	}

}
