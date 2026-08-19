package work

// Request lifecycle for a filesystem-routed (tree) tenant.
//
//	Resolve domain → traverse folders (outside-in) → run each folder's guards + middleware →
//	resolve the HTTP method on the leaf → run its handler → resolve the view (page + nearest
//	index + slots, inside-out) → respond.
//
// Guards/middleware/handlers of different folders index DIFFERENT bytecodes (each folder's own
// router.kitwork.js), so the VM is FastReset per folder before its lambdas run — see execTree.

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func (t *Tenant) serveTree(requestScope *requestscope.Scope) {
	w := requestScope.Writer()
	r := requestScope.Request()
	tree := t.routeTree()
	if tree == nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	// Static assets win first: a real file on disk under the tenant (e.g. /assets/logo.png) is
	// streamed straight from disk (Zero-VM), never routed. Source files are never exposed.
	if t.serveTreeStatic(w, r) {
		return
	}

	match := tree.Resolve(r.URL.Path)

	reqRouter := &Router{
		tenant:         t,
		request:        r,
		responseWriter: w,
		params:         match.Params,
		response:       &Response{},
		Method:         r.Method,
		Path:           r.URL.Path,
		requestID:      requestID(r),
	}
	w.Header().Set("X-Request-ID", reqRouter.requestID)
	ctxObj := &Context{request: &Request{router: reqRouter}}

	// Read inherited metadata from the immutable generation graph:
	// router.meta() merged root→leaf is the base every page's $.meta starts from.
	chainMeta := map[string]value.Value{}
	chainJsonld := []value.Value{}
	for _, node := range match.Chain {
		if node.folder != nil {
			for k, v := range node.folder.meta {
				chainMeta[k] = v
			}
			chainJsonld = append(chainJsonld, node.folder.jsonld...)
		}
	}
	reqRouter.chainMeta = chainMeta
	reqRouter.chainJsonld = chainJsonld
	// Presentation declarations were frozen with the graph before activation.
	reqRouter.treeRender = t.treeRender(match.Node)

	// Semantic outputs are virtual root endpoints. A real filesystem node always wins; otherwise
	// router.rss()/router.sitemap() can answer their declared path without a synthetic folder.
	var generatedMethod *FolderMethod
	if !match.Found && (r.Method == http.MethodGet || r.Method == http.MethodHead) && tree.root.folder != nil {
		generatedMethod = tree.root.folder.generatedOutput(path.Clean("/" + r.URL.Path))
		if generatedMethod != nil {
			match.Found = true
			match.Node = tree.root
		}
	}

	// savMethod (set once a cacheable method resolves) makes finalize persist the response.
	var savMethod *FolderMethod
	savKey := cacheKey(r)

	// finalize renders any deferred view builder (ctx.view/bind/…), saves it if the method opted
	// into caching, then writes the response.
	finalize := func() {
		// Bridge the (request, response) => response.view({...}) / response.render() style: those set
		// a "render"/"view" response, which in tree mode must render against the RESOLVED folder (not
		// the flat render). Fold its data into a builder and render via treeRender.
		if reqRouter.viewBuilder == nil && reqRouter.treeRender != nil {
			if k := reqRouter.response.Kind(); k == "render" || k == "view" {
				vb := ctxObj.viewBuilder()
				if d := reqRouter.response.Data(); d.IsMap() {
					vb.apply(d)
				}
				code := reqRouter.response.Code()
				reqRouter.response = &Response{} // drop the "render" marker so the builder's HTML wins
				reqRouter.response.code = code   // but keep the status (e.g. response.status(404).view())
				vb.flush()
			}
		}
		if reqRouter.viewBuilder != nil && !reqRouter.response.IsSend() {
			reqRouter.viewBuilder.flush()
		}
		if savMethod != nil && reqRouter.err == nil {
			t.saveResponse(savMethod, savKey, reqRouter.response)
		}
		if reqRouter.response.Kind() == "sse" {
			// Streaming can remain open for hours. The SSE response retains only
			// Go-native broker state, so the request VM can return to the pool now.
			requestScope.ReleaseVM()
			reqRouter.streamSSE(w)
		} else {
			reqRouter.responder(w)
		}
	}

	// notFound renders the nearest notfound.kitwork.html (walking up from the deepest folder
	// reached) at HTTP 404 — the bubble the docs describe.
	notFound := func() {
		reqRouter.isNotfound = true
		ctxObj.View() // creates the deferred builder; finalize renders it
		finalize()
	}

	// A segment had no matching folder → 404.
	if !match.Found {
		notFound()
		return
	}

	// Folder-level rate limits (router.ratelimit), outside-in: ONE bucket per client per rule for
	// the folder's whole subtree — a root rule is a site-wide ceiling. Enforced before any guard
	// or handler work.
	for _, node := range match.Chain {
		if node.folder == nil {
			continue
		}
		for _, lim := range node.folder.limits {
			if !t.limiter.Allow(limitKey(lim, "folder:"+node.relPath(), r), lim.Rate, lim.Per) {
				w.Header().Set("Retry-After", fmt.Sprintf("%.0f", lim.Per.Seconds()))
				reqRouter.response.Text(value.New("Too Many Requests"), 429)
				finalize()
				return
			}
		}
	}

	vm, err := requestScope.LeaseVM(enginePool.Acquire, enginePool.Release)
	if err != nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	t.prepareExecutionVM(vm, t.vm.Globals, t.vm.Builtins, requestScope)
	vm.MaxEnergy = t.MaxEnergy

	// Folder before-chain, outside-in: each folder's guards run in order (guard subsumes middleware).
	for _, node := range match.Chain {
		fr := node.folder
		if fr == nil || fr.bytecode == nil {
			continue
		}
		for _, g := range fr.guards {
			if !t.runStage(vm, fr.bytecode, g, ctxObj, reqRouter, true) {
				finalize()
				return
			}
		}
	}

	leaf := match.Node.folder
	method := generatedMethod
	if method == nil {
		method = leaf.methods[r.Method]
	}

	// No explicit method for this verb: a GET on a folder that has a page renders it; anything
	// else is a not-found (method not allowed bubbles to the same 404 view for now).
	if method == nil {
		if r.Method == http.MethodGet && t.folderHasPage(match.Node) {
			ctxObj.View()
			finalize()
		} else {
			notFound()
		}
		return
	}

	// Method-level rate limits (.limit/.ratelimit) — before any handler work, keyed per URL path.
	for _, lim := range method.limits {
		if !t.limiter.Allow(limitKey(lim, "path:"+r.URL.Path, r), lim.Rate, lim.Per) {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", lim.Per.Seconds()))
			reqRouter.response.Text(value.New("Too Many Requests"), 429)
			finalize()
			return
		}
	}

	// Response cache (.cache RAM / .persist disk) — serve a hit with no VM, no render.
	if body, ct, status, headers, ok := t.cachedResponse(method, savKey); ok {
		if t.runtimeHealth != nil {
			t.runtimeHealth.RecordResponseCache(true)
		}
		serveCached(w, r, body, ct, status, headers)
		return
	}
	if method.cacheExpiry != nil || method.persistExpiry != nil {
		if t.runtimeHealth != nil {
			t.runtimeHealth.RecordResponseCache(false)
		}
		savMethod = method // finalize will save the fresh response
	}

	// Method-level guards run after all folder guards, in order.
	for _, g := range method.guards {
		if !t.runStage(vm, leaf.bytecode, g, ctxObj, reqRouter, true) {
			finalize()
			return
		}
	}

	// Handler.
	if reqRouter.err == nil && !reqRouter.response.IsSend() {
		switch {
		case method.isProxy:
			// Reverse proxy (router.proxy): a .cache()/.persist() hit already replayed above with no
			// VM — reaching here means a miss, so fetch the upstream now. A failure is the UPSTREAM's
			// fault, not ours: 502.
			if err := t.serveProxy(vm, leaf.bytecode, method, ctxObj); err != nil {
				reqRouter.err = err
				reqRouter.response.Text(value.New(err.Error()), http.StatusBadGateway)
			}
		case method.outputKind != "":
			if err := t.executeGeneratedOutput(vm, leaf.bytecode, method, ctxObj); err != nil {
				reqRouter.err = err
				reqRouter.response.Text(value.New(err.Error()), http.StatusInternalServerError)
			}
		case method.handle != nil:
			res := t.execTree(vm, leaf.bytecode, method.handle, ctxObj)
			if res.K == value.Invalid {
				t.recordRuntimeFailure(reqRouter, leaf.bytecode, "handler", res)
				reqRouter.err = fmt.Errorf("%s", res.Text())
			} else if reqRouter.viewBuilder == nil && !reqRouter.response.IsSend() && res.Truthy() {
				// A raw value (not a deferred view builder) → send it directly (JSON or HTML).
				if res.K == value.Map || res.K == value.Array {
					reqRouter.response.JSON(res)
				} else {
					reqRouter.response.HTML(res)
				}
			}
		case method.isView:
			ctxObj.View(method.viewArgs...)
		case t.folderHasPage(match.Node):
			ctxObj.View()
		default:
			notFound()
			return
		}
	}

	// Post-processing: error() on failure, else success(); finally() always.
	if reqRouter.err != nil {
		if method.errorH != nil {
			res := t.execTree(vm, leaf.bytecode, method.errorH, ctxObj)
			if res.K == value.Invalid {
				t.recordRuntimeFailure(reqRouter, leaf.bytecode, "error", res)
			}
			if res.K != value.Invalid && !reqRouter.response.IsSend() && res.Truthy() {
				if reqRouter.response.Code() == 0 {
					reqRouter.response.Status(500)
				}
				if res.K == value.Map || res.K == value.Array {
					reqRouter.response.JSON(res)
				} else {
					reqRouter.response.HTML(res)
				}
			}
		}
	} else if method.success != nil {
		res := t.execTree(vm, leaf.bytecode, method.success, ctxObj)
		if res.K == value.Invalid {
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "success", res)
			reqRouter.err = fmt.Errorf("%s", res.Text())
		}
	}
	if method.final != nil {
		res := t.execTree(vm, leaf.bytecode, method.final, ctxObj)
		if res.K == value.Invalid {
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "finally", res)
			if reqRouter.err == nil {
				reqRouter.err = fmt.Errorf("%s", res.Text())
			}
		}
	}

	finalize()
}

