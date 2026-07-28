package work

import (
	"net/http"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/capabilities"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

var enginePool = app.NewPool()

// Router struct is defined in router.go
type Config struct {
	root     string
	base     string
	multiple bool
}

func (t *Tenant) Kitwork(vals ...value.Value) *KitWork { return &KitWork{tenant: t} }

func (t *Tenant) prepareExecutionVM(
	vm *runtime.VM,
	globals map[string]value.Value,
	builtins []value.Value,
	requestScopes ...*requestscope.Scope,
) {
	if vm == nil {
		return
	}
	var requestScope *requestscope.Scope
	if len(requestScopes) > 0 {
		requestScope = requestScopes[0]
	}

	vm.Globals = make(map[string]value.Value, len(globals))
	for name, val := range globals {
		vm.Globals[name] = val
	}
	vm.Builtins = append([]value.Value(nil), builtins...)

	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(&KitWork{tenant: t, vm: vm, requestScope: requestScope})
	})
	if len(vm.Builtins) == 0 {
		vm.Builtins = []value.Value{kitworkFunc}
	} else {
		vm.Builtins[0] = kitworkFunc
	}
	vm.Globals[kitwork] = kitworkFunc
	if requestScope != nil {
		vm.Context = requestScope.Context()
	}
}

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
	tenant       *Tenant
	vm           *runtime.VM
	requestScope *requestscope.Scope
}

func (w *KitWork) Capability(name string) value.Value {
	if w == nil || w.tenant == nil {
		if val, ok := capabilities.DefaultRegistry.Get(name, nil); ok {
			return val
		}
		return value.Value{K: value.Nil}
	}
	factoryScope := capabilities.Scope(w.tenant)
	var requestCache *capabilities.InstanceCache
	if capabilities.DefaultRegistry.GetLifetime(name) == capabilities.LifetimeRequest && w.requestScope != nil {
		factoryScope = w.requestScope
		requestCache = w.requestScope.CapabilitiesCache()
	}
	accessScope := factoryScope
	if w.requestScope != nil {
		accessScope = w.requestScope
	}
	if val, ok := capabilities.DefaultRegistry.ResolveAuthorized(
		name,
		factoryScope,
		accessScope,
		w.tenant.AppCapabilitiesCache(),
		w.tenant.CapabilitiesCache(),
		requestCache,
	); ok {
		return val
	}
	return value.Value{K: value.Nil}
}

// Serve handles every request for this tenant. Kitwork is FILESYSTEM-ROUTED: after the built-in,
// always-on assets (the client hydrate runtime and the vendored fonts — identical bytes for every
// tenant), the request walks the folder tree (see tree_serve.go). There is no flat route table.
func (t *Tenant) Serve(w http.ResponseWriter, r *http.Request) {
	if !t.beginRequest() {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	defer t.endRequest()
	generationLease, err := t.generationLease()
	if err != nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	requestScope := requestscope.New(t, w, r)
	if generationLease != nil && !requestScope.AddCleanup(generationLease.Release) {
		generationLease.Release()
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	defer requestScope.Close()

	// /kit.js — the client runtime the render injects into every hydrated page.
	if serveHydrateIf(w, r) {
		return
	}
	// /jitfonts/* — vendored woff2 served straight off the embedded FS.
	if serveFontIf(w, r) {
		return
	}
	t.serveTree(requestScope)
}
