package work

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	httphelper "github.com/kitwork/engine/utilities/http"
	"github.com/kitwork/engine/value"
)

// router.proxy(target) — answer a folder from an UPSTREAM url instead of a page (a reverse proxy
// mounted at the folder). Declared in tree_folder.go; executed here.
//
// The design in one line: proxy is a THIN wrapper over the pieces that already exist — the
// SSRF-guarded outbound client (helpers/http) for the fetch, and the route's own .cache()/.persist()
// tiers for the storage. Nothing new is invented:
//
//	router.proxy("https://cdn.example.com/logo.png").persist("30d")   // fixed upstream
//	router.proxy((ctx) => "https://cdn/" + ctx.params("id")).persist("30d")  // computed per request
//
// LIFECYCLE — the "cached mount":
//   - MISS: resolve the target (VM only if it is a handler) → fetch → reply with the upstream bytes
//     under the upstream's own Content-Type → the route tier stores body+type (tree_cache.go).
//   - HIT:  cachedResponse() replays the stored bytes BEFORE any of this runs — no VM, no refetch.
//
// This generalises past images: it is a reverse-proxy-with-cache (BFF / gateway) that happens to be
// perfect for a remote image mount. A future .resize() would layer on top of the same bytes.

// proxyMaxBytes caps the upstream body a proxy reads into memory: the host's 64 MiB, past which the
// answer is TOO_LARGE (a 502). A variable so a test reaches the cap without 64 MiB.
var proxyMaxBytes = httphelper.HostMaxBytes

// serveProxy answers one proxy request. Errors are returned so the caller can map them to 502.
func (t *Tenant) serveProxy(vm *runtime.VM, bc *compiler.Bytecode, method *FolderMethod, ctxObj *Context) error {
	// 1. Resolve the upstream. A handler computes it per request (params/path); otherwise it is a
	//    fixed URL declared in the router — either way the TENANT owns it, never the visitor.
	target := method.proxyTarget
	if target.K == value.Func {
		target = t.execTree(vm, bc, lambdaOf(target), ctxObj)
		if target.K == value.Invalid {
			return &stageFailure{text: fmt.Sprintf("proxy target handler failed: %v", target.V), failure: target, run: true}
		}
	}
	url := strings.TrimSpace(target.Text())
	if url == "" {
		return fmt.Errorf("proxy: target resolved to an empty url")
	}

	// 2. Fetch. No cache tiers are wired into the request on purpose: the ROUTE's .cache()/.persist()
	//    is the tier the author declared, and storing in both would keep the same bytes twice. The
	//    transport still blocks private/loopback space (SSRF backstop) regardless of the target. The
	//    request ends with the visitor's: a closed connection cancels it.
	request, err := httphelper.NewRequest("GET", url, proxyMaxBytes)
	if err != nil {
		return &stageFailure{text: fmt.Sprintf("proxy %s: %v", url, err), failure: value.InvalidFailure(value.InvalidArgumentCode, "proxy: "+err.Error())}
	}
	var visitorContext context.Context
	if visitor := ctxObj.Request(); visitor != nil && visitor.request() != nil {
		visitorContext = visitor.request().Context()
	}
	answer := request.SendGo(visitorContext)
	if answer.Status == 0 || answer.Code != "" {
		// the log keeps the URL whole; a .error() handler reads the request's failure as a program
		// reads one: its code (NETWORK, TIMEOUT, DENIED, TOO_LARGE…), a message with no query string
		return &stageFailure{text: fmt.Sprintf("proxy %s: %s", url, answer.Message), failure: value.InvalidFailure(answer.Code, "proxy: "+answer.Message)}
	}

	// 3. Replay the upstream bytes under the upstream's OWN media type — an image must not come back
	//    as octet-stream. "typed" is the response kind that carries an arbitrary Content-Type, and it
	//    is exactly what tree_cache stores/serves, so a cached hit is byte-identical. When the upstream
	//    sent none, the bytes are sniffed (stdlib, no dependency).
	contentType := strings.TrimSpace(answer.Header.Get("Content-Type"))
	if contentType == "" && len(answer.Body) > 0 {
		contentType = http.DetectContentType(answer.Body)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctxObj.Response().setType(contentType)
	ctxObj.Response().Return(value.New(answer.Body), "typed", answer.Status)
	return nil
}
