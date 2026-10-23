package work

// notFound() and error(…) in a Kitwork UI site (router.work) answer with a page (20/10/2026): the
// nearest notfound.ui with 404, the nearest error.ui with 500, found from the route's folder up to the
// site root, drawn by respondUI, the way a path no folder matches draws notfound.ui. Without such a
// page, notFound() keeps its text and error(…) answers its text with 500, never 200. A template site
// (router.kitwork.js) keeps its own handling. The answer follows the cache rules every answer follows:
// kept only when it called .cache(), after the guards.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// nearestFolder is the nearest folder with a router file from node up: the one whose file holds the
// handler running.
func nearestFolder(node *RouteNode) *FolderRouter {
	for n := node; n != nil; n = n.parent {
		if n.folder != nil && n.folder.bytecode != nil {
			return n.folder
		}
	}
	return nil
}

// uiRoute reports whether the route is a Kitwork UI site's: its router file is router.work.
func uiRoute(node *RouteNode) bool {
	folder := nearestFolder(node)
	return folder != nil && folder.ui
}

// errorMessage is what error(…) gives the page to read as message: a text as it is, the message of an
// error object (safe(() => …) gives { message, code, at }), never its place or anything else of it.
func errorMessage(v value.Value) string {
	if v.IsMap() {
		if message, ok := v.Map()["message"]; ok && message.IsString() {
			return message.Text()
		}
		return ""
	}
	if v.IsString() {
		return v.Text()
	}
	return ""
}

// errorText is the text of an error(…) with no error.ui to draw it.
func errorText(message string) string {
	if strings.TrimSpace(message) == "" {
		return http.StatusText(http.StatusInternalServerError)
	}
	return message
}

// answerUIPage draws the page a handler's notFound() or error(…) asked for, at the end of the request,
// before it is kept or written. Nothing when the request is not a Kitwork UI site's, when a page
// answered already (respondUI), or when the handler went on to answer otherwise (a .error() hook that
// answered).
func (t *Tenant) answerUIPage(reqRouter *Router, node *RouteNode) {
	if reqRouter.uiAnswered || isUncaught(reqRouter.err) || !uiRoute(node) {
		return
	}
	response := reqRouter.response
	if kind := response.Kind(); kind != "" && kind != "error" {
		return
	}
	switch {
	case reqRouter.isNotfound || response.uiPage == uiNotFoundPage:
		if !t.hasUIPage(node, uiNotFoundPage) {
			return // no notfound.ui: the text it answers today
		}
		reqRouter.isNotfound = true
	case response.uiPage == uiErrorPage:
		if !t.hasUIPage(node, uiErrorPage) {
			if response.Kind() == "error" && (response.code < 400 || response.code > 599) {
				response.code = http.StatusInternalServerError // response.error("…") was 200
			}
			return
		}
	default:
		return
	}
	// the page is the answer: the marker of the text answer goes, the status, headers, cookies and
	// .cache() of the response stay
	response.data = value.Value{K: value.Nil}
	response.kind = ""
	t.respondUI(reqRouter, node, nil)
}

// serveUIAnswer draws the folder's .ui page for a handler that answered with context.view({ … }) or
// response.view({ … }) in router.work (20/10/2026): content.ui inside the nearest view.ui, the frame and
// its regions with it, all reading the names of the object the view was given — only those (23/10/2026:
// router.view, which fed the page every name of its function and its file, is gone) — and the page's text
// written before it in its head. The folder with no page of its own is an error that says so. Called right
// after the handler, on the VM it ran on: the functions in the object run there when the page calls them.
func (t *Tenant) serveUIAnswer(vm *runtime.VM, ctxObj *Context, reqRouter *Router, node *RouteNode, handler *value.Lambda) {
	response := reqRouter.response
	data := response.Data()
	response.data = value.Value{K: value.Nil}
	response.kind = ""
	if !t.folderHasUIPage(node) {
		relative := "/" + strings.Trim(node.relPath(), "/")
		err := uncaught(fmt.Sprintf("context.view(…) vẽ trang .ui của thư mục, mà %s không có content.ui: thêm content.ui vào thư mục này, hoặc trả lời bằng context.json(…)", relative))
		t.recordFailure(reqRouter, "view", err)
		reqRouter.fail(err, http.StatusInternalServerError)
		return
	}
	scope, converter, err := viewData(vm, handler, data)
	if err != nil {
		t.recordFailure(reqRouter, "view", err)
		reqRouter.fail(err, http.StatusInternalServerError)
		return
	}
	t.respondUI(reqRouter, node, scope)
	if failure := converter.fatal; failure.K == value.Invalid && failure.IsError {
		// a function of the page that hit what nothing catches (energy, cancellation) ends the request,
		// whatever the page wrote
		t.recordRuntimeFailure(reqRouter, nearestFolder(node).bytecode, "view", failure)
		reqRouter.fail(uncaught(failure.Text()), http.StatusInternalServerError)
	}
}

// viewData is what the page reads: the names of the object context.view({ … }) was given, each turned into
// the plain value the ui package evaluates, with the converter that made them (its functions run on vm when
// the page calls them, and its fatal is what ended one of them that nothing may catch). A value that holds
// itself is the handler's error, naming where the handler is written.
func viewData(vm *runtime.VM, handler *value.Lambda, data value.Value) (map[string]any, *uiConverter, error) {
	converter := &uiConverter{vm: vm}
	scope := map[string]any{}
	if !data.IsMap() {
		return scope, converter, nil
	}
	for name, v := range data.Map() {
		if !visibleName(name) {
			continue
		}
		if x, ok := converter.convert(v, 0); ok {
			scope[name] = x
		}
		if converter.cycle != nil {
			return nil, nil, cycleFailure(handler, name, converter.cycle)
		}
	}
	return scope, converter, nil
}
