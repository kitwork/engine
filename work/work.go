package work

import (
	"path/filepath"
)

type KitWork struct {
	source string
	entity Entity
	// bytecode *compiler.Bytecode
}

func New(source, identity, host string) *KitWork {
	return &KitWork{
		source: source,
		entity: Entity{
			Identity: identity,
			Host:     host,
		},
	}
}

func (w *KitWork) Path() string {
	return filepath.Join(w.source, w.entity.Identity, w.entity.Host)
}

type Work struct {
	handle *Handle
}
