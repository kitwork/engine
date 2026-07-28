package work

import (
	"github.com/kitwork/engine/utilities/safepath"
)

// insideAppRoot reports whether an ALREADY-RESOLVED path stays inside this tenant's app.
//
// The boundary is the IDENTITY root (apps/<identity>/), not the domain folder, because
// STABILITY.md §1 scopes isolation to the app's identity: sibling domains of the same app and
// identity-level shares such as _core/ are legitimately reachable, while another app's files are
// never reachable. Single-tenant layouts have no identity, so the domain folder is the boundary.
func (t *Tenant) insideAppRoot(resolved string) bool {
	if t == nil || resolved == "" {
		return false
	}
	inside, err := safepath.Contains(t.resolveApp(), resolved)
	return err == nil && inside
}

func (t *Tenant) insideSiteRoot(resolved string) bool {
	if t == nil || resolved == "" {
		return false
	}
	inside, err := safepath.Contains(t.resolve(), resolved)
	return err == nil && inside
}
