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
	httputil "github.com/kitwork/engine/utilities/http"
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
		numberParams:   match.Numbers,
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
			if node.folder.transition != "" {
				reqRouter.transition = node.folder.transition
			}
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

	reqRouter.node = match.Node
	reqRouter.requestScope = requestScope

	// savMethod (set once a cacheable method resolves) makes finalize persist the response.
	var savMethod *FolderMethod
	var savKey string
	var savCookies int // the cookies the guards set before the handler: not the answer's

	// finalize renders any deferred view builder (ctx.view/bind/…), saves it if the method opted
	// into caching, then writes the response.
	finalize := func() {
		// notFound() or error(…) of a Kitwork UI handler: the nearest notfound.ui or error.ui
		t.answerUIPage(reqRouter, reqRouter.node)
		// Bridge the (request, response) => response.view({...}) / response.render() style: those set
		// a "render"/"view" response, which in tree mode must render against the RESOLVED folder (not
		// the flat render). Fold its data into a builder and render via treeRender.
		// A builder may be there already: the page's text written before response.view({…}) went into
		// it. The "render" marker is dropped in place, so the response keeps its status, its headers and
		// cookies (until 19/10/2026 a fresh response lost all but the status).
		if reqRouter.treeRender != nil {
			if k := reqRouter.response.Kind(); k == "render" || k == "view" {
				vb := ctxObj.viewBuilder()
				if d := reqRouter.response.Data(); d.IsMap() {
					vb.apply(d)
				}
				reqRouter.response.data = value.Value{K: value.Nil}
				reqRouter.response.kind = "" // the builder's HTML is the answer
				vb.flush()
			}
		}
		if reqRouter.viewBuilder != nil && !reqRouter.response.IsSend() {
			reqRouter.viewBuilder.flush()
		}
		if savMethod != nil && reqRouter.err == nil {
			t.saveResponse(savMethod, savKey, reqRouter.response, savCookies)
		}
		if reqRouter.response.Kind() == "sse" && reqRouter.err == nil {
			// the handler returned its stream: the request stays open as the stream, holding its VM and
			// its generation, until it closes (stream.go)
			t.serveStream(w, reqRouter)
			return
		}
		if reqRouter.response.Kind() == "sse" {
			// the request failed after context.sse was read: the failure is the answer, not the stream
			reqRouter.response.data = value.Value{K: value.Nil}
			reqRouter.response.kind = ""
		}
		reqRouter.responder(w)
	}

	// notFound renders the nearest notfound.kitwork.html (walking up from the deepest folder
	// reached) at HTTP 404 — the bubble the docs describe.
	notFound := func() {
		reqRouter.isNotfound = true
		if t.hasUINotFound(match.Node) { // a Kitwork UI site: the nearest notfound.ui
			t.respondUI(reqRouter, match.Node, nil)
			finalize()
			return
		}
		ctxObj.View() // creates the deferred builder; finalize renders it
		finalize()
	}

	// A segment had no matching folder → 404.
	if !match.Found {
		notFound()
		return
	}

	// A Kitwork UI route takes a request that may change something only from its own site's pages or
	// from no browser at all (request_origin.go): refused before any limit, guard or handler. A
	// template site is not checked.
	ui := uiRoute(match.Node)
	if ui {
		if err := crossOriginRefusal(r); err != nil {
			reqRouter.fail(err, http.StatusForbidden)
			finalize()
			return
		}
	}

	// Folder-level rate limits (router.limit; router.ratelimit of a template), outside-in: ONE bucket per
	// client per rule for the folder's whole subtree — a root rule is a site-wide ceiling, a branch's adds
	// to it. Enforced before any guard or handler work.
	for _, node := range match.Chain {
		if node.folder == nil {
			continue
		}
		if t.overLimit(w, reqRouter, node.folder.limits, "folder:"+node.relPath()) {
			finalize()
			return
		}
	}
	// A Kitwork UI route's own .limit() counts with the folders', before any guard (KITWORK_ROUTER.md mục 4,
	// 12): a request over it never reaches a guard's work. A template's keeps its place after the folder
	// guards.
	if ui {
		if route := routeMethod(match.Node.folder, generatedMethod, r.Method); route != nil &&
			t.overLimit(w, reqRouter, route.limits, "path:"+normalizedPath(r.URL.Path)) {
			finalize()
			return
		}
	}

	vm, err := requestScope.LeaseVM(enginePool.Acquire, enginePool.Release)
	if err != nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	// observe() binds its observer to the request the VM runs (stream.go)
	requestVMs.Store(vm, reqRouter)
	defer requestVMs.Delete(vm)
	t.prepareExecutionVM(vm, t.vm.Globals, t.vm.Builtins, requestScope)
	vm.MaxEnergy = t.MaxEnergy

	// Folder before-chain, outside-in: each folder's guards run in order (guard subsumes middleware). In a
	// Kitwork UI folder config.work's come first, then router.work's; each runs with the bytecode of the
	// file that wrote it (code), so a folder with a config.work and no router.work guards its subtree too.
	for _, node := range match.Chain {
		fr := node.folder
		if fr == nil {
			continue
		}
		for _, g := range fr.guards {
			if !t.runStage(vm, fr, g, ctxObj, reqRouter, true) {
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

	// A Kitwork UI site (KITWORK_ROUTER §2, 20/10/2026): HEAD runs as GET and drops the body; a method
	// the folder did not declare is 405 with Allow, OPTIONS is Allow. A template site keeps its 404s.
	asGet := r.Method == http.MethodGet
	if ui && r.Method == http.MethodHead {
		w = headOnly{w}
		reqRouter.responseWriter = w
		asGet = true
		if method == nil {
			method = leaf.methods[http.MethodGet]
		}
	}
	if ui && method == nil && !asGet {
		if allow := t.allowedMethods(match.Node, leaf); allow != "" {
			reqRouter.response.setHeader("Allow", allow)
			if r.Method == http.MethodOptions {
				reqRouter.response.Return(value.New(""), "text", http.StatusNoContent)
			} else {
				reqRouter.response.Return(value.New("Method Not Allowed"), "text", http.StatusMethodNotAllowed)
			}
			finalize()
			return
		}
	}

	// No explicit method for this verb: a GET on a folder that has a page renders it; anything
	// else is a not-found.
	if method == nil {
		if asGet && t.folderHasPage(match.Node) {
			ctxObj.View()
			finalize()
		} else if asGet && t.folderHasUIPage(match.Node) {
			t.respondUI(reqRouter, match.Node, nil) // a content.ui with no router.get: a page with no data
			finalize()
		} else {
			notFound()
		}
		return
	}

	// Method-level rate limits of a template (.limit/.ratelimit) — before any handler work, keyed per URL
	// path. A Kitwork UI route's ran before the guards.
	if !ui && t.overLimit(w, reqRouter, method.limits, "path:"+normalizedPath(r.URL.Path)) {
		finalize()
		return
	}

	// Method-level guards run after all folder guards, in order — and before the response cache: a
	// hit replays the bytes a request the guard let through got, so reading it first let any request
	// past the guard (19/10/2026). A guard that answers or blocks is not cached (savMethod unset yet).
	for _, g := range method.guards {
		if !t.runStage(vm, leaf, g, ctxObj, reqRouter, true) {
			finalize()
			return
		}
	}

	// Response cache (.cache RAM / .persist disk), on the route only (20/10/2026: .cache() on an answer
	// is gone). Only a GET (and its HEAD) reads or writes it. Invalid or disallowed projected query
	// values execute normally but never read or write a cache entry.
	if (method.cacheExpiry != nil || method.persistExpiry != nil) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		key, cacheable := cacheKey(r, method)
		if cacheable {
			savKey = key
			if body, ct, status, headers, ok := t.cachedResponse(method, savKey); ok {
				if t.runtimeHealth != nil {
					t.runtimeHealth.RecordResponseCache(true)
				}
				serveCached(w, r, reqRouter.response, body, ct, status, headers)
				return
			}
			savMethod = method // finalize will save the fresh response
			savCookies = len(reqRouter.response.cookies)
		}
		if t.runtimeHealth != nil {
			t.runtimeHealth.RecordResponseCache(false)
		}
	}

	// Handler.
	if reqRouter.err == nil && !reqRouter.response.IsSend() {
		switch {
		case method.isProxy:
			// Reverse proxy (router.proxy): a .cache()/.persist() hit already replayed above with no
			// VM — reaching here means a miss, so fetch the upstream now. A failure is the UPSTREAM's
			// fault, not ours: 502.
			if err := t.serveProxy(vm, leaf, method, ctxObj); err != nil {
				t.recordStageFailure(reqRouter, leaf.bytecode, "proxy", err)
				reqRouter.fail(err, http.StatusBadGateway)
			}
		case method.outputKind != "":
			if err := t.executeGeneratedOutput(vm, leaf, method, ctxObj); err != nil {
				t.recordStageFailure(reqRouter, leaf.bytecode, "output", err)
				reqRouter.fail(err, http.StatusInternalServerError)
			}
		case method.handle != nil:
			reqRouter.method, reqRouter.handling = method, true
			res := t.execTree(vm, leaf, method.handle, ctxObj)
			reqRouter.handling = false
			if stream := reqRouter.openedStream(); stream != nil && res.K != value.Invalid && !stream.is(res) {
				// context.sse chose the answer: the handler returns the stream (KITWORK_SSE.md mục 1)
				res = value.InvalidFailure(value.InvalidArgumentCode, "route này đã mở luồng SSE (context.sse); trả về stream: return stream, hay return context.sse.subscribe(…) (KITWORK_SSE.md mục 1)")
			}
			if res.K == value.Invalid {
				t.recordRuntimeFailure(reqRouter, leaf.bytecode, "handler", res)
				reqRouter.err = uncaught(res.Text())
				if uiRoute(match.Node) {
					// router.work: an answer the handler began before it failed is not the answer (an
					// edit after it, a second one) — the request fails (20/10/2026). A template site
					// keeps what it had.
					reqRouter.response.data = value.Value{K: value.Nil}
					reqRouter.response.kind = ""
					reqRouter.viewBuilder = nil
				}
			} else if uiRoute(match.Node) && reqRouter.response.Kind() == "render" {
				// router.work: the handler answered with context.view({ … }): the folder's .ui page
				t.serveUIAnswer(vm, ctxObj, reqRouter, match.Node, method.handle)
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

	// Post-processing: error() on failure, else success(); finally() always. The method's own .error()
	// answers its failure; without one the nearest folder's router.error() does, leaf to root, as the
	// docs say (14/10/2026: a folder's was kept and never run).
	if reqRouter.err != nil {
		errorH, errorFolder := method.errorH, leaf
		for index := len(match.Chain) - 1; errorH == nil && index >= 0; index-- {
			if folder := match.Chain[index].folder; folder != nil && folder.errorH != nil && folder.bytecode != nil {
				errorH, errorFolder = folder.errorH, folder
			}
		}
		if errorH != nil {
			res := t.execTree(vm, errorFolder, errorH, ctxObj)
			if res.K == value.Invalid {
				t.recordRuntimeFailure(reqRouter, errorFolder.bytecode, "error", res)
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
		res := t.execTree(vm, leaf, method.success, ctxObj)
		if res.K == value.Invalid {
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "success", res)
			reqRouter.err = uncaught(res.Text())
		}
	}
	if method.final != nil {
		res := t.execTree(vm, leaf, method.final, ctxObj)
		if res.K == value.Invalid {
			t.recordRuntimeFailure(reqRouter, leaf.bytecode, "finally", res)
			if reqRouter.err == nil {
				reqRouter.err = uncaught(res.Text())
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

// overLimit counts the request against each rule of limits in scope (a folder's subtree, a route's path) and
// answers 429 with Retry-After at the first it is over, reporting whether it did.
func (t *Tenant) overLimit(w http.ResponseWriter, reqRouter *Router, limits []methodLimit, scope string) bool {
	for _, lim := range limits {
		if !t.limiter.Allow(limitKey(lim, scope, reqRouter.request), lim.Rate, lim.Per) {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", lim.Per.Seconds()))
			reqRouter.response.Text(value.New("Too Many Requests"), 429)
			return true
		}
	}
	return false
}

// routeMethod is the route a request reaches in its folder before anything runs: a built-in door's, the
// folder's method, or for HEAD the GET it runs as (a Kitwork UI site's, KITWORK_ROUTER.md mục 2).
func routeMethod(leaf *FolderRouter, generated *FolderMethod, method string) *FolderMethod {
	if generated != nil {
		return generated
	}
	if leaf == nil {
		return nil
	}
	if route := leaf.methods[method]; route != nil {
		return route
	}
	if method == http.MethodHead {
		return leaf.methods[http.MethodGet]
	}
	return nil
}

// code is the bytecode a function of the folder belongs to, the one it runs with: config.work's for what
// config.work handed the router (router.guard), the router file's for everything else.
func (f *FolderRouter) code(l *value.Lambda) *compiler.Bytecode {
	if f == nil {
		return nil
	}
	if f.configBytecode != nil && l != nil && l.Program != nil {
		if program, ok := runtime.ProgramFromRef(l.Program); ok && program == f.configBytecode.Program {
			return f.configBytecode
		}
	}
	return f.bytecode
}

// execTree loads the folder's bytecode into the VM (its lambdas index THAT bytecode), then runs
// one lambda of it with the arguments its folder's router file hands: by position for a router.work's,
// by name for a router.kitwork.js's — the folder that wrote the function, not the request's leaf, so a
// guard of a router.work over a template folder still takes its context (23/10/2026).
func (t *Tenant) execTree(vm *runtime.VM, folder *FolderRouter, l *value.Lambda, ctxObj *Context) value.Value {
	bc := folder.code(l)
	if l == nil || bc == nil {
		return value.Value{K: value.Nil}
	}
	vm.FastResetPrepared(bc.Program)
	started := time.Now()
	var result value.Value
	// the site's program runs: in router.work the order of its response is checked (response_order.go)
	ctxObj.Response().runProgram(func() { result = vm.ExecuteLambda(l, ctxObj.arguments(l, folder.ui)) })
	t.recordVMExecution(bc.Program, vm, result, time.Since(started))
	if response, ok := result.V.(*Response); ok && response == ctxObj.Response() {
		// `return response.json(data)`: the answer is already on the request's response; returning it
		// says no more (19/10/2026), it is not data to send in its place
		return value.Value{K: value.Nil}
	}
	if unsent, ok := httputil.UnsentFailure(result); ok {
		// `return http.get(url)` sent the request at the return until 22/10/2026; now it would answer
		// 200 with nothing, so it fails naming .send() (KITWORK_HTTP.md)
		return unsent
	}
	return result
}

// runStage runs one guard/middleware lambda and reports whether the pipeline may continue.
// A guard that returns data auto-sends it (like the flat lifecycle); middleware does not.
func (t *Tenant) runStage(vm *runtime.VM, folder *FolderRouter, l *value.Lambda, ctxObj *Context, r *Router, isGuard bool) bool {
	bc := folder.code(l)
	res := t.execTree(vm, folder, l, ctxObj)
	if res.K == value.Invalid {
		stage := "middleware"
		if isGuard {
			stage = "guard"
			r.err = uncaught(fmt.Sprintf("guard error: %v", res.V))
		} else {
			r.err = uncaught(fmt.Sprintf("middleware error: %v", res.V))
		}
		t.recordRuntimeFailure(r, bc, stage, res)
		return false
	}
	if r.response.IsSend() {
		return false // stage sent its own response (ctx.json / redirect / …)
	}
	if res.IsBool() && !res.Truthy() {
		// a guard that says no is 403 (KITWORK_ROUTER §14, 20/10/2026; the error answered 500)
		r.err = errRequestRejected
		if r.response.code == 0 {
			r.response.code = http.StatusForbidden
		}
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

// isUISource reports whether a URL path names a Kitwork UI source file: *.ui (interface) or *.work
// (logic). Their names carry no ".kitwork.", so the *.kitwork.* rule alone would serve them.
func isUISource(urlPath string) bool {
	switch strings.ToLower(path.Ext(urlPath)) {
	case ".ui", ".work":
		return true
	}
	return false
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
	if isUISource(clean) { // never expose Kitwork UI sources: *.ui (interface), *.work (logic)
		return false
	}
	// Presentation declarations were collected before generation activation.
	presentation := t.presentation().View()

	ui := t.uiSite()
	if ui && !uiStaticAllowed(clean) && !assetMounted(presentation.AssetMounts, clean) {
		// a Kitwork UI site serves only its allowlist (KITWORK_UI §5): assets/, the catalog of root
		// files, .well-known/, and what router.assets(…) adds; no other file under the app, whatever its name
		return false
	}
	for i, seg := range strings.Split(clean, "/") { // any dot SEGMENT: .env, .persist/…, .git/…
		if ui && i == 1 && seg == ".well-known" {
			continue // the one dot folder a site serves: /.well-known/… (ACME, security.txt, app links)
		}
		if strings.HasPrefix(seg, ".") {
			return false
		}
	}

	// /favicon.ico: browsers request it unprompted; .favicon() names the file that answers.
	if clean == "/favicon.ico" && presentation.FaviconFile != "" {
		if info, err := os.Stat(presentation.FaviconFile); err == nil && !info.IsDir() {
			staticCacheHeaders(w, clean, presentation.FaviconFile, info)
		}
		http.ServeFile(w, r, presentation.FaviconFile)
		return true
	}

	// Allowlist + alias mapping: once .assets() declared anything, only those URL prefixes are public,
	// and each maps to its (possibly different, e.g. private "_assets/") disk dir. Zero-config (no
	// mounts) leaves diskClean == clean so any safe file under the tenant auto-serves. A Kitwork UI
	// site's router.assets(…) only adds to its allowlist (KITWORK_ROUTER.md mục 4, 24/10/2026): its
	// assets/ is served at /assets/… whatever it declares.
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
		if !matched && !ui {
			return false
		}
	}
	if ui && diskClean != clean && !uiDiskServed(diskClean) {
		return false // a mount never publishes a dot file or a source of the site under another name
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

	staticCacheHeaders(w, clean, full, info) // Cache-Control and ETag; ServeFile answers 304 by them
	http.ServeFile(w, r, full)
	return true
}
