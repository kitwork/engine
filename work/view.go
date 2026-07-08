package work

import "github.com/kitwork/engine/value"

// View / Bind / Data / Meta / Notfound all return the DEFERRED ViewBuilder a filesystem-routed
// handler accumulates binding + meta on; the tree lifecycle renders it once at the end of the
// request (see viewbuilder.go), so .title()/.meta() set anywhere still reach <head>.
//
//	ctx.view()                       → render this folder's page.kitwork.html
//	ctx.view({ user })               → bind `user` into it
//	ctx.bind({ note }).title("…")    → bind + set meta
func (c *Context) View(args ...value.Value) *ViewBuilder { return c.viewBuilder().apply(args...) }
func (c *Context) Bind(v value.Value) *ViewBuilder        { return c.viewBuilder().Bind(v) }
func (c *Context) Data(v value.Value) *ViewBuilder        { return c.viewBuilder().Data(v) }
func (c *Context) Meta(v value.Value) *ViewBuilder        { return c.viewBuilder().Meta(v) }

// Notfound marks a 404 and renders the nearest notfound.kitwork.html, bubbling up the folder
// chain. Variadic so the reflection layer does not auto-invoke it as a 0-arg getter — it must stay
// callable as ctx.notfound().
func (c *Context) Notfound(args ...value.Value) *ViewBuilder {
	c.request.router.isNotfound = true
	return c.viewBuilder()
}
