package work

import (
	"errors"
	"fmt"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// View is the answer of a page: in router.work, the folder's .ui page drawn with the names of the
// object, after the page's edits (20/10/2026): context.title("Quán Cà Phê").view({ menu }). A template
// site's ctx.view(), ctx.bind() and ctx.data() rendered a page.kitwork.html until 27/10/2026, when the
// template pages went: they fail now, saying what to write instead.
func (c *Context) View(args ...value.Value) (*Response, error) {
	response := c.Response()
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
	// a guard of a router.work or config.work over a template's folder is told the guard's rule first
	if err := response.answering("view"); err != nil {
		return response, err
	}
	return response, templatePagesGone("ctx.view(…)")
}

// templatePagesGone is the error of an answer that drew a template page (.kitwork.html), gone on
// 27/10/2026.
func templatePagesGone(call string) error {
	return fmt.Errorf("%s vẽ trang template (.kitwork.html), đã bỏ (27/10/2026): trang của một thư mục là content.ui, viết router.get((context) => context.view({ … })) trong router.work", call)
}

// refuseGuardAnswer refuses what a guard of a router.work or a router config.work may not answer
// (KITWORK_ROUTER.md mục 4, 25/10/2026): anything but context.redirect(…) and context.status(…).json(…), a page
// least of all. The compiler refuses it where it can follow the guard (compiler/router_guard.go); this, where it
// cannot (a guard a call makes, one read off an object, a view taken apart), however it is reached
// (context.view, context.title(…).view, context.response.text). A template site's guard is not refused.
func (r *Router) refuseGuardAnswer(answer string) error {
	if r == nil || !r.guarding {
		return nil
	}
	switch {
	case answer == "redirect", answer == "json" && r.guardStatus:
		return nil
	case answer == "view":
		return errors.New(compiler.ContextViewInGuard("context.view(…)"))
	case answer == "json":
		return errors.New(compiler.ContextAnswerInGuard("context.json(…) không có status"))
	}
	return errors.New(compiler.ContextAnswerInGuard("context." + answer + "(…)"))
}

// Bind is the answer of router.view (28/10/2026, KITWORK_ROUTER_VIEW.md): the folder's .ui page drawn with the
// names of the object, as View draws it for router.get; a function among them is a job of the page, which the
// bridge calls (view_call.go).
func (c *Context) Bind(args ...value.Value) (*Response, error) {
	response := c.Response()
	if response.inUISite() {
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
	return response, templatePagesGone("ctx.bind(…)")
}

// ViewVar is the value of a var of router.view's function: what the page holds when the bridge calls a job,
// initial otherwise. The compiler writes the call, var title = "" becoming var title = context.viewVar("title",
// ""); an author never writes it (view_call.go).
func (c *Context) ViewVar(name value.Value, initial value.Value) value.Value {
	return c.viewVar(name, initial)
}
func (c *Context) Data(v value.Value) (any, error) { return nil, templatePagesGone("ctx.data(…)") }

// The page's text from the context. In router.work each is the response's edit, before the answer:
// context.title("x").description("y").view({ … }) (response_order.go). A template site keeps the
// deferred builder they give there, chain and all: ctx.bind({ note }).title(note.title).type("article").
// A guard of a router.work edits the router.work way over a template's folder too (guarding): the function
// takes the way of the file that wrote it.
func (c *Context) Meta(v value.Value) (any, error) {
	if response := c.Response(); response.inUISite() || c.router().guarding {
		return response.Meta(v)
	}
	return c.viewBuilder().Meta(v), nil
}
func (c *Context) Jsonld(args ...value.Value) (any, error) {
	if response := c.Response(); response.inUISite() || c.router().guarding {
		return response.Jsonld(args...)
	}
	return c.viewBuilder().Jsonld(args...), nil
}
func (c *Context) Title(v value.Value) (any, error) {
	if response := c.Response(); response.inUISite() || c.router().guarding {
		return response.Title(v)
	}
	return c.viewBuilder().Title(v), nil
}
func (c *Context) Description(v value.Value) (any, error) {
	if response := c.Response(); response.inUISite() || c.router().guarding {
		return response.Description(v)
	}
	return c.viewBuilder().Description(v), nil
}
func (c *Context) Image(v value.Value) (any, error) {
	if response := c.Response(); response.inUISite() || c.router().guarding {
		return response.Image(v)
	}
	return c.viewBuilder().Image(v), nil
}

// Notfound marks a 404 and renders the nearest notfound.kitwork.html (notfound.ui in router.work,
// answerUIPage), bubbling up the folder chain. In router.work it is an answer and gives the response.
// Variadic so the reflection layer does not auto-invoke it as a 0-arg getter — it must stay callable as
// ctx.notfound().
func (c *Context) Notfound(args ...value.Value) (any, error) {
	if response := c.Response(); response.inUISite() || c.router().guarding {
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
