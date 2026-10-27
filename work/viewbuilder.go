package work

import (
	"github.com/kitwork/engine/value"
)

// ViewBuilder holds the page's text a handler writes before its answer: context.title("…"),
// description, meta, jsonld. A Kitwork UI page reads it when it is drawn (respondUI). Until
// 27/10/2026 it was also the deferred template view (ctx.view/bind/data rendered a page.kitwork.html
// at the end of the request); that went with the template pages.
type ViewBuilder struct {
	ctx    *Context
	meta   map[string]value.Value
	jsonld []value.Value // ctx.jsonld() nodes for this request, after the inherited chain's
}

func (c *Context) viewBuilder() *ViewBuilder {
	r := c.request.router
	if r.viewBuilder == nil {
		r.viewBuilder = &ViewBuilder{ctx: c, meta: map[string]value.Value{}}
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

// Meta merges meta fields; the shorthands below each write one canonical meta key.
func (vb *ViewBuilder) Meta(v value.Value) *ViewBuilder        { vb.merge(vb.meta, v); return vb }
func (vb *ViewBuilder) Title(v value.Value) *ViewBuilder       { vb.meta["title"] = v; return vb }
func (vb *ViewBuilder) Description(v value.Value) *ViewBuilder { vb.meta["description"] = v; return vb }
func (vb *ViewBuilder) Image(v value.Value) *ViewBuilder       { vb.meta["image"] = v; return vb }
func (vb *ViewBuilder) Url(v value.Value) *ViewBuilder         { vb.meta["url"] = v; return vb }
func (vb *ViewBuilder) Type(v value.Value) *ViewBuilder        { vb.meta["type"] = v; return vb }
func (vb *ViewBuilder) Language(v value.Value) *ViewBuilder    { vb.meta["language"] = v; return vb }

// Jsonld appends one or more JSON-LD nodes for this page, on top of any router-level router.jsonld()
// nodes. A .ui page does not write them yet.
func (vb *ViewBuilder) Jsonld(args ...value.Value) *ViewBuilder {
	for _, a := range args {
		if a.K == value.Map || a.K == value.Array {
			vb.jsonld = append(vb.jsonld, a)
		}
	}
	return vb
}
