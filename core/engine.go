package core

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

type Engine struct {
	Source string
	cache  map[string]*work.Host
}

func New(source string) *Engine {
	return &Engine{
		Source: source,
		cache:  make(map[string]*work.Host),
	}
}

func (e *Engine) Load(hostname string) (*work.Host, error) {
	if h, ok := e.cache[hostname]; ok {
		return h, nil
	}

	sourcePath := path.Join(e.Source, hostname, "app.js")
	bc, err := Source(sourcePath).Blueprint()
	if err != nil {
		return nil, err
	}

	// Tạo Host mới từ Bytecode
	host := work.NewHost(bc)

	// Nạp Router cho Host bằng cách chạy script khởi tạo
	host.VM.Globals["kitwork"] = value.New(host.Provider())
	host.VM.Run()

	e.cache[hostname] = host
	return host, nil
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("[CRITICAL] Panic: %v\n", rec)
			http.Error(w, "Service Unavailable", 503)
		}
	}()

	domain := strings.Split(r.Host, ":")[0]
	host, err := e.Load(domain)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}

	// Bàn giao toàn bộ quyền xử lý cho Host
	host.Serve(w, r)
}