// limitClient resolves the client half of a .limit() bucket key for a rule dimension.
func limitClient(dim string, r *http.Request) string {
	switch dim {
	case "user":
		if account, _ := GetClientUserAccount(r); account != "" {
			return account
		}
		return GetClientIP(r) // anonymous: fall back to the IP so the rule still bites
	case "browser":
		return GetClientBrowserFingerprint(r)
	case "global":
		return "" // one shared bucket — no client component
	default: // "ip"
		return GetClientIP(r)
	}
}

// limitKey builds a rule's full bucket key: dimension + client + scope (folder subtree or URL
// path) + the rule's own rate/window. Including rate+per keeps STACKED rules on the same
// dimension (burst 30/1s + sustained 600/1m) in separate buckets — sharing one would let the
// first-created window swallow both rules.
func limitKey(lim methodLimit, scope string, r *http.Request) string {
	return lim.Dim + "|" + limitClient(lim.Dim, r) + "|" + scope + "|" + strconv.Itoa(lim.Rate) + "/" + lim.Per.String()
}

// execTree loads the folder's bytecode into the VM (its lambdas index THAT bytecode), then runs
// one lambda with the usual (ctx/request/response) argument binding.
func (t *Tenant) execTree(vm *runtime.VM, bc *compiler.Bytecode, l *value.Lambda, ctxObj *Context) value.Value {
	if l == nil || bc == nil {
		return value.Value{K: value.Nil}
	}
	vm.FastResetPrepared(bc.Program)
	started := time.Now()
	result := vm.ExecuteLambda(l, ctxObj.arguments(l))
	t.recordVMExecution(bc.Program, vm, result, time.Since(started))
	return result
}

