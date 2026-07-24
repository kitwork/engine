package app

import (
	"database/sql"
	"path/filepath"

	"github.com/kitwork/engine/capabilities"
)

// Scope represents a tenant app scope in the engine runtime.
type Scope struct {
	id     string
	domain string
	root   string
	db     *sql.DB
}

func NewScope(id, domain, root string, db *sql.DB) *Scope {
	return &Scope{
		id:     id,
		domain: domain,
		root:   root,
		db:     db,
	}
}

func (s *Scope) AppID() string  { return s.id }
func (s *Scope) Domain() string { return s.domain }
func (s *Scope) ResolvePath(paths ...string) string {
	if len(paths) == 0 {
		return s.root
	}
	return filepath.Join(append([]string{s.root}, paths...)...)
}
func (s *Scope) DB(name string) *sql.DB { return s.db }

var _ capabilities.Scope = (*Scope)(nil)
