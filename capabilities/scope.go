package capabilities

import (
	"database/sql"

	"github.com/kitwork/engine/utilities/safepath"
)

// Identity represents the app and domain identifier of a tenant.
type Identity interface {
	AppID() string
	Domain() string
}

// PathResolver resolves paths within a tenant's designated directory boundary.
type PathResolver interface {
	ResolvePath(paths ...string) string
}

// DatabaseProvider provides access to named database connections.
type DatabaseProvider interface {
	DB(name string) *sql.DB
}

// Scope combines tenant identity, path resolution, and database access.
type Scope interface {
	Identity
	PathResolver
	DatabaseProvider
}

// CleanPath resolves paths under base and strictly verifies that the resulting path
// remains inside base (least privilege boundary enforcement).
func CleanPath(base string, paths ...string) (string, error) {
	if base == "" {
		base = "."
	}
	return safepath.Resolve(base, paths...)
}
