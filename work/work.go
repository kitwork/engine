package work

import (
	"net/http"
	"sync"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

var vmPool = sync.Pool{
	New: func() interface{} {
		// Tạo một VM trắng, sẽ được cấu hình lại bằng FastReset
		return runtime.New(nil, nil)
	},
}

// Router struct is defined in router.go
type Config struct {
	root     string
	base     string
	multiple bool
}

func (t *Tenant) Kitwork(vals ...value.Value) *KitWork { return &KitWork{tenant: t} }

func (w *KitWork) Cache() *GeneralCache {
	return &GeneralCache{tenant: w.tenant}
}

// KitWork is the per-tenant capability surface returned by kitwork() in the VM.
// Every capability is a METHOD on *KitWork, so they must all live in package work
// (Go requires methods in the type's package) — that's why this package is large.
// Capability → file map:
//
//	router.go    Router()        log.go       Log()         db.go        Database()
//	http.go      HTTP()          jwt.go       JWT()         render.go    Render()
//	qrcode.go    Qrcode()        napas.go     Napas()       file.go      File()
//	collection.go Collection()   (directory-backed Markdown content)
//	browser.go   Browser()       chromedp.go  Chromedp()/Screenshot()   go.go  Go()
//	env.go       Env()           (per-tenant, path-isolated env)
type KitWork struct {
	tenant *Tenant
}

func (w *KitWork) Capability(name string) value.Value {
	if val, ok := capabilities.DefaultRegistry.Get(name, w.tenant); ok {
		return val
	}
	return value.Value{K: value.Nil}
}

// Serve handles every request for this tenant. Kitwork is FILESYSTEM-ROUTED: after the built-in,
// always-on assets (the client hydrate runtime and the vendored fonts — identical bytes for every
// tenant), the request walks the folder tree (see tree_serve.go). There is no flat route table.
func (t *Tenant) Serve(w http.ResponseWriter, r *http.Request) {
	// /kit.js — the client runtime the render injects into every hydrated page.
	if serveHydrateIf(w, r) {
		return
	}
	// /jitfonts/* — vendored woff2 served straight off the embedded FS.
	if serveFontIf(w, r) {
		return
	}
	t.serveTree(w, r)
}