// runStage runs one guard/middleware lambda and reports whether the pipeline may continue.
// A guard that returns data auto-sends it (like the flat lifecycle); middleware does not.
func (t *Tenant) runStage(vm *runtime.VM, bc *compiler.Bytecode, l *value.Lambda, ctxObj *Context, r *Router, isGuard bool) bool {
	res := t.execTree(vm, bc, l, ctxObj)
	if res.K == value.Invalid {
		stage := "middleware"
		if isGuard {
			stage = "guard"
			r.err = fmt.Errorf("guard error: %v", res.V)
		} else {
			r.err = fmt.Errorf("middleware error: %v", res.V)
		}
		t.recordRuntimeFailure(r, bc, stage, res)
		return false
	}
	if r.response.IsSend() {
		return false // stage sent its own response (ctx.json / redirect / …)
	}
	if res.IsBool() && !res.Truthy() {
		r.err = fmt.Errorf("request rejected")
		return false
	}
	if isGuard && !res.IsBlank() && !res.IsBool() {
		r.response.Send(res) // guard returned data → auto response
		return false
	}
	return r.err == nil
}

// treeRender points the render engine at a folder: page = <folder>/page.kitwork.html, while the
// nearest index.kitwork.html and each slot resolve by walking UP from the folder to the root —
// exactly the inside-out view resolution the docs describe, reusing the existing render engine.
func (t *Tenant) treeRender(n *RouteNode) *plannedRenderer {
	if n == nil || t.renderPlan() == nil {
		return nil
	}
	return t.renderPlan().renderer(n.relPath())
}

