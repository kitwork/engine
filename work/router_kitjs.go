package work

import "github.com/kitwork/engine/value"

// Kitjs opts the whole pending site generation into the component-first
// engine/jit/javascript composer. It is intentionally site-wide and defaults
// off so legacy tenants continue using jit/js + hydrate unchanged.
//
//	kitwork().router().kitjs(true)
func (f *FolderRouter) Kitjs(args ...value.Value) *FolderRouter {
	if f == nil || f.tenant == nil {
		return f
	}
	enabled := true
	if len(args) > 0 {
		enabled = args[0].K == value.Bool && args[0].N != 0
	}
	f.tenant.presentation().SetKitJS(enabled)
	return f
}

// KitJS is the acronym-preserving Go alias. The constrained JavaScript surface
// resolves router.kitjs(...) to Kitjs.
func (f *FolderRouter) KitJS(args ...value.Value) *FolderRouter { return f.Kitjs(args...) }
