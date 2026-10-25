package work

import (
	"errors"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// Bind / Data / Meta / Notfound return the DEFERRED ViewBuilder a filesystem-routed handler
// accumulates binding + meta on; the tree lifecycle renders it once at the end of the request (see
// viewbuilder.go), so .title()/.meta() set anywhere still reach <head>.
//
//	ctx.view()                       → render this folder's page.kitwork.html
//	ctx.view({ user })               → bind `user` into it
//	ctx.bind({ note }).title("…")    → bind + set meta
//
// View fills the same deferred view and returns the request's response, as response.view does: the
// answer of a page, after its edits (20/10/2026): context.title("Quán Cà Phê").view({ menu }).
func (c *Context) View(args ...value.Value) (*Response, error) {
	response := c.Response()
	if err := c.router().refuseGuardView(); err != nil {
		return response, err
	}
	if response.inUISite() {
		// router.work: the answer of the folder's .ui page, drawn with the names of the object
		// (serveUIAnswer); no page name, no deferred template view
		data := value.New(map[string]value.Value{})
		for _, arg := range args {
			if arg.IsMap() {
				data = arg
			}
		}
		return response.View(data)
	}
	if err := response.answering("view"); err != nil {
		return response, err
	}
	c.viewBuilder().apply(args...)
	return response, nil
}

// refuseGuardView refuses a page drawn by a guard of a router.work or a router config.work (KITWORK_ROUTER.md
// mục 4, 25/10/2026): the compiler refuses one where it can follow the guard (compiler/router_guard.go), this
// one where it cannot (a guard a call makes, one read off an object, a view taken apart), whether it is reached
// as context.view, context.title(…).view or context.response.view. A template site's guard is not refused.
func (r *Router) refuseGuardView() error {
	if r != nil && r.guarding {
		return errors.New(compiler.ContextViewInGuard("context.view(…)"))
	}
	return nil
}

func (c *Context) Bind(v value.Value) *ViewBuilder { return c.viewBuilder().Bind(v) }
func (c *Context) Data(v value.Value) *ViewBuilder { return c.viewBuilder().Data(v) }

// The page's text from the context. In router.work each is the response's edit, before the answer:
// context.title("x").description("y").view({ … }) (response_order.go). A template site keeps the
// deferred builder they give there, chain and all: ctx.bind({ note }).title(note.title).type("article").
func (c *Context) Meta(v value.Value) (any, error) {
	if response := c.Response(); response.inUISite() {
		return response.Meta(v)
	}
	return c.viewBuilder().Meta(v), nil
}
func (c *Context) Jsonld(args ...value.Value) (any, error) {
	if response := c.Response(); response.inUISite() {
		return response.Jsonld(args...)
	}
	return c.viewBuilder().Jsonld(args...), nil
}
func (c *Context) Title(v value.Value) (any, error) {
	if response := c.Response(); response.inUISite() {
		return response.Title(v)
	}
	return c.viewBuilder().Title(v), nil
}
func (c *Context) Description(v value.Value) (any, error) {
	if response := c.Response(); response.inUISite() {
		return response.Description(v)
	}
	return c.viewBuilder().Description(v), nil
}
func (c *Context) Image(v value.Value) (any, error) {
	if response := c.Response(); response.inUISite() {
		return response.Image(v)
	}
	return c.viewBuilder().Image(v), nil
}

// Notfound marks a 404 and renders the nearest notfound.kitwork.html (notfound.ui in router.work,
// answerUIPage), bubbling up the folder chain. In router.work it is an answer and gives the response.
// Variadic so the reflection layer does not auto-invoke it as a 0-arg getter — it must stay callable as
// ctx.notfound().
func (c *Context) Notfound(args ...value.Value) (any, error) {
	if response := c.Response(); response.inUISite() {
		if err := response.answering("notFound"); err != nil {
			return response, err
		}
		c.request.router.isNotfound = true
		c.viewBuilder()
		return response, nil
	}
	c.request.router.isNotfound = true
	return c.viewBuilder(), nil
}
