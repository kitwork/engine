package work

import (
	"path/filepath"
	"strings"
)

// insideAppRoot reports whether an ALREADY-RESOLVED path stays inside this tenant's app.
//
// The boundary is the IDENTITY root (apps/<identity>/), not the domain folder, because
// STABILITY.md §1 scopes isolation to the app's identity: sibling domains of the same app and
// identity-level shares such as _core/ are legitimately reachable, while another app's files are
// never reachable. Single-tenant layouts have no identity, so the domain folder is the boundary.
//
// This is a lexical check (filepath.Rel on cleaned absolute paths). It does not follow symlinks —
// a link planted inside the app root can still point outward, but planting one already requires
// write access to the app's own directory.
func (t *Tenant) insideAppRoot(resolved string) bool {
	if t == nil || resolved == "" {
		return false
	}
	base, err := filepath.Abs(t.resolveApp())
	if err != nil {
		return false
	}
	target, err := filepath.Abs(resolved)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
