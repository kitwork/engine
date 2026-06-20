package work

import (
	"os"
	"path"
	"strings"

	"github.com/kitwork/engine/value"
)

// View renders the page for the current route and writes it as HTML. It uses the
// render registered via router.context({ render }) if present, otherwise a zero-config
// convention render (see defaultRender) — so a site needs NO render wiring at all.
//
//	ctx.view()                 → page derived from the route, bound with { request }
//	ctx.view({ user })         → same page, binding merged on top of { request }
//	ctx.view("/about")         → explicit page, bound with { request }
//	ctx.view("/about", { x })  → explicit page + binding
//
// Order-independent: a string arg is the page, a map arg is the binding.
func (c *Context) View(args ...value.Value) {
	render := c.tenant().viewRender
	if render == nil {
		render = c.defaultRender() // zero-config convention; router.context overrides it
	}

	var pageArg string
	var bindArg value.Value
	havePage := false
	for _, a := range args {
		if a.IsString() && !havePage {
			pageArg = a.String()
			havePage = true
		} else if a.IsMap() {
			bindArg = a
		}
	}

	notfound := c.request.router.isNotfound

	page := pageArg
	notfoundMode := false
	if notfound && (!havePage || pageArg == "*") {
		// router.notfound("*") / notfound() → ALWAYS render the not-found page, never
		// the requested path's own page. So a path with no registered route returns the
		// 404 page even if a page file happens to exist there. (router.notfound("/x")
		// still renders an explicit custom page.)
		notfoundMode = true
	} else if !havePage {
		page = c.derivePage(render)
	}

	// Clone: never mutate the shared render across concurrent requests.
	rc := *render
	rc.page = page
	rc.notfoundMode = notfoundMode

	// Binding always exposes `request`; caller-supplied keys merge on top.
	binding := map[string]value.Value{"request": value.New(c.Request())}
	if bindArg.IsMap() {
		for k, v := range bindArg.Map() {
			binding[k] = v
		}
	}

	html := rc.Bind(value.Value{K: value.Map, V: binding})
	if notfound {
		c.Response().HTML(html, 404)
	} else {
		c.Response().HTML(html)
	}
}

// derivePage turns the matched route pattern into a page path under the render's
// directory. Static segments pass through; ":id" / ":id?" resolve to the literal
// value's folder when it exists, else "[id]", else are dropped (optional / absent).
// So /users/:id? → /users/5 (if app/users/5/page exists) → /users/[id] → /users.
func (c *Context) derivePage(render *Render) string {
	pattern := c.request.router.Path
	params := c.request.router.params

	segs := strings.Split(strings.Trim(pattern, "/"), "/")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		if seg == "" || seg == "*" {
			continue
		}
		if strings.HasPrefix(seg, ":") {
			name := strings.TrimSuffix(strings.TrimPrefix(seg, ":"), "?")
			val := params[name]
			if val == "" {
				continue // optional / missing param
			}
			if c.pageExists(render, append(append([]string{}, out...), val)) {
				out = append(out, val) // literal value folder
			} else {
				out = append(out, "["+name+"]") // dynamic folder
			}
			continue
		}
		out = append(out, seg)
	}
	return "/" + strings.Join(out, "/")
}

// pageExists reports whether <dir>/<segs...>/page.kitwork.html is on disk.
func (c *Context) pageExists(render *Render, segs []string) bool {
	p := render.pathJoin(render.path, path.Join(segs...), render.getfile("page"))
	_, err := os.Stat(p)
	return err == nil
}

// defaultRender is the zero-config convention used when no router.context({ render })
// was set: rooted at app/, "notfound" fallback. The nested-shell walk-up finds each
// section's index.kitwork.html and partials auto-resolve by sibling, so structure is
// fully automatic; global data is empty (templates supply their own `??` defaults).
// A site only needs router.context to inject global data or custom layout paths.
func (c *Context) defaultRender() *Render {
	r := NewRender(c.tenant())
	r.directory = "views"
	r.path = "/"
	r.notfound = "notfound"
	// JIT is delivered via the auto-served /jitcss stylesheet (link it in <head>), so the
	// per-render inline injection stays off here. Use render.jit() to inline instead.
	return r
}
