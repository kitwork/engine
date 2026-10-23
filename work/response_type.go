package work

// .type() and the names a program sees on the response.
//
// The media type of an answer is the edit .type(), before the answer (KITWORK_ROUTER §6, 20/10/2026):
// context.type("image/png").file(bytes), context.type("text/csv").text(csv). An answer's own media type
// applies only when no .type() came before; in router.work a .type() against json or view is an error
// (response_order.go). It is the one way: there is no { type } of an answer. A template site
// (router.kitwork.js) keeps its ctx.type(x).send(…).

import "strings"

// hiddenResponseMembers are the response's Go getters the engine reads and a program does not see
// (value.HiddenMembers): reading one gives what a key the response does not have gives.
var hiddenResponseMembers = map[string]bool{"data": true, "kind": true, "code": true}

// uiResponseMembers are the only names a router.work program sees on the response (KITWORK_ROUTER
// §6): the edits, the answers, and the head names the engine has (url, language, jsonld). canonical
// and robots are the spec's and not the engine's yet. A template site keeps every name it has.
var uiResponseMembers = map[string]bool{
	"status": true, "type": true, "header": true, "headers": true, "cookie": true,
	"title": true, "description": true, "image": true, "canonical": true, "robots": true, "meta": true,
	"json": true, "view": true, "html": true, "text": true, "svg": true, "file": true, "redirect": true,
	"notfound": true, "error": true,
	"url": true, "language": true, "jsonld": true,
	// context.header(…).sse: the stream, after the edits that apply to it (KITWORK_SSE.md mục 1)
	"sse": true,
}

func (r *Response) HiddenMember(name string) bool {
	folded := strings.ToLower(name)
	if hiddenResponseMembers[folded] {
		return true
	}
	return r.inUISite() && !uiResponseMembers[folded]
}

// callOnlyResponseMembers are the response's methods of no argument that answer the request: read,
// they are the function, run only by a call (value.CallOnly). Context and Request have none: their
// methods of no argument only read (context.method, request.body() puts the body back).
var callOnlyResponseMembers = map[string]bool{"notfound": true, "helloworld": true}

func (r *Response) CallOnly(name string) bool { return callOnlyResponseMembers[name] }

// Type sets the media type the answer goes out with, an edit before the answer.
func (r *Response) Type(mediaType string) (*Response, error) {
	if err := r.editing("type"); err != nil {
		return r, err
	}
	r.setType(mediaType)
	return r, nil
}

// Type on the context is the response's, as the other shortcuts are.
func (c *Context) Type(mediaType string) (*Response, error) {
	return c.Response().Type(mediaType)
}

// folder is the nearest folder with a router file on the request's route: the one whose file the
// handler running is written in, or holds it.
func (r *Response) folder() *FolderRouter {
	if r.ctx == nil || r.ctx.request == nil || r.ctx.request.router == nil {
		return nil
	}
	return nearestFolder(r.ctx.request.router.node)
}

// inUISite reports whether the request is a Kitwork UI site's: its router file is router.work.
func (r *Response) inUISite() bool {
	folder := r.folder()
	return folder != nil && folder.ui
}