func (t *Tenant) folderHasPage(n *RouteNode) bool {
	return n != nil && t.renderPlan() != nil && t.renderPlan().hasPage(n.relPath())
}

// serveTreeStatic streams a real on-disk file under the tenant (assets, images, …) with no VM.
// It refuses path traversal, dot segments (protects .env, .persist/) and any *.kitwork.* source so
// templates/routers are never exposed. When the root router declared .assets(), only those
// prefixes are served (allowlist); /favicon.ico honors .favicon(). Returns true if it served.
func (t *Tenant) serveTreeStatic(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	if clean == "/" || strings.Contains(clean, "..") {
		return false
	}
	if strings.Contains(strings.ToLower(clean), extension+".") { // never expose *.kitwork.* sources
		return false
	}
	for _, seg := range strings.Split(clean, "/") { // any dot SEGMENT: .env, .persist/…, .git/…
		if strings.HasPrefix(seg, ".") {
			return false
		}
	}

	// Presentation declarations were collected before generation activation.
	presentation := t.presentation().View()

	// /favicon.ico: browsers request it unprompted; .favicon() names the file that answers.
	if clean == "/favicon.ico" && presentation.FaviconFile != "" {
		http.ServeFile(w, r, presentation.FaviconFile)
		return true
	}

	// Allowlist + alias mapping: once .assets() declared anything, only those URL prefixes are public,
	// and each maps to its (possibly different, e.g. private "_assets/") disk dir. Zero-config (no
	// mounts) leaves diskClean == clean so any safe file under the tenant auto-serves.
	diskClean := clean
	if len(presentation.AssetMounts) > 0 {
		matched := false
		for _, m := range presentation.AssetMounts {
			up := "/" + m.URL
			if clean == up || strings.HasPrefix(clean, up+"/") {
				diskClean = "/" + m.Disk + strings.TrimPrefix(clean, up)
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	base := t.resolve()
	full := filepath.Join(base, filepath.FromSlash(diskClean))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return false
	}
	// Most dynamic routes map to a route directory or to no disk entry. Only pay
	// for symlink-aware containment when there is a file we might actually serve.
	if !t.insideSiteRoot(full) {
		return false
	}

	http.ServeFile(w, r, full)
	return true
}
