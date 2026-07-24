package capabilities

import "database/sql"

// Scope provides neutral, isolated access to a tenant's environment without coupling to work.Tenant.
type Scope interface {
	AppID() string
	Domain() string
	ResolvePath(paths ...string) string
	DB(name string) *sql.DB
}
