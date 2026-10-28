package work

import (
	"fmt"
	"net/http"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/database/statement"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

var enginePool = app.NewPool()

// VMPoolHealth returns counters only. It never exposes or retains a pooled VM.
func VMPoolHealth() VMPoolHealthSnapshot {
	stats := enginePool.Stats()
	return VMPoolHealthSnapshot{
		Active:   stats.Active,
		Created:  stats.Created,
		Acquired: stats.Acquired,
		Released: stats.Released,
	}
}

// Router struct is defined in router.go
type Config struct {
	root     string
	base     string
	multiple bool
	layout   RootLayout
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

	vm.PrepareHostState(globals, builtins)

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

// Safe is the global safe, which the "kitwork" module exports as well (14/10/2026): import { safe }
// from "kitwork", the habit of import { log }, is const { safe } = kitwork(), and declares safe as this
// same function. Without it the import gave null and every call of safe was a NOT_A_FUNCTION error.
func (w *KitWork) Safe() value.Value { return value.NewSafeFunction() }

// KitWork is the per-tenant capability surface returned by kitwork() in the VM.
// Every capability is a METHOD on *KitWork, so they must all live in package work
// (Go requires methods in the type's package) — that's why this package is large.
// Capability → file map:
//
//	router.go    Router()        log.go       Log()         db.go        Database()
//	http.go      HTTP()          jwt.go       JWT()         render.go    Render()
//	qrcode.go    Qrcode()        napas.go     Napas()       file.go      File()
//	collection.go Collection()   (directory-backed Markdown content)
//	browser.go   Browser()       chromedp.go  Chromedp()/Screenshot()   routine.go  Routine()
//	env.go       Env()           (per-tenant, path-isolated env)
type KitWork struct {
	tenant       *Tenant
	vm           *runtime.VM
	requestScope *requestscope.Scope
	// queueJobs collects the jobs a file of .shared/queue/ declares while the loader runs it (queue_load.go);
	// scheduleDeclarations the schedules a file of .schedule/ declares (schedule_load.go).
	queueJobs            *queueDeclarations
	scheduleDeclarations *scheduleDeclarations
	// folderSettings collects the calls the config.work of .shared/queue/ or of .schedule/ makes while the
	// loader runs it (background_config.go): queue.parallel(4), schedule.keep("7d").
	folderSettings *folderSettings
}

// MissingMember is a word the module kitwork has not. An older word of it (sqlite, removed on 22/10/2026;
// cron, 22/10/2026; go, 22/10/2026, now routine.go of kitwork/routine; cache and sse, 22/10/2026, now
// kitwork/storage and context.sse) is the error that says what the spec writes instead; the compiler
// refuses its import in the same words. Any other word is nothing, as it was.
func (w *KitWork) MissingMember(name string) value.Value {
	if spelling, removed := statement.RemovedKitworkWord(name); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`%s của "kitwork" đã bỏ: %s`, name, spelling))
	}
	switch name {
	case "cron":
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.CronRemoved)
	case "go":
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.RoutineGoRemoved)
	case "cache":
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.CacheRemoved)
	case "sse":
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.SSERemoved)
	case "env":
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.EnvironmentRenamed)
	}
	return value.Value{K: value.Nil}
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

	// Everything the engine serves for itself lives under /_kitwork/ (kitassets.go): the page
	// JavaScript, one file per part, and the vendored woff2 (/_kitwork/fonts/…, straight off the
	// embedded FS). It is answered here, before the folder tree, so nothing under it is ever a
	// tenant's file; the names used before (/kit/…, /jitfonts/…) still answer. The one-response
	// /kit.js went on 03/10 — no page asked for it any more, and it carried none of the directive files.
	if t.serveEngineIf(w, r) {
		return
	}
	// /_db/query — authenticated read-only SQL over HTTP. Off unless a database declaration exposes it.
	if t.serveDataAPIIf(w, r, requestScope) {
		return
	}
	// /v2/pipeline, /v3/pipeline — libSQL/Hrana wire protocol for URL + token database clients.
	if t.serveLibSQLIf(w, r, requestScope) {
		return
	}
	t.serveTree(requestScope)
}
