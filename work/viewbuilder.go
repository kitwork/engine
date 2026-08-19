package work

import (
	"encoding/json"

	"github.com/kitwork/engine/value"
)

// ViewBuilder is the DEFERRED view result a filesystem-routed handler returns. Instead of
// rendering immediately, ctx.view()/ctx.bind()/ctx.data()/ctx.meta() accumulate binding + meta on
// this builder; the engine renders it ONCE at the end of the request (after then/finally), so a
// .title()/.meta() set anywhere in the handler still reaches <head>.
//
//	ctx.view()                          → render this folder's page.kitwork.html
//	ctx.bind({ note })                  → add `note` to the binding ($.note)
//	ctx.data({ note })                  → set the user data outright (replaces prior binds)
//	ctx.view().title("x").image("y")    → set $.meta.title / $.meta.image
//
// The binding always carries two defaults the head/templates rely on: $.request and $.meta.
type ViewBuilder struct {
	ctx      *Context
	page     string // explicit page override (rare in tree mode)
	havePage bool
	data     map[string]value.Value
	meta     map[string]value.Value
	jsonld   []value.Value // ctx.jsonld() nodes for this request, appended to the inherited chain
	rendered bool
}

func (c *Context) viewBuilder() *ViewBuilder {
	r := c.request.router
	if r.viewBuilder == nil {
		r.viewBuilder = &ViewBuilder{ctx: c, data: map[string]value.Value{}, meta: map[string]value.Value{}}
	}
	return r.viewBuilder
}

func (vb *ViewBuilder) merge(dst map[string]value.Value, v value.Value) {
	if v.IsMap() {
		for k, val := range v.Map() {
			dst[k] = val
		}
	}
}

// apply handles ctx.view() args: a string is an explicit page, a map is binding.
func (vb *ViewBuilder) apply(args ...value.Value) *ViewBuilder {
	for _, a := range args {
		if a.IsString() && !vb.havePage {
			vb.page, vb.havePage = a.String(), true
		} else if a.IsMap() {
			vb.merge(vb.data, a)
		}
	}
	return vb
}

// Bind ADDS fields to the binding (merge, cumulative). Data SETS the user data (replace).
func (vb *ViewBuilder) Bind(v value.Value) *ViewBuilder { vb.merge(vb.data, v); return vb }

func (vb *ViewBuilder) Data(v value.Value) *ViewBuilder {
	vb.data = map[string]value.Value{}
	vb.merge(vb.data, v)
	return vb
}

// Meta merges meta fields; the shorthands below each write one canonical $.meta key.
func (vb *ViewBuilder) Meta(v value.Value) *ViewBuilder        { vb.merge(vb.meta, v); return vb }
func (vb *ViewBuilder) Title(v value.Value) *ViewBuilder       { vb.meta["title"] = v; return vb }
func (vb *ViewBuilder) Description(v value.Value) *ViewBuilder { vb.meta["description"] = v; return vb }
func (vb *ViewBuilder) Image(v value.Value) *ViewBuilder       { vb.meta["image"] = v; return vb }
func (vb *ViewBuilder) Url(v value.Value) *ViewBuilder         { vb.meta["url"] = v; return vb }
func (vb *ViewBuilder) Type(v value.Value) *ViewBuilder        { vb.meta["type"] = v; return vb }
func (vb *ViewBuilder) Language(v value.Value) *ViewBuilder    { vb.meta["language"] = v; return vb }

// Jsonld appends one or more JSON-LD nodes for this page; they accumulate on top of any router-level
// router.jsonld() nodes and render as <script type="application/ld+json"> in <head>.
func (vb *ViewBuilder) Jsonld(args ...value.Value) *ViewBuilder {
	for _, a := range args {
		if a.K == value.Map || a.K == value.Array {
			vb.jsonld = append(vb.jsonld, a)
		}
	}
	return vb
}

// flush renders the accumulated view exactly once. The tree lifecycle calls it at the end.
func (vb *ViewBuilder) flush() {
	if vb.rendered {
		return
	}
	vb.rendered = true
	c := vb.ctx
	r := c.request.router

	rd := r.treeRender // always set for a filesystem-routed request (see tree_serve.go)
	if rd == nil {
		return
	}

	// meta = inherited chain meta (root→leaf, declared via router.meta()) + this builder on top.
	meta := map[string]value.Value{}
	for k, v := range r.chainMeta {
		meta[k] = v
	}
	for k, v := range vb.meta {
		meta[k] = v
	}

	// jsonld = inherited chain nodes + this builder's, serialised HTML-safe into ONE JSON string
	// exposed as $.meta.jsonld (the head template wraps it in a <script>). Only overrides a
	// manually-set meta.jsonld when router.jsonld()/ctx.jsonld() was used, so the legacy
	// meta({ jsonld }) path stays working.
	if nodes := append(append([]value.Value{}, r.chainJsonld...), vb.jsonld...); len(nodes) > 0 {
		if blocks := renderJSONLD(nodes); blocks != "" {
			jv := value.New(blocks)
			jv.Raw = true // already HTML-safe → the head can write {{ $.meta.jsonld }} without raw()
			meta["jsonld"] = jv
		}
	}

	// binding: two always-present defaults ($.request, $.meta) + the user data.
	binding := map[string]value.Value{
		"request": value.New(c.Request()),
		"meta":    {K: value.Map, V: meta},
	}
	for k, v := range vb.data {
		binding[k] = v
	}

	notfound := r.isNotfound
	page, notfoundMode := "", false
	if vb.havePage {
		page = vb.page
	} else if notfound {
		notfoundMode = true
	}

	html := rd.BindPage(page, notfoundMode, value.Value{K: value.Map, V: binding})
	if notfound {
		c.Response().HTML(html, 404)
	} else {
		c.Response().HTML(html)
	}
}

// renderJSONLD serialises the accumulated JSON-LD nodes into ONE HTML-safe JSON string exposed as
// $.meta.jsonld — DATA, not markup. The head template owns the <script type="application/ld+json">
// wrapper; the string is nonetheless safe to drop in verbatim because json.Marshal escapes <, > and
// & so no value (even one containing "</script>") can break out of the element. A single node is
// emitted as-is (a default schema.org @context added when absent); several nodes are combined under
// one @context into an @graph array so the author still writes just one script tag.
func renderJSONLD(nodes []value.Value) string {
	if len(nodes) == 0 {
		return ""
	}
	var doc any
	if len(nodes) == 1 {
		doc = withContext(nodes[0].Interface())
	} else {
		graph := make([]any, 0, len(nodes))
		for _, n := range nodes {
			graph = append(graph, n.Interface())
		}
		doc = map[string]any{"@context": "https://schema.org", "@graph": graph}
	}
	data, err := json.Marshal(doc) // escapes < > & → safe inside a <script>
	if err != nil {
		return ""
	}
	return string(data)
}

// withContext adds a default schema.org @context to a node that omits one. Interface() returns a
// fresh Go map, so this never mutates the VM object.
func withContext(obj any) any {
	if m, ok := obj.(map[string]any); ok {
		if _, has := m["@context"]; !has {
			m["@context"] = "https://schema.org"
		}
		return m
	}
	return obj
}
