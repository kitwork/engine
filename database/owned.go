package database

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
)

type ownedConnection struct {
	database *sql.DB
	owner    *ownedConnectionOwner
}

type ownedConnectionOwner struct{ marker byte }

// OwnedResolver lazily resolves a logical database name from a host-owned
// database root. found=false lets several independent roots coexist.
type OwnedResolver func(alias string) (connection *sql.DB, found bool, err error)

type ownedConnectionResolver struct {
	resolve OwnedResolver
	owner   *ownedConnectionOwner
}

var ownedDatabases = struct {
	sync.RWMutex
	connections map[string]ownedConnection
	resolvers   []ownedConnectionResolver
}{connections: make(map[string]ownedConnection)}

// RegisterOwned publishes one host-owned, in-process database connection.
// The returned teardown only removes the entry created by this call.
func RegisterOwned(alias string, connection *sql.DB) (func(), error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, fmt.Errorf("owned database alias is empty")
	}
	if connection == nil {
		return nil, fmt.Errorf("owned database %q connection is nil", alias)
	}
	owner := &ownedConnectionOwner{}
	ownedDatabases.Lock()
	if _, exists := ownedDatabases.connections[alias]; exists {
		ownedDatabases.Unlock()
		return nil, fmt.Errorf("owned database alias %q is already registered", alias)
	}
	ownedDatabases.connections[alias] = ownedConnection{database: connection, owner: owner}
	ownedDatabases.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			ownedDatabases.Lock()
			if current, exists := ownedDatabases.connections[alias]; exists && current.owner == owner {
				delete(ownedDatabases.connections, alias)
			}
			ownedDatabases.Unlock()
		})
	}, nil
}

// RegisterOwnedResolver publishes a lazy host-owned database root. Resolution
// callbacks run without the registry lock, so they may discover/open a node
// database without blocking unrelated aliases.
func RegisterOwnedResolver(resolve OwnedResolver) (func(), error) {
	if resolve == nil {
		return nil, fmt.Errorf("owned database resolver is nil")
	}
	owner := &ownedConnectionOwner{}
	ownedDatabases.Lock()
	ownedDatabases.resolvers = append(ownedDatabases.resolvers, ownedConnectionResolver{
		resolve: resolve, owner: owner,
	})
	ownedDatabases.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			ownedDatabases.Lock()
			for index, current := range ownedDatabases.resolvers {
				if current.owner == owner {
					ownedDatabases.resolvers = append(
						ownedDatabases.resolvers[:index],
						ownedDatabases.resolvers[index+1:]...,
					)
					break
				}
			}
			ownedDatabases.Unlock()
		})
	}, nil
}

// ResolveOwned resolves an exact alias or one lazy database-root member.
// More than one matching root fails closed instead of choosing by order.
func ResolveOwned(alias string) (*sql.DB, error) {
	alias = strings.TrimSpace(alias)
	ownedDatabases.RLock()
	if current, exists := ownedDatabases.connections[alias]; exists {
		ownedDatabases.RUnlock()
		return current.database, nil
	}
	resolvers := append([]ownedConnectionResolver(nil), ownedDatabases.resolvers...)
	ownedDatabases.RUnlock()

	var resolved *sql.DB
	for _, current := range resolvers {
		connection, found, err := current.resolve(alias)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if connection == nil {
			return nil, fmt.Errorf("owned database resolver returned nil for %q", alias)
		}
		if resolved != nil && resolved != connection {
			return nil, fmt.Errorf("owned database alias %q is ambiguous across managed roots", alias)
		}
		resolved = connection
	}
	return resolved, nil
}

// LookupOwned returns a borrowed handle. Its lifecycle belongs to the host
// declaration, not the requesting tenant or Database adapter.
func LookupOwned(alias string) *sql.DB {
	connection, _ := ResolveOwned(alias)
	return connection
}
